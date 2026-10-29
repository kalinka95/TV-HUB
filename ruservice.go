package main

import (
	"net/url"
	"strings"
)

// Российские сервисы, которые обычно не открываются через зарубежный VPN.
var ruHosts = []string{"okko.tv", "premier.one", "vk.com", "vkvideo.ru", "kion.ru", "wink.ru", "ivi.ru", "ivi.tv",
	"kinopoisk.ru", "yandex.ru", "yandex.net", "rutube.ru", "smotrim.ru", "start.ru", "more.tv", "1tv.ru", "ntv.ru", "ctc.ru"}

func isRuService(a App) bool {
	u, err := url.Parse(a.Target)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if strings.HasSuffix(h, ".ru") || strings.HasSuffix(h, ".su") || strings.HasSuffix(h, ".xn--p1ai") || strings.HasSuffix(h, ".рф") {
		return true
	}
	for _, x := range ruHosts {
		if h == x || strings.HasSuffix(h, "."+x) {
			return true
		}
	}
	return false
}

// Сайты, которые не пускают в аккаунт браузер, которым управляет программа (у TVHub это служебный
// порт управления Edge). Для них Edge открывается как обычный браузер.
var plainHosts = []string{"twitch.tv"}

func plainBrowser(a App) bool {
	if a.Type != "web" {
		return false
	}
	if a.Plain {
		return true
	}
	u, err := url.Parse(a.Target)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, x := range plainHosts {
		if h == x || strings.HasSuffix(h, "."+x) {
			return true
		}
	}
	return false
}

// openInsideTVHub — открывать ли сайт прямо в окне TVHub (так по умолчанию). В отдельном Edge
// остаются сайты с особым «браузером» (YouTube для телевизоров) и те, где это выбрали вручную.
func openInsideTVHub(a App) bool {
	return a.Type == "web" && a.UserAgent == "" && !a.InEdge
}
