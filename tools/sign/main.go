// Подписывает TVHub.exe для автообновления: go run ./tools/sign dist/TVHub.exe
// Закрытый ключ берётся из переменной окружения UPDATE_SIGNING_KEY (секрет GitHub).
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sign <file>")
		os.Exit(2)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("UPDATE_SIGNING_KEY")))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintln(os.Stderr, "UPDATE_SIGNING_KEY не задан или неверен")
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sum := sha256.Sum256(data)
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), []byte(hex.EncodeToString(sum[:])))
	if err := os.WriteFile(os.Args[1]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("подписано:", os.Args[1]+".sig")
}
