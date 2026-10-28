//go:build windows

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const platformName = "windows"

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	procGetWindowLongPtr  = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr  = user32.NewProc("SetWindowLongPtrW")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo    = user32.NewProc("GetMonitorInfoW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procSetForeground     = user32.NewProc("SetForegroundWindow")
	procBringToTop        = user32.NewProc("BringWindowToTop")
	procIsIconic          = user32.NewProc("IsIconic")
	procRegisterHotKey    = user32.NewProc("RegisterHotKey")
	procGetMessage        = user32.NewProc("GetMessageW")
	procFindWindow        = user32.NewProc("FindWindowW")
	procMessageBox        = user32.NewProc("MessageBoxW")
	procSetSuspendState   = windows.NewLazySystemDLL("powrprof.dll").NewProc("SetSuspendState")
)

const (
	gwlStyle        = ^uintptr(15) // -16
	wsPopup         = 0x80000000
	wsVisible       = 0x10000000
	wsOverlappedWnd = 0x00CF0000
	swpFrameChanged = 0x0020
	swpNoZOrder     = 0x0004
	swpShowWindow   = 0x0040
	monitorNearest  = 2
	swRestore       = 9
	swMinimize      = 6
	wmHotkey        = 0x0312
	modControl      = 0x0002
	modShift        = 0x0004
	modNoRepeat     = 0x4000
	vkBrowserHome   = 0xAC
	createNoWindow  = 0x08000000
	windowClassName = "webview"
	windowTitle     = "TVHub"
)

type rect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	CbSize  uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

var (
	wv         webview2.WebView
	hwnd       uintptr
	fsMu       sync.Mutex
	fsOn       bool
	savedRect  rect
	savedStyle uintptr
)

func appDataDir() string {
	if d := os.Getenv("APPDATA"); d != "" {
		return filepath.Join(d, "TVHub")
	}
	return filepath.Join(os.TempDir(), "TVHub")
}

func msgBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(windowTitle)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10)
}

// ---------- Полноэкранный режим без рамок ----------

func setFullscreen(on bool) {
	fsMu.Lock()
	defer fsMu.Unlock()
	if hwnd == 0 || on == fsOn {
		return
	}
	if on {
		savedStyle, _, _ = procGetWindowLongPtr.Call(hwnd, gwlStyle)
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&savedRect)))
		mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
		mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
		procSetWindowLongPtr.Call(hwnd, gwlStyle, wsPopup|wsVisible)
		r := mi.Monitor
		procSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top),
			uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpFrameChanged|swpShowWindow)
	} else {
		style := savedStyle
		if style == 0 {
			style = wsOverlappedWnd | wsVisible
		}
		procSetWindowLongPtr.Call(hwnd, gwlStyle, style)
		r := savedRect
		procSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top),
			uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), swpFrameChanged|swpShowWindow|swpNoZOrder)
	}
	fsOn = on
}

func bringToFront() {
	if hwnd == 0 {
		return
	}
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	procBringToTop.Call(hwnd)
	procSetForeground.Call(hwnd)
}

// ---------- Запуск приложений ----------

func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c
}

func findBrowser() string {
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		cands = append(cands,
			filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
			filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// closeWebApp закрывает окна онлайн-сервисов, открытые из TVHub (при выходе и перезапуске).
func closeWebApp() { closeAllWebApps() }

func launchApp(a App) error {
	switch a.Type {
	case "music":
		return launchMusic(a)
	case "web":
		return openWebApp(a)
	case "exe":
		if a.Target == "" {
			return errors.New("не указан путь к программе")
		}
		var args *uint16
		if a.Args != "" {
			args = windows.StringToUTF16Ptr(a.Args)
		}
		dir := windows.StringToUTF16Ptr(filepath.Dir(a.Target))
		return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(a.Target), args, dir, 3)
	}
	return fmt.Errorf("неизвестный тип приложения %q", a.Type)
}

// ---------- Системные действия ----------

func systemAction(action string) error {
	switch action {
	case "exit":
		closeWebApp()
		wv.Dispatch(func() { wv.Terminate() })
	case "minimize":
		wv.Dispatch(func() { procShowWindow.Call(hwnd, swMinimize) })
	case "fullscreen":
		wv.Dispatch(func() { setFullscreen(!fsOn) })
	case "shutdown":
		return hiddenCmd("shutdown", "/s", "/t", "0").Start()
	case "restart":
		return hiddenCmd("shutdown", "/r", "/t", "0").Start()
	case "browser-setup":
		// Обычное окно Edge с тем же профилем, что и у сервисов: тут ставят блокировщик рекламы
		browser := findBrowser()
		if browser == "" {
			return errors.New("не найден Microsoft Edge или Google Chrome")
		}
		store := "https://microsoftedge.microsoft.com/addons/search/uBlock%20Origin%20Lite"
		if strings.Contains(strings.ToLower(browser), "chrome.exe") {
			store = "https://chromewebstore.google.com/search/uBlock%20Origin%20Lite"
		}
		c := exec.Command(browser, "--user-data-dir="+filepath.Join(dataDir, "browser"), "--no-first-run", "--no-default-browser-check", "--start-maximized", store)
		if err := c.Start(); err != nil {
			return err
		}
		go c.Wait()
	case "front":
		wv.Dispatch(bringToFront)
	case "admin-on":
		cfgMu.Lock()
		cfg.Settings.RunAsAdmin = true
		saveConfigLocked()
		cfgMu.Unlock()
		if selfElevated() {
			applyStartupMode()
			return nil
		}
		if err := relaunchElevated(); err != nil {
			cfgMu.Lock()
			cfg.Settings.RunAsAdmin = false
			saveConfigLocked()
			cfgMu.Unlock()
			return err
		}
	case "admin-off":
		cfgMu.Lock()
		cfg.Settings.RunAsAdmin = false
		auto := cfg.Settings.Autostart
		saveConfigLocked()
		cfgMu.Unlock()
		setAdminAutostart(false)
		setRunKey(auto)
		if selfElevated() {
			return relaunchNormal()
		}
	case "sleep":
		go procSetSuspendState.Call(0, 0, 0)
	default:
		return fmt.Errorf("неизвестное действие %q", action)
	}
	return nil
}

func setAutostart(on bool) error {
	cfgMu.Lock()
	admin := cfg.Settings.RunAsAdmin
	cfgMu.Unlock()
	if admin && selfElevated() {
		applyStartupMode()
		return nil
	}
	return setRunKey(on)
}

func setRunKey(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue("TVHub")
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("TVHub", `"`+exe+`"`)
}

// goHome закрывает открытый сервис и выводит TVHub на главный экран.
func goHome() {
	if wv == nil {
		return
	}
	go pauseWebApps() // окна остаются в фоне, видео и звук — на паузе
	cfgMu.Lock()
	fs := cfg.Settings.Fullscreen
	cfgMu.Unlock()
	wv.Dispatch(func() {
		bringToFront()
		if fs {
			setFullscreen(true)
		}
		wv.Eval("window.tvHome && window.tvHome()")
	})
}

// ---------- Глобальная кнопка «Домой» ----------

// Кнопка Home на Bluetooth-пульте приходит в Windows как VK_BROWSER_HOME.
// Ловим её везде: закрываем открытый сервис и возвращаемся в оболочку.
func homeHotkeyLoop() {
	runtime.LockOSThread()
	r1, _, _ := procRegisterHotKey.Call(0, 1, modNoRepeat, vkBrowserHome)
	r2, _, _ := procRegisterHotKey.Call(0, 2, modControl|modShift|modNoRepeat, 'H')
	if r1 == 0 || r2 == 0 {
		log.Printf("не удалось зарегистрировать горячую клавишу «Домой» (home=%d ctrl+shift+h=%d)", r1, r2)
	}
	type msg struct {
		Hwnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      [2]int32
		_       uint32
	}
	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		if m.Message == wmHotkey {
			goHome()
		}
	}
}

