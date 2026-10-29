// Каналы 18+: отдельный открытый список каталога iptv-org. Выключены по умолчанию;
// включаются в настройках после подтверждения возраста и закрыты PIN-кодом.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var adultURL = envOr("TVHUB_ADULT_URL", "https://iptv-org.github.io/iptv/categories/xxx.m3u")

const adultGroup = "Для взрослых"

var (
	adultMu    sync.Mutex
	adultList  []Channel
	adultAt    time.Time
	adultErr   string
	pinDigitRe = regexp.MustCompile(`^[0-9]{4}$`)
)

// withAdult добавляет каналы 18+ в конец списка, если они включены в настройках.
func withAdult(main []Channel, force bool) []Channel {
	cfgMu.Lock()
	on := cfg.Settings.Adult
	cfgMu.Unlock()
	if !on {
		return main
	}
	adultMu.Lock()
	if force || adultList == nil || time.Since(adultAt) > 6*time.Hour {
		if rc, err := openPlaylist(adultURL); err == nil {
			adultList = parseM3U(rc)
			rc.Close()
			adultAt, adultErr = time.Now(), ""
		} else {
			adultErr = err.Error()
			log.Printf("каналы 18+: %v", err)
			if adultList == nil {
				adultAt = time.Now().Add(-6*time.Hour + 5*time.Minute) // повторим через 5 минут
			}
		}
	}
	extra := adultList
	adultMu.Unlock()
	out := make([]Channel, 0, len(main)+len(extra))
	out = append(out, main...)
	n := 0
	for _, c := range main {
		n = max(n, c.N)
	}
	for _, c := range extra {
		n++
		c.N, c.Group, c.Adult = n, adultGroup, true
		out = append(out, c)
	}
	return out
}

func pinHash(pin string) string {
	h := sha256.Sum256([]byte("tvhub-adult:" + pin))
	return hex.EncodeToString(h[:])
}

// handleAdult: включить (с PIN-кодом), выключить, проверить PIN.
func handleAdult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On    *bool  `json:"on"`
		Pin   string `json:"pin"`
		Check bool   `json:"check"`
		Reset bool   `json:"reset"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	cfgMu.Lock()
	defer cfgMu.Unlock()
	hasPin := cfg.AdultPin != ""
	pinOK := hasPin && pinHash(req.Pin) == cfg.AdultPin
	switch {
	case req.Check:
		writeJSON(w, map[string]any{"ok": pinOK})
	case req.Reset:
		// Забыли PIN — можно задать новый, только если каналы 18+ выключены
		if cfg.Settings.Adult {
			writeJSON(w, map[string]any{"ok": false, "error": "Сначала выключите каналы 18+"})
			return
		}
		cfg.AdultPin = ""
		saveConfigLocked()
		writeJSON(w, map[string]any{"ok": true, "hasPin": false})
	case req.On != nil && *req.On:
		if !pinDigitRe.MatchString(req.Pin) {
			writeJSON(w, map[string]any{"ok": false, "error": "PIN — 4 цифры"})
			return
		}
		if hasPin && !pinOK {
			writeJSON(w, map[string]any{"ok": false, "error": "Неверный PIN"})
			return
		}
		cfg.AdultPin, cfg.Settings.Adult = pinHash(req.Pin), true
		saveConfigLocked()
		writeJSON(w, map[string]any{"ok": true, "hasPin": true})
	case req.On != nil:
		cfg.Settings.Adult = false
		saveConfigLocked()
		writeJSON(w, map[string]any{"ok": true, "hasPin": hasPin})
	default:
		adultMu.Lock()
		e := adultErr
		adultMu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "on": cfg.Settings.Adult, "hasPin": hasPin, "error": e})
	}
}
