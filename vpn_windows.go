//go:build windows

// Переключатель VPN (Happ): узнаём, запущен ли он, запускаем и закрываем.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
		if st, err := os.Stat(p); err == nil {
			if !st.IsDir() {
				return p
			}
			// Указана папка — ищем Happ.exe внутри неё
			for _, pat := range []string{`Happ*.exe`, `happ*.exe`, `*\Happ*.exe`, `*.exe`} {
				if f := pickHappExe(filepath.Join(p, pat)); f != "" {
					return f
				}
			}
		}
	}
	return findVPNAuto()
}

var (
	vpnAutoMu   sync.Mutex
	vpnAutoPath string
	vpnAutoAt   time.Time
)

// findVPNAuto ищет Happ, если путь в настройках не указан: запущенная программа, ссылки happ://,
// список установленных программ, обычные папки, ярлыки в «Пуске» и на рабочем столе.
// Найденный путь запоминается на минуту, чтобы не искать при каждой проверке состояния.
func findVPNAuto() string {
	vpnAutoMu.Lock()
	defer vpnAutoMu.Unlock()
	if vpnAutoPath != "" && time.Since(vpnAutoAt) < time.Minute {
		if _, err := os.Stat(vpnAutoPath); err == nil {
			return vpnAutoPath
		}
	}
	if vpnAutoPath == "" && time.Since(vpnAutoAt) < 15*time.Second {
		return "" // недавно искали и не нашли
	}
	p := searchHapp()
	vpnAutoPath, vpnAutoAt = p, time.Now()
	if p != "" {
		log.Printf("VPN: Happ найден: %s", p)
	}
	return p
}

func searchHapp() string {
	okExe := func(f string) string {
		f = strings.Trim(strings.TrimSpace(f), `"`)
		if i := strings.LastIndex(strings.ToLower(f), ".exe"); i >= 0 {
			f = f[:i+4]
		}
		if st, err := os.Stat(f); err == nil && !st.IsDir() && strings.HasSuffix(strings.ToLower(f), ".exe") {
			return f
		}
		return ""
	}
	// 1. Happ уже запущен — берём путь у процесса
	for _, pr := range allProcs() {
		if strings.HasPrefix(strings.ToLower(pr.exe), "happ") && pr.path != "" {
			if f := okExe(pr.path); f != "" {
				return f
			}
		}
	}
	// 2. Happ регистрирует ссылки happ:// — в реестре записано, какой программой их открывать
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, `Software\Classes\happ\shell\open\command`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		cmd, _, _ := k.GetStringValue("")
		k.Close()
		if m := exeInCmd.FindString(cmd); m != "" {
			if f := okExe(m); f != "" {
				return f
			}
		}
	}
	// 3. Список установленных программ (то, что видно в «Приложения и возможности»)
	for _, loc := range []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	} {
		k, err := registry.OpenKey(loc.root, loc.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range names {
			sk, err := registry.OpenKey(loc.root, loc.path+`\`+n, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			name, _, _ := sk.GetStringValue("DisplayName")
			icon, _, _ := sk.GetStringValue("DisplayIcon")
			dir, _, _ := sk.GetStringValue("InstallLocation")
			sk.Close()
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "happ") {
				continue
			}
			if f := okExe(strings.SplitN(icon, ",", 2)[0]); f != "" && !strings.Contains(strings.ToLower(filepath.Base(f)), "unins") {
				return f
			}
			if dir = strings.Trim(dir, `" `); dir != "" {
				for _, pat := range []string{`Happ*.exe`, `*\Happ*.exe`} {
					if f := pickHappExe(filepath.Join(dir, pat)); f != "" {
						return f
					}
				}
			}
		}
	}
	// 4. Обычные папки установки
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "APPDATA"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v, filepath.Join(v, "Programs"))
		}
	}
	for _, r := range roots {
		for _, pat := range []string{`Happ*\Happ*.exe`, `Happ*\*\Happ*.exe`, `*\Happ*.exe`} {
			if f := pickHappExe(filepath.Join(r, pat)); f != "" && strings.HasPrefix(strings.ToLower(filepath.Base(f)), "happ") {
				return f
			}
		}
	}
	// 5. Ярлыки в меню «Пуск» и на рабочем столе
	var lnks []string
	for _, d := range []string{
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`),
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`),
		filepath.Join(os.Getenv("USERPROFILE"), "Desktop"),
		filepath.Join(os.Getenv("PUBLIC"), "Desktop"),
	} {
		for _, pat := range []string{`Happ*.lnk`, `*\Happ*.lnk`} {
			m, _ := filepath.Glob(filepath.Join(d, pat))
			lnks = append(lnks, m...)
		}
	}
	for _, l := range lnks {
		if f := okExe(lnkTarget(l)); f != "" {
			return f
		}
	}
	return ""
}

var exeInCmd = regexp.MustCompile(`(?i)[a-z]:\\[^"*?<>|]+?\.exe`)

