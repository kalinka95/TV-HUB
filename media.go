// Управление музыкой с экрана TVHub и с телефона: общие команды.
package main

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
