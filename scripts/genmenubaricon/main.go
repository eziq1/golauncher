// Command genmenubaricon draws assets/icon-menubar.png, the macOS menu bar
// (status item) icon: the arc and arrow of assets/icon-small.svg without
// its tile, black on transparent, as a template image that macOS tints for
// light and dark menu bars. It is 44×36 pixels, 22×18 points at 2x (menu
// bar icons are 18 points tall).
//
//	go run ./scripts/genmenubaricon
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

// The strokes of icon-small.svg, in its 512-unit coordinates.
const (
	cx, cy, r = 220.0, 256.0, 120.0  // the arc: M318.3 187.2 A120 120 0 1 0 318.3 324.8
	gapAngle  = 35.0 * math.Pi / 180 // the arc leaves out ±35° on the right
	stroke    = 40.0                 // thinner than the app icon's 48, for 18 points
)

var segments = [][4]float64{
	{220, 256, 412, 256}, // the arrow's shaft
	{372, 216, 412, 256}, // and head
	{412, 256, 372, 296},
}

var arcEnds = [][2]float64{{318.3, 187.2}, {318.3, 324.8}}

// dist returns the distance from (x, y) to the strokes' center lines.
func dist(x, y float64) float64 {
	d := math.Inf(1)
	if a := math.Atan2(y-cy, x-cx); math.Abs(a) >= gapAngle {
		d = math.Abs(math.Hypot(x-cx, y-cy) - r)
	}
	for _, e := range arcEnds {
		d = min(d, math.Hypot(x-e[0], y-e[1]))
	}
	for _, s := range segments {
		d = min(d, segDist(x, y, s))
	}
	return d
}

func segDist(x, y float64, s [4]float64) float64 {
	dx, dy := s[2]-s[0], s[3]-s[1]
	t := ((x-s[0])*dx + (y-s[1])*dy) / (dx*dx + dy*dy)
	t = max(0, min(1, t))
	return math.Hypot(x-(s[0]+t*dx), y-(s[1]+t*dy))
}

func main() {
	out := flag.String("o", "assets/icon-menubar.png", "the PNG to write")
	flag.Parse()
	const w, h, margin = 44, 36, 2
	// The strokes' bounds, with their width.
	x0, x1 := cx-r-stroke/2, 412+stroke/2
	y0, y1 := cy-r-stroke/2, cy+r+stroke/2
	scale := float64(h-2*margin) / (y1 - y0)
	ox := (float64(w) - (x1-x0)*scale) / 2 // centered
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	const n = 8 // samples per pixel side
	for py := range h {
		for px := range w {
			hits := 0
			for sy := range n {
				for sx := range n {
					fx := float64(px) + (float64(sx)+0.5)/n
					fy := float64(py) + (float64(sy)+0.5)/n
					x := x0 + (fx-ox)/scale
					y := y0 + (fy-margin)/scale
					if dist(x, y) <= stroke/2 {
						hits++
					}
				}
			}
			a := uint8(math.Round(float64(hits) * 255 / (n * n)))
			img.SetNRGBA(px, py, color.NRGBA{A: a})
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(f, img); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}
