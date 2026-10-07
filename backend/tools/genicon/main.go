// Command genicon writes xDocker's PNG icons (blue rounded square with a
// white cube) in the sizes DSM needs: genicon <dir> <pattern> <size>...
// e.g. genicon ui/images xdocker_%d.png 16 24 32 48 64 72 256
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: genicon <dir> <pattern> <size>...")
		os.Exit(2)
	}
	dir, pattern := os.Args[1], os.Args[2]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	for _, a := range os.Args[3:] {
		n, err := strconv.Atoi(a)
		if err != nil {
			panic(err)
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf(pattern, n)))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, draw(n)); err != nil {
			panic(err)
		}
		f.Close()
	}
}

type pt struct{ x, y float64 }

func draw(n int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	blue := color.NRGBA{0x08, 0x68, 0xCE, 0xFF}
	s := float64(n)
	r := s * 0.2
	// Cube faces in a 24-unit design grid, scaled to the icon.
	sc := func(p pt) pt { return pt{p.x / 24 * s, p.y / 24 * s} }
	top := []pt{sc(pt{12, 4.5}), sc(pt{18.5, 8.1}), sc(pt{12, 11.7}), sc(pt{5.5, 8.1})}
	left := []pt{sc(pt{5.5, 8.1}), sc(pt{12, 11.7}), sc(pt{12, 19.5}), sc(pt{5.5, 15.9})}
	right := []pt{sc(pt{12, 11.7}), sc(pt{18.5, 8.1}), sc(pt{18.5, 15.9}), sc(pt{12, 19.5})}
	const ss = 4 // supersampling for smooth edges
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var bg, w1, w2, w3 int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if inRoundRect(px, py, s, r) {
						bg++
					}
					switch {
					case inPoly(px, py, top):
						w1++
					case inPoly(px, py, left):
						w2++
					case inPoly(px, py, right):
						w3++
					}
				}
			}
			total := float64(ss * ss)
			if bg == 0 {
				continue
			}
			c := blend(blue, color.NRGBA{255, 255, 255, 255}, float64(w1)/total)
			c = blend(c, color.NRGBA{0xDC, 0xEA, 0xFB, 255}, float64(w2)/total)
			c = blend(c, color.NRGBA{0xA9, 0xCB, 0xF2, 255}, float64(w3)/total)
			c.A = uint8(math.Round(255 * float64(bg) / total))
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func blend(a, b color.NRGBA, t float64) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*(1-t) + float64(y)*t)) }
	return color.NRGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

func inRoundRect(x, y, s, r float64) bool {
	cx := math.Max(r, math.Min(s-r, x))
	cy := math.Max(r, math.Min(s-r, y))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

func inPoly(x, y float64, ps []pt) bool {
	in := false
	for i, j := 0, len(ps)-1; i < len(ps); j, i = i, i+1 {
		if (ps[i].y > y) != (ps[j].y > y) && x < (ps[j].x-ps[i].x)*(y-ps[i].y)/(ps[j].y-ps[i].y)+ps[i].x {
			in = !in
		}
	}
	return in
}
