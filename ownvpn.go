// Свой VPN в TVHub: серверы из ссылки-подписки (как в Happ) и движок Xray.
// Российские сайты идут напрямую, всё остальное — через VPN, поэтому VPN не нужно выключать.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OwnVPN — настройки своего VPN (хранятся в config.json).
type OwnVPN struct {
	Sub    string   `json:"sub,omitempty"`   // ссылка-подписка или ссылки на серверы
	Links  []string `json:"links,omitempty"` // ссылки на серверы из подписки
	Server int      `json:"server"`          // выбранный сервер
	On     bool     `json:"on,omitempty"`    // включён
}

type vpnServer struct {
	Name     string         `json:"name"`
	Proto    string         `json:"proto"`
	Host     string         `json:"host"`
	Port     int            `json:"port"`
	outbound map[string]any // настройка для Xray
}

const (
	ownSocksPort = 20808
	ownHTTPPort  = 20809
)

func b64decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.NewReplacer("\r", "", "\n", "", " ", "").Replace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("не base64")
}

// parseSubscription разбирает ответ подписки: список ссылок (обычный или в base64).
func parseSubscription(body string) ([]string, []vpnServer) {
	text := strings.TrimSpace(body)
	if !strings.Contains(text, "://") {
		if b, err := b64decode(text); err == nil {
			text = string(b)
		}
	}
	var links []string
	var servers []vpnServer
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "://") {
			continue
		}
		if s, err := parseLink(line); err == nil {
			links = append(links, line)
			servers = append(servers, s)
		}
	}
	return links, servers
}

