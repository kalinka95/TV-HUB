// Телепрограмма (XMLTV): загрузка, привязка к каналам, «сейчас в эфире», напоминания и уведомления.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Открытые источники телепрограммы для России — пробуем по очереди, если в плейлисте своей нет.
var defaultEPGSources = []string{
	"https://epg.pw/xmltv/epg_RU.xml.gz",
	"https://iptvx.one/EPG",
	"http://epg.it999.ru/epg.xml.gz",
}

type Prog struct {
	Start int64  `json:"s"`
	Stop  int64  `json:"e"`
	Title string `json:"t"`
	Desc  string `json:"d,omitempty"`
	Cat   string `json:"c,omitempty"`
}

type epgChan struct {
	ID    string
	Names []string
	Progs []Prog
}

type epgState struct {
	chans    map[string]*epgChan // ключ — id в нижнем регистре
	byName   map[string]string   // нормализованное название → id
	source   string
	loadedAt time.Time
	err      string
	loading  bool
	progs    int
}

var (
	epgMu sync.RWMutex
	epg   = epgState{chans: map[string]*epgChan{}, byName: map[string]string{}}
)

// ---------- Загрузка ----------

func epgSources() []string {
	cfgMu.Lock()
	own := strings.TrimSpace(cfg.Settings.EPGURL)
	cfgMu.Unlock()
	if own != "" {
		return []string{own}
	}
	tvgMu.Lock()
	pl := playlistTvg
	tvgMu.Unlock()
	var out []string
	if pl != "" {
		out = append(out, pl)
	}
	if env := os.Getenv("TVHUB_EPG_DEFAULT"); env != "" { // для отладки
		return append(out, env)
	}
	return append(out, defaultEPGSources...)
}

func openEPG(ctx context.Context, src string) (io.ReadCloser, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", defaultUA)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("сервер ответил %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
	return os.Open(strings.Trim(src, `"`))
}

type xmlText struct {
	Lang  string `xml:"lang,attr"`
	Value string `xml:",chardata"`
}

func pickText(list []xmlText) string {
	best := ""
	for _, t := range list {
		v := strings.TrimSpace(t.Value)
		if v == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(t.Lang), "ru") || hasCyrillic(v) {
			return v
		}
		if best == "" {
			best = v
		}
	}
	return best
}

func parseXMLTVTime(s string) int64 {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"20060102150405 -0700", "20060102150405-0700", "20060102150405", "200601021504 -0700", "200601021504"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func parseXMLTV(r io.Reader) (map[string]*epgChan, int, error) {
	br := bufio.NewReaderSize(r, 256*1024)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, 0, err
		}
		defer gz.Close()
		br = bufio.NewReaderSize(gz, 256*1024)
	}
	dec := xml.NewDecoder(br)
	dec.Strict = false
	dec.CharsetReader = charsetReader
	now := time.Now().Unix()
	from, to := now-6*3600, now+48*3600
	chans := map[string]*epgChan{}
	get := func(id string) *epgChan {
		k := strings.ToLower(strings.TrimSpace(id))
		c := chans[k]
		if c == nil {
			c = &epgChan{ID: id}
			chans[k] = c
		}
		return c
	}
	count := 0
	sawTV := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if count > 0 {
				break // обрезанный файл: берём то, что успели прочитать
			}
			return nil, 0, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "tv":
			sawTV = true
		case "channel":
			var c struct {
				ID    string    `xml:"id,attr"`
				Names []xmlText `xml:"display-name"`
			}
			if dec.DecodeElement(&c, &se) == nil && c.ID != "" {
				ch := get(c.ID)
				for _, n := range c.Names {
					if v := strings.TrimSpace(n.Value); v != "" {
						ch.Names = append(ch.Names, v)
					}
				}
			}
		case "programme":
			var p struct {
				Start   string    `xml:"start,attr"`
				Stop    string    `xml:"stop,attr"`
				Channel string    `xml:"channel,attr"`
				Title   []xmlText `xml:"title"`
				Desc    []xmlText `xml:"desc"`
				Cat     []xmlText `xml:"category"`
			}
			if dec.DecodeElement(&p, &se) != nil {
				continue
			}
			s, e := parseXMLTVTime(p.Start), parseXMLTVTime(p.Stop)
			if s == 0 || e < from || s > to {
				continue
			}
			if e <= s {
				e = s + 1800
			}
			desc := pickText(p.Desc)
			if utf8.RuneCountInString(desc) > 400 {
				desc = string([]rune(desc)[:400]) + "…"
			}
			ch := get(p.Channel)
			ch.Progs = append(ch.Progs, Prog{Start: s, Stop: e, Title: pickText(p.Title), Desc: desc, Cat: pickText(p.Cat)})
			count++
		}
	}
	if !sawTV {
		return nil, 0, errors.New("это не телепрограмма в формате XMLTV")
	}
	for _, c := range chans {
		sort.Slice(c.Progs, func(i, j int) bool { return c.Progs[i].Start < c.Progs[j].Start })
	}
	return chans, count, nil
}

