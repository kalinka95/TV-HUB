//go:build windows

// Окна онлайн-сервисов в Microsoft Edge: повторное открытие выводит уже открытое окно,
// «Домой» ставит видео и звук на паузу. Управление — через DevTools-протокол Edge
// (только с этого компьютера, порт выбирает сам Edge).
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Профиль Edge для приложения: общий для всех сайтов (входы в аккаунты общие),
// отдельный — для приложений со своим User-Agent (YouTube в ТВ-режиме) и для музыки.
func appProfile(a App) string {
	switch {
	case a.Type == "music":
		return "browser-music"
	case a.UserAgent != "":
		return "browser-" + a.ID
	case !isRuService(a):
		return "browser-intl" // зарубежные сайты — отдельно, чтобы им не мешал обход VPN
	}
	return "browser"
}

// bypassVPN — открывать ли сервис в обход VPN (российские сервисы, если не выключено в настройках).
func bypassVPN(a App) bool {
	cfgMu.Lock()
	off := cfg.Settings.VPNForRu
	cfgMu.Unlock()
	return !off && isRuService(a)
}

// prepareRuService: если VPN работает «туннелем» (весь трафик через VPN), обойти его нельзя —
// выключаем VPN перед открытием российского сервиса и сообщаем об этом.
func prepareRuService(a App) {
	if !bypassVPN(a) || vpnTunnel() == "" {
		return
	}
	if err := setVPN(false); err != nil && err != errNeedClick {
		notify("vpn", "Не удалось выключить VPN для «"+a.Name+"»", err.Error(), nil)
		return
	}
	notify("vpn", "VPN выключен, чтобы открылся «"+a.Name+"»", "Российские сервисы не работают через VPN. Включить его снова — кнопка VPN вверху", nil)
	pushEvent(map[string]string{"type": "vpn"})
}

func profileDir(name string) string { return filepath.Join(dataDir, name) }

// processCmdline читает командную строку процесса (Windows 8.1+).
func processCmdline(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 32*1024)
	var n uint32
	if err := windows.NtQueryInformationProcess(h, 60 /* ProcessCommandLineInformation */, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n); err != nil {
		return ""
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	return us.String()
}

// profileProcs — процессы Edge, запущенные с этим профилем, и главный из них.
func profileProcs(dir string) (all []uint32, main uint32) {
	needle := strings.ToLower("--user-data-dir=" + dir)
	for _, p := range allProcs() {
		low := strings.ToLower(p.exe)
		if low != "msedge.exe" && low != "chrome.exe" {
			continue
		}
		cl := strings.ToLower(processCmdline(p.pid))
		if !strings.Contains(strings.ReplaceAll(cl, `"`, ""), needle) {
			continue
		}
		all = append(all, p.pid)
		if !strings.Contains(cl, "--type=") {
			main = p.pid
		}
	}
	return
}

func killProfile(dir string) {
	all, _ := profileProcs(dir)
	for _, pid := range all {
		hiddenCmd("taskkill", "/PID", fmt.Sprint(pid), "/T", "/F").Run()
	}
}

// ---------- DevTools ----------

type cdpTarget struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
	WS    string `json:"webSocketDebuggerUrl"`
}

func devtoolsPort(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

var cdpHTTP = &http.Client{Timeout: 3 * time.Second}

func cdpTargets(dir string) ([]cdpTarget, string) {
	return cdpTargetsPort(devtoolsPort(dir))
}

func cdpTargetsPort(port string) ([]cdpTarget, string) {
	if port == "" {
		return nil, ""
	}
	resp, err := cdpHTTP.Get("http://127.0.0.1:" + port + "/json/list")
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	var list []cdpTarget
	json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list)
	return list, port
}

func browserWS(port string) string {
	resp, err := cdpHTTP.Get("http://127.0.0.1:" + port + "/json/version")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	json.NewDecoder(resp.Body).Decode(&v)
	return v.WS
}

