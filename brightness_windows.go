//go:build windows

// Яркость экрана: сначала через DDC/CI (сам монитор или телевизор меняет подсветку).
// Если экран так не умеет — программно: гамма видеокарты (Windows позволяет примерно до 60%)
// плюс полупрозрачный тёмный слой поверх экрана, сквозь который проходят нажатия.
package main

import (
	"log"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dxva2                    = windows.NewLazySystemDLL("dxva2.dll")
	procNumPhysical          = dxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	procGetPhysical          = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procDestroyPhysical      = dxva2.NewProc("DestroyPhysicalMonitors")
	procGetMonitorBrightness = dxva2.NewProc("GetMonitorBrightness")
	procSetMonitorBrightness = dxva2.NewProc("SetMonitorBrightness")
	procEnumDisplayMonitors  = user32.NewProc("EnumDisplayMonitors")
	gdi32                    = windows.NewLazySystemDLL("gdi32.dll")
	procCreateDC             = gdi32.NewProc("CreateDCW")
	procDeleteDC             = gdi32.NewProc("DeleteDC")
	procSetDeviceGammaRamp   = gdi32.NewProc("SetDeviceGammaRamp")
)

type physicalMonitor struct {
	Handle windows.Handle
	Desc   [128]uint16
}

type monitorInfoEx struct {
	CbSize  uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

type displayInfo struct {
	Name   string `json:"name"`
	Method string `json:"method"` // ddc — подсветка экрана, gamma — программное затемнение
	hmon   uintptr
	device string
	rc     rect
}

var (
	brMu       sync.Mutex
	brMonitors []uintptr
	brCB       = windows.NewCallback(func(h, hdc, r, l uintptr) uintptr {
		brMonitors = append(brMonitors, h)
		return 1
	})
)

func listDisplays() []displayInfo {
	brMonitors = nil
	procEnumDisplayMonitors.Call(0, 0, brCB, 0)
	var out []displayInfo
	for _, h := range brMonitors {
		mi := monitorInfoEx{CbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		procGetMonitorInfo.Call(h, uintptr(unsafe.Pointer(&mi)))
		d := displayInfo{hmon: h, device: windows.UTF16ToString(mi.Device[:]), Name: windows.UTF16ToString(mi.Device[:]), rc: mi.Monitor}
		var n uint32
		if r, _, _ := procNumPhysical.Call(h, uintptr(unsafe.Pointer(&n))); r != 0 && n > 0 {
			mons := make([]physicalMonitor, n)
			if r, _, _ := procGetPhysical.Call(h, uintptr(n), uintptr(unsafe.Pointer(&mons[0]))); r != 0 {
				if desc := windows.UTF16ToString(mons[0].Desc[:]); desc != "" && desc != "Generic PnP Monitor" {
					d.Name = desc
				}
				var mn, cur, mx uint32
				if r, _, _ := procGetMonitorBrightness.Call(uintptr(mons[0].Handle), uintptr(unsafe.Pointer(&mn)), uintptr(unsafe.Pointer(&cur)), uintptr(unsafe.Pointer(&mx))); r != 0 && mx > mn {
					d.Method = "ddc"
				}
				procDestroyPhysical.Call(uintptr(n), uintptr(unsafe.Pointer(&mons[0])))
			}
		}
		if d.Method == "" {
			d.Method = "gamma"
		}
		out = append(out, d)
	}
	return out
}

func setDDC(h uintptr, level int) bool {
	var n uint32
	if r, _, _ := procNumPhysical.Call(h, uintptr(unsafe.Pointer(&n))); r == 0 || n == 0 {
		return false
	}
	mons := make([]physicalMonitor, n)
	if r, _, _ := procGetPhysical.Call(h, uintptr(n), uintptr(unsafe.Pointer(&mons[0]))); r == 0 {
		return false
	}
	defer procDestroyPhysical.Call(uintptr(n), uintptr(unsafe.Pointer(&mons[0])))
	ok := false
	for _, m := range mons {
		var mn, cur, mx uint32
		if r, _, _ := procGetMonitorBrightness.Call(uintptr(m.Handle), uintptr(unsafe.Pointer(&mn)), uintptr(unsafe.Pointer(&cur)), uintptr(unsafe.Pointer(&mx))); r == 0 || mx <= mn {
			continue
		}
		v := mn + uint32(level)*(mx-mn)/100
		if r, _, _ := procSetMonitorBrightness.Call(uintptr(m.Handle), uintptr(v)); r != 0 {
			ok = true
		}
	}
	return ok
}

// setGamma затемняет картинку через гамму видеокарты. Windows не даёт сделать её слишком
// тёмной, поэтому при отказе пробуем чуть светлее.
func setGamma(device string, level int) int {
	dev, _ := windows.UTF16PtrFromString(device)
	disp, _ := windows.UTF16PtrFromString("DISPLAY")
	hdc, _, _ := procCreateDC.Call(uintptr(unsafe.Pointer(disp)), uintptr(unsafe.Pointer(dev)), 0, 0)
	if hdc == 0 {
		return 0
	}
	defer procDeleteDC.Call(hdc)
	for lv := level; lv <= 100; lv += 5 {
		var ramp [3][256]uint16
		for i := 0; i < 256; i++ {
			v := i * 257 * lv / 100
			if v > 65535 {
				v = 65535
			}
			ramp[0][i], ramp[1][i], ramp[2][i] = uint16(v), uint16(v), uint16(v)
		}
		if r, _, _ := procSetDeviceGammaRamp.Call(hdc, uintptr(unsafe.Pointer(&ramp))); r != 0 {
			return lv
		}
	}
	return 0
}

// applyBrightness ставит яркость (10–100) на всех экранах и сообщает, каким способом.
func applyBrightness(level int) []displayInfo {
	if level < 10 {
		level = 10
	}
	if level > 100 {
		level = 100
	}
	brMu.Lock()
	defer brMu.Unlock()
	ds := listDisplays()
	active := map[string]bool{}
	for i := range ds {
		if ds[i].Method == "ddc" && setDDC(ds[i].hmon, level) {
			setGamma(ds[i].device, 100) // программное затемнение больше не нужно
			setOverlay(ds[i].device, ds[i].rc, 0)
			active[ds[i].device] = true
			continue
		}
		// Гамма даёт часть затемнения, остальное — тёмный слой поверх экрана
		got := setGamma(ds[i].device, level)
		if got == 0 {
			got = 100
		}
		rest := level * 100 / got // какая доля яркости ещё нужна, %
		alpha := 255 * (100 - rest) / 100
		ds[i].Method = "gamma"
		if alpha > 0 {
			ds[i].Method = "overlay"
		}
		active[ds[i].device] = true
		setOverlay(ds[i].device, ds[i].rc, alpha)
	}
	// Слои на отключённых экранах убираем
	for dev := range dimWins {
		if !active[dev] {
			setOverlay(dev, rect{}, 0)
		}
	}
	return ds
}

// ---------- Тёмный слой поверх экрана ----------

var (
	procCreateWindowEx  = user32.NewProc("CreateWindowExW")
	procRegisterClassEx = user32.NewProc("RegisterClassExW")
	procDefWindowProc   = user32.NewProc("DefWindowProcW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
	procSetLayeredAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procGetStockObject  = gdi32.NewProc("GetStockObject")
	dimWins             = map[string]uintptr{}
	dimAlpha            = map[string]int{}
	dimClassOnce        sync.Once
	dimClassName, _     = windows.UTF16PtrFromString("TVHubDimmer")
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

// onUI выполняет f в главном потоке окна (там работает цикл сообщений) и ждёт результата.
func onUI(f func()) {
	if wv == nil {
		return
	}
	done := make(chan struct{})
	wv.Dispatch(func() { f(); close(done) })
	<-done
}

func setOverlay(device string, r rect, alpha int) {
	if alpha < 0 {
		alpha = 0
	}
	if alpha > 235 {
		alpha = 235 // не делаем экран совсем чёрным
	}
	onUI(func() {
		h := dimWins[device]
		if alpha == 0 {
			if h != 0 {
				procDestroyWindow.Call(h)
				delete(dimWins, device)
				delete(dimAlpha, device)
			}
			return
		}
		if h == 0 {
			dimClassOnce.Do(func() {
				var inst windows.Handle
				windows.GetModuleHandleEx(0, nil, &inst)
				black, _, _ := procGetStockObject.Call(4) // BLACK_BRUSH
				wc := wndClassEx{WndProc: procDefWindowProc.Addr(), Instance: inst, Background: windows.Handle(black), ClassName: dimClassName}
				wc.Size = uint32(unsafe.Sizeof(wc))
				procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
			})
			const exStyle = 0x00080000 | 0x00000020 | 0x00000008 | 0x00000080 | 0x08000000 // layered, transparent, topmost, toolwindow, noactivate
			h, _, _ = procCreateWindowEx.Call(exStyle, uintptr(unsafe.Pointer(dimClassName)), 0, wsPopup,
				uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0, 0, 0, 0)
			if h == 0 {
				log.Printf("яркость: не удалось создать затемняющий слой")
				return
			}
			dimWins[device] = h
		}
		procSetLayeredAttrs.Call(h, 0, uintptr(alpha), 2) // LWA_ALPHA
		dimAlpha[device] = alpha
		topmost := ^uintptr(0)                                                                                                              // HWND_TOPMOST
		procSetWindowPos.Call(h, topmost, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0010|0x0040) // NOACTIVATE|SHOWWINDOW
	})
}

// keepDimmerOnTop следит, чтобы слой оставался поверх окон, которые сами встают «поверх всех».
func keepDimmerOnTop() {
	for {
		time.Sleep(3 * time.Second)
		if wv == nil {
			continue
		}
		wv.Dispatch(func() {
			for _, h := range dimWins {
				procSetWindowPos.Call(h, ^uintptr(0), 0, 0, 0, 0, 0x0001|0x0002|0x0010) // NOSIZE|NOMOVE|NOACTIVATE
			}
		})
	}
}

// resetBrightness возвращает обычную картинку (гамму) при выходе из TVHub.
func resetBrightness() {
	brMu.Lock()
	defer brMu.Unlock()
	for _, d := range listDisplays() {
		setGamma(d.device, 100)
	}
	for dev, h := range dimWins { // вызывается из главного потока после выхода из цикла окна
		procDestroyWindow.Call(h)
		delete(dimWins, dev)
	}
}

func startDimmer() { go keepDimmerOnTop() }
