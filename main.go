// TVHub — оболочка в стиле ТВ-приставки для Windows.
// Общая часть: конфиг, HTTP API, разбор M3U, прокси для потоков.
package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Версию подставляет сборка на GitHub (-ldflags -X main.appVersion=…)
var appVersion = "1.6.9"

//go:embed web
var webFS embed.FS

// ---------- Конфиг ----------

type Playlist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"` // http(s) ссылка или путь к файлу .m3u
}

type App struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`   // "web" — открыть сайт в Edge на весь экран, "exe" — запустить программу
	Target    string `json:"target"` // ссылка или путь к .exe
	Args      string `json:"args,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	Color     string `json:"color,omitempty"`
	Plain     bool   `json:"plain,omitempty"`  // обычный браузер: без управления TVHub (для сайтов, которые его не любят)
	Inside    bool   `json:"inside,omitempty"` // открывать прямо в окне TVHub (встроенный браузер, проба)
	InEdge    bool   `json:"inEdge,omitempty"` // открывать в отдельном Edge, а не внутри TVHub
}

type Settings struct {
	Autostart         bool    `json:"autostart"`
	Fullscreen        bool    `json:"fullscreen"`
	ResumeLastChannel bool    `json:"resumeLastChannel"`
	KeepAdSound       bool    `json:"keepAdSound"`    // false = глушить звук на рекламных вставках
	PhoneRemoteOff    bool    `json:"phoneRemoteOff"` // false = пульт на телефоне включён
	VPNPath           string  `json:"vpnPath,omitempty"`
	RunAsAdmin        bool    `json:"runAsAdmin,omitempty"`
	LatinNames        bool    `json:"latinNames,omitempty"`   // true = оставить названия каналов как в плейлисте
	EPGURL            string  `json:"epgUrl,omitempty"`       // своя ссылка на телепрограмму (XMLTV)
	SaverMinutes      int     `json:"saverMinutes,omitempty"` // через сколько минут заставка (0 = 10 мин, -1 = выкл)
	City              *City   `json:"city,omitempty"`         // город для погоды
	AutoUpdateOff     bool    `json:"autoUpdateOff,omitempty"`
	Brightness        int     `json:"brightness,omitempty"` // 10–100, 0 = 100
	DarkSites         bool    `json:"darkSites,omitempty"`  // Edge затемняет сайты без своей тёмной темы
	VPNForRu          bool    `json:"vpnForRu,omitempty"`   // true = российские сервисы тоже через VPN
	VPNHidden         bool    `json:"vpnHidden,omitempty"`
	EQ                *EQ     `json:"eq,omitempty"`     // эквалайзер (Equalizer APO)
	OwnVPN            *OwnVPN `json:"ownVpn,omitempty"` // свой VPN (Xray) вместо Happ
}

type Config struct {
	Playlists        []Playlist              `json:"playlists"`
	ActivePlaylist   string                  `json:"activePlaylist"`
	Apps             []App                   `json:"apps"`
	Favorites        []string                `json:"favorites"`
	LastChannel      string                  `json:"lastChannel"`
	Settings         Settings                `json:"settings"`
	RemoteKey        string                  `json:"remoteKey"` // секрет в ссылке пульта на телефоне
	Reminders        []Reminder              `json:"reminders,omitempty"`
	YaMusicAdded     bool                    `json:"yaMusicAdded,omitempty"`
	ClearCity        bool                    `json:"clearCity,omitempty"`        // только в запросе: убрать город
	Likes            map[string]bool         `json:"likes,omitempty"`            // треки, отмеченные «Мне нравится» через TVHub
	DeletedPlaylists []Playlist              `json:"deletedPlaylists,omitempty"` // недавно удалённые — их можно вернуть
	InsideLast       map[string]InsideResume `json:"insideLast,omitempty"`       // где остановились на сайтах внутри TVHub
}

