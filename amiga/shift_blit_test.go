package amiga

import (
	"testing"
)

// Horizontal scroll by 1 pixel (shift=1) twice should not destroy the bitmap.
func TestShiftedCopyStability(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)

	plane := 0x20000
	bpr := 80
	rows := 30
	// Distinct pattern: bit 15 of word 0 set on each row, rest clear
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		buf[0] = 0x80 // pixel 0 set
		buf[1] = 0x00
		for i := 2; i < bpr; i++ {
			buf[i] = 0
		}
		// also set a marker at byte 40
		buf[40] = 0xFF
		a.ChipRAM.Write(plane+r*bpr, buf)
	}

	// Copy with shiftA=1 ascending, full width, to same plane overlapping
	// This is tricky - copy to temp first then back
	temp := 0x30000
	// A->D copy shift 1: content moves right by 1 pixel into temp
	a.Blitter.con0 = 0x19F0 // shiftA=1, USEA|USED, minterm A
	a.Blitter.con1 = 0
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.aptH = uint16(plane >> 16)
	a.Blitter.aptL = uint16(plane)
	a.Blitter.dptH = uint16(temp >> 16)
	a.Blitter.dptL = uint16(temp)
	a.Blitter.Execute(uint16(rows<<6 | 40))

	// Check temp row 0: pixel 0 should have moved to pixel 1
	buf := make([]byte, 4)
	a.ChipRAM.Read(temp, buf)
	t.Logf("after shift-1 copy: first words %02X %02X %02X %02X", buf[0], buf[1], buf[2], buf[3])
	// Original 0x8000 >> 1 = 0x4000 → bytes 0x40, 0x00
	if buf[0] != 0x40 || buf[1] != 0x00 {
		t.Errorf("shift-1 expected 40 00, got %02X %02X", buf[0], buf[1])
	}

	// Now DESC shift - copy back with shift for left move
	// Start at end of each line
	srcLast := uint32(temp + (rows-1)*bpr + bpr - 2)
	dstLast := uint32(plane + (rows-1)*bpr + bpr - 2)
	a.Blitter.con0 = 0x19F0 // shift 1
	a.Blitter.con1 = 0x0002 // DESC
	a.Blitter.aptH = uint16(srcLast >> 16)
	a.Blitter.aptL = uint16(srcLast)
	a.Blitter.dptH = uint16(dstLast >> 16)
	a.Blitter.dptL = uint16(dstLast)
	// full width DESC amod: 2*width == bpr → amod = -bpr + 2*width = 0 for full width?
	// earlier analysis: amod = -bpr + 2*width; width words 40, 2*width=80=bpr, amod=0
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.Execute(uint16(rows<<6 | 40))

	a.ChipRAM.Read(plane, buf)
	t.Logf("after DESC shift-1 back: %02X %02X %02X %02X", buf[0], buf[1], buf[2], buf[3])
}

// Unshifted DESC full-width copy identity
func TestDescFullWidthIdentity(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	plane := 0x20000
	bpr := 80
	rows := 20
	orig := make([]byte, bpr*rows)
	for r := 0; r < rows; r++ {
		for i := 0; i < bpr; i++ {
			orig[r*bpr+i] = byte(r ^ i)
		}
		a.ChipRAM.Write(plane+r*bpr, orig[r*bpr:(r+1)*bpr])
	}
	temp := 0x30000
	// ASC copy to temp
	a.Blitter.con0 = 0x09F0
	a.Blitter.con1 = 0
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.aptH = uint16(plane >> 16)
	a.Blitter.aptL = uint16(plane)
	a.Blitter.dptH = uint16(temp >> 16)
	a.Blitter.dptL = uint16(temp)
	a.Blitter.Execute(uint16(rows<<6 | 40))

	// DESC copy temp back to plane
	srcLast := uint32(temp + rows*bpr - 2)
	dstLast := uint32(plane + rows*bpr - 2)
	a.Blitter.con1 = 0x0002
	a.Blitter.aptH = uint16(srcLast >> 16)
	a.Blitter.aptL = uint16(srcLast)
	a.Blitter.dptH = uint16(dstLast >> 16)
	a.Blitter.dptL = uint16(dstLast)
	a.Blitter.Execute(uint16(rows<<6 | 40))

	got := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, got)
	for i := range orig {
		if orig[i] != got[i] {
			t.Fatalf("DESC identity fail at %d: $%02X vs $%02X", i, orig[i], got[i])
		}
	}
	t.Log("DESC full-width identity OK")
}
