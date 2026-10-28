// Тёмная тема для всего компьютера и тёмные сайты в окнах сервисов.
package main

import (
	"encoding/json"
	"io"
	"net/http"
)

func handleTheme(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			Windows *bool `json:"windows"`
			Sites   *bool `json:"sites"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
		if req.Windows != nil {
			if err := setWindowsDark(*req.Windows); err != nil {
				writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		}
		if req.Sites != nil {
			cfgMu.Lock()
			changed := cfg.Settings.DarkSites != *req.Sites
			cfg.Settings.DarkSites = *req.Sites
			saveConfigLocked()
			cfgMu.Unlock()
			if changed {
				go restartWebApps() // настройка Edge применяется при новом запуске сайтов
			}
		}
	}
	cfgMu.Lock()
	sites := cfg.Settings.DarkSites
	cfgMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "windows": windowsDark(), "sites": sites})
}
