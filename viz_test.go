package main

import (
	"math"
	"testing"
)

func TestVizAnalyzer(t *testing.T) {
	a := newVizAnalyzer()
	a.rate = 48000
	// Низкий тон 80 Гц: светятся левые (низкие) полосы, правые — нет
	for i := range a.ring {
		a.ring[i] = float32(0.5 * math.Sin(2*math.Pi*80*float64(i)/48000))
	}
	var b []int
	var bass int
	for i := 0; i < 10; i++ {
		b, bass, _ = a.frame()
	}
	if b[3] < 70 || b[vizBands-2] > 20 {
		t.Fatalf("бас не туда: %v", b)
	}
	if bass < 40 {
		t.Fatalf("бас слабый: %d", bass)
	}
	// Тишина — столбики опускаются до нуля
	for i := range a.ring {
		a.ring[i] = 0
	}
	for i := 0; i < 60; i++ {
		b, bass, _ = a.frame()
	}
	for _, v := range b {
		if v > 1 {
			t.Fatalf("в тишине столбики не опустились: %v", b)
		}
	}
}
