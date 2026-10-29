package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWeatherWttr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"current_condition":[{"temp_C":"-3","FeelsLikeC":"-7","weatherCode":"326","humidity":"80","windspeedKmph":"18"}],
		"weather":[{"date":"2026-10-29","maxtempC":"1","mintempC":"-4","hourly":[{"weatherCode":"113"},{"weatherCode":"113"},{"weatherCode":"113"},{"weatherCode":"113"},{"weatherCode":"116"}]}]}`))
	}))
	defer srv.Close()
	old := wttrBase
	wttrBase = srv.URL
	defer func() { wttrBase = old }()
	d, err := weatherWttr(context.Background(), &City{Name: "Москва", Lat: 55.75, Lon: 37.6})
	if err != nil {
		t.Fatal(err)
	}
	if d.Now.Temp != -3 || d.Now.Code != 71 || d.Now.Wind != 5 || len(d.Days) != 1 || d.Days[0].Code != 2 || d.Days[0].Min != -4 {
		t.Fatalf("%+v %+v", d.Now, d.Days)
	}
}
