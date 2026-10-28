// Пульт на телефоне: отдельный сервер в домашней сети + события для интерфейса.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

const remotePort = 8777

// ---------- События для интерфейса (Server-Sent Events) ----------

var (
	evMu   sync.Mutex
	evSubs = map[chan string]bool{}
)

func pushEvent(v any) {
	b, _ := json.Marshal(v)
	evMu.Lock()
	defer evMu.Unlock()
	for ch := range evSubs {
		select {
		case ch <- string(b):
		default:
		}
	}
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no stream", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan string, 32)
	evMu.Lock()
	evSubs[ch] = true
	evMu.Unlock()
	defer func() {
		evMu.Lock()
		delete(evSubs, ch)
		evMu.Unlock()
	}()
	io.WriteString(w, ": ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", m)
			fl.Flush()
		case <-ping.C:
			io.WriteString(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// ---------- Ключ пульта и адреса ----------

func newRemoteKey() string {
	const abc = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

func ensureRemoteKey() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg.RemoteKey == "" {
		cfg.RemoteKey = newRemoteKey()
		saveConfigLocked()
	}
}

// lanIPs — адреса компьютера в домашней сети, лучшие первыми.
func lanIPs() []string {
	if ip := os.Getenv("TVHUB_LAN_IP"); ip != "" { // для отладки
		return []string{ip}
	}
	type cand struct {
		ip    string
		score int
	}
	var out []cand
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifc.Name)
		virtual := false
		for _, v := range []string{"vethernet", "virtualbox", "vmware", "hyper-v", "docker", "wsl", "tailscale", "zerotier", "vpn", "tap", "tun"} {
			if strings.Contains(name, v) {
				virtual = true
			}
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			s := 0
			if ip[0] == 192 && ip[1] == 168 {
				s += 3
			} else if ip[0] == 10 {
				s += 2
			} else {
				s += 1
			}
			if strings.Contains(name, "wi-fi") || strings.Contains(name, "wlan") || strings.Contains(name, "wireless") || strings.Contains(name, "ethernet") {
				s += 2
			}
			if virtual {
				s -= 10
			}
			out = append(out, cand{ip.String(), s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	res := make([]string, 0, len(out))
	for _, c := range out {
		res = append(res, c.ip)
	}
	return res
}

var (
	remMu    sync.Mutex
	remSrv   *http.Server
	remPort  int
	remError string
)

func remoteURLs() []string {
	remMu.Lock()
	port := remPort
	remMu.Unlock()
	cfgMu.Lock()
	key := cfg.RemoteKey
	cfgMu.Unlock()
	if port == 0 {
		return nil
	}
	var urls []string
	for _, ip := range lanIPs() {
		urls = append(urls, fmt.Sprintf("http://%s:%d/r?k=%s", ip, port, key))
	}
	return urls
}

// ---------- Сервер пульта в домашней сети ----------

var (
	failMu sync.Mutex
	fails  = map[string][]time.Time{}
)

// Защита от перебора ключа: не больше 20 ошибок в минуту с одного адреса.
func remoteAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		failMu.Lock()
		recent := fails[host][:0]
		for _, t := range fails[host] {
			if time.Since(t) < time.Minute {
				recent = append(recent, t)
			}
		}
		fails[host] = recent
		blocked := len(recent) >= 20
		failMu.Unlock()
		if blocked {
			http.Error(w, "too many attempts", 429)
			return
		}
		cfgMu.Lock()
		key := cfg.RemoteKey
		cfgMu.Unlock()
		got := r.Header.Get("X-Remote")
		if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			failMu.Lock()
			fails[host] = append(fails[host], time.Now())
			failMu.Unlock()
			http.Error(w, "bad key", 403)
			return
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/r/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next(w, r)
	}
}

var remoteKeys = map[string]bool{
	"up": true, "down": true, "left": true, "right": true, "ok": true, "back": true, "home": true, "menu": true,
	"chup": true, "chdown": true, "volup": true, "voldown": true, "mute": true, "playpause": true,
	"backspace": true, "enter": true, "esc": true, "space": true,
	"0": true, "1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
}

var helloMu sync.Mutex
var helloSeen = map[string]time.Time{}

func startRemoteServer() {
	remMu.Lock()
	defer remMu.Unlock()
	if remSrv != nil {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /r", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/remote.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /r/icon.png", func(w http.ResponseWriter, r *http.Request) {
		b, _ := webFS.ReadFile("web/icon.png")
		w.Header().Set("Content-Type", "image/png")
		w.Write(b)
	})
	mux.HandleFunc("POST /r/api/hello", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		helloMu.Lock()
		last := helloSeen[host]
		helloSeen[host] = time.Now()
		helloMu.Unlock()
		if time.Since(last) > 10*time.Minute {
			notify("phone", "Телефон подключён как пульт", "", nil)
		}
		writeJSON(w, map[string]any{"ok": true, "version": appVersion})
	}))
	mux.HandleFunc("POST /r/api/key", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Key string }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		if !remoteKeys[req.Key] {
			http.Error(w, "unknown key", 400)
			return
		}
		if req.Key == "home" {
			goHome()
			w.WriteHeader(204)
			return
		}
		sendRemoteKey(req.Key)
		remoteReply(w)
	}))
	mux.HandleFunc("POST /r/api/text", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text string }
		json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req)
		if req.Text != "" {
			sendRemoteText(req.Text)
		}
		remoteReply(w)
	}))
	mux.HandleFunc("POST /r/api/brightness", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Delta int }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		level, ds := changeBrightness(0, req.Delta)
		writeJSON(w, map[string]any{"level": level, "displays": ds})
	}))
	mux.HandleFunc("GET /r/api/brightness", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"level": currentBrightness()})
	}))
	// Музыка на пульте: что играет, управление, показать экран музыки на ноутбуке
	mux.HandleFunc("GET /r/api/media", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		st, ok := getMedia()
		writeJSON(w, map[string]any{"media": st, "available": ok})
	}))
	mux.HandleFunc("POST /r/api/media", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Action string }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		var err error
		info := ""
		switch req.Action {
		case "show":
			showScreen("music")
		case "open":
			err = launchApp(yandexMusicApp)
		case "playpause":
			if st, _ := getMedia(); st.Title == "" {
				info = startMusic() // ничего не играет — включаем «Мою волну» и сообщаем, что вышло
			} else {
				err = mediaAction(req.Action)
			}
		default:
			err = mediaAction(req.Action)
		}
		if err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "info": info})
	}))
	// Картинку телефон грузит тегом <img>, поэтому ключ — в адресе
	mux.HandleFunc("GET /r/api/media/cover", func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock()
		key := cfg.RemoteKey
		cfgMu.Unlock()
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("k")), []byte(key)) != 1 {
			http.Error(w, "bad key", 403)
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
	mux.HandleFunc("POST /r/api/screen", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		if req.Name != "settings" && req.Name != "music" && req.Name != "home" {
			http.Error(w, "unknown screen", 400)
			return
		}
		if req.Name == "home" {
			goHome()
		} else {
			showScreen(req.Name)
		}
		writeJSON(w, map[string]any{"ok": true})
	}))
	mux.HandleFunc("GET /r/api/windows", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"windows": listWindows()})
	}))
	mux.HandleFunc("POST /r/api/window", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID, Action string }
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		if err := windowAction(req.ID, req.Action); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	}))
	mux.HandleFunc("POST /r/api/mouse", remoteAuth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DX, DY, Wheel int
			Click         string
		}
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		sendRemoteMouse(req.DX, req.DY, req.Wheel, req.Click)
		remoteReply(w)
	}))

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", remotePort))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		remError = err.Error()
		log.Printf("пульт: %v", err)
		return
	}
	remError = ""
	remPort = ln.Addr().(*net.TCPAddr).Port
	remSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go remSrv.Serve(ln)
	log.Printf("пульт на телефоне: порт %d", remPort)
}

