// Погода (Open-Meteo, бесплатно и без ключа) и обои дня для заставки (Bing).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	wttrBase    = envOr("TVHUB_WTTR_BASE", "https://wttr.in")
	metnoBase   = envOr("TVHUB_METNO_BASE", "https://api.met.no")
)

func getJSON(ctx context.Context, u string, v any) error {
	return getJSONc(ctx, http.DefaultClient, u, v)
}

func getJSONc(ctx context.Context, c *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", defaultUA)
	if strings.Contains(u, "met.no") { // met.no просит, чтобы программа представлялась
		req.Header.Set("User-Agent", "TVHub/"+appVersion+" github.com/kalinka95/TV-HUB")
	}
	resp, err := c.Do(req)
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
	wMu      sync.Mutex
	wCache   weatherData
	wKey     string
	wLastErr string // последняя ошибка — для отчёта
)

type weatherSource struct {
	name string
	get  func(context.Context, *http.Client, *City) (weatherData, error)
}

// fetchWeather пробует источники погоды по очереди, у каждого свои 8 секунд. Если напрямую
// ни один не ответил, пробуем ещё раз через VPN Happ (его локальный прокси), если он включён.
func fetchWeather(city *City) (weatherData, error) {
	sources := []weatherSource{{"Open-Meteo", weatherOpenMeteo}, {"met.no", weatherMetNo}, {"wttr.in", weatherWttr}}
	clients := []*http.Client{{Timeout: 8 * time.Second}}
	if p := systemProxyURL(); p != "" {
		if u, err := url.Parse(p); err == nil {
			clients = append(clients, &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(u)}})
		}
	}
	var errs []string
	for ci, c := range clients {
		for _, src := range sources {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			d, err := src.get(ctx, c, city)
			cancel()
			if err == nil && d.Now != nil {
				return d, nil
			}
			via := ""
			if ci > 0 {
				via = " через VPN"
			}
			errs = append(errs, src.name+via+": "+shortErr(err))
		}
	}
	return weatherData{}, errors.New(strings.Join(errs, "; "))
}

