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
	d, err := weatherWttr(context.Background(), http.DefaultClient, &City{Name: "Москва", Lat: 55.75, Lon: 37.6})
	if err != nil {
		t.Fatal(err)
	}
	if d.Now.Temp != -3 || d.Now.Code != 71 || d.Now.Wind != 5 || len(d.Days) != 1 || d.Days[0].Code != 2 || d.Days[0].Min != -4 {
		t.Fatalf("%+v %+v", d.Now, d.Days)
	}
}

// Open-Meteo не отвечает — погода берётся у met.no.
func TestWeatherFallbackMetNo(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	met := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"properties":{"timeseries":[
		{"time":"2020-01-01T10:00:00Z","data":{"instant":{"details":{"air_temperature":2.5,"relative_humidity":70,"wind_speed":3}},"next_1_hours":{"summary":{"symbol_code":"lightsnow_day"}}}},
		{"time":"2099-01-01T12:00:00Z","data":{"instant":{"details":{"air_temperature":-1}},"next_6_hours":{"summary":{"symbol_code":"cloudy"}}}}]}}`))
	}))
	defer met.Close()
	ob, mb, wb := weatherBase, metnoBase, wttrBase
	weatherBase, metnoBase, wttrBase = bad.URL, met.URL, bad.URL
	defer func() { weatherBase, metnoBase, wttrBase = ob, mb, wb }()
	d, err := fetchWeather(&City{Name: "Москва", Lat: 55.75, Lon: 37.6})
	if err != nil {
		t.Fatal(err)
	}
	if d.Now.Temp != 2.5 || d.Now.Code != 71 || len(d.Days) != 2 {
		t.Fatalf("%+v %+v", d.Now, d.Days)
	}
}
