package amiga

import (
	"testing"
)

// Measure horizontal black runs vs blue fill extent on insert-disk art.
//
// Pure-horizontal Draw uses a 1-row area blit with USEA=0 and AFWM/ALWM
// clipping BLTADAT=$FFFF. If those masks are ignored, the top disk edge
// expands to whole 16-pixel words (e.g. black[96..207] instead of [103..196]).
func TestHorizOverrun(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}

	bpl1, bpl2 := 0xA572, 0xC4B2
	bpr := 40
	penAt := func(x, y int) int {
		buf := make([]byte, 1)
		a.ChipRAM.Read(bpl1+y*bpr+x/8, buf)
		p := 0
		if buf[0]&(1<<uint(7-x%8)) != 0 {
			p |= 1
		}
		a.ChipRAM.Read(bpl2+y*bpr+x/8, buf)
		if buf[0]&(1<<uint(7-x%8)) != 0 {
			p |= 2
		}
		return p
	}

	// Script polyline (offset +70,+40): outer top is H y=50 x=103→196.
	var bmin, bmax = -1, -1
	for x := 0; x < 320; x++ {
		if penAt(x, 50)&1 != 0 {
			if bmin < 0 {
				bmin = x
			}
			bmax = x
		}
	}
	t.Logf("y=50 top edge black[%d..%d] n=%d (expect 103..196)", bmin, bmax, bmax-bmin+1)
	if bmin != 103 || bmax != 196 {
		t.Errorf("top disk edge overrun/misaligned: black[%d..%d], want [103..196]", bmin, bmax)
	}
	// Must not spill into neighbouring 16-pixel words outside the line.
	if penAt(96, 50)&1 != 0 || penAt(102, 50)&1 != 0 {
		t.Errorf("top edge overruns left of x=103")
	}
	if penAt(197, 50)&1 != 0 || penAt(207, 50)&1 != 0 {
		t.Errorf("top edge overruns right of x=196")
	}

	for _, y := range []int{51, 52, 72, 73, 96, 97} {
		var black, blue []int
		for x := 0; x < 320; x++ {
			p := penAt(x, y)
			if p&1 != 0 {
				black = append(black, x)
			}
			if p&2 != 0 {
				blue = append(blue, x)
			}
		}
		b0, b1 := -1, -1
		if len(black) > 0 {
			b0, b1 = black[0], black[len(black)-1]
		}
		u0, u1 := -1, -1
		if len(blue) > 0 {
			u0, u1 = blue[0], blue[len(blue)-1]
		}
		t.Logf("y=%d black[%d..%d] n=%d  blue[%d..%d] n=%d",
			y, b0, b1, len(black), u0, u1, len(blue))
	}
}
