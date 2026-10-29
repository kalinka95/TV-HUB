package main

import (
	"image/png"
	"os"
	"testing"
)

// Плашка громкости рисуется для разных уровней; TVHUB_OSD_PNG — куда сохранить картинку для просмотра.
func TestRenderVolumeOSD(t *testing.T) {
	for _, lv := range []int{0, 7, 48, 100} {
		img := renderVolumeOSD(460, 92, lv, false)
		if img.Bounds().Dx() != 460 || img.Pix[3] == 0 && img.Pix[len(img.Pix)/2+3] == 0 {
			t.Fatal("пустая картинка")
		}
		if p := os.Getenv("TVHUB_OSD_PNG"); p != "" {
			f, _ := os.Create(p + "-" + string(rune('a'+lv%26)) + ".png")
			png.Encode(f, img)
			f.Close()
		}
	}
	renderVolumeOSD(460, 92, 30, true)
}
