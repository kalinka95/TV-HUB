//go:build windows

// Звук компьютера для визуализатора: «петля» WASAPI — то, что Windows выводит на
// устройство по умолчанию (телевизор по HDMI или динамики), без программ и настроек.
package main

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iidIAudioClient        = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioCaptureClient = windows.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0, Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

// waveFormat — начало WAVEFORMATEX(TENSIBLE): нам нужны каналы, частота и разрядность.
type waveFormat struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
	ValidBits      uint16
	ChannelMask    uint32
	SubFormat      windows.GUID
}

// vizCapture слушает звук, пока onData возвращает true. Звук сводится в моно.
func vizCapture(onData func(mono []float32, rate int) bool) error {
	return withVolumeDevice(func(dev *comObj) error {
		var client *comObj
		if r := dev.call(3, uintptr(unsafe.Pointer(&iidIAudioClient)), 23, 0, uintptr(unsafe.Pointer(&client))); r != 0 || client == nil { // Activate
			return errors.New("нет доступа к звуку")
		}
		defer client.release()
		var wf *waveFormat
		if r := client.call(8, uintptr(unsafe.Pointer(&wf))); r != 0 || wf == nil { // GetMixFormat
			return errors.New("неизвестный формат звука")
		}
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(wf)))
		ch, rate, bits := int(wf.Channels), int(wf.SamplesPerSec), int(wf.BitsPerSample)
		isFloat := wf.FormatTag == 3 || (wf.FormatTag == 0xFFFE && wf.SubFormat.Data1 == 3)
		if ch < 1 || (bits != 32 && bits != 16) {
			return errors.New("неподдерживаемый формат звука")
		}
		const loopback = 0x00020000                                                                          // AUDCLNT_STREAMFLAGS_LOOPBACK
		if r := client.call(3, 0, loopback, uintptr(200*10000), 0, uintptr(unsafe.Pointer(wf)), 0); r != 0 { // Initialize (общий режим, буфер 200 мс)
			return errors.New("не удалось подключиться к звуку")
		}
		var capture *comObj
		if r := client.call(14, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&capture))); r != 0 || capture == nil { // GetService
			return errors.New("нет захвата звука")
		}
		defer capture.release()
		if r := client.call(10); r != 0 { // Start
			return errors.New("звук не запускается")
		}
		defer client.call(11) // Stop
		mono := make([]float32, 0, 4096)
		quiet := 0
		for {
			mono = mono[:0]
			for {
				var n uint32
				if capture.call(5, uintptr(unsafe.Pointer(&n))) != 0 || n == 0 { // GetNextPacketSize
					break
				}
				var data *byte
				var frames, flags uint32
				if capture.call(3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0) != 0 { // GetBuffer
					break
				}
				silent := flags&2 != 0 || data == nil // AUDCLNT_BUFFERFLAGS_SILENT
				var raw []byte
				if !silent {
					raw = unsafe.Slice(data, int(frames)*ch*bits/8)
				}
				for i := 0; i < int(frames); i++ {
					s := float32(0)
					if !silent {
						for c := 0; c < ch; c++ {
							off := (i*ch + c) * bits / 8
							switch {
							case isFloat && bits == 32:
								s += math.Float32frombits(binary.LittleEndian.Uint32(raw[off:]))
							case bits == 16:
								s += float32(int16(binary.LittleEndian.Uint16(raw[off:]))) / 32768
							default:
								s += float32(int32(binary.LittleEndian.Uint32(raw[off:]))) / math.MaxInt32
							}
						}
						s /= float32(ch)
					}
					mono = append(mono, s)
				}
				capture.call(4, uintptr(frames)) // ReleaseBuffer
			}
			if len(mono) == 0 {
				// Когда ничего не играет, Windows не присылает звук совсем — подаём тишину,
				// чтобы столбики плавно опустились
				quiet++
				if quiet%3 == 0 {
					mono = append(mono, make([]float32, rate/33)...)
				}
			} else {
				quiet = 0
			}
			if len(mono) > 0 && !onData(mono, rate) {
				return nil
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
