package main

import "testing"

func TestRussify(t *testing.T) {
	cases := map[string]string{
		"Perviy Kanal (1080p)":        "Первый канал",
		"Rossiya 1 [Geo-blocked]":     "Россия 1",
		"NTV":                         "НТВ",
		"Match TV HD":                 "Матч ТВ HD",
		"Pyatnitsa":                   "Пятница",
		"Domashniy (720p) [Not 24/7]": "Домашний",
		"Telekanal Krasnoyarsk":       "Телеканал Красноярск",
		"Mayak":                       "Маяк",
		"OTR":                         "ОТР",
		"Первый канал":                "Первый канал",
		"Zvezda +2":                   "Звезда +2",
		"Discovery Channel":           "Дискавери",
		"Tatarstan 24":                "Татарстан 24",
		"Kultura":                     "Россия-Культура",
	}
	for in, want := range cases {
		if got := russifyName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
