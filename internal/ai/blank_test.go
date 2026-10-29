package ai

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
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
		return name
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
	shots := atrest.Dir{Path: dir}
	if !blankImage(shots, white) || blankImage(shots, page) || blankImage(shots, "missing.png") {
		t.Fatal("blank detection")
	}
	// cifradas en disco (TRACEREPORTS_ENCRYPT_AT_REST) se detectan igual
	v, _ := atrest.New(make([]byte, 32), nil, true)
	enc := atrest.Dir{Path: t.TempDir(), Vault: v}
	for _, name := range []string{white, page} {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		enc.WriteFile(name, raw)
	}
	if !blankImage(enc, white) || blankImage(enc, page) {
		t.Fatal("blank detection (encrypted)")
	}
}
