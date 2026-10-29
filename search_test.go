package main

import "testing"

func TestSearchChannels(t *testing.T) {
	list := []Channel{{N: 1, Name: "Первый канал"}, {N: 2, Name: "Россия 1"}, {N: 3, Name: "Пятница!", Orig: "Pyatnica"}, {N: 4, Name: "Канал Disney"}, {N: 12, Name: "Ёлки ТВ"}}
	names := func(cs []Channel) (s []string) {
		for _, c := range cs {
			s = append(s, c.Name)
		}
		return
	}
	if r := names(searchChannels(list, "кан", 10)); len(r) != 2 || r[0] != "Канал Disney" || r[1] != "Первый канал" {
		t.Fatal(r)
	}
	if r := names(searchChannels(list, "елки", 10)); len(r) != 1 {
		t.Fatal(r)
	}
	if r := names(searchChannels(list, "12", 10)); len(r) != 1 || r[0] != "Ёлки ТВ" {
		t.Fatal(r)
	}
	if r := names(searchChannels(list, "pyat", 10)); len(r) != 1 {
		t.Fatal(r)
	}
}
