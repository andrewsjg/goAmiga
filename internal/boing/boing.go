// Package boing draws the Amiga's Boing Ball, goAmiga's icon: a sphere
// checkered red and white in 16 segments around and 8 from pole to pole,
// tilted as in the 1984 demo, lit from the upper left, on a transparent
// background with a soft shadow below and to the right. Small icons get
// fewer, bigger checks (8 by 4 at 16 pixels, 12 by 6 at 32), which stay
// checks rather than blurring to pink.
package boing

import (
	"image"
	"image/color"
	"math"
)

// Ball returns the ball drawn in a size×size image.
func Ball(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // samples per pixel each way, for smooth edges
	s := float64(size)
	r := s * 0.42 // the ball's radius
	cx, cy := s*0.47, s*0.46
	// The shadow: an ellipse offset down and to the right.
	shx, shy, shr := cx+s*0.06, cy+s*0.08, r
	tilt := -17.0 * math.Pi / 180 // the ball leans, as in the demo
	spin := 0.35                  // turn it a little so no seam faces front
	// The light, from the upper left and in front.
	lx, ly, lz := -0.45, -0.55, 0.70
	ln := math.Sqrt(lx*lx + ly*ly + lz*lz)
	lx, ly, lz = lx/ln, ly/ln, lz/ln
	around := 16.0
	switch {
	case size < 24:
		around = 8
	case size < 48:
		around = 12
	}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var rr, gg, bb, aa float64 // premultiplied
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss - cx) / r
					y := (float64(py) + (float64(sy)+0.5)/ss - cy) / r
					d2 := x*x + y*y
					if d2 > 1 {
						// Outside the ball: the shadow, if under it.
						dx := (float64(px) + (float64(sx)+0.5)/ss - shx) / shr
						dy := (float64(py) + (float64(sy)+0.5)/ss - shy) / (shr * 0.95)
						if e := dx*dx + dy*dy; e < 1 {
							a := 0.32 * (1 - e*e)
							rr, gg, bb, aa = rr+0, gg+0, bb+0, aa+a
						}
						continue
					}
					z := math.Sqrt(1 - d2)
					// Undo the tilt (a turn about the view axis), then the
					// spin about the ball's own axis, to find the point on
					// the ball's surface.
					tx := x*math.Cos(tilt) - y*math.Sin(tilt)
					ty := x*math.Sin(tilt) + y*math.Cos(tilt)
					lon := math.Atan2(tx, z) + spin
					lat := math.Asin(math.Max(-1, math.Min(1, ty)))
					u := int(math.Floor(lon / (2 * math.Pi / around)))
					v := int(math.Floor((lat + math.Pi/2) / (math.Pi / (around / 2))))
					red := (u+v)&1 == 0
					// Lambert shading with ambient light and a highlight.
					diff := math.Max(0, x*lx+y*ly+z*lz)
					shade := 0.35 + 0.65*diff
					spec := math.Pow(diff, 40) * 0.6
					var c [3]float64
					if red {
						c = [3]float64{0.86, 0.06, 0.08}
					} else {
						c = [3]float64{0.97, 0.97, 0.97}
					}
					for i := range c {
						c[i] = math.Min(1, c[i]*shade+spec)
					}
					rr, gg, bb, aa = rr+c[0], gg+c[1], bb+c[2], aa+1
				}
			}
			n := float64(ss * ss)
			img.SetRGBA(px, py, color.RGBA{
				R: uint8(rr / n * 255), G: uint8(gg / n * 255),
				B: uint8(bb / n * 255), A: uint8(aa / n * 255),
			})
		}
	}
	return img
}

// Icons returns the ball at the sizes window systems ask for.
func Icons() []image.Image {
	var out []image.Image
	for _, n := range []int{16, 32, 48, 64, 128, 256} {
		out = append(out, Ball(n))
	}
	return out
}
