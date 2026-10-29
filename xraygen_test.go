package main

import (
	"encoding/base64"
	"os"
	"testing"
)

// TVHUB_XRAY_OUT=папка — сохранить настройки для разных серверов, чтобы проверить их настоящим Xray.
func TestWriteXrayConfigs(t *testing.T) {
	dir := os.Getenv("TVHUB_XRAY_OUT")
	if dir == "" {
		t.Skip()
	}
	vm := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VM","add":"de.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","net":"ws","host":"de.example.com","path":"/ws","tls":"tls","sni":"de.example.com"}`))
	for i, l := range []string{
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=tcp&security=reality&pbk=h5d3b9bJh2rR8a8lCUM9a2B1v3lSOR2kV0c6zzSeFzU&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#R",
		"vless://11111111-2222-3333-4444-555555555555@nl.example.com:443?type=xhttp&security=tls&sni=nl.example.com&path=%2Fx&host=nl.example.com#X",
		"vmess://" + vm,
		"trojan://pass@fi.example.com:443?type=grpc&serviceName=svc&sni=fi.example.com#T",
		"ss://" + base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:secret")) + "@1.2.3.4:8388#S",
		"vless://11111111-2222-3333-4444-555555555555@h.example.com:80?type=tcp&headerType=http&host=a.com&path=%2F#H",
	} {
		s, err := parseLink(l)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := xrayConfig(s)
		os.WriteFile(dir+"/c"+string(rune('0'+i))+".json", b, 0o644)
	}
}