func fragName(u *url.URL, def string) string {
	if u.Fragment != "" {
		return u.Fragment
	}
	return def
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// streamSettings — транспорт и шифрование соединения (общие для vless/trojan/vmess).
func streamSettings(network, security, host, path, sni, fp, alpn, pbk, sid, spx, serviceName, mode, headerType string, insecure bool) map[string]any {
	if network == "" {
		network = "tcp"
	}
	if network == "splithttp" {
		network = "xhttp"
	}
	st := map[string]any{"network": network}
	switch network {
	case "ws":
		ws := map[string]any{"path": orStr(path, "/")}
		if host != "" {
			ws["headers"] = map[string]any{"Host": host}
		}
		st["wsSettings"] = ws
	case "grpc":
		st["grpcSettings"] = map[string]any{"serviceName": serviceName, "multiMode": mode == "multi"}
	case "xhttp":
		x := map[string]any{"path": orStr(path, "/"), "mode": orStr(mode, "auto")}
		if host != "" {
			x["host"] = host
		}
		st["xhttpSettings"] = x
	case "httpupgrade":
		h := map[string]any{"path": orStr(path, "/")}
		if host != "" {
			h["host"] = host
		}
		st["httpupgradeSettings"] = h
	case "tcp":
		if headerType == "http" {
			req := map[string]any{"path": []string{orStr(path, "/")}}
			if host != "" {
				req["headers"] = map[string]any{"Host": splitList(host)}
			}
			st["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http", "request": req}}
		}
	}
	switch security {
	case "tls":
		t := map[string]any{"serverName": orStr(sni, host), "allowInsecure": insecure}
		if fp != "" {
			t["fingerprint"] = fp
		}
		if a := splitList(alpn); len(a) > 0 {
			t["alpn"] = a
		}
		st["security"] = "tls"
		st["tlsSettings"] = t
	case "reality":
		st["security"] = "reality"
		st["realitySettings"] = map[string]any{"serverName": sni, "fingerprint": orStr(fp, "chrome"), "publicKey": pbk, "shortId": sid, "spiderX": spx}
	default:
		st["security"] = "none"
	}
	return st
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseLink разбирает одну ссылку на сервер: vless://, vmess://, trojan://, ss://.
func parseLink(link string) (vpnServer, error) {
	link = strings.TrimSpace(link)
	switch {
	case strings.HasPrefix(link, "vmess://"):
		b, err := b64decode(strings.TrimPrefix(link, "vmess://"))
		if err != nil {
			return vpnServer{}, err
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			return vpnServer{}, err
		}
		g := func(k string) string {
			switch x := v[k].(type) {
			case string:
				return x
			case float64:
				return strconv.Itoa(int(x))
			}
			return ""
		}
		port := atoi(g("port"))
		if g("add") == "" || port == 0 || g("id") == "" {
			return vpnServer{}, errors.New("неполная ссылка vmess")
		}
		sec := ""
		if g("tls") == "tls" {
			sec = "tls"
		}
		out := map[string]any{"tag": "proxy", "protocol": "vmess",
			"settings": map[string]any{"vnext": []any{map[string]any{"address": g("add"), "port": port,
				"users": []any{map[string]any{"id": g("id"), "alterId": atoi(g("aid")), "security": orStr(g("scy"), "auto")}}}}},
			"streamSettings": streamSettings(g("net"), sec, g("host"), g("path"), g("sni"), g("fp"), g("alpn"), "", "", "", g("path"), "", g("type"), false)}
		return vpnServer{Name: orStr(g("ps"), g("add")), Proto: "vmess", Host: g("add"), Port: port, outbound: out}, nil

	case strings.HasPrefix(link, "vless://"), strings.HasPrefix(link, "trojan://"):
		u, err := url.Parse(link)
		if err != nil || u.Hostname() == "" || u.Port() == "" || u.User == nil {
			return vpnServer{}, errors.New("неполная ссылка")
		}
		q := u.Query()
		port := atoi(u.Port())
		secret := u.User.Username()
		if p, ok := u.User.Password(); ok && u.Scheme == "trojan" && secret == "" {
			secret = p
		}
		security := q.Get("security")
		if u.Scheme == "trojan" && security == "" {
			security = "tls"
		}
		st := streamSettings(q.Get("type"), security, q.Get("host"), q.Get("path"), q.Get("sni"), q.Get("fp"), q.Get("alpn"),
			q.Get("pbk"), q.Get("sid"), q.Get("spx"), q.Get("serviceName"), q.Get("mode"), q.Get("headerType"), q.Get("allowInsecure") == "1")
		var out map[string]any
		if u.Scheme == "vless" {
			user := map[string]any{"id": secret, "encryption": orStr(q.Get("encryption"), "none")}
			if f := q.Get("flow"); f != "" {
				user["flow"] = f
			}
			out = map[string]any{"tag": "proxy", "protocol": "vless",
				"settings":       map[string]any{"vnext": []any{map[string]any{"address": u.Hostname(), "port": port, "users": []any{user}}}},
				"streamSettings": st}
		} else {
			out = map[string]any{"tag": "proxy", "protocol": "trojan",
				"settings":       map[string]any{"servers": []any{map[string]any{"address": u.Hostname(), "port": port, "password": secret}}},
				"streamSettings": st}
		}
		return vpnServer{Name: fragName(u, u.Hostname()), Proto: u.Scheme, Host: u.Hostname(), Port: port, outbound: out}, nil

	case strings.HasPrefix(link, "ss://"):
		rest := strings.TrimPrefix(link, "ss://")
		name := ""
		if i := strings.Index(rest, "#"); i >= 0 {
			name, _ = url.PathUnescape(rest[i+1:])
			rest = rest[:i]
		}
		if i := strings.Index(rest, "?"); i >= 0 {
			rest = rest[:i]
		}
		var userinfo, hostport string
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			userinfo, hostport = rest[:i], rest[i+1:]
			if b, err := b64decode(userinfo); err == nil {
				userinfo = string(b)
			} else if un, err := url.PathUnescape(userinfo); err == nil {
				userinfo = un
			}
		} else if b, err := b64decode(rest); err == nil { // всё целиком в base64
			s := string(b)
			i := strings.LastIndex(s, "@")
			if i < 0 {
				return vpnServer{}, errors.New("неполная ссылка ss")
			}
			userinfo, hostport = s[:i], s[i+1:]
		}
		mp := strings.SplitN(userinfo, ":", 2)
		u, err := url.Parse("x://" + hostport)
		if len(mp) != 2 || err != nil || u.Hostname() == "" || u.Port() == "" {
			return vpnServer{}, errors.New("неполная ссылка ss")
		}
		port := atoi(u.Port())
		out := map[string]any{"tag": "proxy", "protocol": "shadowsocks",
			"settings": map[string]any{"servers": []any{map[string]any{"address": u.Hostname(), "port": port, "method": mp[0], "password": mp[1]}}}}
		return vpnServer{Name: orStr(name, u.Hostname()), Proto: "ss", Host: u.Hostname(), Port: port, outbound: out}, nil
	}
	if i := strings.Index(link, "://"); i > 0 {
		return vpnServer{}, fmt.Errorf("протокол %s TVHub пока не поддерживает", link[:i])
	}
	return vpnServer{}, errors.New("это не ссылка на сервер")
}

