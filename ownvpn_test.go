package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseLinks(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"Германия","add":"de.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","net":"ws","host":"de.example.com","path":"/ws","tls":"tls","sni":"de.example.com"}`
	ssUser := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secret"))
	sub := strings.Join([]string{
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=tcp&security=reality&pbk=PUBKEY&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#%D0%9D%D0%B8%D0%B4%D0%B5%D1%80%D0%BB%D0%B0%D0%BD%D0%B4%D1%8B",
		"vmess://" + base64.StdEncoding.EncodeToString([]byte(vmessJSON)),
		"trojan://pass123@fi.example.com:8443?type=grpc&serviceName=svc&sni=fi.example.com#Finland",
		"ss://" + ssUser + "@1.2.3.4:8388#SS",
		"hysteria2://x@h.example.com:443#H2",
		"мусор",
	}, "\n")
	// подписка обычно в base64
	links, servers := parseSubscription(base64.StdEncoding.EncodeToString([]byte(sub)))
	if len(links) != 4 || len(servers) != 4 {
		t.Fatalf("серверов %d: %+v", len(servers), servers)
	}
	if servers[0].Name != "Нидерланды" || servers[0].Proto != "vless" || servers[0].Port != 443 {
		t.Errorf("vless: %+v", servers[0])
	}
	st := servers[0].outbound["streamSettings"].(map[string]any)
	if st["security"] != "reality" || st["realitySettings"].(map[string]any)["publicKey"] != "PUBKEY" {
		t.Errorf("reality: %+v", st)
	}
	if servers[1].Name != "Германия" || servers[1].outbound["streamSettings"].(map[string]any)["network"] != "ws" {
		t.Errorf("vmess: %+v", servers[1])
	}
	if servers[2].Proto != "trojan" || servers[2].outbound["streamSettings"].(map[string]any)["grpcSettings"].(map[string]any)["serviceName"] != "svc" {
		t.Errorf("trojan: %+v", servers[2].outbound)
	}
	ss := servers[3].outbound["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)
	if ss["method"] != "chacha20-ietf-poly1305" || ss["password"] != "secret" || ss["port"] != 8388 {
		t.Errorf("ss: %+v", ss)
	}
	b, err := xrayConfig(servers[0])
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if json.Unmarshal(b, &cfg) != nil || !strings.Contains(string(b), "geoip:ru") || !strings.Contains(string(b), `"port": 20809`) {
		t.Fatal(string(b))
	}
	if _, err := parseLink("hysteria2://x@h:443"); err == nil || !strings.Contains(err.Error(), "hysteria2") {
		t.Error(err)
	}
}

func TestSubscriptionNamesWithSpaces(t *testing.T) {
	sub := "vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?security=reality&pbk=k&sni=a.com#🇳🇱 Нидерланды 1\ntrojan://p@fi.example.com:443#Финляндия%20Хельсинки"
	_, servers := parseSubscription(base64.StdEncoding.EncodeToString([]byte(sub)))
	if len(servers) != 2 || servers[0].Name != "🇳🇱 Нидерланды 1" || servers[1].Name != "Финляндия Хельсинки" {
		t.Fatalf("%+v", servers)
	}
}

func TestUnwrapAppLink(t *testing.T) {
	for in, want := range map[string]string{
		"happ://add/https://sub.example.com/abc123":                        "https://sub.example.com/abc123",
		"happ://add/https%3A%2F%2Fsub.example.com%2Fabc%3Fx%3D1":           "https://sub.example.com/abc?x=1",
		"v2raytun://import/https://sub.example.com/u/1#Мой VPN":            "https://sub.example.com/u/1",
		"sing-box://import-remote-profile?url=https%3A%2F%2Fs.example.com": "https://s.example.com",
	} {
		if got, ok := unwrapAppLink(in); !ok || got != want {
			t.Errorf("%s → %q", in, got)
		}
	}
	if _, ok := unwrapAppLink("happ://crypt2/AbCdEf"); ok {
		t.Error("зашифрованная ссылка не должна разворачиваться")
	}
	if _, ok := unwrapAppLink("vless://id@h:443#x"); ok {
		t.Error("ссылка на сервер — не обёртка")
	}
}
