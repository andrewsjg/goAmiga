package amiga

import (
	"fmt"
	"testing"
)

func TestReadPixelProbe(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Run to first Flood, resolve LVOs, call ReadPixel behavior via ROM
	const flood = uint32(0xFC5AD0)
	for i := 0; i < 3_000_000; i++ {
		if a.CPU.PC == flood {
			gfx := a.CPU.A[6]
			buf := make([]byte, 6)
			for _, lvo := range []struct {
				off  int
				name string
			}{
				{-0x13E, "ReadPixel"},
				{-0x144, "WritePixel"},
				{-0x14A, "Flood"},
				{-0x156, "SetAPen"},
				{-0xF0, "Move"},
				{-0xF6, "Draw"},
				{-0xEA, "SetRast"},
				{-0x132, "RectFill"},
			} {
				addr := int(gfx) + lvo.off
				a.CPU.M.Read(addr, buf)
				if buf[0] == 0x4E && buf[1] == 0xF9 {
					target := uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
					// disassemble first words of target
					code := make([]byte, 8)
					a.CPU.M.Read(int(target), code)
					t.Logf("%s LVO %d -> $%08X  head %02X", lvo.name, lvo.off, target, code)
					// detect stub: clr.l d0; rts = 4280 4E75
					if code[0] == 0x42 && code[1] == 0x80 && code[2] == 0x4E && code[3] == 0x75 {
						t.Logf("  *** STUB returns 0! ***")
					}
					if code[0] == 0x4E && code[1] == 0x75 {
						t.Logf("  *** RTS only stub ***")
					}
				} else {
					t.Logf("%s @ $%08X: %02X", lvo.name, addr, buf)
				}
			}

			// After outlines drawn, sample a few pixels via chip RAM manually
			// and via executing ReadPixel if not stub
			rp := a.CPU.A[1]
			// Manual planar read at (107,109) and at outline point
			readPix := func(x, y int) int {
				// from BitMap at RP+4
				bb := make([]byte, 4)
				a.ChipRAM.Read(int(rp+4), bb)
				bm := int(uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3]))
				bprb := make([]byte, 2)
				a.ChipRAM.Read(bm, bprb)
				bpr := int(bprb[0])<<8 | int(bprb[1])
				depthb := make([]byte, 1)
				a.ChipRAM.Read(bm+5, depthb)
				depth := int(depthb[0])
				pen := 0
				for p := 0; p < depth && p < 8; p++ {
					a.ChipRAM.Read(bm+8+p*4, bb)
					plane := int(uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3]))
					byteOff := y*bpr + x/8
					bit := 7 - (x % 8)
					pb := make([]byte, 1)
					a.ChipRAM.Read(plane+byteOff, pb)
					if pb[0]&(1<<uint(bit)) != 0 {
						pen |= 1 << uint(p)
					}
				}
				return pen
			}
			t.Logf("manual pixel (107,109)=%d (100,50)=%d (0,0)=%d (160,100)=%d",
				readPix(107, 109), readPix(100, 50), readPix(0, 0), readPix(160, 100))
			// count non-zero pixels roughly
			nz := 0
			for y := 0; y < 200; y += 2 {
				for x := 0; x < 320; x += 2 {
					if readPix(x, y) != 0 {
						nz++
					}
				}
			}
			t.Logf("nonzero samples (every 2nd) ~%d", nz)
			break
		}
		a.Step()
	}
	_ = fmt.Sprintf
}
