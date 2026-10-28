//go:build windows

// Переключатель VPN (Happ): узнаём, запущен ли он, запускаем и закрываем.
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var procInternetSetOption = windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")

// findVPN ищет программу Happ: сначала путь из настроек, потом обычные места установки.
func findVPN() string {
	cfgMu.Lock()
	p := strings.Trim(cfg.Settings.VPNPath, `" `)
	cfgMu.Unlock()
	if p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA", "APPDATA"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v, filepath.Join(v, "Programs"))
		}
	}
	for _, r := range roots {
		for _, pat := range []string{`Happ*\Happ*.exe`, `happ*\happ*.exe`, `Happ*\*\Happ*.exe`} {
			m, _ := filepath.Glob(filepath.Join(r, pat))
			for _, f := range m {
				low := strings.ToLower(filepath.Base(f))
				if strings.Contains(low, "unins") || strings.Contains(low, "update") || strings.Contains(low, "helper") {
					continue
				}
				return f
			}
		}
	}
	return ""
}

// processPIDs — идентификаторы запущенных процессов с таким именем файла.
func processPIDs(exe string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []uint32
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), exe) {
			out = append(out, pe.ProcessID)
		}
	}
	return out
}

// happProcs — процессы Happ: всё, что запущено из папки Happ, плюс его ядро (xray, sing-box).
func happProcs(dir string) []procInfo {
	dir = strings.ToLower(filepath.Clean(dir)) + `\`
	var out []procInfo
	for _, p := range allProcs() {
		low := strings.ToLower(p.path)
		base := strings.ToLower(p.exe)
		if (p.path != "" && strings.HasPrefix(low, dir)) || strings.HasPrefix(base, "happ") ||
			((base == "xray.exe" || base == "sing-box.exe") && (p.path == "" || strings.HasPrefix(low, dir))) {
			out = append(out, p)
		}
	}
	return out
}

type procInfo struct {
	pid       uint32
	exe, path string
}

func allProcs() []procInfo {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []procInfo
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		out = append(out, procInfo{pid: pe.ProcessID, exe: windows.UTF16ToString(pe.ExeFile[:]), path: processExe(pe.ProcessID)})
	}
	return out
}

var tunRe = regexp.MustCompile(`(?i)happ|xray|sing|wintun|v2ray|clash|\btun`)

// vpnTunnel — имя сетевого адаптера VPN, если он поднят.
func vpnTunnel() string {
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		low := strings.ToLower(i.Name)
		if strings.Contains(low, "teredo") || strings.Contains(low, "isatap") || strings.Contains(low, "6to4") {
			continue
		}
		if i.Flags&net.FlagUp != 0 && tunRe.MatchString(i.Name) {
			if addrs, _ := i.Addrs(); len(addrs) > 0 {
				return i.Name
			}
		}
	}
	return ""
}

// localProxy — системный прокси Windows, указывающий на этот компьютер (так работает режим «системный прокси»).
func localProxy() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	s := strings.ToLower(srv)
	if en == 1 && (strings.Contains(s, "127.0.0.1") || strings.Contains(s, "localhost") || strings.Contains(s, "[::1]")) {
		return srv
	}
	return ""
}

func vpnConnected() (bool, string, string) {
	t, p := vpnTunnel(), localProxy()
	return t != "" || p != "", t, p
}

func vpnStatus() map[string]any {
	p := findVPN()
	if p == "" {
		return map[string]any{"found": false}
	}
	procs := happProcs(filepath.Dir(p))
	on, tun, proxy := vpnConnected()
	names := []string{}
	for _, x := range procs {
		names = append(names, x.exe)
	}
	return map[string]any{"found": true, "path": p, "name": "Happ", "on": on, "running": len(procs) > 0,
		"tunnel": tun, "proxy": proxy, "procs": names}
}

// showHapp открывает окно Happ (запускает, если не запущен), чтобы нажать кнопку в нём.
func showHapp(p string) {
	pids := map[uint32]bool{}
	for _, x := range happProcs(filepath.Dir(p)) {
		pids[x.pid] = true
	}
	enumMu.Lock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	list := enumList
	enumMu.Unlock()
	for _, it := range list {
		if pids[it.pid] {
			focusWindow(it.hwnd)
			return
		}
	}
	// Окна нет (Happ свёрнут в трей или не запущен) — повторный запуск обычно показывает окно
	windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(p), nil,
		windows.StringToUTF16Ptr(filepath.Dir(p)), 1)
}

func waitVPN(want bool, d time.Duration) bool {
	for t := time.Now(); time.Since(t) < d; time.Sleep(400 * time.Millisecond) {
		if on, _, _ := vpnConnected(); on == want {
			return true
		}
	}
	on, _, _ := vpnConnected()
	return on == want
}

// resetLocalProxy выключает системный прокси Windows, если он указывает на этот компьютер
// (его оставляет VPN-программа, закрытая принудительно, — иначе пропадёт интернет).
func resetLocalProxy() {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	s := strings.ToLower(srv)
	if en == 1 && (strings.Contains(s, "127.0.0.1") || strings.Contains(s, "localhost") || strings.Contains(s, "[::1]")) {
		k.SetDWordValue("ProxyEnable", 0)
		procInternetSetOption.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
		procInternetSetOption.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
	}
}

var errNeedClick = errors.New("need click")

var errHappAdmin = errors.New("Happ запущен от имени администратора, поэтому TVHub и пульт не могут им управлять. Включите «Режим администратора» в «Настройки» → «Общие», и кнопка VPN заработает полностью")

func happElevated(dir string) bool {
	for _, x := range happProcs(dir) {
		if pidElevated(x.pid) {
			return true
		}
	}
	return false
}

func setVPN(on bool) error {
	p := findVPN()
	if p == "" {
		return errors.New("Happ не найден. Укажите путь к Happ.exe в «Настройки» → «VPN»")
	}
	dir := filepath.Dir(p)
	if on {
		if ok, _, _ := vpnConnected(); ok {
			return nil
		}
		if len(happProcs(dir)) == 0 {
			// Запускаем Happ; ShellExecute сам спросит права администратора, если они нужны
			windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(p), nil,
				windows.StringToUTF16Ptr(dir), 1)
			if waitVPN(true, 8*time.Second) { // подключился сам (автоподключение)
				wv.Dispatch(bringToFront)
				return nil
			}
		}
		if !selfElevated() && happElevated(dir) {
			wv.Dispatch(bringToFront)
			return errHappAdmin
		}
		showHapp(p)
		return errNeedClick
	}

	// Выключение: закрываем Happ и его ядро
	if !selfElevated() && happElevated(dir) {
		return errHappAdmin
	}
	procs := happProcs(dir)
	for _, x := range procs {
		hiddenCmd("taskkill", "/PID", fmt.Sprint(x.pid), "/T", "/F").Run()
	}
	time.Sleep(500 * time.Millisecond)
	if left := happProcs(dir); len(left) > 0 {
		// Запущены от администратора — закрываем с правами администратора (Windows спросит подтверждение)
		var args []string
		for _, x := range left {
			args = append(args, "/PID", fmt.Sprint(x.pid))
		}
		windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr("taskkill.exe"),
			windows.StringToUTF16Ptr(strings.Join(args, " ")+" /T /F"), nil, 0)
		for i := 0; i < 40 && len(happProcs(dir)) > 0; i++ {
			time.Sleep(250 * time.Millisecond)
		}
	}
	resetLocalProxy()
	if waitVPN(false, 4*time.Second) {
		wv.Dispatch(bringToFront)
		return nil
	}
	// Туннель держит служба Happ — отключать нужно кнопкой в самом Happ
	_, tun, _ := vpnConnected()
	log.Printf("vpn: после закрытия Happ туннель остался: %s", tun)
	showHapp(p)
	return errNeedClick
}

// openVPNApp выводит окно Happ на экран (запускает, если закрыт).
func openVPNApp() error {
	p := findVPN()
	if p == "" {
		return errors.New("Happ не найден. Укажите путь к Happ.exe в «Настройки» → «VPN»")
	}
	if !selfElevated() && happElevated(filepath.Dir(p)) {
		return errHappAdmin
	}
	showHapp(p)
	return nil
}