// charsetReader понимает windows-1251 — в такой кодировке бывают старые файлы телепрограммы.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "":
		return in, nil
	case "windows-1251", "cp1251", "win-1251":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		var out bytes.Buffer
		for _, c := range b {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC0:
				out.WriteRune(rune(0x410 + int(c) - 0xC0))
			case c == 0xA8:
				out.WriteRune('Ё')
			case c == 0xB8:
				out.WriteRune('ё')
			default:
				out.WriteRune(' ')
			}
		}
		return &out, nil
	}
	return nil, fmt.Errorf("кодировка %s не поддерживается", label)
}

var epgReloadMu sync.Mutex

func reloadEPG() {
	if !epgReloadMu.TryLock() {
		return
	}
	defer epgReloadMu.Unlock()
	epgMu.Lock()
	epg.loading = true
	epgMu.Unlock()
	var lastErr error
	for _, src := range epgSources() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		rc, err := openEPG(ctx, src)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("%s: %v", src, err)
			continue
		}
		chans, n, err := parseXMLTV(rc)
		rc.Close()
		cancel()
		if err != nil || n == 0 {
			if err == nil {
				err = errors.New("в телепрограмме нет передач на ближайшие дни")
			}
			lastErr = fmt.Errorf("%s: %v", src, err)
			continue
		}
		byName := map[string]string{}
		for k, c := range chans {
			for _, nm := range c.Names {
				byName[nameKey(nm)] = k
				byName[nameKey(russifyName(nm))] = k
			}
		}
		epgMu.Lock()
		epg = epgState{chans: chans, byName: byName, source: src, loadedAt: time.Now(), progs: n}
		epgMu.Unlock()
		log.Printf("телепрограмма: %s — %d каналов, %d передач", src, len(chans), n)
		pushEvent(map[string]string{"type": "epg"})
		return
	}
	epgMu.Lock()
	epg.loading = false
	if lastErr != nil {
		epg.err = lastErr.Error()
	}
	epgMu.Unlock()
	log.Printf("телепрограмма не загрузилась: %v", lastErr)
}

// ---------- Привязка к каналам ----------

func matchEPG(c Channel) *epgChan {
	if c.TvgID != "" {
		if ch := epg.chans[strings.ToLower(strings.TrimSpace(c.TvgID))]; ch != nil && len(ch.Progs) > 0 {
			return ch
		}
	}
	for _, n := range []string{russifyName(c.Name), c.Name} {
		if id, ok := epg.byName[nameKey(n)]; ok {
			if ch := epg.chans[id]; ch != nil && len(ch.Progs) > 0 {
				return ch
			}
		}
	}
	return nil
}

func nowNext(progs []Prog, now int64) (cur, next *Prog) {
	i := sort.Search(len(progs), func(i int) bool { return progs[i].Stop > now })
	if i < len(progs) {
		if progs[i].Start <= now {
			cur = &progs[i]
			if i+1 < len(progs) {
				next = &progs[i+1]
			}
		} else {
			next = &progs[i]
		}
	}
	return
}

func handleEPGNow(w http.ResponseWriter, r *http.Request) {
	chs, _ := getChannels(false)
	now := time.Now().Unix()
	type nn struct {
		C *Prog `json:"c,omitempty"`
		N *Prog `json:"n,omitempty"`
	}
	res := map[string]nn{}
	names := map[string]string{}
	cfgMu.Lock()
	latin := cfg.Settings.LatinNames
	cfgMu.Unlock()
	epgMu.RLock()
	for _, c := range chs {
		ch := matchEPG(c)
		if ch == nil {
			continue
		}
		cur, next := nowNext(ch.Progs, now)
		if cur != nil || next != nil {
			short := func(p *Prog) *Prog {
				if p == nil {
					return nil
				}
				return &Prog{Start: p.Start, Stop: p.Stop, Title: p.Title, Cat: p.Cat}
			}
			res[c.URL] = nn{short(cur), short(next)}
		}
		// Русское название из телепрограммы, если своё осталось латиницей
		if !latin && hasLatin(russifyName(c.Name)) {
			for _, n := range ch.Names {
				if hasCyrillic(n) {
					names[c.URL] = cleanName(n)
					break
				}
			}
		}
	}
	st := map[string]any{"source": epg.source, "channels": len(epg.chans), "programmes": epg.progs,
		"error": epg.err, "loading": epg.loading, "matched": len(res)}
	if !epg.loadedAt.IsZero() {
		st["updated"] = epg.loadedAt.Unix()
	}
	epgMu.RUnlock()
	writeJSON(w, map[string]any{"now": res, "names": names, "status": st})
}

