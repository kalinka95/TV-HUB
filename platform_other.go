//go:build !windows

// Режим разработки для Linux/macOS: только сервер, интерфейс открывается в браузере.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const platformName = "dev"

func appDataDir() string {
	if d := os.Getenv("TVHUB_DATA"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "tvhub-dev")
}

func launchApp(a App) error {
	log.Printf("launch %s (%s) %s", a.Name, a.Type, a.Target)
	go func() { time.Sleep(1500 * time.Millisecond); pushEvent(map[string]string{"type": "appShown"}) }()
	return nil
}
func systemAction(a string) error { log.Printf("system %s", a); return nil }
func setAutostart(on bool) error  { log.Printf("autostart %v", on); return nil }

// В режиме разработки нажатия с телефона уходят прямо в интерфейс.
func sendRemoteKey(k string)                   { pushEvent(map[string]string{"type": "key", "key": k}) }
func sendRemoteText(t string)                  { pushEvent(map[string]string{"type": "text", "text": t}) }
func sendRemoteMouse(dx, dy, wh int, c string) { log.Printf("mouse %d %d %d %s", dx, dy, wh, c) }
func goHome()                                  { pushEvent(map[string]string{"type": "home"}) }

func runPlatform() {
	addr, err := startServer()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(addr)
	select {}
}

// ---------- Окна (в режиме разработки — пример списка) ----------

type winItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	App    string `json:"app"`
	TVHub  bool   `json:"tvhub,omitempty"`
	Active bool   `json:"active,omitempty"`
}

var devWins = []winItem{
	{ID: "1", Title: "TVHub", App: "Главный экран", TVHub: true},
	{ID: "2", Title: "Кинопоиск — фильмы и сериалы онлайн", App: "Microsoft Edge", Active: true},
	{ID: "3", Title: "YouTube", App: "Microsoft Edge"},
	{ID: "4", Title: "Интерстеллар.mkv - VLC media player", App: "VLC"},
	{ID: "5", Title: "Зависшая программа", App: "Program"},
}

func listWindows() []winItem { return devWins }

func windowAction(id, action string) error {
	log.Printf("window %s %s", id, action)
	for i, w := range devWins {
		if w.ID != id {
			continue
		}
		switch action {
		case "focus":
			for j := range devWins {
				devWins[j].Active = devWins[j].ID == id
			}
		case "close", "kill":
			if w.TVHub {
				return fmt.Errorf("TVHub закрывается в меню «Система» → «Закрыть TVHub»")
			}
			if w.App == "Program" && action == "close" {
				return nil // имитируем зависшую программу
			}
			devWins = append(devWins[:i], devWins[i+1:]...)
		}
		return nil
	}
	return fmt.Errorf("окно уже закрыто")
}

// ---------- VPN (в режиме разработки — имитация) ----------

var devVPN = true

func vpnStatus() map[string]any {
	return map[string]any{"found": true, "path": `C:\Program Files\Happ\Happ.exe`, "name": "Happ", "on": devVPN}
}

var errNeedClick = fmt.Errorf("need click")

func setVPN(on bool) error {
	if on && os.Getenv("TVHUB_VPN_CLICK") == "1" {
		return errNeedClick
	}
	devVPN = on
	return nil
}

func platformPreInit() bool { return false }
func remoteBlocked() string { return "" }

// ---------- Музыка (в режиме разработки — пример) ----------

type mediaState struct {
	OK       bool    `json:"ok"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album,omitempty"`
	Status   string  `json:"status"`
	App      string  `json:"app,omitempty"`
	Pos      float64 `json:"pos"`
	Dur      float64 `json:"dur"`
	CoverVer string  `json:"coverVer,omitempty"`
	CoverURL string  `json:"coverUrl,omitempty"`
	Liked    *bool   `json:"liked,omitempty"`
	At       int64   `json:"at"`
}

var devMedia = mediaState{OK: true, Title: "Звезда по имени Солнце", Artist: "Кино", Album: "Звезда по имени Солнце", Status: "Playing", App: "Yandex.Music", Pos: 42, Dur: 225,
	CoverURL: os.Getenv("TVHUB_DEV_COVER")}
var (
	devMediaStart = time.Now()
	devMediaPos   = 42.0
	devLiked      = false
)

func getMedia() (mediaState, bool) {
	m := devMedia
	if m.Status == "Playing" {
		m.Pos = float64(int(time.Since(devMediaStart).Seconds()+devMediaPos) % 225)
	}
	liked := devLiked
	m.Liked = &liked
	m.At = time.Now().Unix()
	fillCoverFallback(&m)
	return m, true
}
func mediaCoverBytes() []byte { return nil }
func mediaSeek(pos float64) error {
	log.Printf("media seek %.1f", pos)
	devMediaStart, devMediaPos = time.Now(), max(0, min(pos, 224))
	return nil
}
func mediaLike() (bool, error) { devLiked = !devLiked; return devLiked, nil }
func mediaAction(a string) error {
	log.Printf("media %s", a)
	if a == "playpause" {
		if devMedia.Status == "Playing" {
			devMedia.Status = "Paused"
		} else {
			devMedia.Status = "Playing"
		}
	}
	return nil
}
func launchMusic(a App) error      { log.Printf("music %s", a.Target); return nil }
func musicAppInfo() map[string]any { return map[string]any{"desktop": false} }

// ---------- Обновления (в режиме разработки — без установки) ----------

func installUpdate(f string) error {
	log.Printf("update install %s", f)
	updMu.Lock()
	updState = ""
	updMu.Unlock()
	return nil
}
func rollbackAvailable() bool { return false }
func rollbackUpdate() error {
	return fmt.Errorf("в режиме разработки недоступно")
}
func cleanupAfterUpdate() {}

// ---------- Яркость (в режиме разработки — имитация) ----------

type displayInfo struct {
	Name   string `json:"name"`
	Method string `json:"method"`
}

func applyBrightness(level int) []displayInfo {
	log.Printf("brightness %d", level)
	return []displayInfo{{Name: "Телевизор (тест)", Method: "gamma"}}
}
func resetBrightness() {}

func waitUIReady() {}
func startDimmer() {}

// ---------- Тёмная тема (в режиме разработки — имитация) ----------

var devDark = false

func windowsDark() bool            { return devDark }
func setWindowsDark(on bool) error { devDark = on; return nil }
func restartWebApps()              {}
func showScreen(name string)       { pushEvent(map[string]string{"type": "screen", "name": name}) }
func startMusic() string {
	log.Printf("start music")
	return "Включил «Мою волну» (тест)"
}
func openVPNApp() error    { log.Printf("open vpn app"); return nil }
func vpnTunnel() string    { return "" }
func lastWaveInfo() string { return "" }

func eqSystemStatus() map[string]any {
	return map[string]any{"installed": os.Getenv("TVHUB_EQ_DIR") != "", "dir": os.Getenv("TVHUB_EQ_DIR")}
}
func applyEQ(e EQ) error {
	log.Printf("eq: %s", strings.ReplaceAll(eqConfigText(e), "\n", " | "))
	return nil
}
func openEQDownload() error { log.Printf("eq download"); return nil }

func systemProxyURL() string { return os.Getenv("TVHUB_TEST_PROXY") }

func mediaDiag() map[string]any { return map[string]any{"dev": true} }

func tvFront() {}

func insideOpen() bool                      { return false }
func uiEvent(ev map[string]any, cmd string) { pushEvent(ev) }