// Российские адреса — напрямую, без VPN (кроме этого — все адреса из России по geoip).
var ruDirectDomains = []string{"domain:ru", "domain:su", "domain:xn--p1ai", "domain:yandex.net", "domain:yandex.com", "domain:yastatic.net",
	"domain:ya.ru", "domain:yandexcloud.net", "domain:vk.com", "domain:vk.me", "domain:userapi.com", "domain:vkuser.net", "domain:mycdn.me",
	"domain:okko.tv", "domain:ivi.tv", "domain:premier.one", "domain:more.tv", "domain:kion.ru", "domain:wink.ru", "domain:start.ru"}

// xrayConfig — настройки Xray: вход для программ на этом компьютере и выбор, что пускать через VPN.
func xrayConfig(s vpnServer) ([]byte, error) {
	if s.outbound == nil {
		return nil, errors.New("сервер не выбран")
	}
	sniff := map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": ownSocksPort, "protocol": "socks", "settings": map[string]any{"udp": true}, "sniffing": sniff},
			map[string]any{"tag": "http", "listen": "127.0.0.1", "port": ownHTTPPort, "protocol": "http", "sniffing": sniff},
		},
		"outbounds": []any{s.outbound, map[string]any{"tag": "direct", "protocol": "freedom"}, map[string]any{"tag": "block", "protocol": "blackhole"}},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch", "rules": []any{
			map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "direct"},
			map[string]any{"type": "field", "domain": ruDirectDomains, "outboundTag": "direct"},
			map[string]any{"type": "field", "ip": []string{"geoip:ru"}, "outboundTag": "direct"},
		}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ---------- Состояние и команды ----------

var (
	ownMu      sync.Mutex
	ownErr     string
	ownBusy    string // что сейчас делаем: «скачиваю Xray», «подключаюсь»
	ownServers []vpnServer
)

func ownVPNConf() OwnVPN {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfg.Settings.OwnVPN == nil {
		return OwnVPN{}
	}
	return *cfg.Settings.OwnVPN
}

func saveOwnVPN(v OwnVPN) {
	cfgMu.Lock()
	cfg.Settings.OwnVPN = &v
	if v.Sub == "" && len(v.Links) == 0 {
		cfg.Settings.OwnVPN = nil
	}
	saveConfigLocked()
	cfgMu.Unlock()
}

// ownVPNConfigured — есть ли свой VPN (тогда кнопка VPN на главном экране управляет им, а не Happ).
func ownVPNConfigured() bool { return len(ownVPNConf().Links) > 0 }

func ownVPNOn() bool { v := ownVPNConf(); return v.On && len(v.Links) > 0 }

func ownServerList() []vpnServer {
	v := ownVPNConf()
	ownMu.Lock()
	defer ownMu.Unlock()
	if len(ownServers) != len(v.Links) {
		ownServers = nil
		for _, l := range v.Links {
			if s, err := parseLink(l); err == nil {
				ownServers = append(ownServers, s)
			}
		}
	}
	return ownServers
}

func ownVPNStatus() map[string]any {
	v := ownVPNConf()
	type srv struct {
		Name  string `json:"name"`
		Proto string `json:"proto"`
		Host  string `json:"host"`
	}
	list := []srv{}
	for _, s := range ownServerList() {
		list = append(list, srv{s.Name, s.Proto, s.Host})
	}
	ownMu.Lock()
	e, busy := ownErr, ownBusy
	ownMu.Unlock()
	return map[string]any{"configured": len(v.Links) > 0, "sub": v.Sub, "servers": list, "server": v.Server, "on": v.On && xrayRunning(),
		"want": v.On, "installed": xrayInstalled(), "error": e, "busy": busy}
}

// fetchSubscription скачивает подписку (или берёт ссылки, вставленные напрямую).
// unwrapAppLink достаёт обычную подписку из ссылок для приложений: happ://add/https://…,
// v2raytun://import/…, hiddify://import/…, streisand://import/…, sing-box://import-remote-profile?url=…
func unwrapAppLink(s string) (string, bool) {
	low := strings.ToLower(s)
	if !strings.Contains(low, "://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		return s, false
	}
	if strings.HasPrefix(low, "happ://crypt") {
		return s, false // зашифрованная — открыть может только Happ
	}
	rest := s[strings.Index(s, "://")+3:]
	if u, err := url.Parse(s); err == nil {
		if v := u.Query().Get("url"); strings.HasPrefix(v, "http") {
			return v, true
		}
	}
	for _, cand := range []string{rest, func() string { d, _ := url.PathUnescape(rest); return d }()} {
		if i := strings.Index(strings.ToLower(cand), "http"); i >= 0 {
			v := cand[i:]
			if j := strings.Index(v, "#"); j >= 0 {
				v = v[:j]
			}
			if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				return v, true
			}
		}
	}
	return s, false
}

