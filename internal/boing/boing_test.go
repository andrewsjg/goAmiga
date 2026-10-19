package boing

import (
	"image/png"
	"os"
	"testing"
)

// The ball is opaque in the middle, transparent in the corners, and has
// both colours.
func TestBall(t *testing.T) {
	b := Ball(64)
	if a := b.RGBAAt(30, 29).A; a != 255 {
		t.Errorf("centre alpha %d, want opaque", a)
	}
	if a := b.RGBAAt(0, 0).A; a != 0 {
		t.Errorf("corner alpha %d, want transparent", a)
	}
	red, white := false, false
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			c := b.RGBAAt(x, y)
			if c.A == 255 && c.R > 150 && c.G < 60 {
				red = true
			}
			if c.A == 255 && c.R > 200 && c.G > 200 && c.B > 200 {
				white = true
			}
		}
	}
	if !red || !white {
		t.Errorf("red %v white %v: want both checks", red, white)
	}
	if p := os.Getenv("BOING_PNG"); p != "" {
		f, _ := os.Create(p)
		png.Encode(f, Ball(512))
		f.Close()
	}
}