// lnkTarget достаёт путь к программе из ярлыка Windows (.lnk) — ищем в файле строку «X:\…\*.exe».
func lnkTarget(lnk string) string {
	b, err := os.ReadFile(lnk)
	if err != nil || len(b) > 1<<20 {
		return ""
	}
	if m := exeInCmd.Find(b); m != nil { // путь в однобайтовой кодировке
		return string(m)
	}
	// путь в UTF-16
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	for _, off := range []int{0, 1} {
		if off == 1 && len(b) > 2 {
			u = make([]uint16, (len(b)-1)/2)
			for i := range u {
				u[i] = uint16(b[2*i+1]) | uint16(b[2*i+2])<<8
			}
		}
		if m := exeInCmd.FindString(string(utf16Decode(u))); m != "" {
			return m
		}
	}
	return ""
}

func utf16Decode(u []uint16) []rune {
	out := make([]rune, 0, len(u))
	for _, c := range u {
		if c == 0 {
			out = append(out, '"') // разделитель строк
			continue
		}
		out = append(out, rune(c))
	}
	return out
}

// pickHappExe — первый подходящий exe по шаблону (без установщиков и служебных программ).
func pickHappExe(pattern string) string {
	m, _ := filepath.Glob(pattern)
	var fallback string
	for _, f := range m {
		low := strings.ToLower(filepath.Base(f))
		if strings.Contains(low, "unins") || strings.Contains(low, "update") || strings.Contains(low, "helper") ||
			strings.Contains(low, "crash") || strings.Contains(low, "setup") || strings.Contains(low, "service") {
			continue
		}
		if strings.HasPrefix(low, "happ") {
			return f
		}
		if fallback == "" {
			fallback = f
		}
	}
	return fallback
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

var tunRe = regexp.MustCompile(`(?i)happ|xray|sing-?box|wintun|v2ray|clash|\btun\b|tap-windows|wireguard|outline`)

// vpnTunnel — сетевой адаптер VPN, если он поднят. Смотрим и на имя, и на описание адаптера:
// у адаптера Happ имя может быть обычным («Ethernet 3»), а описание — «Wintun Userspace Tunnel» и т. п.
func vpnTunnel() string {
	var size uint32 = 15000
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, aa, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			break
		}
		for a := aa; a != nil; a = a.Next {
			if a.OperStatus != windows.IfOperStatusUp || a.FirstUnicastAddress == nil {
				continue
			}
			name, desc := windows.UTF16PtrToString(a.FriendlyName), windows.UTF16PtrToString(a.Description)
			low := strings.ToLower(name + " " + desc)
			if strings.Contains(low, "teredo") || strings.Contains(low, "isatap") || strings.Contains(low, "6to4") {
				continue
			}
			if tunRe.MatchString(name) || tunRe.MatchString(desc) {
				return name
			}
		}
		return ""
	}
	// Запасной способ — по именам адаптеров
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

var (
	ruCheckMu  sync.Mutex
	ruCheckAt  time.Time
	ruCheckFor bool
	ruCheckOK  bool
)

// ruTrafficForeign — идут ли российские сайты через VPN: спрашиваем Яндекс, с какого адреса
// к нему приходят, и узнаём страну этого адреса. Учитывает и правила Happ «российское — напрямую».
// known=false — проверить не получилось. Ответ живёт 20 секунд.
func ruTrafficForeign() (foreign, known bool) {
	ruCheckMu.Lock()
	defer ruCheckMu.Unlock()
	if time.Since(ruCheckAt) < 20*time.Second {
		return ruCheckFor, ruCheckOK
	}
	c := &http.Client{Timeout: 3 * time.Second}
	get := func(u string) string {
		resp, err := c.Get(u)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ""
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return strings.Trim(strings.TrimSpace(string(b)), `"`)
	}
	ip := ""
	for _, u := range []string{"https://yandex.ru/internet/api/v0/ip", "https://ipv4-internet.yandex.net/api/v0/ip"} {
		if ip = get(u); net.ParseIP(ip) != nil {
			break
		}
		ip = ""
	}
	cc := ""
	if ip != "" {
		for _, u := range []string{"http://ip-api.com/line/" + ip + "?fields=countryCode", "https://ipinfo.io/" + ip + "/country"} {
			if v := strings.ToUpper(get(u)); len(v) == 2 {
				cc = v
				break
			}
		}
	}
	ruCheckAt, ruCheckOK, ruCheckFor = time.Now(), cc != "", cc != "" && cc != "RU"
	if cc != "" {
		log.Printf("VPN: Яндекс видит адрес %s (%s)", ip, cc)
	}
	return ruCheckFor, ruCheckOK
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
	defer func() { // VPN переключился — прошлая проверка «откуда видят российские сайты» уже неверна
		ruCheckMu.Lock()
		ruCheckAt = time.Time{}
		ruCheckMu.Unlock()
	}()
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

// systemProxyURL — локальный прокси VPN (Happ) в виде адреса для запросов TVHub, если он включён.
// В настройках Windows он записан как «127.0.0.1:10809» или «http=127.0.0.1:10809;https=…».
func systemProxyURL() string {
	p := localProxy()
	if p == "" {
		return ""
	}
	if strings.Contains(p, "=") {
		var pick string
		for _, part := range strings.Split(p, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 && (kv[0] == "https" || (kv[0] == "http" && pick == "")) {
				pick = kv[1]
			}
		}
		p = pick
	}
	if p == "" {
		return ""
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	return p
}