// cdpCall — минимальный WebSocket-клиент: одна команда, один ответ.
func cdpCall(wsURL, method string, params any) (json.RawMessage, error) {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("нет адреса DevTools")
	}
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(4 * time.Second))
	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		u.RequestURI(), u.Host, base64.StdEncoding.EncodeToString(key))
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "101") {
		return nil, errors.New("DevTools не принял подключение")
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if l == "\r\n" {
			break
		}
	}
	msg, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	// Кадр клиента обязательно маскируется
	frame := []byte{0x81}
	switch n := len(msg); {
	case n < 126:
		frame = append(frame, byte(0x80|n))
	case n < 65536:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	frame = append(frame, mask...)
	for i, c := range msg {
		frame = append(frame, c^mask[i%4])
	}
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return nil, err
		}
		n := int64(hdr[1] & 0x7f)
		if n == 126 {
			b := make([]byte, 2)
			io.ReadFull(br, b)
			n = int64(binary.BigEndian.Uint16(b))
		} else if n == 127 {
			b := make([]byte, 8)
			io.ReadFull(br, b)
			n = int64(binary.BigEndian.Uint64(b))
		}
		if n > 16<<20 {
			return nil, errors.New("слишком большой ответ")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, err
		}
		if hdr[0]&0x0f != 1 {
			continue
		}
		var r struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &r) == nil && r.ID == 1 {
			if r.Error != nil {
				return nil, errors.New(r.Error.Message)
			}
			return r.Result, nil
		}
	}
}

const pauseJS = `(() => { document.querySelectorAll('video,audio').forEach((m) => { try { m.pause(); } catch (e) {} }); })()`

// pauseProfile ставит на паузу всё видео и звук в окнах профиля (и во вложенных плеерах).
func pauseProfile(dir string) {
	list, _ := cdpTargets(dir)
	var wg sync.WaitGroup
	for _, t := range list {
		if (t.Type == "page" || t.Type == "iframe") && t.WS != "" {
			wg.Add(1)
			go func(ws string) {
				defer wg.Done()
				cdpCall(ws, "Runtime.evaluate", map[string]any{"expression": pauseJS})
			}(t.WS)
		}
	}
	wg.Wait()
}

// sameSite: youtube.com/tv и www.youtube.com — один и тот же сервис.
func sameSite(a, b string) bool {
	ha, hb := hostOf(a), hostOf(b)
	return ha != "" && ha == hb
}

func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	h := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(h, ".")
	if len(parts) > 2 {
		h = strings.Join(parts[len(parts)-2:], ".")
	}
	return h
}

// windowOfProfile ищет окно профиля с заданным заголовком (или любое, если заголовок пуст).
func windowOfProfile(dir, title string) uintptr {
	all, _ := profileProcs(dir)
	pids := map[uint32]bool{}
	for _, p := range all {
		pids[p] = true
	}
	enumMu.Lock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	list := enumList
	enumMu.Unlock()
	var any uintptr
	for _, it := range list {
		if !pids[it.pid] {
			continue
		}
		if any == 0 {
			any = it.hwnd
		}
		if title != "" && (it.Title == title || strings.HasPrefix(it.Title, title)) {
			return it.hwnd
		}
	}
	if title == "" {
		return any
	}
	return 0
}

// fullscreenTarget разворачивает окно сайта на весь экран через DevTools.
func fullscreenTarget(port, targetID string) {
	ws := browserWS(port)
	if ws == "" {
		return
	}
	res, err := cdpCall(ws, "Browser.getWindowForTarget", map[string]any{"targetId": targetID})
	if err != nil {
		return
	}
	var w struct {
		WindowID int `json:"windowId"`
	}
	json.Unmarshal(res, &w)
	if w.WindowID != 0 {
		cdpCall(ws, "Browser.setWindowBounds", map[string]any{"windowId": w.WindowID, "bounds": map[string]any{"windowState": "fullscreen"}})
	}
}

var (
	webLaunchMu sync.Mutex
	appWinMu    sync.Mutex
	appWins     = map[string]uintptr{} // приложение → его окно (запоминаем сразу после открытия)
)

