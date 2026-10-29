// Окошко громкости: рисуем картинку сами (тёмная плашка, значок динамика, полоса и проценты),
// а показывает её окно поверх всех программ (volume_windows.go).
package main

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

var (
	osdFontOnce sync.Once
	osdFont     *opentype.Font
)

// roundRect добавляет в контур прямоугольник со скруглёнными углами.
func roundRect(r *vector.Rasterizer, x0, y0, x1, y1, rad float32) {
	const k = 0.5523 // кривая Безье, похожая на четверть окружности
	r.MoveTo(x0+rad, y0)
	r.LineTo(x1-rad, y0)
	r.CubeTo(x1-rad+rad*k, y0, x1, y0+rad-rad*k, x1, y0+rad)
	r.LineTo(x1, y1-rad)
	r.CubeTo(x1, y1-rad+rad*k, x1-rad+rad*k, y1, x1-rad, y1)
	r.LineTo(x0+rad, y1)
	r.CubeTo(x0+rad-rad*k, y1, x0, y1-rad+rad*k, x0, y1-rad)
	r.LineTo(x0, y0+rad)
	r.CubeTo(x0, y0+rad-rad*k, x0+rad-rad*k, y0, x0+rad, y0)
	r.ClosePath()
}

// arc добавляет дугу толщиной th вокруг (cx, cy) от угла a0 до a1 (в радианах).
func arc(r *vector.Rasterizer, cx, cy, rad, th float32, a0, a1 float64) {
	const n = 24
	pt := func(rr float32, a float64) (float32, float32) {
		return cx + rr*float32(math.Cos(a)), cy + rr*float32(math.Sin(a))
	}
	x, y := pt(rad+th/2, a0)
	r.MoveTo(x, y)
	for i := 1; i <= n; i++ {
		x, y = pt(rad+th/2, a0+(a1-a0)*float64(i)/n)
		r.LineTo(x, y)
	}
	for i := n; i >= 0; i-- {
		x, y = pt(rad-th/2, a0+(a1-a0)*float64(i)/n)
		r.LineTo(x, y)
	}
	r.ClosePath()
}

func fill(dst *image.RGBA, src image.Image, path func(r *vector.Rasterizer)) {
	b := dst.Bounds()
	r := vector.NewRasterizer(b.Dx(), b.Dy())
	path(r)
	r.Draw(dst, b, src, image.Point{})
}

type hGradient struct {
	x0, x1 int
	c0, c1 color.NRGBA
}

func (g hGradient) ColorModel() color.Model { return color.NRGBAModel }
func (g hGradient) Bounds() image.Rectangle {
	return image.Rect(-1e6, -1e6, 1e6, 1e6)
}
func (g hGradient) At(x, y int) color.Color {
	t := 0.0
	if g.x1 > g.x0 {
		t = math.Max(0, math.Min(1, float64(x-g.x0)/float64(g.x1-g.x0)))
	}
	mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	return color.NRGBA{mix(g.c0.R, g.c1.R), mix(g.c0.G, g.c1.G), mix(g.c0.B, g.c1.B), 255}
}

// renderVolumeOSD рисует плашку громкости размером w×h (пиксели с учётом прозрачности).
func renderVolumeOSD(w, h, level int, muted bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	W, H := float32(w), float32(h)
	white := image.NewUniform(color.NRGBA{255, 255, 255, 255})

	// Плашка с лёгкой обводкой
	fill(img, image.NewUniform(color.NRGBA{255, 255, 255, 36}), func(r *vector.Rasterizer) { roundRect(r, 0, 0, W, H, H/2) })
	fill(img, image.NewUniform(color.NRGBA{18, 21, 29, 240}), func(r *vector.Rasterizer) { roundRect(r, 1.5, 1.5, W-1.5, H-1.5, H/2-1.5) })

	// Значок динамика
	cx, cy, s := H*0.62, H/2, H*0.17
	fill(img, white, func(r *vector.Rasterizer) {
		r.MoveTo(cx-s*1.3, cy-s*0.55)
		r.LineTo(cx-s*0.45, cy-s*0.55)
		r.LineTo(cx+s*0.55, cy-s*1.35)
		r.LineTo(cx+s*0.55, cy+s*1.35)
		r.LineTo(cx-s*0.45, cy+s*0.55)
		r.LineTo(cx-s*1.3, cy+s*0.55)
		r.ClosePath()
	})
	if muted || level == 0 {
		// Крестик
		x, y, d, t := cx+s*1.55, cy, s*0.62, s*0.2
		// Две перекладины рисуем отдельно, иначе в месте пересечения получается дырка
		line := func(x0, y0, x1, y1 float32) {
			dx, dy := x1-x0, y1-y0
			l := float32(math.Hypot(float64(dx), float64(dy)))
			nx, ny := -dy/l*t, dx/l*t
			fill(img, white, func(r *vector.Rasterizer) {
				r.MoveTo(x0+nx, y0+ny)
				r.LineTo(x1+nx, y1+ny)
				r.LineTo(x1-nx, y1-ny)
				r.LineTo(x0-nx, y0-ny)
				r.ClosePath()
			})
		}
		line(x-d, y-d, x+d, y+d)
		line(x-d, y+d, x+d, y-d)
	} else {
		waves := 1
		if level > 33 {
			waves = 2
		}
		if level > 66 {
			waves = 3
		}
		fill(img, white, func(r *vector.Rasterizer) {
			for i := 0; i < waves; i++ {
				arc(r, cx+s*0.35, cy, s*(0.75+0.62*float32(i)), s*0.2, -math.Pi/3.6, math.Pi/3.6)
			}
		})
	}

	// Полоса громкости
	bx0, bx1, bh := H*1.18, W-H*1.32, H*0.13
	by0 := cy - bh/2
	fill(img, image.NewUniform(color.NRGBA{255, 255, 255, 46}), func(r *vector.Rasterizer) { roundRect(r, bx0, by0, bx1, by0+bh, bh/2) })
	shown := level
	if muted {
		shown = 0
	}
	if shown > 0 {
		x := bx0 + (bx1-bx0)*float32(shown)/100
		if x-bx0 < bh {
			x = bx0 + bh
		}
		grad := hGradient{int(bx0), int(bx1), color.NRGBA{79, 140, 255, 255}, color.NRGBA{139, 92, 246, 255}}
		fill(img, grad, func(r *vector.Rasterizer) { roundRect(r, bx0, by0, x, by0+bh, bh/2) })
	}

	// Проценты
	osdFontOnce.Do(func() { osdFont, _ = opentype.Parse(gobold.TTF) })
	if osdFont != nil {
		face, err := opentype.NewFace(osdFont, &opentype.FaceOptions{Size: float64(H) * 0.36, DPI: 72, Hinting: font.HintingFull})
		if err == nil {
			txt := strconv.Itoa(level)
			if muted {
				txt = "—"
			}
			d := &font.Drawer{Dst: img, Src: white, Face: face}
			tw := d.MeasureString(txt).Ceil()
			m := face.Metrics()
			base := int(cy) + (m.Ascent.Ceil()-m.Descent.Ceil())/2
			d.Dot = fixed.P(int(W-H*0.42)-tw, base)
			d.DrawString(txt)
			face.Close()
		}
	}
	return img
}

// osdBGRA — пиксели в порядке синий-зелёный-красный-прозрачность (так их ждёт Windows).
func osdBGRA(img *image.RGBA) []byte {
	out := make([]byte, len(img.Pix))
	for i := 0; i+3 < len(img.Pix); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = img.Pix[i+2], img.Pix[i+1], img.Pix[i], img.Pix[i+3]
	}
	return out
}
