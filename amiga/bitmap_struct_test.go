package amiga

import (
	"fmt"
	"testing"
)

func TestBitMapStruct(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Run to first full-plane blit
	prevBusy := false
	for i := 0; i < 3_000_000; i++ {
		if a.Blitter.busy && !prevBusy {
			sz := a.Blitter.lastSize
			h, w := int(sz>>6), int(sz&0x3F)
			if h >= 100 && w >= 10 && a.Blitter.preDpt == 0xA572 {
				// Dump RastPort $A4D2 and BitMap $A54A
				dump := func(addr int, n int, label string) {
					buf := make([]byte, n)
					a.ChipRAM.Read(addr, buf)
					t.Logf("%s @ $%05X: %02X", label, addr, buf)
				}
				dump(0xA4D2, 48, "RastPort")
				// BitMap from RP+4
				bb := make([]byte, 4)
				a.ChipRAM.Read(0xA4D6, bb) // wait RP+4 = A4D6
				// Actually RP at A4D2: +0 Layer, +4 BitMap
				a.ChipRAM.Read(0xA4D2+4, bb)
				bm := int(uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3]))
				t.Logf("BitMap ptr from RP = $%05X", bm)
				dump(bm, 40, "BitMap")
				// Parse BitMap
				bbuf := make([]byte, 40)
				a.ChipRAM.Read(bm, bbuf)
				bpr := int(bbuf[0])<<8 | int(bbuf[1])
				rows := int(bbuf[2])<<8 | int(bbuf[3])
				flags := bbuf[4]
				depth := bbuf[5]
				t.Logf("  BytesPerRow=%d Rows=%d Flags=%d Depth=%d", bpr, rows, flags, depth)
				for p := 0; p < int(depth) && p < 8; p++ {
					off := 8 + p*4
					pl := uint32(bbuf[off])<<24 | uint32(bbuf[off+1])<<16 | uint32(bbuf[off+2])<<8 | uint32(bbuf[off+3])
					t.Logf("  Plane[%d]=$%08X", p, pl)
				}
				// FgPen
				fg := make([]byte, 3)
				a.ChipRAM.Read(0xA4D2+25, fg)
				t.Logf("  FgPen=%d BgPen=%d DrMd=%d", fg[0], fg[1], fg[2])
				return
			}
		}
		prevBusy = a.Blitter.busy
		a.Step()
	}
	t.Fatal("no full plane blit found")
	_ = fmt.Sprintf
}
