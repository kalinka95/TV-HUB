package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Если API GitHub отказал (лимит), версия берётся со страницы выпусков.
func TestCheckUpdateFallback(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer api.Close()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/o/r/releases/latest":
			http.Redirect(w, r, "https://github.com/o/r/releases/tag/v9.9.9", http.StatusFound)
		case "/o/r/releases/download/v9.9.9/notes.txt":
			w.Write([]byte("Что нового\n- пункт\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer web.Close()
	os.Setenv("TVHUB_UPDATE_API", api.URL)
	os.Setenv("TVHUB_UPDATE_WEB", web.URL)
	defer os.Unsetenv("TVHUB_UPDATE_API")
	defer os.Unsetenv("TVHUB_UPDATE_WEB")
	old := updateRepo
	updateRepo = "o/r"
	defer func() { updateRepo = old }()

	u, err := checkUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Version != "9.9.9" || u.URL != web.URL+"/o/r/releases/download/v9.9.9/TVHub.exe" || u.SumURL == "" || u.Notes != "Что нового\n- пункт" {
		t.Fatalf("%+v", u)
	}
}
