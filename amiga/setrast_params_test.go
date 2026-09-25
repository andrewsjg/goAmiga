package amiga

import (
	"fmt"
	"testing"
)

// Log moduli and template contents for every full-plane BPL blit (200x20).
func TestSetRastParams(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	const bpl1Start, bpl1End = 0xA572, 0xC4B2
	const bpl2Start, bpl2End = 0xC4B2, 0xC4B2 + 8000
	step := 0; _ = step
	prevBusy := false
	count := 0

	for i := 0; i < 8_000_000 && count < 40; i++ {
		step = i; _ = step
		if a.Blitter.busy && !prevBusy {
			dpt := a.Blitter.preDpt
			sz := a.Blitter.lastSize
			h := int(sz >> 6)
			w := int(sz & 0x3F)
			if h == 0 {
				h = 1024
			}
			if w == 0 {
				w = 64
			}
			// Full-frame plane blits
			if h >= 100 && w >= 10 &&
				((dpt >= bpl1Start && dpt < bpl1End) || (dpt >= bpl2Start && dpt < uint32(bpl2End))) {
				// Dump moduli from blitter
				apt := a.Blitter.preApt
				// Read first 8 words of template/source A
				buf := make([]byte, 16)
				a.ChipRAM.Read(int(apt&0x7FFFE), buf)
				// also a few words further along (word 20 and 40)
				buf2 := make([]byte, 4)
				a.ChipRAM.Read(int((apt+40)&0x7FFFE), buf2)
				a.ChipRAM.Read(int((apt+80)&0x7FFFE), buf2[2:])
				plane := "BPL1"
				if dpt >= bpl2Start {
					plane = "BPL2"
				}
				t.Logf("step=%d %s con0=$%04X con1=$%04X apt=$%05X dpt=$%05X sz=%dx%d amod=%d bmod=%d cmod=%d dmod=%d afwm=$%04X alwm=$%04X",
					i, plane, a.Blitter.con0, a.Blitter.con1, apt, dpt, h, w,
					a.Blitter.amod, a.Blitter.bmod, a.Blitter.cmod, a.Blitter.dmod,
					a.Blitter.afwm, a.Blitter.alwm)
				t.Logf("  A[0..7]=%02X  A+20=%02X%02X A+40=%02X%02X bdat=$%04X adat=$%04X",
					buf, buf2[0], buf2[1], buf2[2], buf2[3], a.Blitter.bdat, a.Blitter.adat)
				count++
			}
		}
		prevBusy = a.Blitter.busy
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("logged %d full-plane blits", count)
	_ = fmt.Sprintf
}
