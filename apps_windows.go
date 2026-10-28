//go:build windows

// Открытые окна Windows для вкладки «Окна» на пульте: список, переключение, закрытие.
package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEnumWindows       = user32.NewProc("EnumWindows")
	procIsWindowVisible   = user32.NewProc("IsWindowVisible")
	procIsWindow          = user32.NewProc("IsWindow")
	procGetWindowTextLen  = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText     = user32.NewProc("GetWindowTextW")
	procGetWindow         = user32.NewProc("GetWindow")
	procGetWindowThreadPI = user32.NewProc("GetWindowThreadProcessId")
	procPostMessage       = user32.NewProc("PostMessageW")
	procAttachThreadInput = user32.NewProc("AttachThreadInput")
	procDwmGetAttr        = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
)

const (
	gwlExStyle     = ^uintptr(19) // -20
	wsExToolWindow = 0x00000080
	gwOwner        = 4
	wmClose        = 0x0010
	dwmCloaked     = 14
)

// Системные окна, которые не показываем и не даём завершать.
var systemExe = map[string]bool{
	"explorer.exe": true, "dwm.exe": true, "csrss.exe": true, "winlogon.exe": true, "searchhost.exe": true,
	"startmenuexperiencehost.exe": true, "shellexperiencehost.exe": true, "textinputhost.exe": true,
	"lockapp.exe": true, "systemsettingsbroker.exe": true, "searchapp.exe": true, "widgets.exe": true,
}

var friendlyExe = map[string]string{
	"msedge.exe": "Microsoft Edge", "chrome.exe": "Google Chrome", "firefox.exe": "Firefox", "vlc.exe": "VLC",
	"steam.exe": "Steam", "telegram.exe": "Telegram", "applicationframehost.exe": "Приложение Windows",
	"notepad.exe": "Блокнот", "mpc-hc64.exe": "MPC-HC", "potplayermini64.exe": "PotPlayer", "kodi.exe": "Kodi",
	"discord.exe": "Discord", "spotify.exe": "Spotify", "systemsettings.exe": "Параметры Windows",
}

type winItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	App    string `json:"app"`
	TVHub  bool   `json:"tvhub,omitempty"`
	Active bool   `json:"active,omitempty"`
	hwnd   uintptr
	pid    uint32
	exe    string
}

var (
	enumMu   sync.Mutex
	enumList []winItem
	enumCB   = windows.NewCallback(func(h, _ uintptr) uintptr {
		if it, ok := inspectWindow(h); ok {
			enumList = append(enumList, it)
		}
		return 1
	})
)

func getWindowTitle(h uintptr) string {
	n, _, _ := procGetWindowTextLen.Call(h)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

func processExe(pid uint32) string {
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(ph)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(ph, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func inspectWindow(h uintptr) (winItem, bool) {
	if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
		return winItem{}, false
	}
	if owner, _, _ := procGetWindow.Call(h, gwOwner); owner != 0 {
		return winItem{}, false
	}
	ex, _, _ := procGetWindowLongPtr.Call(h, gwlExStyle)
	if ex&wsExToolWindow != 0 {
		return winItem{}, false
	}
	var cloaked uint32
	procDwmGetAttr.Call(h, dwmCloaked, uintptr(unsafe.Pointer(&cloaked)), 4)
	if cloaked != 0 {
		return winItem{}, false
	}
	title := getWindowTitle(h)
	if strings.TrimSpace(title) == "" {
		return winItem{}, false
	}
	var pid uint32
	procGetWindowThreadPI.Call(h, uintptr(unsafe.Pointer(&pid)))
	exePath := processExe(pid)
	exe := strings.ToLower(filepath.Base(exePath))
	if systemExe[exe] || title == "Program Manager" {
		return winItem{}, false
	}
	app := friendlyExe[exe]
	if app == "" {
		app = strings.TrimSuffix(filepath.Base(exePath), filepath.Ext(exePath))
	}
	it := winItem{ID: fmt.Sprint(h), Title: title, App: app, hwnd: h, pid: pid, exe: exe}
	if h == hwnd {
		it.TVHub, it.Title, it.App = true, "TVHub", "Главный экран"
	}
	return it, true
}

func listWindows() []winItem {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	fg, _, _ := procGetForeground.Call()
	out := make([]winItem, 0, len(enumList))
	var tv *winItem
	for _, it := range enumList {
		it.Active = it.hwnd == fg
		if it.TVHub {
			t := it
			tv = &t
			continue
		}
		out = append(out, it)
	}
	if tv != nil {
		out = append([]winItem{*tv}, out...) // TVHub всегда первым
	}
	return out
}

func findWindow(id string) (winItem, bool) {
	for _, it := range listWindows() {
		if it.ID == id {
			return it, true
		}
	}
	return winItem{}, false
}

// Вывести окно на передний план. Windows разрешает это не всегда,
// поэтому временно подключаемся к потоку активного окна.
func focusWindow(h uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		procShowWindow.Call(h, swRestore)
	}
	fg, _, _ := procGetForeground.Call()
	fgThread, _, _ := procGetWindowThreadPI.Call(fg, 0)
	me := windows.GetCurrentThreadId()
	if fgThread != 0 && uint32(fgThread) != me {
		procAttachThreadInput.Call(uintptr(me), fgThread, 1)
		defer procAttachThreadInput.Call(uintptr(me), fgThread, 0)
	}
	procBringToTop.Call(h)
	procSetForeground.Call(h)
	if now, _, _ := procGetForeground.Call(); now != h {
		// Запасной способ: короткое нажатие Alt снимает запрет на смену окна
		sendKbd([]kbInput{{typ: inputKeyboard, vk: 0x12}, {typ: inputKeyboard, vk: 0x12, flags: keyUp}})
		procSetForeground.Call(h)
	}
}

func windowAction(id, action string) error {
	it, ok := findWindow(id)
	if !ok {
		return errors.New("окно уже закрыто")
	}
	switch action {
	case "focus":
		if it.TVHub {
			wv.Dispatch(bringToFront)
			return nil
		}
		focusWindow(it.hwnd)
	case "close":
		if it.TVHub {
			return errors.New("TVHub закрывается в меню «Система» → «Закрыть TVHub»")
		}
		procPostMessage.Call(it.hwnd, wmClose, 0, 0)
		// Если закрыли сервис, открытый из TVHub, — возвращаемся на главный экран
		go func() {
			time.Sleep(700 * time.Millisecond)
			if alive, _, _ := procIsWindow.Call(it.hwnd); alive == 0 {
				wv.Dispatch(bringToFront)
			}
		}()
	case "kill":
		if it.TVHub || systemExe[it.exe] {
			return errors.New("это окно нельзя завершить")
		}
		if err := hiddenCmd("taskkill", "/PID", fmt.Sprint(it.pid), "/T", "/F").Run(); err != nil {
			return errors.New("Windows не дала завершить программу")
		}
		wv.Dispatch(bringToFront)
	default:
		return fmt.Errorf("неизвестное действие %q", action)
	}
	return nil
}
