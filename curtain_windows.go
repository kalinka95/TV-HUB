//go:build windows

// «Занавес» при запуске приложений: пока окно сервиса или программы открывается и растягивается
// на весь экран, TVHub с заставкой перехода стоит поверх всех окон. Как только приложение готово,
// TVHub уходит назад, а приложение оказывается на экране уже целиком.
package main

import (
	"sync"
	"time"
)

var (
	curtainMu  sync.Mutex
	curtainGen int
	curtainOn  bool
)

const hwndTopmost, hwndNoTopmost, hwndTop = ^uintptr(0), ^uintptr(1), 0

// curtainUp ставит TVHub поверх всех окон. Если приложение так и не открылось — через 25 секунд
// занавес снимается сам.
func curtainUp() {
	if wv == nil {
		return
	}
	curtainMu.Lock()
	curtainGen++
	gen := curtainGen
	curtainOn = true
	curtainMu.Unlock()
	onUI(func() { procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, 0x0001|0x0002|0x0010) }) // NOSIZE|NOMOVE|NOACTIVATE
	go func() {
		time.Sleep(25 * time.Second)
		curtainMu.Lock()
		stale := curtainGen != gen || !curtainOn
		curtainMu.Unlock()
		if !stale {
			curtainDown(0)
		}
	}()
}

// curtainDown убирает TVHub назад и показывает окно приложения app (если оно известно).
func curtainDown(app uintptr) {
	curtainMu.Lock()
	was := curtainOn
	curtainOn = false
	curtainMu.Unlock()
	if wv != nil && was {
		onUI(func() { procSetWindowPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, 0x0001|0x0002|0x0010) })
	}
	if app != 0 {
		procSetWindowPos.Call(app, hwndTop, 0, 0, 0, 0, 0x0001|0x0002)
		focusWindow(app)
	}
	pushEvent(map[string]string{"type": "appShown"})
}

// curtainCancel — «Домой» во время загрузки: просто снимаем занавес.
func curtainCancel() {
	curtainMu.Lock()
	was := curtainOn
	curtainOn = false
	curtainMu.Unlock()
	if was && wv != nil {
		wv.Dispatch(func() { procSetWindowPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, 0x0001|0x0002|0x0010) })
	}
}

// topWindows — видимые окна программ сейчас (чтобы потом найти новое).
func topWindows() map[uintptr]winItem {
	enumMu.Lock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	list := enumList
	enumMu.Unlock()
	out := map[uintptr]winItem{}
	for _, it := range list {
		if it.hwnd != hwnd {
			out[it.hwnd] = it
		}
	}
	return out
}

// waitNewWindow ждёт, пока появится новое окно (у процесса pid, если он задан), до d.
func waitNewWindow(before map[uintptr]winItem, pid uint32, d time.Duration) uintptr {
	for t := time.Now(); time.Since(t) < d; time.Sleep(200 * time.Millisecond) {
		for h, it := range topWindows() {
			if _, old := before[h]; old {
				continue
			}
			if pid == 0 || it.pid == pid {
				return h
			}
		}
	}
	return 0
}

// windowOfPID — главное окно процесса, если оно уже есть.
func windowOfPID(pid uint32) uintptr {
	for h, it := range topWindows() {
		if it.pid == pid {
			return h
		}
	}
	return 0
}

// revealProgram — программа запущена: ждём её окно, даём ему развернуться и показываем.
func revealProgram(before map[uintptr]winItem, pid uint32) {
	h := waitNewWindow(before, pid, 15*time.Second)
	if h != 0 {
		time.Sleep(1200 * time.Millisecond) // окно успевает развернуться и отрисоваться
	}
	curtainDown(h)
}
