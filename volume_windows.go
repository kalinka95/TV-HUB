//go:build windows

// Громкость компьютера: меняем её напрямую через звуковую систему Windows (Core Audio),
// поэтому большое окно громкости Windows не появляется, а вместо него TVHub показывает
// своё окошко поверх всех программ.
package main

import (
	"errors"
	"log"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	procSelectObject        = gdi32.NewProc("SelectObject")
	procDeleteObject        = gdi32.NewProc("DeleteObject")

	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioEndpointVolume = windows.GUID{Data1: 0x5CDF2C82, Data2: 0x841E, Data3: 0x4546, Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}}
)

// comObj — объект COM: первое поле — таблица его методов.
type comObj struct{ vtbl *[32]uintptr }

func (o *comObj) call(i int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[i], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}
func (o *comObj) release() { o.call(2) }

// withVolumeDevice открывает устройство вывода звука по умолчанию.
func withVolumeDevice(f func(dev *comObj) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procCoInitializeEx.Call(0, 0); r == 0 || r == 1 {
		defer procCoUninitialize.Call()
	}
	var en *comObj
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, 23,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&en))); r != 0 || en == nil {
		return errors.New("звук Windows недоступен")
	}
	defer en.release()
	var dev *comObj
	if r := en.call(4, 0, 0, uintptr(unsafe.Pointer(&dev))); r != 0 || dev == nil {
		return errors.New("нет устройства вывода звука")
	}
	defer dev.release()
	return f(dev)
}

// withVolume открывает регулятор громкости устройства вывода звука по умолчанию.
func withVolume(f func(v *comObj) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procCoInitializeEx.Call(0, 0); r == 0 || r == 1 { // S_OK, S_FALSE
		defer procCoUninitialize.Call()
	}
	var en *comObj
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, 23, // CLSCTX_ALL
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&en))); r != 0 || en == nil {
		return errors.New("звук Windows недоступен")
	}
	defer en.release()
	var dev *comObj
	if r := en.call(4, 0, 0, uintptr(unsafe.Pointer(&dev))); r != 0 || dev == nil { // GetDefaultAudioEndpoint(eRender, eConsole) — то же, что меняют клавиши громкости
		return errors.New("нет устройства вывода звука")
	}
	defer dev.release()
	var vol *comObj
	if r := dev.call(3, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), 23, 0, uintptr(unsafe.Pointer(&vol))); r != 0 || vol == nil { // Activate
		return errors.New("нет регулятора громкости")
	}
	defer vol.release()
	return f(vol)
}

func volGet(v *comObj) (int, bool) {
	var lv float32
	var mute int32
	v.call(9, uintptr(unsafe.Pointer(&lv)))    // GetMasterVolumeLevelScalar
	v.call(15, uintptr(unsafe.Pointer(&mute))) // GetMute
	return int(math.Round(float64(lv) * 100)), mute != 0
}

// changeVolume меняет громкость на delta процентов (mute — включить или выключить звук).
// Возвращает новую громкость и выключен ли звук.
func changeVolume(delta int, toggleMute bool) (level int, muted bool, err error) {
	err = withVolume(func(v *comObj) error {
		level, muted = volGet(v)
		if toggleMute {
			muted = !muted
			v.call(14, boolArg(muted), 0) // SetMute
			return nil
		}
		level = max(0, min(100, level+delta))
		// Значение — число с плавающей точкой: Windows берёт его из регистра, куда Go кладёт и целые
		v.call(7, uintptr(math.Float32bits(float32(level)/100)), 0) // SetMasterVolumeLevelScalar
		if muted && delta > 0 {
			muted = false
			v.call(14, 0, 0)
		}
		level, muted = volGet(v)
		return nil
	})
	return
}

func boolArg(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}

// volumeAction — «громче», «тише», «без звука» с телефона и с экрана TVHub.
func volumeAction(a string) {
	delta := map[string]int{"volup": 4, "voldown": -4}[a]
	level, muted, err := changeVolume(delta, a == "mute")
	if err != nil {
		log.Printf("громкость: %v — нажимаю клавишу", err)
		tapVK(map[string]uint16{"volup": 0xAF, "voldown": 0xAE, "mute": 0xAD}[a], true)
		return
	}
	showVolumeOSD(level, muted)
}

