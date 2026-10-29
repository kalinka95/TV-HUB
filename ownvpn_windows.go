//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

const xrayZipURL = "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-windows-64.zip"

func xrayDir() string { return filepath.Join(dataDir, "xray") }
func xrayExe() string { return filepath.Join(xrayDir(), "xray.exe") }

func xrayInstalled() bool {
	for _, f := range []string{"xray.exe", "geoip.dat", "geosite.dat"} {
		if _, err := os.Stat(filepath.Join(xrayDir(), f)); err != nil {
			return false
		}
	}
	return true
}

var sha256Re = regexp.MustCompile(`(?i)SHA2-256=\s*([0-9a-f]{64})`)

// xrayInstall скачивает Xray с GitHub и проверяет контрольную сумму, которую публикуют его авторы.
func xrayInstall() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dg, err := fetchBytes(ctx, xrayZipURL+".dgst", 8192)
	if err != nil {
		return fmt.Errorf("контрольная сумма: %v", err)
	}
	m := sha256Re.FindSubmatch(dg)
	if m == nil {
		return errors.New("не нашлась контрольная сумма Xray")
	}
	data, err := fetchBytes(ctx, xrayZipURL, 100<<20)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), string(m[1])) {
		return errors.New("файл Xray повреждён или подменён — установка отменена")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	os.MkdirAll(xrayDir(), 0o755)
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if name != "xray.exe" && name != "geoip.dat" && name != "geosite.dat" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(xrayDir(), name), b, 0o755); err != nil {
			return err
		}
	}
	if !xrayInstalled() {
		return errors.New("в архиве Xray нет нужных файлов")
	}
	return nil
}

var (
	xrayMu  sync.Mutex
	xrayCmd *exec.Cmd
)

func xrayRunning() bool {
	xrayMu.Lock()
	defer xrayMu.Unlock()
	return xrayCmd != nil
}

// xrayStart запускает Xray с настройками conf и включает его как системный прокси Windows,
// чтобы через него шли TVHub, Edge и другие программы. Российские сайты Xray отправляет напрямую.
func xrayStart(conf []byte) error {
	xrayStop()
	// Xray мог остаться от прошлого запуска TVHub (например, после обновления) — закрываем его
	for _, p := range allProcs() {
		if strings.EqualFold(p.path, xrayExe()) {
			hiddenCmd("taskkill", "/PID", fmt.Sprint(p.pid), "/F").Run()
		}
	}
	f := filepath.Join(xrayDir(), "config.json")
	if err := os.WriteFile(f, conf, 0o600); err != nil {
		return err
	}
	if out, err := hiddenCmd(xrayExe(), "run", "-test", "-c", f).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		return errors.New("Xray не принял настройки сервера: " + msg)
	}
	c := hiddenCmd(xrayExe(), "run", "-c", f)
	c.Dir = xrayDir()
	logf, _ := os.Create(filepath.Join(xrayDir(), "xray.log"))
	if logf != nil {
		c.Stdout, c.Stderr = logf, logf
	}
	if err := c.Start(); err != nil {
		return err
	}
	xrayMu.Lock()
	xrayCmd = c
	xrayMu.Unlock()
	go func() {
		c.Wait()
		if logf != nil {
			logf.Close()
		}
		xrayMu.Lock()
		same := xrayCmd == c
		if same {
			xrayCmd = nil
		}
		xrayMu.Unlock()
		if same { // Xray закрылся сам — возвращаем интернет без прокси
			ownProxy(false)
			setOwnErr("VPN отключился: Xray закрылся")
			log.Printf("свой VPN: Xray закрылся")
			pushEvent(map[string]string{"type": "vpn"})
		}
	}()
	// Ждём, пока Xray начнёт принимать соединения
	for i := 0; i < 30; i++ {
		if cn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ownHTTPPort), 200*time.Millisecond); err == nil {
			cn.Close()
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	return ownProxy(true)
}

func xrayStop() {
	xrayMu.Lock()
	c := xrayCmd
	xrayCmd = nil
	xrayMu.Unlock()
	ownProxy(false)
	if c != nil && c.Process != nil {
		c.Process.Kill()
	}
}

// ---------- Системный прокси Windows ----------

const inetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type proxyBackup struct {
	Enable   uint64 `json:"enable"`
	Server   string `json:"server"`
	Override string `json:"override"`
}

func proxyBackupFile() string { return filepath.Join(dataDir, "proxy-backup.json") }

func ownProxyAddr() string { return fmt.Sprintf("127.0.0.1:%d", ownHTTPPort) }

// ownProxy включает (запомнив прежние настройки) или выключает системный прокси на свой VPN.
func ownProxy(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	ov, _, _ := k.GetStringValue("ProxyOverride")
	ours := en == 1 && srv == ownProxyAddr()
	if on {
		if !ours {
			b, _ := json.Marshal(proxyBackup{en, srv, ov})
			os.WriteFile(proxyBackupFile(), b, 0o644)
		}
		k.SetStringValue("ProxyServer", ownProxyAddr())
		k.SetStringValue("ProxyOverride", "<local>;localhost;127.*;10.*;172.16.*;192.168.*")
		k.SetDWordValue("ProxyEnable", 1)
	} else {
		if !ours {
			return nil // прокси не наш (или уже выключен) — не трогаем
		}
		var bk proxyBackup
		if b, err := os.ReadFile(proxyBackupFile()); err == nil && json.Unmarshal(b, &bk) == nil && bk.Server != ownProxyAddr() {
			k.SetStringValue("ProxyServer", bk.Server)
			k.SetStringValue("ProxyOverride", bk.Override)
			k.SetDWordValue("ProxyEnable", uint32(bk.Enable))
		} else {
			k.SetDWordValue("ProxyEnable", 0)
		}
		os.Remove(proxyBackupFile())
	}
	procInternetSetOption.Call(0, 39, 0, 0) // INTERNET_OPTION_SETTINGS_CHANGED
	procInternetSetOption.Call(0, 37, 0, 0) // INTERNET_OPTION_REFRESH
	return nil
}

// happOffForOwn — перед включением своего VPN выключаем Happ, если он подключён (иначе мешает).
func happOffForOwn() {
	if findVPN() == "" {
		return
	}
	if on, _, _ := happConnected(); on {
		if err := setHappVPN(false); err != nil {
			log.Printf("свой VPN: Happ не выключился: %v", err)
		}
	}
}

// ownVPNStartup — при запуске TVHub: прокси остался от прошлого раза — включаем VPN снова
// (если он был включён) или возвращаем прежние настройки.
func ownVPNStartup() {
	if ownVPNOn() {
		go func() {
			if err := ownVPNApply(); err != nil {
				log.Printf("свой VPN: %v", err)
			}
		}()
	} else {
		ownProxy(false)
	}
	go refreshOwnSubscription()
}
