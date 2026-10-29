// Управление музыкой с экрана TVHub и с телефона: общие команды.
package main

import (
	"sync"
	"time"
)

// mediaReq — команда для плеера. Для перемотки: pos — куда (секунды) или delta — на сколько.
type mediaReq struct {
	Action string
	Pos    *float64
	Delta  float64
}

// doMediaCommand выполняет перемотку и «Мне нравится»; остальное — mediaAction.
func doMediaCommand(req mediaReq) (map[string]any, error) {
	switch req.Action {
	case "seek":
		st, _ := getMedia()
		pos := st.Pos + req.Delta
		if req.Pos != nil {
			pos = *req.Pos
		}
		if err := mediaSeek(pos); err != nil {
			return nil, err
		}
		return map[string]any{"pos": pos}, nil
	case "like":
		on, err := mediaLike()
		if err != nil {
			return nil, err
		}
		return map[string]any{"liked": on}, nil
	}
	return nil, mediaAction(req.Action)
}

var (
	dispMu      sync.Mutex
	dispShown   mediaState
	dispPendKey string
	dispPendAt  time.Time
)

// displayMedia — что показывать на экране и телефоне. При смене трека плеер сначала сообщает
// новое название, а обложку — чуть позже. Чтобы не мелькала пустая обложка, до 4 секунд
// показываем прошлый трек и меняем всё разом, когда у нового появилась обложка.
func displayMedia(st mediaState) mediaState {
	dispMu.Lock()
	defer dispMu.Unlock()
	key := st.Title + "|" + st.Artist
	prev := dispShown.Title + "|" + dispShown.Artist
	hasCover := st.CoverVer != "" || st.CoverURL != ""
	if dispShown.Title != "" && key != prev && (st.Title == "" || !hasCover) {
		if dispPendKey != key {
			dispPendKey, dispPendAt = key, time.Now()
		}
		if time.Since(dispPendAt) < 4*time.Second {
			old := dispShown
			if st.Status != "" {
				old.Status = st.Status
			}
			return old
		}
	}
	dispShown, dispPendKey = st, ""
	return st
}
