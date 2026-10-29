// Эквалайзер звука компьютера. В Windows нет общего эквалайзера, поэтому TVHub управляет
// бесплатной программой Equalizer APO: пишет свой файл настроек, и звук меняется сразу.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

// EQ — настройки эквалайзера: 10 полос, усиление каждой от −12 до +12 дБ.
type EQ struct {
	On     bool      `json:"on"`
	Preset string    `json:"preset,omitempty"`
	Gains  []float64 `json:"gains"`
}

var eqBands = []int{31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

type eqPreset struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Gains []float64 `json:"gains"`
}

var eqPresets = []eqPreset{
	{"flat", "Обычный", []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	{"bass", "Больше баса", []float64{7, 6, 4.5, 2, 0, 0, 0, 0, 0, 0}},
	{"movie", "Кино", []float64{4, 3, 1, 0, -1, 1, 3, 3.5, 3, 2}},
	{"voice", "Чёткая речь", []float64{-3, -2, -1, 0, 2, 4, 5, 4, 2, 0}},
	{"music", "Музыка", []float64{4, 3, 1, 0, -1, 0, 1, 2.5, 3.5, 4}},
	{"rock", "Рок", []float64{5, 4, 2, -1, -2, -1, 2, 3.5, 4.5, 5}},
	{"pop", "Поп", []float64{-1, 1, 3, 4, 3, 0, -1, -1, 1, 2}},
	{"night", "Ночной", []float64{-5, -4, -2, 0, 1, 2, 2, 1, -1, -3}},
	{"treble", "Больше высоких", []float64{0, 0, 0, 0, 0, 1, 3, 5, 6, 7}},
}

func eqClamp(g []float64) []float64 {
	out := make([]float64, len(eqBands))
	for i := range out {
		if i < len(g) && !math.IsNaN(g[i]) {
			out[i] = math.Max(-12, math.Min(12, math.Round(g[i]*2)/2))
		}
	}
	return out
}

func currentEQ() EQ {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg.Settings.EQ == nil {
		return EQ{Preset: "flat", Gains: eqClamp(nil)}
	}
	e := *cfg.Settings.EQ
	e.Gains = eqClamp(e.Gains)
	return e
}

// eqConfigText — текст для Equalizer APO. Предусилитель снижаем на величину самого большого
// подъёма, чтобы громкие места не хрипели.
func eqConfigText(e EQ) string {
	var b strings.Builder
	b.WriteString("# Этот файл пишет TVHub (эквалайзер на пульте). Изменения в нём будут перезаписаны.\n")
	if !e.On {
		return b.String()
	}
	mx := 0.0
	for _, g := range e.Gains {
		mx = math.Max(mx, g)
	}
	fmt.Fprintf(&b, "Preamp: %.1f dB\n", -mx)
	parts := make([]string, len(eqBands))
	for i, f := range eqBands {
		parts[i] = fmt.Sprintf("%d %.1f", f, e.Gains[i])
	}
	b.WriteString("GraphicEQ: " + strings.Join(parts, "; ") + "\n")
	return b.String()
}

func eqStatus() map[string]any {
	st := eqSystemStatus()
	return map[string]any{"eq": currentEQ(), "bands": eqBands, "presets": eqPresets, "status": st}
}

// setEQ применяет и сохраняет настройки. Пустые поля запроса оставляют прежние значения.
func setEQ(on *bool, preset string, gains []float64) (map[string]any, error) {
	e := currentEQ()
	if preset != "" {
		for _, p := range eqPresets {
			if p.ID == preset {
				e.Preset, e.Gains = p.ID, eqClamp(p.Gains)
				if on == nil {
					t := true
					on = &t
				}
			}
		}
	}
	if gains != nil {
		e.Gains, e.Preset = eqClamp(gains), "custom"
		for _, p := range eqPresets {
			if fmt.Sprint(eqClamp(p.Gains)) == fmt.Sprint(e.Gains) {
				e.Preset = p.ID
			}
		}
	}
	if on != nil {
		e.On = *on
	}
	err := applyEQ(e)
	cfgMu.Lock()
	cfg.Settings.EQ = &e
	saveConfigLocked()
	cfgMu.Unlock()
	pushEvent(map[string]any{"type": "eq", "eq": e})
	st := eqStatus()
	if err != nil {
		st["error"] = err.Error()
	}
	return st, err
}

func handleEQ(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, eqStatus())
		return
	}
	var req struct {
		On       *bool     `json:"on"`
		Preset   string    `json:"preset"`
		Gains    []float64 `json:"gains"`
		Download bool      `json:"download"`
		Config   bool      `json:"configurator"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req)
	if req.Config {
		if err := openEQConfigurator(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if req.Download {
		if err := openEQDownload(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	st, err := setEQ(req.On, req.Preset, req.Gains)
	st["ok"] = err == nil
	writeJSON(w, st)
}

const eqDownloadURL = "https://sourceforge.net/projects/equalizerapo/files/latest/download"
