//go:build windows

// Музыкальный режим: что сейчас играет (через системные медиа-элементы управления Windows)
// и запуск Яндекс Музыки.
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// Скрипт PowerShell читает GlobalSystemMediaTransportControls — то же, что показывает
// Windows во всплывающем окне громкости: название, исполнитель, обложка, позиция.
const mediaScript = `
$ErrorActionPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, [Type]$t) { $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $null = $task.Wait(5000); if ($task.IsCompleted) { $task.Result } else { $null } }
$null = [Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager, Windows.Media.Control, ContentType = WindowsRuntime]
$null = [Windows.Storage.Streams.IRandomAccessStreamWithContentType, Windows.Storage.Streams, ContentType = WindowsRuntime]
$mgr = Await ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager])
if (-not $mgr) { [Console]::Out.WriteLine('{"ok":false}'); exit }
$lastKey = ''
while ($true) {
  $o = @{ ok = $true }
  $s = $mgr.GetCurrentSession()
  if ($s) {
    $p = Await ($s.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
    $pb = $s.GetPlaybackInfo()
    $tl = $s.GetTimelineProperties()
    $o.title = [string]$p.Title; $o.artist = [string]$p.Artist; $o.album = [string]$p.AlbumTitle
    $o.status = [string]$pb.PlaybackStatus
    $o.app = [string]$s.SourceAppUserModelId
    $o.pos = $tl.Position.TotalSeconds; $o.dur = $tl.EndTime.TotalSeconds
    $key = "$($p.Title)|$($p.Artist)"
    if ($key -ne $lastKey) {
      $lastKey = $key
      $o.cover = ''
      if ($p.Thumbnail) {
        $rs = Await ($p.Thumbnail.OpenReadAsync()) ([Windows.Storage.Streams.IRandomAccessStreamWithContentType])
        if ($rs) {
          $st = [System.IO.WindowsRuntimeStreamExtensions]::AsStreamForRead($rs)
          $ms = New-Object System.IO.MemoryStream
          $st.CopyTo($ms)
          if ($ms.Length -lt 3000000) { $o.cover = [Convert]::ToBase64String($ms.ToArray()) }
          $st.Dispose(); $ms.Dispose()
        }
      }
    }
  }
  [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress))
  [Console]::Out.Flush()
  Start-Sleep -Milliseconds 1500
}
`

type mediaState struct {
	OK       bool    `json:"ok"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album,omitempty"`
	Status   string  `json:"status"`
	App      string  `json:"app,omitempty"`
	Pos      float64 `json:"pos"`
	Dur      float64 `json:"dur"`
	CoverVer string  `json:"coverVer,omitempty"`
	At       int64   `json:"at"`
}

var (
	mediaMu      sync.Mutex
	media        mediaState
	mediaCover   []byte
	mediaCmd     *exec.Cmd
	mediaLastReq time.Time
	mediaFails   int
)

func ensureMediaMonitor() {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	mediaLastReq = time.Now()
	if mediaCmd != nil || mediaFails > 5 {
		return
	}
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	u := utf16.Encode([]rune(mediaScript))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.StdoutPipe()
	if err != nil {
		mediaFails++
		return
	}
	if err := cmd.Start(); err != nil {
		mediaFails++
		log.Printf("музыка: не удалось запустить PowerShell: %v", err)
		return
	}
	mediaCmd = cmd
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 64*1024), 8<<20)
		for sc.Scan() {
			var st struct {
				mediaState
				Cover *string `json:"cover"`
			}
			if json.Unmarshal(sc.Bytes(), &st) != nil {
				continue
			}
			mediaMu.Lock()
			cover := media.CoverVer
			if st.Cover != nil {
				if raw, err := base64.StdEncoding.DecodeString(*st.Cover); err == nil && len(raw) > 0 {
					h := sha1.Sum(raw)
					mediaCover, cover = raw, hex.EncodeToString(h[:6])
				} else {
					mediaCover, cover = nil, ""
				}
			}
			media = st.mediaState
			media.CoverVer = cover
			media.At = time.Now().Unix()
			idle := time.Since(mediaLastReq) > 2*time.Minute
			mediaMu.Unlock()
			if idle { // никто не смотрит на экран музыки — выключаем слежение
				cmd.Process.Kill()
				break
			}
		}
		cmd.Wait()
		mediaMu.Lock()
		if mediaCmd == cmd {
			mediaCmd = nil
			if time.Since(mediaLastReq) < 10*time.Second {
				mediaFails++
			}
		}
		mediaMu.Unlock()
	}()
}