func shortErr(err error) string {
	if err == nil {
		return "пустой ответ"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "deadline") || strings.Contains(s, "Timeout") || strings.Contains(s, "timeout"):
		return "не отвечает"
	case strings.Contains(s, "no such host"):
		return "адрес не найден"
	case strings.Contains(s, "refused") || strings.Contains(s, "reset") || strings.Contains(s, "forcibly"):
		return "соединение сброшено"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

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
	d, err := fetchWeather(city)
	if err != nil {
		wLastErr = err.Error()
		log.Printf("погода: %v", err)
		if wKey == key && wCache.Now != nil {
			writeJSON(w, wCache) // нет связи — показываем последний прогноз
			return
		}
		writeJSON(w, weatherData{City: city, Error: "Не удалось получить погоду: " + err.Error()})
		return
	}
	wLastErr = ""
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

// weatherWttr — запасной источник погоды (wttr.in), если Open-Meteo недоступен.
func weatherWttr(ctx context.Context, c *http.Client, city *City) (weatherData, error) {
	var res struct {
		Current []struct {
			Temp  string `json:"temp_C"`
			Feels string `json:"FeelsLikeC"`
			Code  string `json:"weatherCode"`
			Hum   string `json:"humidity"`
			Wind  string `json:"windspeedKmph"`
		} `json:"current_condition"`
		Weather []struct {
			Date   string `json:"date"`
			Max    string `json:"maxtempC"`
			Min    string `json:"mintempC"`
			Hourly []struct {
				Code string `json:"weatherCode"`
			} `json:"hourly"`
		} `json:"weather"`
	}
	u := wttrBase + "/" + fmt.Sprintf("%.4f,%.4f", city.Lat, city.Lon) + "?format=j1"
	if err := getJSONc(ctx, c, u, &res); err != nil {
		return weatherData{}, err
	}
	if len(res.Current) == 0 {
		return weatherData{}, fmt.Errorf("пустой ответ")
	}
	num := func(s string) float64 { f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return f }
	code := func(s string) int { return wwoToWMO(int(num(s))) }
	cc := res.Current[0]
	h := time.Now().Hour()
	d := weatherData{City: city, Updated: time.Now().Unix(), Now: &weatherNow{
		Temp: num(cc.Temp), Feels: num(cc.Feels), Code: code(cc.Code), Hum: num(cc.Hum),
		Wind: math.Round(num(cc.Wind)/3.6*10) / 10, Day: h >= 7 && h < 19,
	}}
	for _, x := range res.Weather {
		wc := 0
		if len(x.Hourly) > 4 {
			wc = code(x.Hourly[4].Code) // полдень
		} else if len(x.Hourly) > 0 {
			wc = code(x.Hourly[0].Code)
		}
		d.Days = append(d.Days, weatherDay{Date: x.Date, Code: wc, Max: num(x.Max), Min: num(x.Min)})
	}
	return d, nil
}

// wwoToWMO переводит коды погоды wttr.in в коды Open-Meteo, по которым рисуются значки.
func wwoToWMO(c int) int {
	switch c {
	case 113:
		return 0
	case 116:
		return 2
	case 119, 122:
		return 3
	case 143, 248, 260:
		return 45
	case 176, 263, 266, 293, 296, 353:
		return 61
	case 299, 302, 356:
		return 63
	case 305, 308, 359:
		return 65
	case 179, 182, 185, 281, 284, 311, 314, 317, 320, 350, 362, 365, 374, 377:
		return 66
	case 227, 323, 326, 368:
		return 71
	case 329, 332, 335:
		return 73
	case 230, 338, 371, 395:
		return 75
	case 200, 386, 389, 392:
		return 95
	}
	return 3
}

// weatherOpenMeteo — основной источник погоды.
func weatherOpenMeteo(ctx context.Context, c *http.Client, city *City) (weatherData, error) {
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
	if err := getJSONc(ctx, c, weatherBase+"/v1/forecast?"+q.Encode(), &res); err != nil {
		return weatherData{}, err
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
	return d, nil
}

// weatherMetNo — запасной источник: норвежская метеослужба (бесплатно, без ключа).
func weatherMetNo(ctx context.Context, c *http.Client, city *City) (weatherData, error) {
	type sum struct {
		Summary struct {
			Symbol string `json:"symbol_code"`
		} `json:"summary"`
	}
	var res struct {
		Properties struct {
			Timeseries []struct {
				Time time.Time `json:"time"`
				Data struct {
					Instant struct {
						Details struct {
							Temp float64 `json:"air_temperature"`
							Hum  float64 `json:"relative_humidity"`
							Wind float64 `json:"wind_speed"`
						} `json:"details"`
					} `json:"instant"`
					Next1 *sum `json:"next_1_hours"`
					Next6 *sum `json:"next_6_hours"`
				} `json:"data"`
			} `json:"timeseries"`
		} `json:"properties"`
	}
	u := fmt.Sprintf("%s/weatherapi/locationforecast/2.0/compact?lat=%.4f&lon=%.4f", metnoBase, city.Lat, city.Lon)
	if err := getJSONc(ctx, c, u, &res); err != nil {
		return weatherData{}, err
	}
	ts := res.Properties.Timeseries
	if len(ts) == 0 {
		return weatherData{}, errors.New("пустой ответ")
	}
	sym := func(i int) string {
		if n := ts[i].Data.Next1; n != nil && n.Summary.Symbol != "" {
			return n.Summary.Symbol
		}
		if n := ts[i].Data.Next6; n != nil {
			return n.Summary.Symbol
		}
		return ""
	}
	// Ближайшая к текущему времени точка прогноза
	cur := 0
	for i := range ts {
		if ts[i].Time.After(time.Now()) {
			break
		}
		cur = i
	}
	det := ts[cur].Data.Instant.Details
	sc := sym(cur)
	d := weatherData{City: city, Updated: time.Now().Unix(), Now: &weatherNow{
		Temp: det.Temp, Feels: det.Temp, Code: metnoToWMO(sc), Hum: det.Hum, Wind: det.Wind, Day: !strings.Contains(sc, "night"),
	}}
	// По дням: максимум, минимум и погода около полудня (по местному времени компьютера)
	type agg struct {
		max, min float64
		code     int
		noon     int
	}
	days := map[string]*agg{}
	var order []string
	for i, t := range ts {
		lt := t.Time.Local()
		k := lt.Format("2006-01-02")
		a := days[k]
		if a == nil {
			if len(order) == 5 {
				break
			}
			a = &agg{max: -1e9, min: 1e9, noon: 99, code: -1}
			days[k] = a
			order = append(order, k)
		}
		v := t.Data.Instant.Details.Temp
		a.max, a.min = math.Max(a.max, v), math.Min(a.min, v)
		if dh := int(math.Abs(float64(lt.Hour() - 13))); dh < a.noon {
			if s := sym(i); s != "" {
				a.noon, a.code = dh, metnoToWMO(s)
			}
		}
	}
	for _, k := range order {
		a := days[k]
		if a.code < 0 {
			a.code = 3
		}
		d.Days = append(d.Days, weatherDay{Date: k, Code: a.code, Max: a.max, Min: a.min})
	}
	return d, nil
}

// metnoToWMO переводит значки met.no в коды Open-Meteo.
func metnoToWMO(s string) int {
	s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(s, "_day"), "_night"), "_polartwilight")
	switch {
	case strings.Contains(s, "thunder"):
		return 95
	case strings.Contains(s, "sleet"):
		return 66
	case s == "heavysnow" || s == "heavysnowshowers":
		return 75
	case s == "lightsnow" || s == "lightsnowshowers":
		return 71
	case strings.Contains(s, "snow"):
		return 73
	case s == "heavyrain" || s == "heavyrainshowers":
		return 65
	case s == "lightrain" || s == "lightrainshowers":
		return 61
	case strings.Contains(s, "rain"):
		return 63
	case s == "fog":
		return 45
	case s == "cloudy":
		return 3
	case s == "partlycloudy":
		return 2
	case s == "fair":
		return 1
	case s == "clearsky":
		return 0
	}
	return 3
}
