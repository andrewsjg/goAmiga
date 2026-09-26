package amiga

import (
	"testing"
)

// Capture BLTCON1 values used by real Draw during insert-disk boot.
func TestDrawOctantsBoot(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	hist := map[uint16]int{}
	samples := 0
	prevSize := uint16(0)
	prevCycles := 0

	for i := 0; i < 3_000_000; i++ {
		// Detect new blit: completionCycles just programmed
		cc := a.Blitter.completionCycles
		sz := a.Blitter.lastSize
		if cc > 0 && (cc != prevCycles || sz != prevSize) && a.Blitter.con1&1 != 0 {
			c1 := a.Blitter.con1
			hist[c1&0x1F]++
			if samples < 50 {
				t.Logf("step=%d LINE BLTCON1=$%04X key=%d SING=%v aptL=$%04X amod=%d bmod=%d len=%d startBit=%d",
					i, c1, (c1>>2)&7, c1&2 != 0, a.Blitter.aptL, a.Blitter.amod, a.Blitter.bmod,
					sz>>6, (a.Blitter.con0>>12)&0xF)
				samples++
			}
		}
		prevCycles = cc
		prevSize = sz
		a.Step()
		if a.CPU.PC == 0xFC5AD0 { // flood — outlines done
			t.Logf("reached first Flood at step %d", i)
			break
		}
	}
	t.Logf("BLTCON1 low5 histogram: %v", hist)
}
