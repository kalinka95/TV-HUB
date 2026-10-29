// Bluetooth и Wi-Fi прямо в TVHub: подключить наушники, колонку, геймпад или сеть,
// не выходя в настройки Windows.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// btDevice — устройство Bluetooth. Kind — что это: headphones, speaker, gamepad,
// keyboard, mouse, phone, computer, tv, other (для значка).
type btDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Paired    bool   `json:"paired"`
	Connected bool   `json:"connected"`
}

type wifiNet struct {
	SSID      string `json:"ssid"`
	Bars      int    `json:"bars"` // 0–4
	Secure    bool   `json:"secure"`
	Connected bool   `json:"connected"`
}

// btKind переводит класс устройства Bluetooth (MajorClass/MinorClass) в значок.
func btKind(cls, name string) string {
	c, n := strings.ToLower(cls), strings.ToLower(name)
	switch {
	case strings.Contains(c, "headphone"), strings.Contains(c, "headset"), strings.Contains(c, "handsfree"),
		strings.Contains(n, "buds"), strings.Contains(n, "airpods"), strings.Contains(n, "наушник"), strings.Contains(n, "headphone"):
		return "headphones"
	case strings.Contains(c, "loudspeaker"), strings.Contains(c, "portableaudio"), strings.Contains(c, "caraudio"), strings.Contains(c, "hifi"),
		strings.Contains(n, "speaker"), strings.Contains(n, "колонк"), strings.Contains(n, "jbl"), strings.Contains(n, "алиса"), strings.Contains(n, "station"):
		return "speaker"
	case strings.Contains(c, "gamepad"), strings.Contains(c, "joystick"), strings.Contains(n, "controller"), strings.Contains(n, "gamepad"), strings.Contains(n, "dualsense"):
		return "gamepad"
	case strings.Contains(c, "keyboard"):
		return "keyboard"
	case strings.Contains(c, "pointing"), strings.Contains(c, "mouse"), strings.Contains(c, "remote"):
		return "mouse"
	case strings.HasPrefix(c, "phone"):
		return "phone"
	case strings.HasPrefix(c, "computer"):
		return "computer"
	case strings.Contains(c, "video"), strings.Contains(c, "display"):
		return "tv"
	case strings.HasPrefix(c, "audiovideo"):
		return "headphones"
	}
	return "other"
}

func handleConnStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, connStatus())
}

func handleBT(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string
		ID     string
		On     bool
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	res, err := btAction(req.Action, req.ID, req.On)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if res == nil {
		res = map[string]any{}
	}
	res["ok"] = true
	pushEvent(map[string]any{"type": "conn"})
	writeJSON(w, res)
}

func handleWifi(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action   string
		SSID     string
		Password string
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	res, err := wifiAction(req.Action, req.SSID, req.Password)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if res == nil {
		res = map[string]any{}
	}
	res["ok"] = true
	if req.Action != "scan" {
		pushEvent(map[string]any{"type": "conn"})
	}
	writeJSON(w, res)
}
