package amiga

import "testing"

// graphics.library Draw horizontal: USEA=0, ADAT=$FFFF, AFWM/ALWM clip the run.
func TestHorizAreaBlitMasks(t *testing.T) {
	a := newTestAmigaBlitter()
	// Clear row at $2000 (40 bytes)
	for i := 0; i < 40; i++ {
		a.ChipRAM.Write(0x2000+i, []byte{0})
	}
	// Draw pixels x=5..20 (16 pixels) on a line: words at 0 and 2.
	// Word0 bits: pixels 0-15, want bits for x=5..15 → mask $07FF
	// Word1 bits: pixels 16-31, want bits for x=16..20 → mask $F800
	// AFWM=$07FF, ALWM=$F800, width=2, height=1
	// Minterm: D = A | C or simpler D = A: $F0 with USEA would copy A;
	// with USEA off, A from ADAT. Use minterm $F0 (A), USEC|USED? Actually
	// need write D. USEA=0 USEC=0 USED=1 minterm $F0 → D=A.
	a.Blitter.con0 = 0x01F0 // USED, minterm A
	a.Blitter.con1 = 0
	a.Blitter.afwm = 0x07FF // pixels 5-15
	a.Blitter.alwm = 0xF800 // pixels 16-20
	a.Blitter.adat = 0xFFFF
	a.Blitter.dptH, a.Blitter.dptL = 0, 0x2000
	a.Blitter.dmod = 0
	blitNow(a, 1<<6|2) // 1 high, 2 wide

	dst := make([]byte, 4)
	a.ChipRAM.Read(0x2000, dst)
	w0 := uint16(dst[0])<<8 | uint16(dst[1])
	w1 := uint16(dst[2])<<8 | uint16(dst[3])
	if w0 != 0x07FF {
		t.Errorf("word0: got $%04X want $07FF (AFWM must apply with USEA=0)", w0)
	}
	if w1 != 0xF800 {
		t.Errorf("word1: got $%04X want $F800 (ALWM must apply with USEA=0)", w1)
	}
}