func handleEPGChannel(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("u")
	chs, _ := getChannels(false)
	now := time.Now().Unix()
	out := []Prog{}
	epgMu.RLock()
	for _, c := range chs {
		if c.URL != u {
			continue
		}
		if ch := matchEPG(c); ch != nil {
			for _, p := range ch.Progs {
				if p.Stop > now-2*3600 && p.Start < now+30*3600 {
					out = append(out, p)
				}
			}
		}
		break
	}
	epgMu.RUnlock()
	writeJSON(w, map[string]any{"progs": out})
}

// ---------- Уведомления ----------

type NoticeAction struct {
	Type string `json:"type"` // play
	URL  string `json:"url,omitempty"`
}

type Notice struct {
	ID     int64         `json:"id"`
	At     int64         `json:"at"`
	Kind   string        `json:"kind"` // info, reminder, phone, vpn, update, error
	Title  string        `json:"title"`
	Text   string        `json:"text,omitempty"`
	Action *NoticeAction `json:"action,omitempty"`
}

var (
	noticeMu   sync.Mutex
	notices    []Notice
	noticeSeq  int64
	noticeRead int64
)

func notify(kind, title, text string, action *NoticeAction) {
	noticeMu.Lock()
	noticeSeq++
	n := Notice{ID: noticeSeq, At: time.Now().Unix(), Kind: kind, Title: title, Text: text, Action: action}
	notices = append([]Notice{n}, notices...)
	if len(notices) > 50 {
		notices = notices[:50]
	}
	noticeMu.Unlock()
	pushEvent(map[string]any{"type": "notice", "notice": n})
}

func handleNotices(w http.ResponseWriter, r *http.Request) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	unread := 0
	for _, n := range notices {
		if n.ID > noticeRead {
			unread++
		}
	}
	writeJSON(w, map[string]any{"notices": notices, "unread": unread})
}

func handleNoticesRead(w http.ResponseWriter, r *http.Request) {
	var req struct{ Clear bool }
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	noticeMu.Lock()
	noticeRead = noticeSeq
	if req.Clear {
		notices = nil
	}
	noticeMu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- Напоминания о передачах ----------

type Reminder struct {
	ChURL  string `json:"chUrl"`
	ChName string `json:"chName"`
	Title  string `json:"title"`
	Start  int64  `json:"start"`
}

func handleReminder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reminder
		Remove bool `json:"remove"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	cfgMu.Lock()
	list := cfg.Reminders[:0:0]
	for _, x := range cfg.Reminders {
		if !(x.ChURL == req.ChURL && x.Start == req.Start) {
			list = append(list, x)
		}
	}
	if !req.Remove {
		list = append(list, req.Reminder)
	}
	cfg.Reminders = list
	saveConfigLocked()
	out := cfg.Reminders
	cfgMu.Unlock()
	writeJSON(w, map[string]any{"reminders": out})
}

func reminderLoop() {
	for {
		time.Sleep(15 * time.Second)
		now := time.Now().Unix()
		var due []Reminder
		cfgMu.Lock()
		keep := cfg.Reminders[:0:0]
		for _, x := range cfg.Reminders {
			switch {
			case x.Start-60 <= now && x.Start > now-15*60:
				due = append(due, x)
			case x.Start > now:
				keep = append(keep, x)
			}
		}
		changed := len(keep) != len(cfg.Reminders)
		cfg.Reminders = keep
		if changed {
			saveConfigLocked()
		}
		cfgMu.Unlock()
		for _, x := range due {
			when := "Сейчас начинается"
			if x.Start > now {
				when = "Через минуту начнётся"
			}
			notify("reminder", fmt.Sprintf("%s «%s»", when, x.Title), "на канале «"+x.ChName+"»", &NoticeAction{Type: "play", URL: x.ChURL})
		}
	}
}

// startBackground запускает фоновые задачи: телепрограмма, напоминания, обои.
func startBackground() {
	go func() {
		// Сначала список каналов (в нём может быть ссылка на телепрограмму), потом сама программа
		getChannels(false)
		for {
			reloadEPG()
			time.Sleep(12 * time.Hour)
		}
	}()
	go reminderLoop()
	go updateLoop()
	go restoreBrightness()
	cleanupAfterUpdate()
	go refreshWallpapers()
}