func runPlatform() {
	runtime.LockOSThread()

	// Только один экземпляр: повторный запуск выводит уже открытое окно.
	_, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(`Local\TVHubSingleInstance`))
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		cls, _ := windows.UTF16PtrFromString(windowClassName)
		ttl, _ := windows.UTF16PtrFromString(windowTitle)
		if h, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(ttl))); h != 0 {
			procShowWindow.Call(h, swRestore)
			procSetForeground.Call(h)
		}
		return
	}

	addr, err := startServer()
	if err != nil {
		msgBox("Не удалось запустить внутренний сервер: " + err.Error())
		return
	}

	os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--autoplay-policy=no-user-gesture-required --disable-features=HardwareMediaKeyHandling")
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = dataDir
	}
	wv = webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("TVHUB_DEBUG") == "1",
		AutoFocus: true,
		DataPath:  filepath.Join(local, "TVHub", "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title: windowTitle, Width: 1280, Height: 720, IconId: 1, Center: true,
		},
	})
	if wv == nil {
		msgBox("Не найден компонент Microsoft Edge WebView2.\n\nУстановите его с сайта:\nhttps://go.microsoft.com/fwlink/p/?LinkId=2124703\nи запустите TVHub снова.")
		return
	}
	defer wv.Destroy()
	hwnd = uintptr(wv.Window())

	if cfg.Settings.Fullscreen {
		setFullscreen(true)
	}
	go homeHotkeyLoop()

	wv.Navigate(strings.TrimSpace(addr))
	wv.Run()
	closeWebApp()
	resetBrightness() // программное затемнение не должно остаться после выхода
}

func hasArg(a string) bool {
	for _, x := range os.Args[1:] {
		if x == a {
			return true
		}
	}
	return false
}

// platformPreInit — до запуска серверов: дождаться прежнего экземпляра и,
// если включён «Режим администратора», перезапуститься с правами администратора.
func platformPreInit() bool {
	if hasArg("--wait-previous") {
		waitPrevious()
		// Прежний экземпляр мог сохранить настройки, пока закрывался — перечитываем
		cfgMu.Lock()
		loadConfig()
		cfgMu.Unlock()
	}
	cfgMu.Lock()
	admin := cfg.Settings.RunAsAdmin
	cfgMu.Unlock()
	if admin && !selfElevated() && !hasArg("--no-elevate") {
		exe, err := os.Executable()
		if err == nil && windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(exe),
			windows.StringToUTF16Ptr("--wait-previous"), nil, 1) == nil {
			return true // новый экземпляр с правами администратора запущен, этот закрываем
		}
		log.Printf("режим администратора: запуск отменён, работаю с обычными правами")
	}
	return false
}

// waitUIReady ждёт, пока создано окно TVHub (нужно затемняющему слою).
func waitUIReady() {
	for i := 0; i < 300 && (wv == nil || hwnd == 0); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

// showScreen выводит TVHub на экран и открывает нужную вкладку («Музыка», «Настройки»).
func showScreen(name string) {
	if wv == nil {
		return
	}
	go pauseWebApps()
	wv.Dispatch(func() {
		bringToFront()
		wv.Eval("window.tvShow && window.tvShow(" + strconv.Quote(name) + ")")
	})
}