func stopRemoteServer() {
	remMu.Lock()
	defer remMu.Unlock()
	if remSrv != nil {
		remSrv.Close()
		remSrv = nil
		remPort = 0
	}
}

func applyRemoteSetting() {
	cfgMu.Lock()
	off := cfg.Settings.PhoneRemoteOff
	cfgMu.Unlock()
	if off {
		stopRemoteServer()
	} else {
		startRemoteServer()
	}
}

// ---------- API для интерфейса TVHub ----------

func handleRemoteInfo(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	off := cfg.Settings.PhoneRemoteOff
	cfgMu.Unlock()
	remMu.Lock()
	e := remError
	remMu.Unlock()
	writeJSON(w, map[string]any{"enabled": !off, "urls": remoteURLs(), "error": e, "app": remoteAppURL()})
}

// remoteAppURL — ссылка на приложение «Пульт TVHub» для Android (собирается на GitHub).
func remoteAppURL() string {
	if updateRepo == "" {
		return ""
	}
	return "https://github.com/" + updateRepo + "/releases/download/remote-app/TVHub-Remote.apk"
}

func handleRemoteRegen(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	cfg.RemoteKey = newRemoteKey()
	saveConfigLocked()
	cfgMu.Unlock()
	handleRemoteInfo(w, r)
}

func handleRemoteQR(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	u := r.URL.Query().Get("u")
	ok := u != "" && u == remoteAppURL()
	for _, x := range remoteURLs() {
		if x == u {
			ok = true
		}
	}
	if !ok {
		http.Error(w, "unknown url", 404)
		return
	}
	png, err := qrcode.Encode(u, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// remoteReply предупреждает телефон, если нажатия не дойдут до активного окна.
func remoteReply(w http.ResponseWriter) {
	if warn := remoteBlocked(); warn != "" {
		writeJSON(w, map[string]string{"warn": warn})
		return
	}
	w.WriteHeader(204)
}
