// Запасной поиск обложки по названию трека и исполнителю (открытый каталог iTunes, без ключа).
package main

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	coverMu    sync.Mutex
	coverCache = map[string]string{} // «исполнитель — трек» → ссылка на обложку ("" — не нашли)
	coverBusy  = map[string]bool{}
)

var coverBase = envOr("TVHUB_COVER_API", "https://itunes.apple.com")

// fillCoverFallback подставляет найденную обложку, если плеер её не сообщил.
// Поиск идёт в фоне: обложка появится при следующем обновлении экрана.
func fillCoverFallback(st *mediaState) {
	if st.Title == "" || st.CoverVer != "" || st.CoverURL != "" {
		return
	}
	key := strings.ToLower(st.Artist + " — " + st.Title)
	coverMu.Lock()
	u, done := coverCache[key]
	busy := coverBusy[key]
	if !done && !busy {
		coverBusy[key] = true
		go lookupCover(key, st.Artist, st.Title)
	}
	coverMu.Unlock()
	st.CoverURL = u
}

func lookupCover(key, artist, title string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var res struct {
		Results []struct {
			Artwork string `json:"artworkUrl100"`
			Artist  string `json:"artistName"`
			Track   string `json:"trackName"`
		} `json:"results"`
	}
	q := url.Values{"term": {strings.TrimSpace(artist + " " + title)}, "entity": {"song"}, "limit": {"5"}, "country": {"ru"}}
	found := ""
	if getJSON(ctx, coverBase+"/search?"+q.Encode(), &res) == nil {
		for _, r := range res.Results {
			if r.Artwork == "" {
				continue
			}
			// Предпочитаем совпадение по исполнителю
			if found == "" || strings.Contains(strings.ToLower(r.Artist), strings.ToLower(artist)) {
				found = strings.Replace(r.Artwork, "100x100", "600x600", 1)
				if strings.Contains(strings.ToLower(r.Artist), strings.ToLower(artist)) {
					break
				}
			}
		}
	}
	coverMu.Lock()
	coverCache[key] = found
	delete(coverBusy, key)
	coverMu.Unlock()
}
