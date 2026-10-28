//go:build windows

// Тёмная тема Windows: та же настройка, что в «Параметры» → «Персонализация» → «Цвета».
package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

var procSendMessageTimeout = user32.NewProc("SendMessageTimeoutW")

func windowsDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}

func setWindowsDark(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, personalizeKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	light := uint32(1)
	if on {
		light = 0
	}
	if err := k.SetDWordValue("AppsUseLightTheme", light); err != nil {
		return err
	}
	k.SetDWordValue("SystemUsesLightTheme", light) // панель задач и меню «Пуск»
	// Сообщаем всем программам, что тема изменилась — они перекрасятся сразу
	name, _ := windows.UTF16PtrFromString("ImmersiveColorSet")
	var res uintptr
	procSendMessageTimeout.Call(0xFFFF /* HWND_BROADCAST */, 0x001A /* WM_SETTINGCHANGE */, 0,
		uintptr(unsafe.Pointer(name)), 0x0002 /* SMTO_ABORTIFHUNG */, 2000, uintptr(unsafe.Pointer(&res)))
	return nil
}
