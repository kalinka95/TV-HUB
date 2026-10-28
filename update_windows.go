//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// installUpdate меняет TVHub.exe на новый и перезапускает программу.
// Прежняя версия остаётся рядом как TVHub.old.exe — к ней можно вернуться в настройках.
func installUpdate(newFile string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	old := filepath.Join(filepath.Dir(exe), "TVHub.old.exe")
	os.Remove(old)
	// Windows разрешает переименовать запущенную программу, но не перезаписать её
	if err := os.Rename(exe, old); err != nil {
		return errors.New("не удалось заменить файл TVHub.exe: " + err.Error())
	}
	if err := copyFile(newFile, exe); err != nil {
		os.Rename(old, exe) // возвращаем как было
		return errors.New("не удалось записать новую версию: " + err.Error())
	}
	return restartSelf(exe, "--wait-previous", "--updated")
}

func rollbackAvailable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(exe), "TVHub.old.exe"))
	return err == nil
}

func rollbackUpdate() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	old := filepath.Join(filepath.Dir(exe), "TVHub.old.exe")
	if _, err := os.Stat(old); err != nil {
		return errors.New("прошлой версии нет")
	}
	broken := filepath.Join(filepath.Dir(exe), "TVHub.new-broken.exe")
	os.Remove(broken)
	if err := os.Rename(exe, broken); err != nil {
		return err
	}
	if err := copyFile(old, exe); err != nil {
		os.Rename(broken, exe)
		return err
	}
	return restartSelf(exe, "--wait-previous")
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func restartSelf(exe string, args ...string) error {
	c := hiddenCmd(exe, args...)
	c.SysProcAttr.HideWindow = false
	c.SysProcAttr.CreationFlags = 0
	if err := c.Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		closeWebApp()
		wv.Dispatch(func() { wv.Terminate() })
	}()
	return nil
}

func cleanupAfterUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(filepath.Dir(exe), "TVHub.new-broken.exe"))
	if hasArg("--updated") {
		notify("update", "TVHub обновлён до версии "+appVersion, "Если что-то сломалось, прошлую версию можно вернуть в «Настройки» → «Обновления»", nil)
	}
}
