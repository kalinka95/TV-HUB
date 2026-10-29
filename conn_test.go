package main

import "testing"

func TestBtKind(t *testing.T) {
	cases := []struct {
		cod  uint32
		cls  string
		name string
		want string
	}{
		{0x240418, "", "WH-1000XM4", "headphones"},  // аудио, наушники
		{0x240404, "", "Гарнитура", "headphones"},   // аудио, гарнитура
		{0x240414, "", "JBL Flip 5", "speaker"},     // аудио, колонка
		{0x200400, "", "Что-то звуковое", "headphones"},
		{0x002508, "", "Xbox Controller", "gamepad"}, // периферия, геймпад
		{0x5a020c, "", "Galaxy S23", "phone"},
		{0, "AudioVideo/AudioVideoUncategorized", "Buds", "headphones"},
		{0, "AudioVideo/AudioVideoLoudspeaker", "Колонка", "speaker"},
	}
	for _, c := range cases {
		cls := c.cls
		if cls == "" {
			cls = codClass(c.cod)
		}
		if got := btKind(cls, c.name); got != c.want {
			t.Errorf("%s (%#x %s): %s, ждали %s", c.name, c.cod, cls, got, c.want)
		}
	}
}