func getMedia() (mediaState, bool) {
	ensureMediaMonitor()
	mediaMu.Lock()
	defer mediaMu.Unlock()
	return media, mediaFails <= 5
}

func mediaCoverBytes() []byte {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	return mediaCover
}

func mediaAction(a string) error {
	m := map[string]uint16{"playpause": 0xB3, "next": 0xB0, "prev": 0xB1, "volup": 0xAF, "voldown": 0xAE, "mute": 0xAD}
	vk, ok := m[a]
	if !ok {
		return errors.New("неизвестная команда")
	}
	tapVK(vk, true)
	return nil
}

// ---------- Яндекс Музыка ----------

func findYandexMusic() string {
	var roots []string
	for _, env := range []string{"LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, filepath.Join(v, "Programs"), v)
		}
	}
	for _, r := range roots {
		for _, pat := range []string{`*andex*usic*\*.exe`, `Яндекс Музыка\*.exe`, `*andex*\*usic*\*.exe`} {
			m, _ := filepath.Glob(filepath.Join(r, pat))
			for _, f := range m {
				low := strings.ToLower(filepath.Base(f))
				if strings.Contains(low, "unins") || strings.Contains(low, "update") || strings.Contains(low, "helper") || strings.Contains(low, "crash") {
					continue
				}
				return f
			}
		}
	}
	return ""
}

var (
	musicMu  sync.Mutex
	musicCmd *exec.Cmd
)

// launchMusic открывает Яндекс Музыку. Её не закрывает кнопка «Домой»: музыка играет дальше.
func launchMusic(a App) error {
	if exe := findYandexMusic(); exe != "" {
		return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(exe), nil,
			windows.StringToUTF16Ptr(filepath.Dir(exe)), 1)
	}
	musicMu.Lock()
	running := musicCmd != nil && musicCmd.ProcessState == nil
	var pid uint32
	if running {
		pid = uint32(musicCmd.Process.Pid)
	}
	musicMu.Unlock()
	if running && focusPID(pid) {
		return nil
	}
	browser := findBrowser()
	if browser == "" {
		return windows.ShellExecute(0, nil, windows.StringToUTF16Ptr(a.Target), nil, nil, 1)
	}
	c := exec.Command(browser, "--user-data-dir="+filepath.Join(dataDir, "browser-music"), "--no-first-run",
		"--no-default-browser-check", "--start-fullscreen", "--autoplay-policy=no-user-gesture-required", "--app="+a.Target)
	if err := c.Start(); err != nil {
		return err
	}
	musicMu.Lock()
	musicCmd = c
	musicMu.Unlock()
	go func() {
		c.Wait()
		musicMu.Lock()
		if musicCmd == c {
			musicCmd = nil
		}
		musicMu.Unlock()
	}()
	return nil
}

func focusPID(pid uint32) bool {
	enumMu.Lock()
	enumList = nil
	procEnumWindows.Call(enumCB, 0)
	list := enumList
	enumMu.Unlock()
	for _, it := range list {
		if it.pid == pid {
			focusWindow(it.hwnd)
			return true
		}
	}
	return false
}

func musicAppInfo() map[string]any {
	exe := findYandexMusic()
	return map[string]any{"desktop": exe != ""}
}
