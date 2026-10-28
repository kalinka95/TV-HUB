//go:build windows

// Яркость экрана: сначала через DDC/CI (сам монитор или телевизор меняет подсветку),
// если экран так не умеет — программно, через гамму видеокарты (работает с любым экраном).
package main

import (
	"log"
	"sync"
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
		d := displayInfo{hmon: h, device: windows.UTF16ToString(mi.Device[:]), Name: windows.UTF16ToString(mi.Device[:])}
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
	for i := range ds {
		if ds[i].Method == "ddc" && setDDC(ds[i].hmon, level) {
			setGamma(ds[i].device, 100) // программное затемнение больше не нужно
			continue
		}
		ds[i].Method = "gamma"
		if got := setGamma(ds[i].device, level); got == 0 {
			ds[i].Method = "none"
			log.Printf("яркость: экран %s не поддерживает изменение", ds[i].Name)
		} else if got != level {
			ds[i].Method = "gamma-limited"
		}
	}
	return ds
}

// resetBrightness возвращает обычную картинку (гамму) при выходе из TVHub.
func resetBrightness() {
	brMu.Lock()
	defer brMu.Unlock()
	for _, d := range listDisplays() {
		setGamma(d.device, 100)
	}
}
