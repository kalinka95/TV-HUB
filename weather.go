// Погода (Open-Meteo, бесплатно и без ключа) и обои дня для заставки (Bing).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type City struct {
	Name    string  `json:"name"`
	Region  string  `json:"region,omitempty"`
	Country string  `json:"country,omitempty"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var (
	geoBase     = envOr("TVHUB_GEO_BASE", "https://geocoding-api.open-meteo.com")
	weatherBase = envOr("TVHUB_WEATHER_BASE", "https://api.open-meteo.com")
	bingBase    = envOr("TVHUB_BING_BASE", "https://www.bing.com")
)

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("сервер ответил %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// ---------- Поиск города ----------

func handleCitySearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, map[string]any{"results": []City{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var res struct {
		Results []struct {
			Name      string  `json:"name"`
			Admin1    string  `json:"admin1"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	u := geoBase + "/v1/search?" + url.Values{"name": {q}, "count": {"8"}, "language": {"ru"}, "format": {"json"}}.Encode()
	if err := getJSON(ctx, u, &res); err != nil {
		writeJSON(w, map[string]any{"results": []City{}, "error": "Не удалось найти город: " + err.Error()})
		return
	}
	out := []City{}
	for _, x := range res.Results {
		out = append(out, City{Name: x.Name, Region: x.Admin1, Country: x.Country, Lat: x.Latitude, Lon: x.Longitude})
	}
	writeJSON(w, map[string]any{"results": out})
}

// ---------- Прогноз ----------

type weatherNow struct {
	Temp  float64 `json:"temp"`
	Feels float64 `json:"feels"`
	Code  int     `json:"code"`
	Wind  float64 `json:"wind"`
	Hum   float64 `json:"hum"`
	Day   bool    `json:"day"`
}

type weatherDay struct {
	Date string  `json:"date"`
	Code int     `json:"code"`
	Max  float64 `json:"max"`
	Min  float64 `json:"min"`
}

type weatherData struct {
	City    *City        `json:"city"`
	Now     *weatherNow  `json:"now,omitempty"`
	Days    []weatherDay `json:"days,omitempty"`
	Updated int64        `json:"updated,omitempty"`
	Error   string       `json:"error,omitempty"`
}

var (
	wMu    sync.Mutex
	wCache weatherData
	wKey   string
)

