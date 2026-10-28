// Автообновление: проверка новой версии на GitHub, скачивание и проверка контрольной суммы.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Задаются при сборке на GitHub (-ldflags -X): репозиторий вида «владелец/TV-HUB».
var updateRepo = ""

// Новая версия на GitHub — это TVHub.exe и его контрольная сумма SHA-256.
// Перед установкой TVHub проверяет, что файл скачался целиком и не повреждён.
const (
	updateAsset = "TVHub.exe"
	updateSum   = "TVHub.exe.sha256"
)

type updateInfo struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	URL     string `json:"-"`
	SumURL  string `json:"-"`
	Size    int64  `json:"size,omitempty"`
}

var (
	updMu       sync.Mutex
	updLatest   *updateInfo
	updChecked  time.Time
	updErr      string
	updState    string // "", "downloading", "ready", "installing"
	updFile     string
	updNotified string
)

// versionNewer: «1.10.0» новее «1.9.2».
func versionNewer(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.TrimFunc(pa[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.TrimFunc(pb[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func checkUpdate(ctx context.Context) (*updateInfo, error) {
	if updateRepo == "" {
		return nil, errors.New("эта сборка сделана не на GitHub, автообновление выключено")
	}
	base := envOr("TVHUB_UPDATE_API", "https://api.github.com")
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/repos/"+updateRepo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TVHub/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errors.New("на GitHub пока нет ни одной версии")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub ответил %d", resp.StatusCode)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	u := &updateInfo{Version: strings.TrimPrefix(rel.Tag, "v"), Notes: rel.Body}
	for _, a := range rel.Assets {
		switch a.Name {
		case updateAsset:
			u.URL, u.Size = a.URL, a.Size
		case updateSum:
			u.SumURL = a.URL
		}
	}
	if u.URL == "" || u.SumURL == "" {
		return nil, errors.New("в новой версии нет файла TVHub.exe или его контрольной суммы")
	}
	return u, nil
}

func fetchBytes(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "TVHub/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("сервер ответил %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// verifySum сверяет SHA-256 файла с опубликованной контрольной суммой.
func verifySum(data, sumFile []byte) error {
	fields := strings.Fields(string(sumFile))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return errors.New("контрольная сумма повреждена")
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(fields[0], hex.EncodeToString(sum[:])) {
		return errors.New("файл повреждён при скачивании, установка отменена")
	}
	return nil
}

func downloadUpdate(u *updateInfo) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sum, err := fetchBytes(ctx, u.SumURL, 4096)
	if err != nil {
		return "", fmt.Errorf("контрольная сумма: %v", err)
	}
	data, err := fetchBytes(ctx, u.URL, 200<<20)
	if err != nil {
		return "", fmt.Errorf("загрузка: %v", err)
	}
	if u.Size > 0 && int64(len(data)) != u.Size {
		return "", errors.New("файл скачался не полностью")
	}
	if err := verifySum(data, sum); err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "update")
	os.MkdirAll(dir, 0o755)
	f := filepath.Join(dir, "TVHub-"+u.Version+".exe")
	if err := os.WriteFile(f, data, 0o755); err != nil {
		return "", err
	}
	return f, nil
}

func updateStatus() map[string]any {
	updMu.Lock()
	defer updMu.Unlock()
	cfgMu.Lock()
	off := cfg.Settings.AutoUpdateOff
	cfgMu.Unlock()
	st := map[string]any{"current": appVersion, "repo": updateRepo, "state": updState, "error": updErr, "auto": !off,
		"canRollback": rollbackAvailable()}
	if !updChecked.IsZero() {
		st["checked"] = updChecked.Unix()
	}
	if updLatest != nil && versionNewer(updLatest.Version, appVersion) {
		st["available"] = updLatest
	}
	return st
}

func runUpdateCheck(manual bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	u, err := checkUpdate(ctx)
	updMu.Lock()
	updChecked = time.Now()
	if err != nil {
		updErr = err.Error()
		updMu.Unlock()
		if manual {
			log.Printf("обновление: %v", err)
		}
		return
	}
	updErr = ""
	updLatest = u
	newer := versionNewer(u.Version, appVersion)
	first := updNotified != u.Version
	if newer {
		updNotified = u.Version
	}
	updMu.Unlock()
	if newer && first {
		notify("update", "Доступна новая версия TVHub "+u.Version, "OK — обновить", &NoticeAction{Type: "update"})
		// Сразу показываем окно с предложением обновиться
		pushEvent(map[string]any{"type": "update", "version": u.Version, "notes": u.Notes})
	} else if newer && manual {
		pushEvent(map[string]any{"type": "update", "version": u.Version, "notes": u.Notes})
	}
}

// Проверяем при запуске и потом каждый час, чтобы новая версия появлялась почти сразу.
func updateLoop() {
	time.Sleep(10 * time.Second)
	for {
		cfgMu.Lock()
		off := cfg.Settings.AutoUpdateOff
		cfgMu.Unlock()
		if !off && updateRepo != "" {
			runUpdateCheck(false)
		}
		time.Sleep(time.Hour)
	}
}

// Что нового: сохраняем описание версии перед перезапуском, чтобы показать его после обновления.
func saveWhatsNew(u *updateInfo) {
	b, _ := json.Marshal(u)
	os.WriteFile(filepath.Join(dataDir, "update", "whatsnew.json"), b, 0o644)
}

func handleWhatsNew(w http.ResponseWriter, r *http.Request) {
	p := filepath.Join(dataDir, "update", "whatsnew.json")
	b, err := os.ReadFile(p)
	if err != nil {
		writeJSON(w, map[string]any{})
		return
	}
	var u updateInfo
	json.Unmarshal(b, &u)
	if u.Version != appVersion {
		writeJSON(w, map[string]any{})
		return
	}
	if r.Method == "POST" { // показали — больше не показываем
		os.Remove(p)
	}
	writeJSON(w, map[string]any{"version": u.Version, "notes": u.Notes})
}

func handleUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, updateStatus())
}

func handleUpdateAction(w http.ResponseWriter, r *http.Request) {
	var req struct{ Action string }
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	switch req.Action {
	case "check":
		runUpdateCheck(true)
	case "install":
		updMu.Lock()
		u := updLatest
		busy := updState != ""
		updMu.Unlock()
		if u == nil || !versionNewer(u.Version, appVersion) {
			writeJSON(w, map[string]any{"ok": false, "error": "Новой версии нет"})
			return
		}
		if busy {
			writeJSON(w, map[string]any{"ok": false, "error": "Обновление уже идёт"})
			return
		}
		updMu.Lock()
		updState = "downloading"
		updMu.Unlock()
		go func() {
			f, err := downloadUpdate(u)
			updMu.Lock()
			if err != nil {
				updState, updErr = "", err.Error()
				updMu.Unlock()
				notify("error", "Обновление не удалось", err.Error(), nil)
				return
			}
			updState, updFile = "installing", f
			updMu.Unlock()
			saveWhatsNew(u)
			if err := installUpdate(f); err != nil {
				updMu.Lock()
				updState, updErr = "", err.Error()
				updMu.Unlock()
				notify("error", "Обновление не удалось", err.Error(), nil)
			}
		}()
	case "rollback":
		if err := rollbackUpdate(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "status": updateStatus()})
}
