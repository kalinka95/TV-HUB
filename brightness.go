// Яркость: общий API для TVHub и пульта на телефоне.
package main

import (
	"encoding/json"
	"io"
	"net/http"
)

func currentBrightness() int {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg.Settings.Brightness <= 0 {
		return 100
	}
	return cfg.Settings.Brightness
}

// changeBrightness меняет яркость на delta (или ставит level) и запоминает её.
func changeBrightness(level, delta int) (int, []displayInfo) {
	if level == 0 {
		level = currentBrightness() + delta
	}
	if level < 10 {
		level = 10
	}
	if level > 100 {
		level = 100
	}
	ds := applyBrightness(level)
	cfgMu.Lock()
	cfg.Settings.Brightness = level
	saveConfigLocked()
	cfgMu.Unlock()
	pushEvent(map[string]any{"type": "brightness", "level": level, "displays": ds})
	return level, ds
}

func handleBrightness(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, map[string]any{"level": currentBrightness()})
		return
	}
	var req struct{ Level, Delta int }
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	level, ds := changeBrightness(req.Level, req.Delta)
	writeJSON(w, map[string]any{"level": level, "displays": ds})
}

// restoreBrightness при запуске возвращает выбранную яркость.
func restoreBrightness() {
	waitUIReady()
	startDimmer()
	if lv := currentBrightness(); lv < 100 {
		applyBrightness(lv)
	}
}