// ---------- Окошко громкости поверх всех программ ----------

var (
	osdMu      sync.Mutex
	osdWin     uintptr
	osdGen     int
	osdVisible bool
	osdClass   sync.Once
	osdName, _ = windows.UTF16PtrFromString("TVHubVolume")
)

type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}

// osdRect — где показать окошко: сверху по центру экрана, на котором TVHub.
func osdRect() (x, y, w, h int32) {
	mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorNearest)
	procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi)))
	r := mi.Monitor
	sw, sh := r.Right-r.Left, r.Bottom-r.Top
	if sw <= 0 || sh <= 0 {
		sw, sh = 1920, 1080
	}
	w = sw * 24 / 100
	h = w / 5
	return r.Left + (sw-w)/2, r.Top + sh*6/100, w, h
}

// showVolumeOSD показывает окошко и через 1,5 секунды плавно его прячет.
func showVolumeOSD(level int, muted bool) {
	if wv == nil {
		return
	}
	x, y, w, h := osdRect()
	pix := osdBGRA(renderVolumeOSD(int(w), int(h), level, muted))
	osdMu.Lock()
	osdGen++
	gen := osdGen
	osdMu.Unlock()
	onUI(func() { osdPaint(x, y, w, h, pix, 255) })
	go func() {
		time.Sleep(1500 * time.Millisecond)
		for a := 255; a >= 0; a -= 32 {
			osdMu.Lock()
			stale := osdGen != gen
			osdMu.Unlock()
			if stale {
				return // уже показали новое значение
			}
			alpha := max(a, 0)
			onUI(func() { osdPaint(x, y, w, h, pix, alpha) })
			time.Sleep(25 * time.Millisecond)
		}
		osdMu.Lock()
		if osdGen == gen {
			onUI(func() { procShowWindow.Call(osdWin, 0); osdVisible = false })
		}
		osdMu.Unlock()
	}()
}

// osdPaint рисует картинку в окне с прозрачностью (выполняется в главном потоке).
func osdPaint(x, y, w, h int32, pix []byte, alpha int) {
	if osdWin == 0 {
		osdClass.Do(func() {
			var inst windows.Handle
			windows.GetModuleHandleEx(0, nil, &inst)
			wc := wndClassEx{WndProc: procDefWindowProc.Addr(), Instance: inst, ClassName: osdName}
			wc.Size = uint32(unsafe.Sizeof(wc))
			procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		})
		const exStyle = 0x00080000 | 0x00000020 | 0x00000008 | 0x00000080 | 0x08000000 // layered, transparent, topmost, toolwindow, noactivate
		osdWin, _, _ = procCreateWindowEx.Call(exStyle, uintptr(unsafe.Pointer(osdName)), 0, wsPopup, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, 0, 0)
		if osdWin == 0 {
			return
		}
	}
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatibleDC.Call(screen)
	defer procDeleteDC.Call(mem)
	bi := bitmapInfoHeader{Width: w, Height: -h, Planes: 1, BitCount: 32} // сверху вниз
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(mem, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return
	}
	defer procDeleteObject.Call(bmp)
	copy(unsafe.Slice((*byte)(bits), len(pix)), pix)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	type point struct{ X, Y int32 }
	type size struct{ CX, CY int32 }
	dst, src, sz := point{x, y}, point{0, 0}, size{w, h}
	blend := uint32(0) | uint32(alpha)<<16 | 1<<24 // AC_SRC_OVER, прозрачность, AC_SRC_ALPHA
	procUpdateLayeredWindow.Call(osdWin, screen, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&sz)), mem,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), 2) // ULW_ALPHA
	if !osdVisible {
		procShowWindow.Call(osdWin, 4) // SW_SHOWNOACTIVATE
		osdVisible = true
	}
	// Поверх всех окон, в том числе затемняющего слоя яркости
	procSetWindowPos.Call(osdWin, ^uintptr(0), 0, 0, 0, 0, 0x0001|0x0002|0x0010)
}
