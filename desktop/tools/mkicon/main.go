// Command mkicon draws the application icon: a prompt chevron and a cursor on
// the screen's own background, as a 256-pixel PNG. It is run by hand when the
// icon changes; its output is committed, so a build needs nothing but Go.
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"

	"golang.org/x/image/vector"
)

func main() {
	const n = 256
	img := image.NewRGBA(image.Rect(0, 0, n, n))

	fill := func(c color.Color, build func(z *vector.Rasterizer)) {
		z := vector.NewRasterizer(n, n)
		build(z)
		z.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{})
	}

	// The tile: One Dark's background, with the rounded corners Windows 11
	// gives its own icons.
	fill(color.RGBA{0x28, 0x2c, 0x34, 0xff}, func(z *vector.Rasterizer) { roundRect(z, 8, 8, 248, 248, 52) })
	// The chevron, as one solid shape so its point is a point.
	fill(color.RGBA{0x61, 0xaf, 0xef, 0xff}, func(z *vector.Rasterizer) {
		z.MoveTo(56, 96)
		z.LineTo(78, 74)
		z.LineTo(134, 128)
		z.LineTo(78, 182)
		z.LineTo(56, 160)
		z.LineTo(90, 128)
		z.ClosePath()
	})
	fill(color.RGBA{0xe4, 0xe8, 0xef, 0xff}, func(z *vector.Rasterizer) { roundRect(z, 140, 160, 196, 180, 6) })

	f, err := os.Create("icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	_ = draw.Src
}

// roundRect adds a rectangle with rounded corners.
func roundRect(z *vector.Rasterizer, x0, y0, x1, y1, r float32) {
	const k = 0.5523 // control point distance for a quarter circle
	z.MoveTo(x0+r, y0)
	z.LineTo(x1-r, y0)
	z.CubeTo(x1-r+r*k, y0, x1, y0+r-r*k, x1, y0+r)
	z.LineTo(x1, y1-r)
	z.CubeTo(x1, y1-r+r*k, x1-r+r*k, y1, x1-r, y1)
	z.LineTo(x0+r, y1)
	z.CubeTo(x0+r-r*k, y1, x0, y1-r+r*k, x0, y1-r)
	z.LineTo(x0, y0+r)
	z.CubeTo(x0, y0+r-r*k, x0+r-r*k, y0, x0+r, y0)
	z.ClosePath()
}
