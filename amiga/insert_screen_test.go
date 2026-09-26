package amiga

import "testing"

// insertScreen boots Kickstart 1.3 with no disk and returns the colour
// histogram ($RGB → pixels) of the insert-disk picture, which the ROM draws
// with lines, Flood fills and BltTemplate text.
func insertScreen(t *testing.T) map[uint16]int {
	t.Helper()
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true
	for f := 0; f < 300; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	hist := map[uint16]int{}
	p := a.Video.Pixels
	for i := 0; i < len(p); i += 4 {
		hist[uint16(p[i]/17)<<8|uint16(p[i+1]/17)<<4|uint16(p[i+2]/17)]++
	}
	return hist
}

// The insert-disk picture comes out right with Kickstart's own Flood: the
// disk's blue fill stays inside its outline and the grey shutter is filled.
// graphics.library's Flood used to be replaced natively (flood.go) because
// it filled whole bitplanes; that was the blitter's descending-modulo bug,
// fixed since. Reference counts are from a render checked by eye (hand
// holding a blue Workbench disk, "V1.3").
func TestInsertScreenFloodFills(t *testing.T) {
	hist := insertScreen(t)
	want := map[uint16]int{
		0x000: 5254,   // outlines and text
		0x77C: 7600,   // disk body (Flood fill)
		0xBBB: 1814,   // shutter (Flood fill)
		0xFFF: 149172, // background
	}
	for c, n := range want {
		if got := hist[c]; got < n*95/100 || got > n*105/100 {
			t.Errorf("colour $%03X: %d pixels, want %d ±5%%", c, got, n)
		}
	}
	if len(hist) != len(want) {
		t.Errorf("colours %v, want exactly %d", hist, len(want))
	}
}
