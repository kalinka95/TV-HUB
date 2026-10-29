// Куда идёт звук: выбор устройства вывода с экрана TVHub и с телефона.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// audioOut — устройство вывода звука. Kind — для значка: tv, headphones, speaker,
// call (гарнитура для звонков у Bluetooth-наушников — для музыки она звучит плохо).
type audioOut struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Default bool   `json:"default"`
}

func audioKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "hands-free"), strings.Contains(n, "handsfree"), strings.Contains(n, "головной телефон"), strings.Contains(n, "headset ("):
		return "call"
	case strings.Contains(n, "hdmi"), strings.Contains(n, "display audio"), strings.Contains(n, "displayport"), strings.Contains(n, "nvidia high definition"),
		strings.Contains(n, "amd high definition"), strings.Contains(n, "телевиз"), strings.Contains(n, " tv"):
		return "tv"
	case strings.Contains(n, "наушник"), strings.Contains(n, "headphone"), strings.Contains(n, "buds"), strings.Contains(n, "airpods"), strings.Contains(n, "stereo"):
		return "headphones"
	}
	return "speaker"
}

func handleAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct{ ID string }
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		if err := setAudioOutput(req.ID); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		pushEvent(map[string]any{"type": "audio"})
	}
	outs, err := audioOutputs()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if outs == nil {
		outs = []audioOut{}
	}
	writeJSON(w, map[string]any{"ok": true, "outputs": outs})
}
