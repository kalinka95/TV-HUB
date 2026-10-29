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
$prevCover = ''; $curCover = ''
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
    if ($key -ne $script:lastKey) {
      $script:lastKey = $key; $script:coverTries = 0; $script:haveCover = $false; $o.cover = ''; $script:myPos = 0; $script:rawLast = -1
      if ($script:curCover) { $script:prevCover = $script:curCover }; $script:curCover = ''
    }
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
    # Обложка часто приходит позже названия — пробуем несколько раз.
    # Первые секунды после смены трека плеер может ещё отдавать обложку прошлого трека — её не берём.
    if (-not $script:haveCover -and $script:coverTries -lt 10 -and $p.Thumbnail) {
      $script:coverTries++
      try {
        $rs = Await ($p.Thumbnail.OpenReadAsync()) ([Windows.Storage.Streams.IRandomAccessStreamWithContentType])
        if ($rs -and $rs.Size -gt 0 -and $rs.Size -lt 3000000) {
          $reader = [Windows.Storage.Streams.DataReader]::new($rs.GetInputStreamAt(0))
          $null = Await ($reader.LoadAsync([uint32]$rs.Size)) ([uint32])
          $bytes = New-Object byte[] ([int]$rs.Size)
          $reader.ReadBytes($bytes)
          $reader.Dispose(); $rs.Dispose()
          $b64 = [Convert]::ToBase64String($bytes)
          if ($b64 -ne $script:prevCover -or $script:coverTries -ge 6) {
            $o.cover = $b64; $script:curCover = $b64
            $script:haveCover = $true
          }
        }
      } catch {}
    }
  }
  [Console]::Out.WriteLine(($o | ConvertTo-Json -Compress))
  [Console]::Out.Flush()
}
$tick = 99
while ($true) {
  # Команды от TVHub (пауза, следующий, предыдущий, перемотка) приходят через файл
  if ($cmdFile -and (Test-Path $cmdFile)) {
    $c = ''
    try { $c = ([IO.File]::ReadAllText($cmdFile)).Trim(); Remove-Item $cmdFile -Force } catch {}
    $s = $mgr.GetCurrentSession()
    # seek:секунды — перемотать; setpos:секунды — TVHub уже перемотал сам, только запоминаем позицию
    if ($c -match '^(seek|setpos):([0-9.]+)$') {
      $v = [double]::Parse($Matches[2], [Globalization.CultureInfo]::InvariantCulture)
      if ($Matches[1] -eq 'seek' -and $s) { $null = Await ($s.TryChangePlaybackPositionAsync([long]($v * 10000000))) ([bool]) }
      $script:myPos = $v; $script:lastT = [DateTime]::UtcNow
      $c = ''
    }
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
	Liked    *bool   `json:"liked,omitempty"`    // трек в «Мне нравится» (nil — TVHub не видит страницу Яндекс Музыки)
	At       int64   `json:"at"`
}

var (
	mediaMu      sync.Mutex
	media        mediaState
	mediaCover   []byte
	mediaCmd     *exec.Cmd
	mediaLastReq time.Time
	mediaFails   int
	seekAt       time.Time // после перемотки пару секунд не верим старой позиции от PowerShell
	seekPos      float64
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
			if since := time.Since(seekAt); since < 1500*time.Millisecond && media.Title != "" {
				media.Pos = seekPos
				if media.Status == "Playing" {
					media.Pos += since.Seconds()
				}
			}
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
	defer func() {
		fillCoverFallback(&st)
		st.Liked = likeState(st)
		// Сразу после перемотки страница ещё может сообщать старое место — не мешаем
		if st.Title != "" && time.Since(seekAt) > 1500*time.Millisecond {
			if p, ok := pagePosition(st.Title, st.Dur); ok {
				st.Pos = p
			}
		}
	}()
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

// mediaSeek перематывает трек на pos секунд. Яндекс Музыку (сайт или приложение, которое
// открыл TVHub) перематываем прямо на странице, остальное — через управление медиа Windows.
func mediaSeek(pos float64) error {
	ensureMediaMonitor()
	mediaMu.Lock()
	st, running := media, mediaCmd != nil
	mediaMu.Unlock()
	if st.Title == "" {
		if w, ok := webMusicInfo(); ok {
			st = w
		}
	}
	if st.Title == "" {
		return errors.New("сейчас ничего не играет")
	}
	if st.Dur > 0 && pos > st.Dur-1 {
		pos = st.Dur - 1
	}
	pos = max(pos, 0)
	cmd := "seek:"
	if seekMusicPage(st.Title, pos, st.Dur) {
		cmd = "setpos:"
	} else if !running {
		return errors.New("этот плеер не даёт перематывать")
	}
	mediaMu.Lock()
	seekAt, seekPos = time.Now(), pos
	if media.Title != "" {
		media.Pos = pos
	}
	mediaMu.Unlock()
	if running {
		os.WriteFile(mediaCmdFile(), []byte(fmt.Sprintf("%s%.1f", cmd, pos)), 0o644)
	}
	return nil
}

// pageAudioJS — функция на странице Яндекс Музыки, которая находит то аудио, что играет сейчас.
// Аудио может быть не вставлено в страницу, а следующий трек часто загружается заранее
// вторым аудио — поэтому запоминаем всё, что страница запускала, и выбираем по длительности.
const pageAudioJS = `(dur) => {
  if (!window.__tvhubHook) {
    window.__tvhubHook = true; window.__tvhubEls = new Set();
    const play = HTMLMediaElement.prototype.play;
    HTMLMediaElement.prototype.play = function (...a) { window.__tvhubEls.add(this); return play.apply(this, a); };
  }
  const els = [...new Set([...document.querySelectorAll('audio,video'), ...window.__tvhubEls])]
    .filter((x) => isFinite(x.duration) && x.duration > 0 && !x.ended);
  const score = (x) => (x.paused ? 1000 : 0) + (dur > 0 ? Math.abs(x.duration - dur) : 0) + (x.currentTime > 0 ? 0 : 100);
  els.sort((a, b) => score(a) - score(b));
  return els[0] || null;
}`

// pageSliderJS — полоса перемотки в плеере Яндекс Музыки (если аудио недоступно).
const pageSliderJS = `() => {
  const bar = (e) => !!e.closest('footer,[class*=layerBar],[class*=layerbar],[class*=PlayerBar],[class*=playerBar],[class*=FullscreenPlayer],[class*=fullscreenPlayer],[data-test-id*=PLAYERBAR]');
  return [...document.querySelectorAll('input[type=range]')].find((e) => bar(e) && !/громк|volume/i.test((e.getAttribute('aria-label') || '') + ' ' + e.className)) || null;
}`

// seekMusicPage перематывает прямо на странице и проверяет, что перемотка случилась.
func seekMusicPage(title string, pos, dur float64) bool {
	js := fmt.Sprintf(`((pos, title, dur) => {
		const m = navigator.mediaSession && navigator.mediaSession.metadata;
		if (!m || m.title !== title) return 'no';
		const a = (%s)(dur);
		if (a) { a.currentTime = Math.max(0, Math.min(pos, a.duration - 0.5)); window.__tvhubSeek = a; return 'audio'; }
		const s = (%s)();
		if (!s) return 'no';
		const max = parseFloat(s.max) || 100, v = dur > 0 && Math.abs(max - dur) > dur * 0.2 ? pos / dur * max : pos;
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(s, String(v));
		s.dispatchEvent(new Event('input', { bubbles: true }));
		s.dispatchEvent(new Event('change', { bubbles: true }));
		window.__tvhubSeek = null;
		return 'slider';
	})(%f, %q, %f)`, pageAudioJS, pageSliderJS, pos, title, dur)
	check := fmt.Sprintf(`((pos, dur) => {
		const a = window.__tvhubSeek || (%s)(dur);
		if (a) return Math.abs(a.currentTime - pos) < 4;
		const s = (%s)();
		if (!s) return false;
		const max = parseFloat(s.max) || 100, v = parseFloat(s.value), p = dur > 0 && Math.abs(max - dur) > dur * 0.2 ? v / max * dur : v;
		return Math.abs(p - pos) < 4;
	})(%f, %f)`, pageAudioJS, pageSliderJS, pos, dur)
	val := func(res json.RawMessage) string {
		var r struct {
			Result struct {
				Value any `json:"value"`
			} `json:"result"`
		}
		json.Unmarshal(res, &r)
		return fmt.Sprint(r.Result.Value)
	}
	for _, t := range musicTargets() {
		res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": js, "returnByValue": true, "userGesture": true})
		if err != nil || val(res) == "no" {
			continue
		}
		time.Sleep(450 * time.Millisecond)
		if res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": check, "returnByValue": true}); err == nil && val(res) == "true" {
			return true
		}
		log.Printf("музыка: перемотка на странице не сработала, пробую через Windows")
		return false
	}
	return false
}

var (
	pagePosMu    sync.Mutex
	pagePosAt    time.Time
	pagePosTitle string
	pagePosVal   float64
	pagePosOK    bool
)

// pagePosition — где сейчас трек по самой странице Яндекс Музыки. Windows узнаёт позицию
// от плеера не всегда, а страница знает её точно. Ответ живёт полсекунды.
func pagePosition(title string, dur float64) (float64, bool) {
	pagePosMu.Lock()
	defer pagePosMu.Unlock()
	if pagePosTitle == title && time.Since(pagePosAt) < 500*time.Millisecond {
		return pagePosVal, pagePosOK
	}
	pagePosTitle, pagePosAt, pagePosOK = title, time.Now(), false
	js := fmt.Sprintf(`((title, dur) => {
		const m = navigator.mediaSession && navigator.mediaSession.metadata;
		if (!m || m.title !== title) return -1;
		const a = (%s)(dur);
		return a ? a.currentTime : -1;
	})(%q, %f)`, pageAudioJS, title, dur)
	for _, t := range musicTargets() {
		res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": js, "returnByValue": true})
		if err != nil {
			continue
		}
		var r struct {
			Result struct {
				Value float64 `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(res, &r) == nil && r.Result.Value >= 0 {
			pagePosVal, pagePosOK = r.Result.Value, true
			break
		}
	}
	return pagePosVal, pagePosOK
}

// likeJS(нажать, название) находит в плеере Яндекс Музыки кнопку «Мне нравится» и
// возвращает, отмечен ли трек: 'on', 'off' или 'none' (кнопки или трека нет).
// По подписи кнопки это видно не всегда, поэтому при первом нажатии страница запоминает,
// как кнопка выглядит до и после (классы и значок), и дальше узнаёт состояние по виду.
const likeJS = `((click, title) => {
  const m = navigator.mediaSession && navigator.mediaSession.metadata;
  if (!m || (title && m.title !== title)) return 'none';
  const vis = (e) => { const r = e.getBoundingClientRect(); return r.width > 4 && r.height > 4 && getComputedStyle(e).visibility !== 'hidden'; };
  const label = (b) => ((b.getAttribute('aria-label') || '') + ' ' + (b.title || '') + ' ' + (b.getAttribute('data-test-id') || '')).toLowerCase();
  const inBar = (e) => !!e.closest('footer,[class*=layerBar],[class*=layerbar],[class*=PlayerBar],[class*=playerBar],[class*=player-controls],[data-test-id*=PLAYERBAR],[class*=FullscreenPlayer],[class*=fullscreenPlayer]');
  const find = () => [...document.querySelectorAll('button,[role=button]')].find((b) => vis(b) && inBar(b)
    && /like|нрав|лайк|избран|favorite/.test(label(b)) && !/dislike|не нрав|дизлайк/.test(label(b)));
  const cls = (e) => String(e.className && e.className.baseVal !== undefined ? e.className.baseVal : e.className || '');
  const fp = (b) => label(b) + '|' + (b.getAttribute('aria-pressed') || '') + (b.getAttribute('aria-checked') || '') + '|' + cls(b).replace(/\S*(focus|hover|anim|transition|pressed-anim)\S*/gi, '').replace(/\s+/g, ' ') + '|' +
    [...b.querySelectorAll('svg,use,path')].map((e) => (e.getAttribute('href') || e.getAttribute('xlink:href') || '') + cls(e) + (e.getAttribute('fill') || '') + (e.getAttribute('d') || '').slice(0, 40)).join(',');
  const guess = (b) => b.getAttribute('aria-pressed') === 'true' || b.getAttribute('aria-checked') === 'true'
    || /_on\b|(^|\s)(active|liked|checked)\b|isLiked|_liked|_active|filled/i.test(cls(b) + ' ' + [...b.querySelectorAll('svg,use')].map((e) => cls(e) + (e.getAttribute('href') || e.getAttribute('xlink:href') || '')).join(' '))
    || /удалить|убрать|remove|unlike/.test(label(b));
  const store = (k, v) => { try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; } };
  const state = (b) => {
    const f = fp(b), on = store('tvhubLikeOn'), off = store('tvhubLikeOff');
    if (on && f === on) return 'on';
    if (off && f === off) return 'off';
    return guess(b) ? 'on' : (on && off ? 'off' : 'unknown');
  };
  const b = find();
  if (!b) return 'none';
  const before = state(b);
  if (click) {
    const f0 = fp(b), wasOn = guess(b);
    b.click();
    // Запоминаем, как выглядит кнопка в обоих состояниях
    setTimeout(() => {
      const b2 = find();
      if (!b2) return;
      const f1 = fp(b2);
      if (f1 === f0 || (store('tvhubLikeOn') && store('tvhubLikeOff'))) return;
      const nowOn = before === 'on' ? false : before === 'off' ? true : !wasOn;
      store('tvhubLikeOn', nowOn ? f1 : f0); store('tvhubLikeOff', nowOn ? f0 : f1);
    }, 500);
  }
  return before;
})`

func evalLike(t cdpTarget, click bool, title string) string {
	res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("%s(%v, %q)", likeJS, click, title), "returnByValue": true, "userGesture": true})
	if err != nil {
		return "none"
	}
	var r struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	json.Unmarshal(res, &r)
	if r.Result.Value == "" {
		return "none"
	}
	return r.Result.Value
}

var (
	likeMu      sync.Mutex
	likeTitle   string
	likeAt      time.Time
	likeVal     *bool
	likeClickAt time.Time
)

// Что отмечали через TVHub: «название|исполнитель» → отмечен. Хранится в настройках.
func likeMemGet(k string) (bool, bool) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	v, ok := cfg.Likes[k]
	return v, ok
}

func likeMemSet(k string, on bool) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg.Likes == nil {
		cfg.Likes = map[string]bool{}
	}
	if len(cfg.Likes) > 3000 { // не даём списку расти бесконечно
		cfg.Likes = map[string]bool{}
	}
	cfg.Likes[k] = on
	saveConfigLocked()
}

func likeKey(st mediaState) string { return st.Title + "|" + st.Artist }

// likeState — отмечен ли играющий трек в Яндекс Музыке (ответ живёт пару секунд,
// чтобы частые запросы экрана и телефона не дёргали страницу).
func likeState(st mediaState) *bool {
	if st.Title == "" {
		return nil
	}
	likeMu.Lock()
	defer likeMu.Unlock()
	if likeTitle == likeKey(st) && time.Since(likeAt) < 2*time.Second {
		return likeVal
	}
	likeTitle, likeAt, likeVal = likeKey(st), time.Now(), nil
	for _, t := range musicTargets() {
		v := evalLike(t, false, st.Title)
		if v == "none" {
			continue
		}
		var on bool
		switch {
		case v == "on":
			on = true
		case v == "off":
			on = false
			if mem, ok := likeMemGet(likeKey(st)); ok && mem && time.Since(likeClickAt) < 10*time.Second {
				on = mem // страница могла не успеть обновить кнопку
			}
		default: // по виду кнопки не понять — берём то, что помним
			on, _ = likeMemGet(likeKey(st))
		}
		likeVal = &on
		break
	}
	return likeVal
}

// mediaLike нажимает «Мне нравится» в Яндекс Музыке и возвращает, отмечен ли трек теперь.
func mediaLike() (bool, error) {
	st, _ := getMedia()
	if st.Title == "" {
		return false, errors.New("сейчас ничего не играет")
	}
	for _, t := range musicTargets() {
		before := evalLike(t, true, st.Title)
		if before == "none" {
			continue
		}
		was, known := likeMemGet(likeKey(st))
		if before == "unknown" {
			before = "off"
			if known && was {
				before = "on"
			}
		}
		on := before != "on"
		// Страница обновляет кнопку не сразу — проверяем, что получилось
		time.Sleep(900 * time.Millisecond)
		if after := evalLike(t, false, st.Title); after == "on" || after == "off" {
			if after != before {
				on = after == "on"
			}
		}
		likeMemSet(likeKey(st), on)
		likeMu.Lock()
		likeTitle, likeAt, likeVal, likeClickAt = likeKey(st), time.Now(), &on, time.Now()
		likeMu.Unlock()
		return on, nil
	}
	return false, errors.New("«Мне нравится» работает только с Яндекс Музыкой, открытой из TVHub")
}

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
		volumeAction(a) // без большого окна Windows — со своим окошком громкости
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
			const a = (` + pageAudioJS + `)(0);
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
			go func() { time.Sleep(600 * time.Millisecond); curtainDown(windowOfPID(p)) }()
			return nil
		}
		before := topWindows()
		// Порт отладки — чтобы TVHub мог нажать «Моя волна» (если приложение это позволяет)
		if err := windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(exe),
			windows.StringToUTF16Ptr("--remote-debugging-port="+ymDesktopPort), windows.StringToUTF16Ptr(filepath.Dir(exe)), 1); err != nil {
			return err
		}
		go revealProgram(before, 0)
		return nil
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

// likeDiagJS — что видно на странице про кнопку «Мне нравится» (для отчёта разработчику).
const likeDiagJS = `(() => {
  const m = navigator.mediaSession && navigator.mediaSession.metadata;
  const label = (b) => ((b.getAttribute('aria-label') || '') + ' ' + (b.title || '') + ' ' + (b.getAttribute('data-test-id') || '')).toLowerCase();
  const like = [...document.querySelectorAll('button,[role=button]')].filter((b) => /like|нрав|лайк|избран|favorite/.test(label(b)));
  const short = (b) => b.outerHTML.replace(/<path[^>]*d="([^"]{0,30})[^"]*"/g, '<path d="$1…"').slice(0, 500);
  let on = '', off = '';
  try { on = localStorage.getItem('tvhubLikeOn') || ''; off = localStorage.getItem('tvhubLikeOff') || ''; } catch (e) {}
  return JSON.stringify({ url: location.host + location.pathname, title: m ? m.title : '', buttons: like.slice(0, 6).map(short), learnedOn: on.slice(0, 300), learnedOff: off.slice(0, 300) });
})()`

// mediaDiag собирает отчёт о музыке и погоде, который можно прислать разработчику.
func mediaDiag() map[string]any {
	st, _ := getMedia()
	out := map[string]any{"title": st.Title, "artist": st.Artist, "app": st.App, "liked": st.Liked}
	var pages []any
	for _, t := range musicTargets() {
		res, err := cdpCall(t.WS, "Runtime.evaluate", map[string]any{"expression": likeDiagJS, "returnByValue": true})
		if err != nil {
			pages = append(pages, "ошибка: "+err.Error())
			continue
		}
		var r struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		json.Unmarshal(res, &r)
		pages = append(pages, json.RawMessage(r.Result.Value))
	}
	out["pages"] = pages
	return out
}