const tvUA = "Mozilla/5.0 (SMART-TV; LINUX; Tizen 6.0) AppleWebKit/537.36 (KHTML, like Gecko) 85.0.4183.93/6.0 TV Safari/537.36"

func defaultConfig() Config {
	return Config{
		Playlists: []Playlist{
			{ID: "pl1", Name: "Открытые каналы России (iptv-org)", URL: "https://iptv-org.github.io/iptv/countries/ru.m3u"},
		},
		ActivePlaylist: "pl1",
		Apps: []App{
			{ID: "youtube", Name: "YouTube", Type: "web", Target: "https://www.youtube.com/tv", UserAgent: tvUA, Color: "#e62117"},
			{ID: "kinopoisk", Name: "Кинопоиск", Type: "web", Target: "https://hd.kinopoisk.ru", Color: "#ff6600"},
			{ID: "okko", Name: "Okko", Type: "web", Target: "https://okko.tv", Color: "#5a2ee6"},
			{ID: "ivi", Name: "Иви", Type: "web", Target: "https://www.ivi.ru", Color: "#ea003d"},
			{ID: "wink", Name: "Wink", Type: "web", Target: "https://wink.ru", Color: "#ff4f12"},
			{ID: "kion", Name: "KION", Type: "web", Target: "https://kion.ru", Color: "#e30611"},
			{ID: "premier", Name: "Premier", Type: "web", Target: "https://premier.one", Color: "#1f1f1f"},
			{ID: "vkvideo", Name: "VK Видео", Type: "web", Target: "https://vkvideo.ru", Color: "#0077ff"},
			{ID: "rutube", Name: "Rutube", Type: "web", Target: "https://rutube.ru", Color: "#100943"},
			{ID: "smotrim", Name: "Смотрим", Type: "web", Target: "https://smotrim.ru", Color: "#0a4da2"},
			yandexMusicApp,
		},
		Favorites: []string{},
		Settings:  Settings{Fullscreen: true},
	}
}

var (
	cfgMu   sync.Mutex
	cfg     Config
	dataDir string
	token   string
)

func configPath() string { return filepath.Join(dataDir, "config.json") }

func loadConfig() {
	cfg = defaultConfig()
	var c Config
	ok := false
	// Если основной файл пропал или повреждён — берём резервную копию
	for _, f := range []string{configPath(), configPath() + ".bak"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		c = Config{}
		if err := json.Unmarshal(b, &c); err != nil || c.Playlists == nil {
			log.Printf("%s повреждён: %v", filepath.Base(f), err)
			continue
		}
		ok = true
		break
	}
	if !ok {
		return
	}
	if c.Favorites == nil {
		c.Favorites = []string{}
	}
	has := false
	for _, a := range c.Apps {
		if a.ID == yandexMusicApp.ID {
			has = true
		}
	}
	if !has && !c.YaMusicAdded {
		c.Apps = append([]App{yandexMusicApp}, c.Apps...)
	}
	c.YaMusicAdded = true
	cfg = c
}

var yandexMusicApp = App{ID: "yamusic", Name: "Яндекс Музыка", Type: "music", Target: "https://music.yandex.ru", Color: "#fc3f1d"}

func saveConfigLocked() error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("настройки не сохранены: %v", err)
		return err
	}
	// Windows не даёт заменить файл, пока его кто-то читает (антивирус, второй экземпляр) — пробуем ещё
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmp, configPath()); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		if err = os.WriteFile(configPath(), b, 0o644); err != nil {
			log.Printf("настройки не сохранены: %v", err)
			return err
		}
		os.Remove(tmp)
	}
	os.WriteFile(configPath()+".bak", b, 0o644)
	return nil
}

// ---------- M3U ----------