// browserWindows — все видимые окна Edge/Chrome сейчас.
func browserWindows() map[uintptr]bool {
	enumMu.Lock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	list := enumList
	enumMu.Unlock()
	out := map[uintptr]bool{}
	for _, it := range list {
		if it.exe == "msedge.exe" || it.exe == "chrome.exe" {
			out[it.hwnd] = true
		}
	}
	return out
}

func rememberAppWindow(id string, h uintptr) {
	appWinMu.Lock()
	appWins[id] = h
	appWinMu.Unlock()
}

// openWebApp выводит уже открытое окно сервиса или открывает новое.
func openWebApp(a App) error {
	webLaunchMu.Lock()
	defer webLaunchMu.Unlock()
	browser := findBrowser()
	if browser == "" {
		go func() { time.Sleep(2500 * time.Millisecond); curtainDown(0) }()
		return windows.ShellExecute(0, nil, windows.StringToUTF16Ptr(a.Target), nil, nil, 1)
	}
	dir := profileDir(appProfile(a))
	prepareRuService(a)
	// Edge, запущенный без нужной настройки обхода VPN, перезапускаем
	if _, main := profileProcs(dir); main != 0 && bypassVPN(a) != strings.Contains(processCmdline(main), "--no-proxy-server") {
		killProfile(dir)
		time.Sleep(700 * time.Millisecond)
		appWinMu.Lock()
		delete(appWins, a.ID)
		appWinMu.Unlock()
	}
	// 1. Окно этого приложения уже открыто — просто показываем его
	appWinMu.Lock()
	h := appWins[a.ID]
	appWinMu.Unlock()
	if h != 0 {
		if alive, _, _ := procIsWindow.Call(h); alive != 0 {
			focusWindow(h)
			go func() { time.Sleep(600 * time.Millisecond); curtainDown(h) }() // окно успевает развернуться
			return nil
		}
	}
	if _, main := profileProcs(dir); main != 0 {
		list, port := cdpTargets(dir)
		if port == "" {
			// Edge открыт прежней версией TVHub, и им нельзя управлять — перезапускаем его один раз
			killProfile(dir)
			time.Sleep(700 * time.Millisecond)
		}
		// 2. Ищем окно сайта через Edge (например, после перезапуска TVHub)
		for _, t := range list {
			if t.Type == "page" && sameSite(t.URL, a.Target) {
				h := windowOfProfile(dir, t.Title)
				if h != 0 {
					focusWindow(h)
					rememberAppWindow(a.ID, h)
				} else if t.WS != "" {
					cdpCall(t.WS, "Page.bringToFront", map[string]any{})
				}
				fullscreenTarget(port, t.ID)
				go func() { time.Sleep(900 * time.Millisecond); curtainDown(h) }()
				return nil
			}
		}
	}
	before := browserWindows()
	// Edge уже запущен — открываем окно в фоне, за TVHub, и показываем, когда страница загрузится
	if _, main := profileProcs(dir); main != 0 {
		if _, port := cdpTargets(dir); port != "" {
			if ws := browserWS(port); ws != "" {
				res, err := cdpCall(ws, "Target.createTarget", map[string]any{"url": a.Target, "newWindow": true, "background": true})
				var t struct {
					TargetID string `json:"targetId"`
				}
				if err == nil && json.Unmarshal(res, &t) == nil && t.TargetID != "" {
					go revealWhenReady(dir, a, before, t.TargetID)
					return nil
				}
			}
		}
	}
	// Новый Edge открываем за пределами экрана — без белого окна поверх TVHub
	args := []string{
		"--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check",
		"--window-position=-32000,-32000", "--window-size=1280,720",
		"--autoplay-policy=no-user-gesture-required",
		"--remote-debugging-port=0", "--remote-allow-origins=http://127.0.0.1",
		"--app=" + a.Target,
	}
	if a.UserAgent != "" {
		args = append(args, "--user-agent="+a.UserAgent)
	}
	if bypassVPN(a) {
		args = append(args, "--no-proxy-server") // в обход системного прокси VPN
	}
	cfgMu.Lock()
	dark := cfg.Settings.DarkSites
	cfgMu.Unlock()
	if dark {
		args = append(args, "--enable-features=WebContentsForceDark") // тёмные сайты
	}
	c := exec.Command(browser, args...)
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait()
	go revealWhenReady(dir, a, before, "")
	return nil
}