func handleWeather(w http.ResponseWriter, r *http.Request) {
	cfgMu.Lock()
	city := cfg.Settings.City
	cfgMu.Unlock()
	if city == nil {
		writeJSON(w, weatherData{})
		return
	}
	key := fmt.Sprintf("%.3f,%.3f", city.Lat, city.Lon)
	wMu.Lock()
	defer wMu.Unlock()
	if wKey == key && wCache.Now != nil && time.Since(time.Unix(wCache.Updated, 0)) < 15*time.Minute {
		writeJSON(w, wCache)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var res struct {
		Current struct {
			Temp  float64 `json:"temperature_2m"`
			Feels float64 `json:"apparent_temperature"`
			Code  int     `json:"weather_code"`
			Wind  float64 `json:"wind_speed_10m"`
			Hum   float64 `json:"relative_humidity_2m"`
			IsDay int     `json:"is_day"`
		} `json:"current"`
		Daily struct {
			Time []string  `json:"time"`
			Code []int     `json:"weather_code"`
			Max  []float64 `json:"temperature_2m_max"`
			Min  []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	q := url.Values{
		"latitude":        {fmt.Sprint(city.Lat)},
		"longitude":       {fmt.Sprint(city.Lon)},
		"current":         {"temperature_2m,apparent_temperature,weather_code,wind_speed_10m,relative_humidity_2m,is_day"},
		"daily":           {"weather_code,temperature_2m_max,temperature_2m_min"},
		"timezone":        {"auto"},
		"forecast_days":   {"5"},
		"wind_speed_unit": {"ms"},
	}
	if err := getJSON(ctx, weatherBase+"/v1/forecast?"+q.Encode(), &res); err != nil {
		if wKey == key && wCache.Now != nil {
			writeJSON(w, wCache) // нет связи — показываем последний прогноз
			return
		}
		writeJSON(w, weatherData{City: city, Error: "Не удалось получить погоду: " + err.Error()})
		return
	}
	d := weatherData{City: city, Updated: time.Now().Unix(), Now: &weatherNow{
		Temp: res.Current.Temp, Feels: res.Current.Feels, Code: res.Current.Code,
		Wind: res.Current.Wind, Hum: res.Current.Hum, Day: res.Current.IsDay == 1,
	}}
	for i, t := range res.Daily.Time {
		if i < len(res.Daily.Code) && i < len(res.Daily.Max) && i < len(res.Daily.Min) {
			d.Days = append(d.Days, weatherDay{Date: t, Code: res.Daily.Code[i], Max: res.Daily.Max[i], Min: res.Daily.Min[i]})
		}
	}
	wCache, wKey = d, key
	writeJSON(w, d)
}

// ---------- Обои дня для заставки ----------

type wallpaper struct {
	File  string `json:"-"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Copy  string `json:"copy,omitempty"`
}

var (
	wallMu sync.Mutex
	walls  []wallpaper
	wallRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.jpg$`)
)

func wallDir() string { return filepath.Join(dataDir, "wallpapers") }

func loadWallIndex() {
	b, err := os.ReadFile(filepath.Join(wallDir(), "index.json"))
	if err != nil {
		return
	}
	var list []wallpaper
	if json.Unmarshal(b, &list) == nil {
		for i := range list {
			list[i].File = filepath.Join(wallDir(), list[i].Name)
		}
		wallMu.Lock()
		walls = list
		wallMu.Unlock()
	}
}

// refreshWallpapers раз в день скачивает новые картинки дня и хранит последние 16 на диске.
func refreshWallpapers() {
	loadWallIndex()
	for {
		if err := fetchBing(); err != nil {
			log.Printf("обои: %v", err)
		}
		time.Sleep(12 * time.Hour)
	}
}

func fetchBing() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var res struct {
		Images []struct {
			URLBase   string `json:"urlbase"`
			URL       string `json:"url"`
			Copyright string `json:"copyright"`
			Title     string `json:"title"`
		} `json:"images"`
	}
	if err := getJSON(ctx, bingBase+"/HPImageArchive.aspx?format=js&idx=0&n=8&mkt=ru-RU", &res); err != nil {
		return err
	}
	os.MkdirAll(wallDir(), 0o755)
	wallMu.Lock()
	have := map[string]bool{}
	for _, x := range walls {
		have[x.Name] = true
	}
	wallMu.Unlock()
	var added []wallpaper
	for _, im := range res.Images {
		src := im.URL
		if im.URLBase != "" {
			src = im.URLBase + "_1920x1080.jpg"
		}
		id := regexp.MustCompile(`OHR\.[A-Za-z0-9_-]+`).FindString(src)
		if id == "" {
			id = fmt.Sprintf("img%x", len(src)*7919+len(im.Title))
		}
		name := id + ".jpg"
		if !wallRe.MatchString(name) || have[name] {
			continue
		}
		if !strings.HasPrefix(src, "http") {
			src = bingBase + src
		}
		if err := download(ctx, src, filepath.Join(wallDir(), name)); err != nil {
			log.Printf("обои %s: %v", name, err)
			continue
		}
		title := im.Title
		copyText := im.Copyright
		if title == "" {
			if i := strings.Index(copyText, "("); i > 0 {
				title = strings.TrimSpace(copyText[:i])
			}
		}
		added = append(added, wallpaper{File: filepath.Join(wallDir(), name), Name: name, Title: title, Copy: copyText})
	}
	wallMu.Lock()
	walls = append(added, walls...)
	for len(walls) > 16 {
		os.Remove(walls[len(walls)-1].File)
		walls = walls[:len(walls)-1]
	}
	b, _ := json.Marshal(walls)
	wallMu.Unlock()
	return os.WriteFile(filepath.Join(wallDir(), "index.json"), b, 0o644)
}

func download(ctx context.Context, src, dst string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("сервер ответил %d", resp.StatusCode)
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 20<<20)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dst)
}

func handleWalls(w http.ResponseWriter, r *http.Request) {
	wallMu.Lock()
	defer wallMu.Unlock()
	out := []map[string]string{}
	for _, x := range walls {
		out = append(out, map[string]string{"url": "/api/wall/" + x.Name + "?t=" + token, "title": x.Title, "copy": x.Copy})
	}
	writeJSON(w, map[string]any{"walls": out})
}

func handleWall(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != token {
		http.Error(w, "forbidden", 403)
		return
	}
	name := r.PathValue("name")
	if !wallRe.MatchString(name) {
		http.Error(w, "bad name", 400)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, filepath.Join(wallDir(), name))
}