type Channel struct {
	N     int    `json:"n"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Logo  string `json:"logo,omitempty"`
	Group string `json:"group,omitempty"`
	UA    string `json:"ua,omitempty"`
	Ref   string `json:"ref,omitempty"`
	TvgID string `json:"tvgId,omitempty"`
	Orig  string `json:"orig,omitempty"` // название из плейлиста, если показываем русское
}

var (
	tvgMu       sync.Mutex
	playlistTvg string // ссылка на телепрограмму из заголовка плейлиста (url-tvg)
)

var attrRe = regexp.MustCompile(`([a-zA-Z0-9_-]+)="([^"]*)"`)

func parseM3U(r io.Reader) []Channel {
	var out []Channel
	var cur *Channel
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		switch {
		case strings.HasPrefix(line, "#EXTM3U"):
			for _, a := range attrRe.FindAllStringSubmatch(line, -1) {
				if k := strings.ToLower(a[1]); (k == "url-tvg" || k == "x-tvg-url") && a[2] != "" {
					tvgMu.Lock()
					playlistTvg = strings.TrimSpace(strings.Split(a[2], ",")[0])
					tvgMu.Unlock()
				}
			}
		case line == "":
		case strings.HasPrefix(line, "#EXTINF"):
			cur = &Channel{}
			head, name := line, ""
			// имя — после последней запятой вне кавычек
			inQ := false
			for i, ch := range line {
				if ch == '"' {
					inQ = !inQ
				} else if ch == ',' && !inQ {
					head, name = line[:i], line[i+1:]
				}
			}
			cur.Name = strings.TrimSpace(name)
			for _, m := range attrRe.FindAllStringSubmatch(head, -1) {
				switch strings.ToLower(m[1]) {
				case "tvg-logo":
					cur.Logo = m[2]
				case "tvg-id":
					cur.TvgID = m[2]
				case "group-title":
					cur.Group = m[2]
				case "tvg-name":
					if cur.Name == "" {
						cur.Name = m[2]
					}
				case "http-user-agent":
					cur.UA = m[2]
				case "http-referrer", "http-referer":
					cur.Ref = m[2]
				}
			}
		case strings.HasPrefix(line, "#EXTGRP:"):
			if cur != nil && cur.Group == "" {
				cur.Group = strings.TrimSpace(line[8:])
			}
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			if cur == nil {
				continue
			}
			opt := line[11:]
			if v, ok := strings.CutPrefix(opt, "http-user-agent="); ok {
				cur.UA = v
			} else if v, ok := strings.CutPrefix(opt, "http-referrer="); ok {
				cur.Ref = v
			}
		case strings.HasPrefix(line, "#"):
		default:
			if cur == nil {
				cur = &Channel{}
			}
			cur.URL = line
			if cur.Name == "" {
				cur.Name = line
			}
			if cur.Group == "" {
				cur.Group = "Без категории"
			}
			cur.N = len(out) + 1
			out = append(out, *cur)
			cur = nil
		}
	}
	return out
}

var httpClient = &http.Client{Timeout: 0}

const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

