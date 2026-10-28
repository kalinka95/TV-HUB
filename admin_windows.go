//go:build windows

// Права администратора: Windows не даёт обычной программе управлять окнами,
// запущенными от администратора (например, Happ). Здесь — проверка и «Режим администратора».
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func selfElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// pidElevated — запущен ли процесс от администратора. Если Windows не даёт даже
// посмотреть на процесс, считаем, что запущен (так ведут себя процессы администратора).
func pidElevated(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return true
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		return true
	}
	defer tok.Close()
	return tok.IsElevated()
}

var (
	blockMu   sync.Mutex
	blockAt   time.Time
	blockHwnd uintptr
	blockVal  bool
)

// remoteBlocked — текст предупреждения, если активное окно запущено от администратора,
// а TVHub — нет (нажатия с телефона туда не дойдут).
func remoteBlocked() string {
	if selfElevated() {
		return ""
	}
	fg, _, _ := procGetForeground.Call()
	if fg == 0 || fg == hwnd {
		return ""
	}
	blockMu.Lock()
	defer blockMu.Unlock()
	if fg != blockHwnd || time.Since(blockAt) > 2*time.Second {
		var pid uint32
		procGetWindowThreadPI.Call(fg, uintptr(unsafe.Pointer(&pid)))
		blockVal = pid != 0 && pidElevated(pid)
		blockHwnd, blockAt = fg, time.Now()
	}
	if !blockVal {
		return ""
	}
	return "Это окно запущено от имени администратора, и Windows не даёт пульту им управлять. Нажмите «Домой» или включите в TVHub «Режим администратора»"
}

// ---------- Режим администратора ----------

const taskName = "TVHub"

// relaunchElevated перезапускает TVHub с правами администратора (Windows спросит подтверждение).
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("runas"), windows.StringToUTF16Ptr(exe),
		windows.StringToUTF16Ptr("--wait-previous"), nil, 1); err != nil {
		return errors.New("запуск с правами администратора отменён")
	}
	closeWebApp()
	wv.Dispatch(func() { wv.Terminate() })
	return nil
}

// Автозапуск в режиме администратора — через Планировщик заданий: так Windows не спрашивает подтверждение при каждом входе.
func setAdminAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !on {
		hiddenCmd("schtasks", "/Delete", "/TN", taskName, "/F").Run()
		return nil
	}
	if !selfElevated() {
		return errors.New("нужны права администратора")
	}
	out, err := hiddenCmd("schtasks", "/Create", "/TN", taskName, "/TR", `"`+exe+`"`, "/SC", "ONLOGON", "/RL", "HIGHEST", "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("планировщик заданий: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// applyStartupMode согласует автозапуск с режимом администратора.
func applyStartupMode() {
	cfgMu.Lock()
	auto, admin := cfg.Settings.Autostart, cfg.Settings.RunAsAdmin
	cfgMu.Unlock()
	if admin && selfElevated() {
		if err := setAdminAutostart(auto); err != nil {
			log.Printf("автозапуск (администратор): %v", err)
		}
		if auto {
			setRunKey(false) // запускает задание планировщика, обычный автозапуск не нужен
		}
		return
	}
	if !admin {
		setAdminAutostart(false)
	}
}

// waitPrevious ждёт, пока закроется прежний экземпляр TVHub (при перезапуске с другими правами).
func waitPrevious() {
	for i := 0; i < 60; i++ {
		h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, windows.StringToUTF16Ptr(`Local\TVHubSingleInstance`))
		if err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return // прежнего экземпляра больше нет
		}
		if err == nil {
			windows.CloseHandle(h)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// relaunchNormal перезапускает TVHub без прав администратора (через Проводник).
func relaunchNormal() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Проводник запускает программы с обычными правами; пауза — чтобы этот экземпляр успел закрыться
	if err := hiddenCmd("cmd", "/c", `ping -n 3 127.0.0.1 >nul & explorer.exe "`+exe+`"`).Start(); err != nil {
		return err
	}
	closeWebApp()
	wv.Dispatch(func() { wv.Terminate() })
	return nil
}
