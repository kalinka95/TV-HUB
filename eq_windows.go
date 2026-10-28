//go:build windows

package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// eqConfigDir — папка настроек Equalizer APO (обычно C:\Program Files\EqualizerAPO\config).
func eqConfigDir() string {
	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\EqualizerAPO`, registry.QUERY_VALUE|view)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("ConfigPath")
		if err != nil || p == "" {
			if ip, _, e := k.GetStringValue("InstallPath"); e == nil && ip != "" {
				p = filepath.Join(ip, "config")
			}
		}
		k.Close()
		if p != "" {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if v := os.Getenv(env); v != "" {
			p := filepath.Join(v, "EqualizerAPO", "config")
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return ""
}

func eqSystemStatus() map[string]any {
	d := eqConfigDir()
	eqDlMu.Lock()
	dl := eqDlState
	eqDlMu.Unlock()
	return map[string]any{"installed": d != "", "dir": d, "download": dl}
}

const eqInclude = "Include: tvhub.txt"

// applyEQ пишет tvhub.txt и подключает его в config.txt — Equalizer APO подхватывает изменения сам.
func applyEQ(e EQ) error {
	dir := eqConfigDir()
	if dir == "" {
		if !e.On {
			return nil
		}
		return errors.New("на компьютере нет Equalizer APO — установите его в «Настройки» → «Звук»")
	}
	deny := errors.New("нет доступа к папке Equalizer APO. Включите «Режим администратора» в «Настройки» → «Система»")
	if err := os.WriteFile(filepath.Join(dir, "tvhub.txt"), []byte(eqConfigText(e)), 0o644); err != nil {
		if os.IsPermission(err) {
			return deny
		}
		return err
	}
	main := filepath.Join(dir, "config.txt")
	b, _ := os.ReadFile(main)
	if strings.Contains(string(b), eqInclude) {
		return nil
	}
	// Первое включение: прежние настройки сохраняем рядом, а config.txt оставляем только с нашим файлом,
	// иначе к нашему эквалайзеру добавятся примерные фильтры, которые ставит установщик.
	if len(b) > 0 {
		bak := filepath.Join(dir, "config.before-tvhub.txt")
		if _, err := os.Stat(bak); err != nil {
			os.WriteFile(bak, b, 0o644)
		}
	}
	if err := os.WriteFile(main, []byte("# Эквалайзер TVHub. Прежние настройки — в config.before-tvhub.txt\n"+eqInclude+"\n"), 0o644); err != nil {
		if os.IsPermission(err) {
			return deny
		}
		return err
	}
	return nil
}

var (
	eqDlMu    sync.Mutex
	eqDlState string // "", "downloading", "started", "error: …"
)

// openEQDownload скачивает установщик Equalizer APO и запускает его.
// Если скачать не вышло — открывает страницу загрузки в браузере.
func openEQDownload() error {
	m := &eqDlMu
	m.Lock()
	busy := eqDlState == "downloading"
	if !busy {
		eqDlState = "downloading"
	}
	m.Unlock()
	if busy {
		return nil
	}
	notify("info", "Скачиваю Equalizer APO…", "Когда появится установщик, нажмите «Да» и «Далее». В конце отметьте устройство, через которое идёт звук на телевизор (HDMI)", nil)
	go func() {
		f, err := downloadEQSetup()
		m.Lock()
		if err != nil {
			eqDlState = "error: " + err.Error()
		} else {
			eqDlState = "started"
		}
		m.Unlock()
		if err != nil {
			log.Printf("Equalizer APO: %v", err)
			windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(eqDownloadURL), nil, nil, 1)
			notify("info", "Скачайте Equalizer APO в браузере", "Скачать сам не получилось, открыл страницу загрузки", nil)
			return
		}
		if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(f), nil,
			windows.StringToUTF16Ptr(filepath.Dir(f)), 1); err != nil {
			notify("error", "Не удалось запустить установщик", err.Error(), nil)
		}
	}()
	return nil
}

func downloadEQSetup() (string, error) {
	c := &http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", eqDownloadURL, nil)
	req.Header.Set("User-Agent", "Wget/1.21.4") // так SourceForge отдаёт сам файл, а не страницу
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || strings.Contains(resp.Header.Get("Content-Type"), "html") {
		return "", errors.New("сайт не отдал файл")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return "", err
	}
	if len(data) < 500<<10 || string(data[:2]) != "MZ" {
		return "", errors.New("скачался не установщик")
	}
	f := filepath.Join(dataDir, "EqualizerAPO-setup.exe")
	if err := os.WriteFile(f, data, 0o755); err != nil {
		return "", err
	}
	return f, nil
}
