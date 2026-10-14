package demo

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// screen draws a schematic screenshot of the shop: the layout of each page with blocks instead of
// text, enough for the replay, the timeline and the failure screenshot of the demo.
func screen(kind string) []byte {
	const w, h = 960, 600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fill := func(x, y, bw, bh int, c color.RGBA) {
		draw.Draw(img, image.Rect(x, y, x+bw, y+bh), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	var (
		bg     = color.RGBA{247, 248, 250, 255}
		bar    = color.RGBA{31, 41, 55, 255}
		card   = color.RGBA{255, 255, 255, 255}
		line   = color.RGBA{209, 213, 219, 255}
		text   = color.RGBA{156, 163, 175, 255}
		dark   = color.RGBA{75, 85, 99, 255}
		blue   = color.RGBA{37, 99, 235, 255}
		green  = color.RGBA{22, 163, 74, 255}
		red    = color.RGBA{220, 38, 38, 255}
		redBg  = color.RGBA{254, 226, 226, 255}
		grnBg  = color.RGBA{220, 252, 231, 255}
		accent = color.RGBA{249, 115, 22, 255}
	)
	fill(0, 0, w, h, bg)
	fill(0, 0, w, 56, bar)
	fill(24, 18, 120, 20, accent) // logo
	for i := 0; i < 4; i++ {
		fill(600+i*80, 24, 56, 8, text)
	}
	textLines := func(x, y, n, width int) {
		for i := 0; i < n; i++ {
			fill(x, y+i*18, width-(i%3)*40, 8, text)
		}
	}
	switch kind {
	case "login":
		fill(330, 130, 300, 320, card)
		fill(360, 160, 140, 14, dark)
		fill(360, 210, 240, 38, line)
		fill(360, 270, 240, 38, line)
		fill(360, 340, 240, 44, blue)
	case "dashboard":
		fill(40, 90, 880, 60, card)
		fill(60, 112, 220, 16, dark)
		for i := 0; i < 3; i++ {
			fill(40+i*300, 180, 280, 160, card)
			textLines(60+i*300, 210, 4, 220)
		}
	case "catalog", "product":
		for i := 0; i < 3; i++ {
			x := 40 + i*300
			fill(x, 90, 280, 380, card)
			fill(x+20, 110, 240, 180, line)
			textLines(x+20, 310, 3, 200)
			c := blue
			if kind == "product" && i == 0 {
				c = color.RGBA{191, 219, 254, 255} // el botón está, con otro selector
			}
			fill(x+20, 410, 160, 40, c)
		}
	case "cart":
		fill(40, 90, 560, 420, card)
		for i := 0; i < 3; i++ {
			fill(60, 120+i*90, 80, 70, line)
			textLines(160, 130+i*90, 2, 300)
		}
		fill(630, 90, 290, 260, card)
		textLines(650, 120, 5, 230)
		fill(650, 280, 250, 44, blue)
	case "confirmed":
		fill(40, 90, 880, 80, grnBg)
		fill(40, 90, 8, 80, green)
		fill(70, 115, 260, 18, green)
		fill(40, 200, 880, 260, card)
		textLines(70, 230, 6, 600)
	case "error":
		fill(40, 90, 880, 80, redBg)
		fill(40, 90, 8, 80, red)
		fill(70, 112, 300, 18, red)
		fill(70, 140, 480, 10, color.RGBA{248, 113, 113, 255})
		fill(40, 200, 560, 300, card)
		textLines(60, 230, 6, 480)
		fill(630, 200, 290, 200, card)
		fill(650, 330, 250, 44, color.RGBA{147, 197, 253, 255}) // el botón de pagar quedó deshabilitado
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