func openPlaylist(src string) (io.ReadCloser, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequest("GET", src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", defaultUA)
		c := &http.Client{Timeout: 30 * time.Second}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("сервер ответил %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
	return os.Open(strings.Trim(src, `"`))
}

type plCache struct {
	src      string
	at       time.Time
	channels []Channel
}

var (
	cacheMu sync.Mutex
	cache   plCache
)

func getChannels(force bool) ([]Channel, error) {
	cfgMu.Lock()
	var src string
	for _, p := range cfg.Playlists {
		if p.ID == cfg.ActivePlaylist {
			src = p.URL
		}
	}
	if src == "" && len(cfg.Playlists) > 0 {
		src = cfg.Playlists[0].URL
	}
	cfgMu.Unlock()
	if src == "" {
		return []Channel{}, nil
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !force && cache.src == src && time.Since(cache.at) < 6*time.Hour {
		return withoutAdult(cache.channels), nil
	}
	rc, err := openPlaylist(src)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	ch := parseM3U(rc)
	cache = plCache{src: src, at: time.Now(), channels: ch}
	return withoutAdult(ch), nil
}

// ---------- Прокси для потоков (обходит CORS, переписывает ссылки в .m3u8) ----------

var uriAttrRe = regexp.MustCompile(`URI="([^"]+)"`)

func proxyURL(target, ua, ref string) string {
	v := url.Values{}
	v.Set("t", token)
	v.Set("u", target)
	if ua != "" {
		v.Set("ua", ua)
	}
	if ref != "" {
		v.Set("ref", ref)
	}
	return "/p?" + v.Encode()
}

// adMarker распознаёт метки рекламных вставок в HLS (SCTE-35, CUE-OUT/CUE-IN).
// Возвращает (true — началась реклама / false — закончилась, найдена ли метка).
func adMarker(t string) (bool, bool) {
	u := strings.ToUpper(t)
	switch {
	case strings.HasPrefix(u, "#EXT-X-CUE-IN"):
		return false, true
	case strings.HasPrefix(u, "#EXT-X-CUE-OUT"), strings.HasPrefix(u, "#EXT-X-CUE-OUT-CONT"):
		return true, true
	case strings.HasPrefix(u, "#EXT-OATCLS-SCTE35"), strings.HasPrefix(u, "#EXT-X-SCTE35"):
		if strings.Contains(u, "CUE-IN") {
			return false, true
		}
		if strings.Contains(u, "CUE-OUT") {
			return true, true
		}
	case strings.HasPrefix(u, "#EXT-X-DATERANGE"):
		if strings.Contains(u, "SCTE35-IN") {
			return false, true
		}
		if strings.Contains(u, "SCTE35-OUT") {
			return true, true
		}
	}
	return false, false
}

var adHosts = []string{"doubleclick.net", "googlesyndication.com", "imasdk.googleapis.com", "adfox.ru", "an.yandex.ru",
	"adriver.ru", "betweendigital.com", "mediasniper.ru", "otm-r.com", "adhigh.net", "/ads/", "/adv/", "/advert"}

func isAdURL(s string) bool {
	l := strings.ToLower(s)
	for _, h := range adHosts {
		if strings.Contains(l, h) {
			return true
		}
	}
	return false
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("t") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	target := q.Get("u")
	pu, err := url.Parse(target)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		http.Error(w, "bad url", 400)
		return
	}
	ua, ref := q.Get("ua"), q.Get("ref")
	req, err := http.NewRequestWithContext(r.Context(), "GET", target, nil)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if ua == "" {
		ua = defaultUA
	}
	req.Header.Set("User-Agent", ua)
	if ref != "" {
		req.Header.Set("Referer", ref)
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()

	br := bufio.NewReaderSize(resp.Body, 64*1024)
	head, _ := br.Peek(7)
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	isM3U8 := strings.Contains(ct, "mpegurl") || strings.HasSuffix(strings.ToLower(resp.Request.URL.Path), ".m3u8") ||
		string(head) == "#EXTM3U"

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")

	if isM3U8 && resp.StatusCode == 200 {
		body, err := io.ReadAll(io.LimitReader(br, 16*1024*1024))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		base := resp.Request.URL // учитываем редиректы
		resolve := func(s string) string {
			u, err := base.Parse(strings.TrimSpace(s))
			if err != nil {
				return s
			}
			return proxyURL(u.String(), q.Get("ua"), ref)
		}
		lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
		inAd := false
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if t == "" {
				continue
			}
			if strings.HasPrefix(t, "#") {
				if v, ok := adMarker(t); ok {
					inAd = v
				}
				lines[i] = uriAttrRe.ReplaceAllStringFunc(l, func(m string) string {
					return `URI="` + resolve(uriAttrRe.FindStringSubmatch(m)[1]) + `"`
				})
			} else {
				lines[i] = resolve(t)
				// Рекламный сегмент: помечаем, чтобы плеер приглушил звук.
				// Сегменты не удаляем — иначе собьётся нумерация живого потока.
				if inAd || isAdURL(t) {
					lines[i] += "&ad=1"
				}
			}
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		io.WriteString(w, strings.Join(lines, "\n"))
		return
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		w.Header().Set("Cache-Control", "max-age=86400") // обложки и логотипы — из кеша
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	buf := make([]byte, 64*1024)
	fl, _ := w.(http.Flusher)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ---------- HTTP API ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Token") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		next(w, r)
	}
}

func handleState(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	c := cfg
	cfgMu.Unlock()
	writeJSON(w, map[string]any{"config": c, "version": appVersion, "platform": platformName, "dataDir": dataDir})
}

func handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	var c Config
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&c); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if c.Favorites == nil {
		c.Favorites = []string{}
	}
	cfgMu.Lock()
	prevAuto := cfg.Settings.Autostart
	prevRemote := cfg.Settings.PhoneRemoteOff
	c.RemoteKey = cfg.RemoteKey // ключ пульта меняет только сервер
	c.Settings.RunAsAdmin = cfg.Settings.RunAsAdmin
	c.Reminders = cfg.Reminders
	c.Settings.Brightness = cfg.Settings.Brightness
	c.Settings.DarkSites = cfg.Settings.DarkSites
	c.Settings.EQ = cfg.Settings.EQ
	c.Settings.OwnVPN = cfg.Settings.OwnVPN // меняется только через /api/ownvpn
	c.Likes = cfg.Likes
	c.InsideLast = cfg.InsideLast
	c.DeletedPlaylists = deletedPlaylists(cfg.DeletedPlaylists, cfg.Playlists, c.Playlists)
	if c.Settings.City == nil { // город сбрасывается только явно, не из-за устаревшей копии настроек
		c.Settings.City = cfg.Settings.City
	}
	if c.ClearCity {
		c.Settings.City, c.ClearCity = nil, false
	}
	prevEPG := cfg.Settings.EPGURL + "|" + cfg.ActivePlaylist
	cfg = c
	err := saveConfigLocked()
	cfgMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if prevEPG != c.Settings.EPGURL+"|"+c.ActivePlaylist {
		go func() { time.Sleep(3 * time.Second); reloadEPG() }()
	}
	if prevRemote != c.Settings.PhoneRemoteOff {
		go applyRemoteSetting()
	}
	if prevAuto != c.Settings.Autostart {
		if err := setAutostart(c.Settings.Autostart); err != nil {
			log.Printf("автозапуск: %v", err)
		}
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleChannels(w http.ResponseWriter, r *http.Request) {
	ch, err := getChannels(r.URL.Query().Get("force") == "1")
	if err != nil {
		writeJSON(w, map[string]any{"channels": []Channel{}, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"channels": displayChannels(ch)})
}

// displayChannels — каналы с названиями для экрана (русскими, если не выключено).
func displayChannels(ch []Channel) []Channel {
	cfgMu.Lock()
	latin := cfg.Settings.LatinNames
	cfgMu.Unlock()
	out := make([]Channel, len(ch))
	for i, c := range ch {
		clean := cleanName(c.Name)
		if !latin {
			c.Group = russifyGroup(c.Group)
		}
		if latin {
			c.Name = clean
		} else if ru := russifyName(c.Name); ru != clean {
			c.Orig, c.Name = clean, ru
		} else {
			c.Name = clean
		}
		out[i] = c
	}
	return out
}

func handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string
		Fresh  bool // открыть сайт с главной страницы, а не там, где остановились
		SignIn bool // войти в аккаунт: обычный Edge, которому Google доверяет
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Fresh {
		cfgMu.Lock()
		delete(cfg.InsideLast, req.ID)
		cfgMu.Unlock()
	}
	cfgMu.Lock()
	var app *App
	for i := range cfg.Apps {
		if cfg.Apps[i].ID == req.ID {
			a := cfg.Apps[i]
			app = &a
		}
	}
	cfgMu.Unlock()
	if app == nil && req.ID == yandexMusicApp.ID { // Яндекс Музыку убрали из приложений — открываем всё равно
		a := yandexMusicApp
		app = &a
	}
	if app == nil {
		http.Error(w, "приложение не найдено", 404)
		return
	}
	if req.SignIn {
		if err := signInApp(*app); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if err := launchApp(*app); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func handleSystem(w http.ResponseWriter, r *http.Request) {
	var req struct{ Action string }
	json.NewDecoder(r.Body).Decode(&req)
	err := systemAction(req.Action)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// startServer поднимает локальный сервер и возвращает адрес интерфейса.
func startServer() (string, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/p", handleProxy)
	mux.HandleFunc("GET /api/state", apiAuth(handleState))
	mux.HandleFunc("POST /api/config", apiAuth(handleSaveConfig))
	mux.HandleFunc("GET /api/channels", apiAuth(handleChannels))
	mux.HandleFunc("POST /api/launch", apiAuth(handleLaunch))
	mux.HandleFunc("POST /api/system", apiAuth(handleSystem))
	mux.HandleFunc("GET /api/events", handleEvents)
	mux.HandleFunc("GET /api/epg", apiAuth(handleEPGNow))
	mux.HandleFunc("GET /api/epg/ch", apiAuth(handleEPGChannel))
	mux.HandleFunc("POST /api/epg/reload", apiAuth(func(w http.ResponseWriter, r *http.Request) {
		go reloadEPG()
		writeJSON(w, map[string]any{"ok": true})
	}))
	mux.HandleFunc("GET /api/update", apiAuth(handleUpdate))
	mux.HandleFunc("POST /api/update", apiAuth(handleUpdateAction))
	mux.HandleFunc("/api/whatsnew", apiAuth(handleWhatsNew))
	mux.HandleFunc("/api/brightness", apiAuth(handleBrightness))
	mux.HandleFunc("/api/eq", apiAuth(handleEQ))
	mux.HandleFunc("/api/theme", apiAuth(handleTheme))
	mux.HandleFunc("GET /api/notices", apiAuth(handleNotices))
	mux.HandleFunc("POST /api/notices/read", apiAuth(handleNoticesRead))
	mux.HandleFunc("POST /api/reminder", apiAuth(handleReminder))
	mux.HandleFunc("GET /api/weather", apiAuth(handleWeather))
	mux.HandleFunc("GET /api/weather/search", apiAuth(handleCitySearch))
	mux.HandleFunc("GET /api/walls", apiAuth(handleWalls))
	mux.HandleFunc("GET /api/wall/{name}", handleWall)
	mux.HandleFunc("GET /api/media", apiAuth(func(w http.ResponseWriter, r *http.Request) {
		st, ok := getMedia()
		writeJSON(w, map[string]any{"media": displayMedia(st), "available": ok, "music": musicAppInfo(), "startInfo": lastWaveInfo()})
	}))
	mux.HandleFunc("POST /api/media", apiAuth(func(w http.ResponseWriter, r *http.Request) {
		var req mediaReq
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		res := map[string]any{}
		if st, _ := getMedia(); req.Action == "playpause" && st.Title == "" {
			res["info"] = startMusic()
		} else if out, err := doMediaCommand(req); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		} else if out != nil {
			res = out
		}
		res["ok"] = true
		writeJSON(w, res)
	}))
	// Визуализатор: экран музыки раз в пару секунд напоминает, что он открыт
	mux.HandleFunc("POST /api/viz", apiAuth(func(w http.ResponseWriter, r *http.Request) {
		vizWant()
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/media/cover", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") != token {
			http.Error(w, "forbidden", 403)
			return
		}
		b := mediaCoverBytes()
		if len(b) == 0 {
			http.Error(w, "no cover", 404)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(b))
		w.Header().Set("Cache-Control", "max-age=3600")
		w.Write(b)
	})
	mux.HandleFunc("/api/ownvpn", apiAuth(handleOwnVPN))
	mux.HandleFunc("GET /api/vpn/info", apiAuth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, vpnInfo()) }))
	mux.HandleFunc("GET /api/vpn", apiAuth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, anyVPNStatus()) }))
	mux.HandleFunc("POST /api/vpn", apiAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ On, Open bool }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		if ownVPNConfigured() { // свой VPN: кнопка на главном экране включает и выключает его
			v := ownVPNConf()
			v.On = req.On
			saveOwnVPN(v)
			if v.On {
				happOffForOwn()
			}
			err := ownVPNApply()
			st := anyVPNStatus()
			if err != nil {
				st["error"] = err.Error()
			}
			writeJSON(w, st)
			return
		}
		if req.Open { // показать окно Happ
			if err := openVPNApp(); err != nil {
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, vpnStatus())
			return
		}
		if err := setVPN(req.On); err != nil {
			st := vpnStatus()
			if err == errNeedClick {
				st["needClick"] = true
			} else {
				st["error"] = err.Error()
			}
			writeJSON(w, st)
			return
		}
		time.Sleep(400 * time.Millisecond)
		writeJSON(w, vpnStatus())
	}))
	mux.HandleFunc("GET /api/remote", apiAuth(handleRemoteInfo))
	mux.HandleFunc("POST /api/remote/regen", apiAuth(handleRemoteRegen))
	mux.HandleFunc("GET /api/remote/qr.png", handleRemoteQR)

	addr := os.Getenv("TVHUB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	go http.Serve(ln, mux)
	return fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), token), nil
}

