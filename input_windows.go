//go:build windows

// Пульт на телефоне: нажатия превращаются в настоящие нажатия клавиш и движения мыши Windows,
// поэтому работают в любом окне — в TVHub, в Edge с Кинопоиском, в VLC и т.д.
package main

import (
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	procSendInput      = user32.NewProc("SendInput")
	procGetForeground  = user32.NewProc("GetForegroundWindow")
	procGetAncestorWin = user32.NewProc("GetAncestor")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004

	mouseMove      = 0x0001
	mouseLeftDown  = 0x0002
	mouseLeftUp    = 0x0004
	mouseRightDown = 0x0008
	mouseRightUp   = 0x0010
	mouseWheel     = 0x0800
)

// Структуры INPUT для 64-битной Windows (40 байт).
type kbInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

type msInput struct {
	typ   uint32
	_     uint32
	dx    int32
	dy    int32
	data  uint32
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
}

func sendKbd(in []kbInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

func sendMs(in []msInput) {
	if len(in) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
}

func tapVK(vk uint16, ext bool, mods ...uint16) {
	var f uint32
	if ext {
		f = keyExtended
	}
	var in []kbInput
	for _, m := range mods {
		in = append(in, kbInput{typ: inputKeyboard, vk: m})
	}
	in = append(in, kbInput{typ: inputKeyboard, vk: vk, flags: f}, kbInput{typ: inputKeyboard, vk: vk, flags: f | keyUp})
	for i := len(mods) - 1; i >= 0; i-- {
		in = append(in, kbInput{typ: inputKeyboard, vk: mods[i], flags: keyUp})
	}
	sendKbd(in)
}

func tvhubInFront() bool {
	fg, _, _ := procGetForeground.Call()
	if fg == 0 || hwnd == 0 {
		return false
	}
	root, _, _ := procGetAncestorWin.Call(fg, 2) // GA_ROOT
	return fg == hwnd || root == hwnd
}

type vkDef struct {
	vk  uint16
	ext bool
}

var vkMap = map[string]vkDef{
	"up": {0x26, true}, "down": {0x28, true}, "left": {0x25, true}, "right": {0x27, true},
	"ok": {0x0D, false}, "enter": {0x0D, false}, "esc": {0x1B, false}, "space": {0x20, false},
	"backspace": {0x08, false}, "menu": {0x5D, true},
	"chup": {0x21, true}, "chdown": {0x22, true},
	"volup": {0xAF, true}, "voldown": {0xAE, true}, "mute": {0xAD, true},
	"playpause": {0xB3, true},
}

func sendRemoteKey(k string) {
	if k == "back" {
		if tvhubInFront() && insideOpen() {
			tapVK(0x25, true, 0x12) // Alt+← — назад по страницам сайта
			return
		}
		if tvhubInFront() {
			tapVK(0x1B, false) // Esc — «Назад» в TVHub
		} else {
			tapVK(0xA6, true) // «Назад» браузера — для сайтов и приложений
		}
		return
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		tapVK(uint16(k[0]), false)
		return
	}
	if k == "volup" || k == "voldown" || k == "mute" {
		volumeAction(k)
		return
	}
	if d, ok := vkMap[k]; ok {
		tapVK(d.vk, d.ext)
	}
}

// sendRemoteText печатает текст с клавиатуры телефона в активное окно (любой язык).
func sendRemoteText(t string) {
	if tvhubInFront() && !insideOpen() { // в самом TVHub (поиск каналов, поля настроек) — передаём текст напрямую
		pushEvent(map[string]string{"type": "text", "text": t})
		return
	}
	var in []kbInput
	for _, u := range utf16.Encode([]rune(t)) {
		if u == '\n' {
			in = append(in, kbInput{typ: inputKeyboard, vk: 0x0D}, kbInput{typ: inputKeyboard, vk: 0x0D, flags: keyUp})
			continue
		}
		in = append(in,
			kbInput{typ: inputKeyboard, scan: u, flags: keyUnicode},
			kbInput{typ: inputKeyboard, scan: u, flags: keyUnicode | keyUp})
	}
	// Отправляем порциями, чтобы длинные ссылки не терялись
	for len(in) > 0 {
		n := min(len(in), 64)
		sendKbd(in[:n])
		in = in[n:]
		time.Sleep(5 * time.Millisecond)
	}
}

func sendRemoteMouse(dx, dy, wheel int, click string) {
	var in []msInput
	if dx != 0 || dy != 0 {
		in = append(in, msInput{typ: inputMouse, dx: int32(dx), dy: int32(dy), flags: mouseMove})
	}
	if wheel != 0 {
		in = append(in, msInput{typ: inputMouse, data: uint32(int32(wheel)), flags: mouseWheel})
	}
	switch click {
	case "left":
		in = append(in, msInput{typ: inputMouse, flags: mouseLeftDown}, msInput{typ: inputMouse, flags: mouseLeftUp})
	case "right":
		in = append(in, msInput{typ: inputMouse, flags: mouseRightDown}, msInput{typ: inputMouse, flags: mouseRightUp})
	}
	sendMs(in)
}

// Проверка размеров структур на этапе компиляции: Windows требует ровно 40 байт.
var _ = [1]struct{}{}[unsafe.Sizeof(kbInput{})-40]
var _ = [1]struct{}{}[unsafe.Sizeof(msInput{})-40]
