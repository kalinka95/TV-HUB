// Живой визуализатор музыки: TVHub слушает звук компьютера (то, что идёт на телевизор),
// раскладывает его на полосы частот и ~30 раз в секунду шлёт экрану музыки.
package main

import (
	"log"
	"math"
	"math/cmplx"
	"sync"
	"time"
)

const (
	vizBands = 32
	vizFFT   = 2048
)

var (
	vizMu      sync.Mutex
	vizLastReq time.Time
	vizRunning bool
)

// vizWant — экран музыки или заставка открыты и хотят визуализатор. Без новых запросов
// за несколько секунд слушать звук перестаём.
func vizWant() {
	vizMu.Lock()
	defer vizMu.Unlock()
	vizLastReq = time.Now()
	if vizRunning {
		return
	}
	vizRunning = true
	go func() {
		a := newVizAnalyzer()
		err := vizCapture(func(mono []float32, rate int) bool {
			a.push(mono, rate)
			vizMu.Lock()
			want := time.Since(vizLastReq) < 6*time.Second
			vizMu.Unlock()
			return want
		})
		if err != nil {
			log.Printf("визуализатор: %v", err)
		}
		vizMu.Lock()
		vizRunning = false
		vizMu.Unlock()
		pushEvent(map[string]any{"type": "viz", "b": make([]int, vizBands), "bass": 0, "lvl": 0})
	}()
}

type vizAnalyzer struct {
	ring   []float32
	pos    int
	filled bool
	rate   int
	last   time.Time
	bands  [vizBands]float64
	bass   float64
	peak   float64 // самая громкая полоса недавно — подстраиваемся под громкость
	window []float64
	buf    []complex128
}

func newVizAnalyzer() *vizAnalyzer {
	a := &vizAnalyzer{ring: make([]float32, vizFFT), window: make([]float64, vizFFT), buf: make([]complex128, vizFFT), peak: 1e-4}
	for i := range a.window {
		a.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(vizFFT-1)) // окно Ханна
	}
	return a
}

// push добавляет новые сэмплы и, если пора, считает и отправляет кадр.
func (a *vizAnalyzer) push(mono []float32, rate int) {
	a.rate = rate
	for _, v := range mono {
		a.ring[a.pos] = v
		a.pos = (a.pos + 1) % vizFFT
		if a.pos == 0 {
			a.filled = true
		}
	}
	if time.Since(a.last) < 33*time.Millisecond {
		return
	}
	a.last = time.Now()
	b, bass, lvl := a.frame()
	pushEvent(map[string]any{"type": "viz", "b": b, "bass": bass, "lvl": lvl})
}

// frame — полосы 0..100 (низкие слева), сила баса и общая громкость 0..100.
func (a *vizAnalyzer) frame() ([]int, int, int) {
	for i := 0; i < vizFFT; i++ {
		a.buf[i] = complex(float64(a.ring[(a.pos+i)%vizFFT])*a.window[i], 0)
	}
	fft(a.buf)
	rate := float64(a.rate)
	if rate <= 0 {
		rate = 48000
	}
	binHz := rate / vizFFT
	// Полосы по логарифмической шкале, как слышит ухо: 40 Гц … 16 кГц
	lo, hi := math.Log(40), math.Log(16000)
	raw := make([]float64, vizBands)
	maxV := 0.0
	for i := 0; i < vizBands; i++ {
		f1 := math.Exp(lo + (hi-lo)*float64(i)/vizBands)
		f2 := math.Exp(lo + (hi-lo)*float64(i+1)/vizBands)
		k1, k2 := int(f1/binHz), int(f2/binHz)
		if k2 <= k1 {
			k2 = k1 + 1
		}
		s := 0.0
		for k := k1; k < k2 && k < vizFFT/2; k++ {
			s = math.Max(s, cmplx.Abs(a.buf[k]))
		}
		raw[i] = s * (1 + float64(i)*0.06) // высокие частоты тише — немного подтягиваем
		maxV = math.Max(maxV, raw[i])
	}
	// Автоподстройка под громкость: пик плавно опускается, если стало тише
	a.peak = math.Max(maxV, a.peak*0.995)
	out := make([]int, vizBands)
	for i, v := range raw {
		db := 20 * math.Log10(v/a.peak+1e-9) // 0 дБ — самое громкое
		x := math.Max(0, math.Min(1, (db+48)/48))
		if a.peak < 0.02 { // почти тишина
			x = 0
		}
		// Быстро вверх, плавно вниз — столбики не дёргаются
		if x > a.bands[i] {
			a.bands[i] = a.bands[i]*0.3 + x*0.7
		} else {
			a.bands[i] = a.bands[i]*0.82 + x*0.18
		}
		out[i] = int(a.bands[i] * 100)
	}
	b := (a.bands[0] + a.bands[1] + a.bands[2] + a.bands[3]) / 4
	if b > a.bass {
		a.bass = b
	} else {
		a.bass = a.bass*0.8 + b*0.2
	}
	lvl := 0.0
	for _, v := range a.bands {
		lvl += v
	}
	return out, int(a.bass * 100), int(lvl / vizBands * 100)
}

// fft — быстрое преобразование Фурье (длина — степень двойки), на месте.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := x[start+k], x[start+k+size/2]*wk
				x[start+k], x[start+k+size/2] = u+v, u-v
				wk *= w
			}
		}
	}
}
