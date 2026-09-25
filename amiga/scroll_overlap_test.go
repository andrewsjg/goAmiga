package amiga

import (
	"testing"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

// Overlapping vertical scroll must use DESC when dest is higher address.
// Wrong direction destroys the bitmap (classic full-screen corruption).
func TestScrollOverlapDown(t *testing.T) {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)

	plane := 0x10000
	bpr := 80 // hires
	rows := 50
	// Fill distinct rows: row r has byte value r+1
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		for i := range buf {
			buf[i] = byte(r + 1)
		}
		a.ChipRAM.Write(plane+r*bpr, buf)
	}

	// Scroll content DOWN by 1 line: row r -> row r+1 for r=0..48
	// Must copy from bottom to top (DESC) so we don't overwrite unread source.
	// Dest starts at last line, source at second-last.
	// width = 40 words, height = 49
	src := uint32(plane + 48*bpr) // last source line
	dst := uint32(plane + 49*bpr) // last dest line
	a.Blitter.con0 = 0x09F0       // A→D copy
	a.Blitter.con1 = 0x0002       // DESC
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.aptH = uint16(src >> 16)
	a.Blitter.aptL = uint16(src)
	a.Blitter.dptH = uint16(dst >> 16)
	a.Blitter.dptL = uint16(dst)
	// In DESC, start at end of each line's last word
	// Actually for DESC, pointers should point to LAST word of first line processed
	// First line processed is bottom: address of last word of row 48
	srcLast := uint32(plane + 48*bpr + bpr - 2)
	dstLast := uint32(plane + 49*bpr + bpr - 2)
	a.Blitter.aptH = uint16(srcLast >> 16)
	a.Blitter.aptL = uint16(srcLast)
	a.Blitter.dptH = uint16(dstLast >> 16)
	a.Blitter.dptL = uint16(dstLast)
	// modulo: after finishing a line (going backwards through words), add modulo
	// After 40 words descending, pointer is at lineStart-2. Want next line start-2 = lineStart-2-bpr.
	// From after last word of line L: addr = L_start - 2.
	// After 40 decrements from L_start+bpr-2: addr = L_start+bpr-2 - 80 = L_start-2.
	// Want to get to (L-1)_start+bpr-2 = L_start-bpr+bpr-2 = L_start-2. Same?
	// Start DESC at bottom line last word: plane+49*bpr-2 for dest...
	// Standard: amod = -2*(words) - bpr? Let me check HRM.
	// Ascending: after width words, ptr is at lineStart+2*width. Add amod to get to next line: amod = bpr - 2*width.
	// Descending: start at lineStart+2*width-2. After width words, ptr = lineStart-2. 
	// Next line up ends at lineStart-2-bpr+bpr wait: next line start = lineStart - bpr, last word = lineStart - 2.
	// From lineStart-2, add amod to get to lineStart-bpr-2? = lineStart-2 + amod => amod = -bpr.
	// Actually after processing line, ptr = start - 2*width = lineStart+2*width-2 - 2*width = lineStart-2.
	// Want next = (lineStart-bpr)+2*width-2. 
	// amod = next - current = (lineStart-bpr+2*width-2) - (lineStart-2) = -bpr + 2*width.
	// If 2*width == bpr (full width), amod = 0 for DESC full-width scroll!
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.Execute(49<<6 | 40) // 49 lines, 40 words

	// Row 0 should still be 1 (untouched if we only scrolled 0..48 -> 1..49)
	// Row 49 should be old row 48 = 49
	// Row 1 should be old row 0 = 1
	buf := make([]byte, 1)
	a.ChipRAM.Read(plane+0*bpr, buf)
	if buf[0] != 1 {
		t.Errorf("row0 after scroll-down: got %d want 1 (source top preserved)", buf[0])
	}
	a.ChipRAM.Read(plane+1*bpr, buf)
	// With DESC copy of 49 lines from row48->49, row47->48, ... row0->1:
	// After correct DESC: row1 should equal old row0 = 1
	if buf[0] != 1 {
		t.Errorf("row1: got %d want 1 (copied from row0)", buf[0])
	}
	a.ChipRAM.Read(plane+49*bpr, buf)
	if buf[0] != 49 {
		t.Errorf("row49: got %d want 49 (from old row48)", buf[0])
	}

	// Now wrong direction (ASC) should destroy - document
	// Reset
	for r := 0; r < rows; r++ {
		b := make([]byte, bpr)
		for i := range b {
			b[i] = byte(r + 1)
		}
		a.ChipRAM.Write(plane+r*bpr, b)
	}
	// ASC copy row0->row1, row1->row2, ... from top: corrupts
	a.Blitter.con1 = 0 // ascending
	a.Blitter.aptH = uint16(plane >> 16)
	a.Blitter.aptL = uint16(plane)
	a.Blitter.dptH = uint16((plane + bpr) >> 16)
	a.Blitter.dptL = uint16(plane + bpr)
	a.Blitter.amod = 0
	a.Blitter.dmod = 0
	a.Blitter.Execute(49<<6 | 40)
	a.ChipRAM.Read(plane+25*bpr, buf)
	t.Logf("ASC overlap row25 value=%d (often corrupted to 1 if bug-like smear)", buf[0])
}