func fetchSubscription(sub string) ([]string, error) {
	sub = strings.TrimSpace(sub)
	if u, ok := unwrapAppLink(sub); ok {
		sub = u
	} else if strings.HasPrefix(strings.ToLower(sub), "happ://") {
		return nil, errors.New("это зашифрованная ссылка Happ — её может открыть только Happ. Попросите у VPN-сервиса обычную ссылку-подписку (https://…): обычно она есть в боте или личном кабинете рядом с кнопкой «Добавить в Happ»")
	}
	body := sub
	if strings.HasPrefix(sub, "http://") || strings.HasPrefix(sub, "https://") {
		c := &http.Client{Timeout: 20 * time.Second}
		req, _ := http.NewRequest("GET", sub, nil)
		req.Header.Set("User-Agent", "v2rayN/7.0") // так подписки отдают обычный список серверов
		resp, err := c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("подписка не скачалась: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("сервер подписки ответил %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		body = string(b)
	}
	links, _ := parseSubscription(body)
	if len(links) == 0 {
		if strings.Contains(body, "happ://") {
			return nil, errors.New("в подписке зашифрованные ссылки Happ — их может открыть только Happ. Попросите у VPN-сервиса обычную ссылку-подписку")
		}
		return nil, errors.New("в подписке не нашлось серверов, которые понимает TVHub (vless, vmess, trojan, shadowsocks)")
	}
	return links, nil
}

func setOwnErr(e string) {
	ownMu.Lock()
	ownErr = e
	ownMu.Unlock()
}

func setOwnBusy(b string) {
	ownMu.Lock()
	ownBusy = b
	ownMu.Unlock()
	pushEvent(map[string]string{"type": "vpn"})
}

// ownVPNApply включает или выключает свой VPN по сохранённым настройкам.
func ownVPNApply() error {
	v := ownVPNConf()
	if !v.On || len(v.Links) == 0 {
		xrayStop()
		setOwnErr("")
		pushEvent(map[string]string{"type": "vpn"})
		return nil
	}
	list := ownServerList()
	if len(list) == 0 {
		return errors.New("нет серверов")
	}
	if v.Server < 0 || v.Server >= len(list) {
		v.Server = 0
	}
	if !xrayInstalled() {
		setOwnBusy("Скачиваю движок VPN (Xray)…")
		err := xrayInstall()
		setOwnBusy("")
		if err != nil {
			setOwnErr("Не удалось скачать Xray: " + err.Error())
			return err
		}
	}
	conf, err := xrayConfig(list[v.Server])
	if err != nil {
		return err
	}
	setOwnBusy("Подключаюсь…")
	err = xrayStart(conf)
	setOwnBusy("")
	if err != nil {
		setOwnErr(err.Error())
		return err
	}
	setOwnErr("")
	pushEvent(map[string]string{"type": "vpn"})
	return nil
}

func handleOwnVPN(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, ownVPNStatus())
		return
	}
	var req struct {
		Sub    *string `json:"sub"`
		Server *int    `json:"server"`
		On     *bool   `json:"on"`
		Remove bool    `json:"remove"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req)
	v := ownVPNConf()
	var err error
	switch {
	case req.Remove:
		v.On = false
		saveOwnVPN(v)
		ownVPNApply()
		saveOwnVPN(OwnVPN{})
	case req.Sub != nil:
		var links []string
		if links, err = fetchSubscription(*req.Sub); err == nil {
			v.Sub, v.Links = strings.TrimSpace(*req.Sub), links
			if v.Server >= len(links) {
				v.Server = 0
			}
			saveOwnVPN(v)
			if v.On {
				err = ownVPNApply()
			}
		}
	case req.Server != nil:
		v.Server = *req.Server
		saveOwnVPN(v)
		if v.On {
			err = ownVPNApply()
		}
	case req.On != nil:
		v.On = *req.On
		saveOwnVPN(v)
		if v.On {
			happOffForOwn() // Happ мешал бы своему VPN — выключаем его
		}
		err = ownVPNApply()
	}
	st := ownVPNStatus()
	if err != nil {
		st["error"] = err.Error()
	}
	writeJSON(w, st)
}

// refreshOwnSubscription раз в сутки обновляет список серверов из подписки.
func refreshOwnSubscription() {
	for {
		time.Sleep(24 * time.Hour)
		v := ownVPNConf()
		if !strings.HasPrefix(v.Sub, "http") {
			continue
		}
		if links, err := fetchSubscription(v.Sub); err == nil {
			v2 := ownVPNConf()
			v2.Links = links
			if v2.Server >= len(links) {
				v2.Server = 0
			}
			saveOwnVPN(v2)
		}
	}
}
