package amiga

import (
	"testing"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

func setupBlitAmiga() *Amiga {
	a := &Amiga{ChipRAM: m68kmem.NewRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	return a
}

func fillPlane(a *Amiga, plane, bpr, rows int, v byte) {
	buf := make([]byte, bpr)
	for i := range buf {
		buf[i] = v
	}
	for r := 0; r < rows; r++ {
		a.ChipRAM.Write(plane+r*bpr, buf)
	}
}

func planeSnap(a *Amiga, plane, bpr, rows int) []byte {
	b := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, b)
	return b
}

// Horizontal outline edge: 1-row area XOR (as graphics Draw pure-horizontal).
func TestXORHorizontalEdgeTwice(t *testing.T) {
	a := setupBlitAmiga()
	plane, bpr, rows := 0x10000, 80, 40
	fillPlane(a, plane, bpr, rows, 0x00)
	// background noise
	a.ChipRAM.Write(plane+5*bpr+10, []byte{0xA5, 0x5A, 0xC3, 0x3C})
	before := planeSnap(a, plane, bpr, rows)

	xorH := func(row, x0word, nwords int, maskFirst, maskLast uint16) {
		start := uint32(plane + row*bpr + x0word*2)
		a.Blitter.con0 = 0x035A // USEC|USED, minterm A xor C, USEA=0
		a.Blitter.con1 = 0
		a.Blitter.adat = 0xFFFF
		a.Blitter.afwm = maskFirst
		a.Blitter.alwm = maskLast
		a.Blitter.amod = 0
		a.Blitter.cmod = 0
		a.Blitter.dmod = 0
		a.Blitter.cptH = uint16(start >> 16)
		a.Blitter.cptL = uint16(start)
		a.Blitter.dptH = uint16(start >> 16)
		a.Blitter.dptL = uint16(start)
		a.Blitter.Execute(uint16(1<<6 | nwords))
	}

	// Draw then erase a segment
	xorH(5, 5, 4, 0x0FFF, 0xFFF0)
	xorH(5, 5, 4, 0x0FFF, 0xFFF0)
	after := planeSnap(a, plane, bpr, rows)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("horiz XOR twice mismatch at +%d $%02X->$%02X", i, before[i], after[i])
		}
	}
}

// Vertical line via LINE mode XOR twice.
func TestXORVerticalLineTwice(t *testing.T) {
	a := setupBlitAmiga()
	plane, bpr, rows := 0x10000, 80, 100
	fillPlane(a, plane, bpr, rows, 0x00)
	a.ChipRAM.Write(plane+10*bpr+8, []byte{0xFF, 0x00})
	before := planeSnap(a, plane, bpr, rows)

	// Vertical line +Y major, start at row 10, bit 0 of word 4
	// BLTCON1 $01: +Y major +X minor (from comment in blitter.go)
	// length 40 pixels
	xorV := func() {
		start := uint32(plane + 10*bpr + 8)
		a.Blitter.con0 = 0x0B5A // startbit 0, A+C+D, A xor C
		a.Blitter.con1 = 0x0001 // LINE only, octant 0 bits → need +Y major
		// From mapping: $01: +Y major, +X minor → LINE|key
		// keyToOctant: (con1>>2)&7
		// $01 >> 2 = 0 → octant 2 = +Y major +X minor. Good.
		a.Blitter.con1 = 0x0001 // LINE, key 0
		a.Blitter.adat = 0x8000
		a.Blitter.bdat = 0xFFFF
		a.Blitter.afwm = 0xFFFF
		a.Blitter.alwm = 0xFFFF
		// pure vertical: dx=0, dy=39. apt error, amod=2*(dy-dx)=78, bmod=2*dy=78?
		// For vertical, X major is false. graphics sets these carefully.
		// Use bmod = 2*bpr for stepping? Line mode uses cmod as line modulo.
		a.Blitter.aptL = 0
		a.Blitter.aptH = 0
		a.Blitter.amod = 0 // 2*(dy-dx) for vertical major: actually major is Y, dx=0 dy=len
		a.Blitter.bmod = 2
		a.Blitter.cmod = int16(bpr)
		a.Blitter.dmod = int16(bpr)
		a.Blitter.cptH = uint16(start >> 16)
		a.Blitter.cptL = uint16(start)
		a.Blitter.dptH = uint16(start >> 16)
		a.Blitter.dptL = uint16(start)
		// For pure vertical, BLTCON0 start bit selects pixel; octant +Y major
		// con1 = LINE | octant encoding for +Y major +X minor = $01
		a.Blitter.Execute(40<<6 | 2) // length 40, width 2 for line mode
	}

	xorV()
	mid := planeSnap(a, plane, bpr, rows)
	changed := 0
	for i := range before {
		if before[i] != mid[i] {
			changed++
		}
	}
	t.Logf("vertical line changed %d bytes", changed)
	xorV()
	after := planeSnap(a, plane, bpr, rows)
	mismatch := 0
	for i := range before {
		if before[i] != after[i] {
			mismatch++
			if mismatch <= 10 {
				t.Logf("  +%d $%02X->$%02X", i, before[i], after[i])
			}
		}
	}
	if mismatch > 0 {
		t.Errorf("vertical XOR twice left %d bytes different", mismatch)
	}
}
