// Каналы 18+ из плейлиста: категории «18+», «XXX», «Для взрослых» и похожие собираются в одну
// категорию «Для взрослых». Пока каналы 18+ не включены в настройках, их не видно; когда включены —
// они закрыты PIN-кодом.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
)

const adultGroup = "Для взрослых"

var (
	adultGroupRe = regexp.MustCompile(`(?i)(^|[^a-zа-я0-9])(xxx|adult|18\s*\+|\+\s*18|для взрослых|эрот|erotic|porn|порно)`)
	pinDigitRe   = regexp.MustCompile(`^[0-9]{4}$`)
)

func isAdultGroup(g string) bool { return adultGroupRe.MatchString(" " + g) }

// withAdult отмечает каналы 18+ из плейлиста. Выключены в настройках — убирает их из списка.
func withAdult(list []Channel, _ bool) []Channel {
	cfgMu.Lock()
	on := cfg.Settings.Adult
	cfgMu.Unlock()
	out := make([]Channel, 0, len(list))
	for _, c := range list {
		if c.Adult || isAdultGroup(c.Group) {
			if !on {
				continue
			}
			c.Adult, c.Group = true, adultGroup
		}
		out = append(out, c)
	}
	return out
}

// adultCount — сколько каналов 18+ в текущем плейлисте (для настроек).
func adultCount() int {
	cacheMu.Lock()
	ch := cache.channels
	cacheMu.Unlock()
	n := 0
	for _, c := range ch {
		if isAdultGroup(c.Group) {
			n++
		}
	}
	return n
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
	count := adultCount()
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
		writeJSON(w, map[string]any{"ok": true, "on": cfg.Settings.Adult, "hasPin": hasPin, "count": count})
	}
}
