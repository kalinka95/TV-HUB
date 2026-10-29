//go:build windows

// Куда идёт звук: список устройств вывода (динамики, телевизор по HDMI, Bluetooth-наушники)
// и переключение устройства по умолчанию — как в меню звука Windows.
package main

import (
	"errors"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procPropVariantClear = ole32.NewProc("PropVariantClear")

	// IPolicyConfig — им пользуется сама Windows, когда в меню звука выбирают другое устройство
	clsidPolicyConfig = windows.GUID{Data1: 0x870AF99C, Data2: 0x171D, Data3: 0x4F9E, Data4: [8]byte{0xAF, 0x0D, 0xE6, 0x3D, 0xF4, 0x0C, 0x2B, 0xC9}}
	iidIPolicyConfig  = windows.GUID{Data1: 0xF8679F50, Data2: 0x850A, Data3: 0x41CF, Data4: [8]byte{0x9C, 0x72, 0x43, 0x0F, 0x29, 0x02, 0x90, 0xC8}}
	pkeyFriendlyName  = struct {
		fmtid windows.GUID
		pid   uint32
	}{windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 14}
)

type propVariant struct {
	vt  uint16
	_   [3]uint16
	str *uint16
	_   uintptr
}

// withAudioEnum открывает список звуковых устройств Windows.
func withAudioEnum(f func(en *comObj) error) error {
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
	return f(en)
}

func audioDevID(d *comObj) string {
	var p *uint16
	if d.call(5, uintptr(unsafe.Pointer(&p))) != 0 || p == nil { // GetId
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return windows.UTF16PtrToString(p)
}

func audioDevName(d *comObj) string {
	var st *comObj
	if d.call(4, 0, uintptr(unsafe.Pointer(&st))) != 0 || st == nil { // OpenPropertyStore(STGM_READ)
		return ""
	}
	defer st.release()
	var pv propVariant
	if st.call(5, uintptr(unsafe.Pointer(&pkeyFriendlyName)), uintptr(unsafe.Pointer(&pv))) != 0 { // GetValue
		return ""
	}
	defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if pv.vt != 31 || pv.str == nil { // VT_LPWSTR
		return ""
	}
	return windows.UTF16PtrToString(pv.str)
}

// audioOutputs — включённые устройства вывода звука; Default — то, куда звук идёт сейчас.
func audioOutputs() ([]audioOut, error) {
	var list []audioOut
	err := withAudioEnum(func(en *comObj) error {
		def := ""
		var d *comObj
		if en.call(4, 0, 1, uintptr(unsafe.Pointer(&d))) == 0 && d != nil { // GetDefaultAudioEndpoint(eRender, eMultimedia)
			def = audioDevID(d)
			d.release()
		}
		var col *comObj
		if r := en.call(3, 0, 1, uintptr(unsafe.Pointer(&col))); r != 0 || col == nil { // EnumAudioEndpoints(eRender, ACTIVE)
			return errors.New("не удалось получить список устройств звука")
		}
		defer col.release()
		var n uint32
		col.call(3, uintptr(unsafe.Pointer(&n)))
		for i := uint32(0); i < n; i++ {
			var dev *comObj
			if col.call(4, uintptr(i), uintptr(unsafe.Pointer(&dev))) != 0 || dev == nil {
				continue
			}
			id, name := audioDevID(dev), audioDevName(dev)
			dev.release()
			if id != "" {
				list = append(list, audioOut{ID: id, Name: name, Kind: audioKind(name), Default: id == def})
			}
		}
		return nil
	})
	return list, err
}

// setAudioOutput делает устройство основным для всего звука (музыка, видео и звонки).
func setAudioOutput(id string) error {
	return withAudioEnum(func(*comObj) error {
		var pc *comObj
		if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidPolicyConfig)), 0, 23,
			uintptr(unsafe.Pointer(&iidIPolicyConfig)), uintptr(unsafe.Pointer(&pc))); r != 0 || pc == nil {
			return errors.New("Windows не дала переключить звук")
		}
		defer pc.release()
		p, err := windows.UTF16PtrFromString(id)
		if err != nil {
			return err
		}
		for role := uintptr(0); role < 3; role++ { // eConsole, eMultimedia, eCommunications
			if r := pc.call(13, uintptr(unsafe.Pointer(p)), role); r != 0 { // SetDefaultEndpoint
				return errors.New("Windows не дала переключить звук")
			}
		}
		return nil
	})
}

// audioFollowDevice — наушники или колонку только что подключили: ждём, пока Windows
// создаст для них устройство звука, и переводим звук на него.
func audioFollowDevice(name string) bool {
	low := strings.ToLower(name)
	for t := time.Now(); time.Since(t) < 12*time.Second; time.Sleep(700 * time.Millisecond) {
		outs, _ := audioOutputs()
		for _, o := range outs {
			if o.Kind == "call" || !strings.Contains(strings.ToLower(o.Name), low) {
				continue
			}
			if o.Default {
				return true
			}
			return setAudioOutput(o.ID) == nil
		}
	}
	return false
}
