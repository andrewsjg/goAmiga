package amiga

import (
	"testing"
)

// Decode insert-disk script polylines and verify each segment is continuous in BPL1.
func TestInsertLineContinuity(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	// Script data at FE89D0
	// Format: FF pp = SetAPen; FE pp x y = Flood; xx yy pairs for Move/Draw polyline until next FE/FF
	// Actually from flood_apen_test notes and script dump

	a, _ := New(rom, nil)
	a.Reset()
	// Boot to complete insert art
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}

	bpl1 := 0xA572
	bpr := 40
	get := func(x, y int) bool {
		if x < 0 || y < 0 || x >= 320 || y >= 200 {
			return false
		}
		buf := make([]byte, 1)
		a.ChipRAM.Read(bpl1+y*bpr+x/8, buf)
		return buf[0]&(1<<uint(7-x%8)) != 0
	}

	// Sample all black pixels and check 8-connectivity of outline
	// vs count isolated single pixels (broken lines)

	isolated := 0
	endpoints := 0 // exactly 1 neighbor
	total := 0
	for y := 1; y < 199; y++ {
		for x := 1; x < 319; x++ {
			if !get(x, y) {
				continue
			}
			// only count if not fully interior of thick line - count 4-neighbors on black
			n := 0
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				if get(x+d[0], y+d[1]) {
					n++
				}
			}
			// also diagonals for 8-connect
			n8 := n
			for _, d := range [][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
				if get(x+d[0], y+d[1]) {
					n8++
				}
			}
			total++
			if n8 == 0 {
				isolated++
			}
			if n8 == 1 {
				endpoints++
			}
		}
	}
	t.Logf("BPL1 ink interior-excl: total=%d isolated=%d endpoints(1-neigh)=%d", total, isolated, endpoints)

	// A good outline of this complexity should have few isolated pixels
	// and endpoints roughly matching open paths (small number)
	if isolated > 20 {
		t.Errorf("too many isolated black pixels: %d (line gaps?)", isolated)
	}
	if endpoints > 80 {
		t.Logf("WARNING: many endpoints %d — possible broken polylines", endpoints)
	}

	// Check specific critical regions from script first polyline
	// FF 01 23 0B 3A 0B 3A 21 ... starts Move(0x23,0x0B)=(35,11) etc — those are early coords?
	// From dump: ff01230b3a0b — pen1, (0x23,0x0B)=(35,11), (0x3A,0x0B)=(58,11)
	// Actually coords are bytes: x=0x23=35, y=0x0B=11 — but art bbox was y=50.. so maybe different scale
	// Looking: 23 0b might be relative or the art starts at y=11 in RP coords with offset

	// Dump a horizontal span across disk label area for black pixel gaps
	t.Log("horizontal black runs at y=100 (disk mid):")
	run := 0
	gaps := 0
	for x := 0; x < 320; x++ {
		if get(x, 100) {
			if run == 0 {
				// start
			}
			run++
		} else if run > 0 {
			if run < 3 {
				gaps++
			}
			run = 0
		}
	}
	t.Logf("short black runs (<3px) on y=100: %d", gaps)
}
