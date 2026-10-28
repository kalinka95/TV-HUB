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
	"fmt"
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
$null = [Windows.Storage.Streams.DataReader, Windows.Storage.Streams, ContentType = WindowsRuntime]
$mgr = Await ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager]::RequestAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager])
if (-not $mgr) { [Console]::Out.WriteLine('{"ok":false}'); exit }
$lastKey = ''
$myPos = 0; $rawLast = -1; $lastT = $null
$coverTries = 0
$haveCover = $false
$cmdFile = $env:TVHUB_MEDIA_CMD
function Emit {
  $o = @{ ok = $true }
  $s = $mgr.GetCurrentSession()
  if ($s) {
    $p = Await ($s.TryGetMediaPropertiesAsync()) ([Windows.Media.Control.GlobalSystemMediaTransportControlsSessionMediaProperties])
    $pb = $s.GetPlaybackInfo()
    $tl = $s.GetTimelineProperties()
    $o.title = [string]$p.Title; $o.artist = [string]$p.Artist; $o.album = [string]$p.AlbumTitle
    $o.status = [string]$pb.PlaybackStatus
    $o.app = [string]$s.SourceAppUserModelId
    $o.dur = $tl.EndTime.TotalSeconds
    $key = "$($p.Title)|$($p.Artist)"
    if ($key -ne $script:lastKey) { $script:lastKey = $key; $script:coverTries = 0; $script:haveCover = $false; $o.cover = ''; $script:myPos = 0; $script:rawLast = -1 }
    # Позиция трека. Многие плееры (и Яндекс Музыка) сообщают её только в начале трека,
    # поэтому досчитываем сами: от времени, когда плеер её сообщил, и своим таймером.
    $t = [DateTime]::UtcNow
    $dt = 0; if ($script:lastT) { $dt = ($t - $script:lastT).TotalSeconds }
    $script:lastT = $t
    $playing = $o.status -eq 'Playing'
    if ($playing -and $dt -gt 0 -and $dt -lt 5) { $script:myPos += $dt }
    $raw = $tl.Position.TotalSeconds
    if ($raw -ne $script:rawLast) {
      $script:rawLast = $raw
      $pos = $raw
      try { $age = ([DateTimeOffset]::UtcNow - $tl.LastUpdatedTime).TotalSeconds; if ($playing -and $age -gt 0 -and $age -lt 3600) { $pos += $age } } catch {}
      $script:myPos = $pos
    }
    $o.pos = $script:myPos
    if ($o.dur -gt 0 -and $o.pos -gt $o.dur) { $o.pos = $o.dur }
    # Обложка часто приходит позже названия — пробуем несколько раз
    if (-not $script:haveCover -and $script:coverTries -lt 8 -and $p.Thumbnail) {
      $script:coverTries++
      try {
        $rs = Await ($p.Thumbnail.OpenReadAsync()) ([Windows.Storage.Streams.IRandomAccessStreamWithContentType])
        if ($rs -and $rs.Size -gt 0 -and $rs.Size -lt 3000000) {
          $reader = [Windows.Storage.Streams.DataReader]::new($rs.GetInputStreamAt(0))
          $null = Await ($reader.LoadAsync([uint32]$rs.Size)) ([uint32])
          $bytes = New-Object byte[] ([int]$rs.Size)
          $reader.ReadBytes($bytes)
          $reader.Dispose(); $rs.Dispose()
          $o.cover = [Convert]::ToBase64String($bytes)
          $script:haveCover = $true
        }
      } catch {}
    }
  }
  [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress))
  [Console]::Out.Flush()
}
$tick = 99
while ($true) {
  # Команды от TVHub (пауза, следующий, предыдущий) приходят через файл
  if ($cmdFile -and (Test-Path $cmdFile)) {
    $c = ''
    try { $c = ([IO.File]::ReadAllText($cmdFile)).Trim(); Remove-Item $cmdFile -Force } catch {}
    $s = $mgr.GetCurrentSession()
    if ($s) {
      switch ($c) {
        'playpause' { $null = Await ($s.TryTogglePlayPauseAsync()) ([bool]) }
        'next' { $null = Await ($s.TrySkipNextAsync()) ([bool]) }
        'prev' { $null = Await ($s.TrySkipPreviousAsync()) ([bool]) }
      }
    }
    Start-Sleep -Milliseconds 300
    $tick = 99
  }
  if ($tick -ge 2) { Emit; $tick = 0 }
  $tick++
  Start-Sleep -Milliseconds 250
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
	CoverURL string  `json:"coverUrl,omitempty"` // обложка по ссылке (с сайта Яндекс Музыки)
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
	cmd.Env = append(os.Environ(), "TVHUB_MEDIA_CMD="+mediaCmdFile())
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
			if st.Title != media.Title || st.Artist != media.Artist {
				cover, mediaCover = "", nil // новый трек — старая обложка не подходит
				if st.Title != "" {
					go func(s mediaState) { fillCoverFallback(&s) }(st.mediaState) // запасную обложку ищем сразу
				}
			}
			if st.Cover != nil && *st.Cover != "" {
				if raw, err := base64.StdEncoding.DecodeString(*st.Cover); err == nil && len(raw) > 0 {
					h := sha1.Sum(raw)
					mediaCover, cover = raw, hex.EncodeToString(h[:6])
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
	st, ok := media, mediaFails <= 5
	mediaMu.Unlock()
	defer func() { fillCoverFallback(&st) }()
	// Windows не сообщает, что играет, или не отдаёт обложку — спросим сайт Яндекс Музыки
	if st.Title == "" || (st.CoverVer == "" && strings.Contains(strings.ToLower(st.App), "edge")) {
		if w, found := webMusicInfo(); found && (st.Title == "" || strings.EqualFold(w.Title, st.Title)) {
			if st.Title != "" {
				st.CoverURL = w.CoverURL
			} else {
				st = w
			}
			ok = true
		}
	}
	return st, ok
}

func mediaCoverBytes() []byte {
	mediaMu.Lock()
	defer mediaMu.Unlock()
	return mediaCover
}

func mediaCmdFile() string { return filepath.Join(dataDir, "media-cmd.txt") }

// mediaAction: пауза и треки — через системное управление медиа Windows (как кнопки
// во всплывающем окне громкости), громкость — клавишами громкости.
func mediaAction(a string) error {
	switch a {
	case "playpause", "next", "prev":
		ensureMediaMonitor()
		mediaMu.Lock()
		idle := media.Title == ""
		mediaMu.Unlock()
		if a == "playpause" && idle {
			// Ничего не играет — Windows некому передать «плей». Включаем «Мою волну» сами.
			go startMusic()
			return nil
		}
		mediaMu.Lock()
		ok := mediaCmd != nil
		mediaMu.Unlock()
		if ok && os.WriteFile(mediaCmdFile(), []byte(a), 0o644) == nil {
			return nil
		}
		// Запасной путь: мультимедийные клавиши
		tapVK(map[string]uint16{"playpause": 0xB3, "next": 0xB0, "prev": 0xB1}[a], true)
		return nil
	case "volup", "voldown", "mute":
		tapVK(map[string]uint16{"volup": 0xAF, "voldown": 0xAE, "mute": 0xAD}[a], true)
		return nil
	}
	return errors.New("неизвестная команда")
}

// webMusicInfo — что играет на сайте Яндекс Музыки в окне TVHub (если Windows не сообщает).
func webMusicInfo() (mediaState, bool) {
	list, _ := cdpTargets(profileDir("browser-music"))
	for _, t := range list {
		if t.Type != "page" || !strings.Contains(t.URL, "music.yandex") || t.WS == "" {
			continue
		}
		res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"returnByValue": true, "expression": `(() => {
			const m = navigator.mediaSession && navigator.mediaSession.metadata;
			if (!m) return null;
			const art = (m.artwork || []).slice().sort((a, b) => parseInt(b.sizes) - parseInt(a.sizes))[0];
			const a = document.querySelector('audio');
			return { title: m.title, artist: m.artist, album: m.album, cover: art ? art.src : '',
				status: navigator.mediaSession.playbackState === 'playing' ? 'Playing' : 'Paused',
				pos: a ? a.currentTime : 0, dur: a && isFinite(a.duration) ? a.duration : 0 };
		})()`})
		if err != nil {
			continue
		}
		var r struct {
			Result struct {
				Value *struct {
					Title, Artist, Album, Cover, Status string
					Pos, Dur                            float64
				} `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(res, &r) == nil && r.Result.Value != nil && r.Result.Value.Title != "" {
			v := r.Result.Value
			return mediaState{OK: true, Title: v.Title, Artist: v.Artist, Album: v.Album, Status: v.Status,
				App: "Yandex.Music.Web", Pos: v.Pos, Dur: v.Dur, CoverURL: v.Cover, At: time.Now().Unix()}, true
		}
	}
	return mediaState{}, false
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

// launchMusic открывает Яндекс Музыку: приложение, если установлено, иначе сайт.
// Кнопка «Домой» музыку не останавливает.
func launchMusic(a App) error {
	defer func() { go startWave() }() // сразу включаем «Мою волну», если ничего не играет
	return launchMusicNoWave(a)
}

func launchMusicNoWave(a App) error {
	if exe := findYandexMusic(); exe != "" {
		if p := ymDesktopProc(exe); p != 0 && focusPID(p) {
			return nil
		}
		// Порт отладки — чтобы TVHub мог нажать «Моя волна» (если приложение это позволяет)
		return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(exe),
			windows.StringToUTF16Ptr("--remote-debugging-port="+ymDesktopPort), windows.StringToUTF16Ptr(filepath.Dir(exe)), 1)
	}
	return openWebApp(a)
}

const ymDesktopPort = "9333"

func ymDesktopProc(exe string) uint32 {
	for _, p := range allProcs() {
		if strings.EqualFold(p.path, exe) && !strings.Contains(processCmdline(p.pid), "--type=") {
			return p.pid
		}
	}
	return 0
}

// startMusic — «плей», когда ничего не играет: открыть Яндекс Музыку (если закрыта) и включить волну.
// Возвращает понятное описание того, что получилось.
func startMusic() string {
	if exe := findYandexMusic(); exe != "" {
		if p := ymDesktopProc(exe); p != 0 {
			if _, port := cdpTargetsPort(ymDesktopPort); port == "" || len(musicTargets()) == 0 {
				// Приложение запущено без возможности управления — перезапускаем его (музыка всё равно не играет)
				hiddenCmd("taskkill", "/PID", fmt.Sprint(p), "/T", "/F").Run()
				time.Sleep(1200 * time.Millisecond)
				launchMusicNoWave(yandexMusicApp)
				return startWave()
			}
			return startWave()
		}
	}
	if len(musicTargets()) == 0 {
		launchMusicNoWave(yandexMusicApp)
	}
	return startWave()
}

// musicTargets — страницы Яндекс Музыки, которыми можно управлять: сайт в окне TVHub и приложение.
func musicTargets() []cdpTarget {
	var out []cdpTarget
	web, _ := cdpTargets(profileDir("browser-music"))
	desk, _ := cdpTargetsPort(ymDesktopPort)
	for _, t := range append(web, desk...) {
		if t.Type == "page" && t.WS != "" && !strings.HasPrefix(t.URL, "devtools:") {
			out = append(out, t)
		}
	}
	return out
}

// waveJS(шаг) ищет на странице Яндекс Музыки, куда нажать, и возвращает координаты для клика мышью:
// 0 — кнопка «плей» рядом с «Моя волна» в основной части страницы, 1 — сам блок «Моя волна»,
// 2 — пункт «Моя волна» в боковом меню (открывает страницу волны), 3 — «плей» в плеере внизу.
// Элемент при необходимости прокручивается в видимую область; waveRestoreJS возвращает прокрутку.
const waveJS = `((step) => {
  if (navigator.mediaSession && navigator.mediaSession.playbackState === 'playing') return 'playing';
  const vis = (e) => { const r = e.getBoundingClientRect(); const st = getComputedStyle(e); return r.width > 4 && r.height > 4 && st.visibility !== 'hidden' && st.display !== 'none'; };
  const label = (b) => ((b.getAttribute('aria-label') || '') + ' ' + (b.title || '') + ' ' + (b.getAttribute('data-test-id') || '') + ' ' + (b.textContent || '')).toLowerCase().replace(/\s+/g, ' ');
  const isPlay = (b) => /(воспр|play|слуш|запуст|включ|продолж)/.test(label(b)) && !/(пауз|pause|настро|setting|плейлист|playlist)/.test(label(b));
  const inNav = (e) => !!e.closest('nav,aside,header,[class*=idebar],[class*=Navbar],[class*=navbar],[class*=NavBar]');
  const inBar = (e) => !!e.closest('footer,[class*=layerBar],[class*=layerbar],[data-test-id*=PLAYERBAR]');
  const btns = [...document.querySelectorAll('button,[role=button],a')].filter(vis);
  const waveText = (e) => /^\s*(моя волна|my wave)/i.test(e.textContent || '');
  let t = null;
  if (step === 0) {
    t = btns.find((b) => !inNav(b) && /моя волна|my ?wave|vibe/.test(label(b)) && isPlay(b));
    if (!t) t = btns.filter((b) => !inNav(b) && !inBar(b) && isPlay(b)).find((b) => {
      for (let e = b.parentElement, i = 0; e && i < 6; e = e.parentElement, i++) if (/(моя волна|my wave)/i.test(e.textContent || '') && (e.textContent || '').length < 400) return true;
      return false;
    });
  } else if (step === 1) {
    const blocks = [...document.querySelectorAll('main *,[class*=ontent] *')].filter((e) => vis(e) && !inNav(e) && waveText(e));
    blocks.sort((a, b) => (a.textContent || '').length - (b.textContent || '').length);
    if (blocks[0]) t = blocks[0].closest('a,button,[role=button]') || blocks[0];
  } else if (step === 2) {
    t = btns.find((b) => inNav(b) && /моя волна|my ?wave/.test(label(b)));
    if (t) { t.click(); return 'nav'; }
  } else if (step === 3) {
    t = btns.find((b) => inBar(b) && isPlay(b)) || btns.find((b) => !inNav(b) && /(воспроизвести|воспроизведение|play_button|playbutton|play button|^\s*play\s*$)/.test(label(b)) && isPlay(b));
  }
  if (!t) {
    const main = btns.filter((b) => !inNav(b)).map((b) => label(b).trim().slice(0, 22)).filter(Boolean);
    return 'none|' + location.host + location.pathname + '|' + 'шаг ' + step + '|' + main.slice(0, 16).join(' / ');
  }
  document.querySelectorAll('[data-tvhub-target]').forEach((e) => e.removeAttribute('data-tvhub-target'));
  t.setAttribute('data-tvhub-target', '1');
  // Запоминаем прокрутку, чтобы вернуть страницу на место после нажатия
  const saved = [];
  for (let e = t.parentElement; e; e = e.parentElement) if (e.scrollHeight > e.clientHeight) saved.push([e, e.scrollTop]);
  window.__tvhubScroll = { saved, y: window.scrollY };
  let r = t.getBoundingClientRect();
  if (r.top < 0 || r.bottom > innerHeight || r.left < 0 || r.right > innerWidth) { t.scrollIntoView({ block: 'center', behavior: 'instant' }); r = t.getBoundingClientRect(); }
  return 'at|' + Math.round(r.left + r.width / 2) + '|' + Math.round(r.top + r.height / 2);
})`

const waveRestoreJS = `(() => { const s = window.__tvhubScroll; if (!s) return; s.saved.forEach(([e, v]) => { e.scrollTop = v; }); window.scrollTo(0, s.y); window.__tvhubScroll = null; })()`
const waveJSClickJS = `(() => { const e = document.querySelector('[data-tvhub-target]'); if (e) e.click(); return !!e; })()`

func mouseClick(t cdpTarget, x, y int) {
	cdpCall(t.WS, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y})
	time.Sleep(120 * time.Millisecond) // кнопка в карточке может появляться только при наведении
	cdpCall(t.WS, "Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1})
	cdpCall(t.WS, "Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})
}

var (
	waveMu     sync.Mutex
	waveInfoMu sync.Mutex
	waveInfo   string // что получилось при последней попытке включить музыку
	waveInfoAt time.Time
)

func setWaveInfo(s string) string {
	waveInfoMu.Lock()
	waveInfo, waveInfoAt = s, time.Now()
	waveInfoMu.Unlock()
	return s
}

// lastWaveInfo — итог последней попытки, если она была недавно (показываем на экране музыки).
func lastWaveInfo() string {
	waveInfoMu.Lock()
	defer waveInfoMu.Unlock()
	if time.Since(waveInfoAt) > 5*time.Minute {
		return ""
	}
	return waveInfo
}

func evalWave(t cdpTarget, step int) string {
	res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": waveJS + "(" + fmt.Sprint(step) + ")", "returnByValue": true, "userGesture": true})
	if err != nil {
		return ""
	}
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(res, &r)
	return r.Result.Value
}

func pressKey(t cdpTarget, key, code string, vk int) {
	for _, typ := range []string{"keyDown", "keyUp"} {
		p := map[string]any{"type": typ, "key": key, "code": code, "windowsVirtualKeyCode": vk}
		if typ == "keyDown" && len(key) == 1 {
			p["text"] = key
		}
		cdpCall(t.WS, "Input.dispatchKeyEvent", p)
	}
}

// startWave включает «Мою волну» в Яндекс Музыке (не дольше ~15 секунд).
// Если музыка уже играет — ничего не делает. Возвращает описание результата.
func startWave() string {
	if !waveMu.TryLock() {
		return "Уже включаю музыку…"
	}
	defer waveMu.Unlock()
	setWaveInfo("Включаю «Мою волну»…")
	playing := func() bool {
		st, _ := getMedia()
		return st.Status == "Playing"
	}
	waitPlay := func(d time.Duration) bool {
		for t := time.Now(); time.Since(t) < d; time.Sleep(300 * time.Millisecond) {
			if playing() {
				return true
			}
		}
		return false
	}
	if playing() {
		return setWaveInfo("")
	}
	// Ждём, пока страница Яндекс Музыки загрузится (до 8 секунд)
	var page cdpTarget
	for t := time.Now(); time.Since(t) < 8*time.Second && page.WS == ""; time.Sleep(400 * time.Millisecond) {
		for _, x := range musicTargets() {
			res, err := cdpCall(x.WS, "Runtime.evaluate", map[string]any{"expression": "document.readyState", "returnByValue": true})
			if err == nil && strings.Contains(string(res), "complete") {
				page = x
				break
			}
		}
	}
	if page.WS == "" {
		tapVK(0xB3, true) // нет страницы — пробуем клавишу «плей»
		if waitPlay(2500 * time.Millisecond) {
			return setWaveInfo("")
		}
		return setWaveInfo("Не удалось управлять Яндекс Музыкой: TVHub не видит её страницу. Откройте её кнопкой «Открыть Яндекс Музыку» и нажмите «плей» ещё раз")
	}
	time.Sleep(700 * time.Millisecond) // кнопки появляются чуть позже загрузки
	diag := ""
	navDone := false
	for step := 0; step < 4; step++ {
		v := evalWave(page, step)
		switch {
		case v == "playing":
			return setWaveInfo("")
		case v == "nav":
			// Открыли страницу «Моя волна» через меню — ищем кнопку заново уже на ней
			if !navDone {
				navDone = true
				time.Sleep(2500 * time.Millisecond)
				if playing() {
					return setWaveInfo("")
				}
				step = -1
			}
		case strings.HasPrefix(v, "at|"):
			var x, y int
			fmt.Sscanf(strings.ReplaceAll(strings.TrimPrefix(v, "at|"), "|", " "), "%d %d", &x, &y)
			mouseClick(page, x, y)
			ok := waitPlay(2500 * time.Millisecond)
			if !ok && !playing() {
				// Клик мышью не сработал — нажимаем на элемент из самой страницы
				cdpCall(page.WS, "Runtime.evaluate", map[string]any{"expression": waveJSClickJS, "userGesture": true})
				ok = waitPlay(2500 * time.Millisecond)
			}
			cdpCall(page.WS, "Runtime.evaluate", map[string]any{"expression": waveRestoreJS})
			if ok {
				return setWaveInfo("")
			}
		case strings.HasPrefix(v, "none|"):
			diag = strings.TrimPrefix(v, "none|")
		}
	}
	// Горячие клавиши Яндекс Музыки: P — воспроизведение, пробел
	pressKey(page, "p", "KeyP", 0x50)
	if waitPlay(2 * time.Second) {
		return setWaveInfo("")
	}
	pressKey(page, " ", "Space", 0x20)
	if waitPlay(2 * time.Second) {
		return setWaveInfo("")
	}
	log.Printf("музыка: «Моя волна» не включилась; %s", diag)
	if diag == "" {
		return setWaveInfo("Нажал «Моя волна», но музыка не заиграла. Проверьте, что вы вошли в аккаунт Яндекса")
	}
	return setWaveInfo("Не получилось включить «Мою волну». Пришлите этот текст разработчику: " + diag)
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
