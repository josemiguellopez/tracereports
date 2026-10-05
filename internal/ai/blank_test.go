package ai

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBlankImage(t *testing.T) {
	dir := t.TempDir()
	save := func(name string, draw func(*image.RGBA)) string {
		img := image.NewRGBA(image.Rect(0, 0, 400, 300))
		draw(img)
		p := filepath.Join(dir, name)
		f, _ := os.Create(p)
		png.Encode(f, img)
		f.Close()
		return p
	}
	white := save("white.png", func(m *image.RGBA) {
		for y := 0; y < 300; y++ {
			for x := 0; x < 400; x++ {
				m.Set(x, y, color.White)
			}
		}
	})
	page := save("page.png", func(m *image.RGBA) {
		for y := 0; y < 300; y++ {
			for x := 0; x < 400; x++ {
				c := color.RGBA{255, 255, 255, 255}
				if y > 100 && y < 140 && x > 50 && x < 350 { // un formulario
					c = color.RGBA{255, 120, 0, 255}
				}
				m.Set(x, y, c)
			}
		}
	})
	if !blankImage(white) || blankImage(page) || blankImage(filepath.Join(dir, "missing.png")) {
		t.Fatal("blank detection")
	}
}