func initCommon() error {
	dataDir = appDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "tvhub.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		log.SetOutput(io.MultiWriter(f, os.Stderr))
	}
	token = os.Getenv("TVHUB_TOKEN")
	if token == "" {
		token = newToken()
	}
	loadConfig()
	ensureRemoteKey()
	if cfg.Playlists == nil {
		return errors.New("bad config")
	}
	return nil
}

func main() {
	if err := initCommon(); err != nil {
		log.Fatal(err)
	}
	if platformPreInit() {
		return
	}
	applyRemoteSetting()
	startBackground()
	runPlatform()
}

// deletedPlaylists запоминает плейлисты, которые убрали из списка (последние 10), чтобы их можно было вернуть.
func deletedPlaylists(prev, before, after []Playlist) []Playlist {
	now := map[string]bool{}
	for _, p := range after {
		now[p.URL] = true
	}
	var out []Playlist
	seen := map[string]bool{}
	add := func(p Playlist) {
		if p.URL == "" || now[p.URL] || seen[p.URL] || len(out) >= 10 {
			return
		}
		seen[p.URL] = true
		out = append(out, p)
	}
	for _, p := range before { // только что удалённые — первыми
		add(p)
	}
	for _, p := range prev {
		add(p)
	}
	return out
}

// anyVPNStatus — для кнопки VPN на главном экране: свой VPN, если он настроен, иначе Happ.
func anyVPNStatus() map[string]any {
	if !ownVPNConfigured() {
		return vpnStatus()
	}
	o := ownVPNStatus()
	name := "TVHub"
	if list := ownServerList(); len(list) > 0 {
		v := ownVPNConf()
		if v.Server >= 0 && v.Server < len(list) {
			name = list[v.Server].Name
		}
	}
	return map[string]any{"found": true, "own": true, "name": "TVHub", "server": name, "on": o["on"], "running": o["want"], "busy": o["busy"], "error": o["error"]}
}

// InsideResume — страница сайта внутри TVHub, на которой остановились (чтобы вернуться к фильму).
type InsideResume struct {
	URL string    `json:"url"`
	At  time.Time `json:"at"`
}