// tvhubMonitor — экран, на котором открыт TVHub (туда и выводим окно сервиса).
func tvhubMonitor() rect {
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	return mi.Monitor
}

// revealWhenReady ждёт, пока страница сервиса загрузится, и только тогда выводит окно
// на весь экран. Пока окно грузится, TVHub показывает заставку перехода.
func revealWhenReady(dir string, a App, before map[uintptr]bool, targetID string) {
	var shown uintptr
	defer func() { curtainDown(shown) }() // TVHub уходит назад, только когда окно готово
	start := time.Now()
	var port string
	var t cdpTarget
	for time.Since(start) < 12*time.Second {
		list, p := cdpTargets(dir)
		port = p
		for _, x := range list {
			if x.Type == "page" && ((targetID != "" && x.ID == targetID) || (targetID == "" && sameSite(x.URL, a.Target))) {
				t = x
			}
		}
		if t.WS != "" {
			res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true})
			s := string(res)
			if err == nil && (strings.Contains(s, `"complete"`) || (strings.Contains(s, `"interactive"`) && time.Since(start) > 4*time.Second)) {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(450 * time.Millisecond) // даём странице отрисоваться
	r := tvhubMonitor()
	moved := false
	if port != "" && t.ID != "" {
		if ws := browserWS(port); ws != "" {
			if res, err := cdpCall(ws, "Browser.getWindowForTarget", map[string]any{"targetId": t.ID}); err == nil {
				var w struct {
					WindowID int `json:"windowId"`
				}
				json.Unmarshal(res, &w)
				if w.WindowID != 0 {
					cdpCall(ws, "Browser.setWindowBounds", map[string]any{"windowId": w.WindowID, "bounds": map[string]any{"windowState": "normal"}})
					cdpCall(ws, "Browser.setWindowBounds", map[string]any{"windowId": w.WindowID, "bounds": map[string]any{
						"left": r.Left, "top": r.Top, "width": r.Right - r.Left, "height": r.Bottom - r.Top}})
					_, err := cdpCall(ws, "Browser.setWindowBounds", map[string]any{"windowId": w.WindowID, "bounds": map[string]any{"windowState": "fullscreen"}})
					moved = err == nil
				}
			}
		}
	}
	var h uintptr
	for i := 0; i < 15 && h == 0; i++ {
		for w := range browserWindows() {
			if !before[w] {
				h = w
			}
		}
		if h == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if h == 0 {
		return
	}
	if !moved { // DevTools недоступен — переносим окно на экран сами
		procSetWindowPos.Call(h, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpNoZOrder|swpShowWindow)
		procShowWindow.Call(h, 3) // SW_MAXIMIZE
	}
	rememberAppWindow(a.ID, h)
	focusWindow(h)                     // пока за TVHub — он ещё поверх всех окон
	time.Sleep(900 * time.Millisecond) // окно успевает перейти на весь экран и отрисоваться
	shown = h
}

// pauseWebApps — на «Домой»: пауза во всех сервисах, кроме музыки.
func pauseWebApps() {
	entries, _ := os.ReadDir(dataDir)
	var wg sync.WaitGroup
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || !strings.HasPrefix(n, "browser") || n == "browser-music" {
			continue
		}
		wg.Add(1)
		go func(dir string) { defer wg.Done(); pauseProfile(dir) }(profileDir(n))
	}
	wg.Wait()
}

// closeAllWebApps — при выходе из TVHub закрываем окна сервисов (музыку тоже).
func closeAllWebApps() {
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "browser") {
			killProfile(profileDir(e.Name()))
		}
	}
}

// restartWebApps закрывает окна сайтов (кроме музыки), чтобы новые настройки Edge применились.
func restartWebApps() {
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "browser") && e.Name() != "browser-music" {
			killProfile(profileDir(e.Name()))
		}
	}
}
