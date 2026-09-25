package amiga

import (
	"image"
	"image/png"
	"os"
	"testing"
)

// Snapshot BPL at 3M steps with forced insert-disk palette (copper colors
// not yet programmed at that point).
func TestForcedPaletteSnap(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		a.Step()
	}
	// Force KS1.3 insert-disk palette
	a.Custom.WriteReg16(0x180, 0x0FFF)
	a.Custom.WriteReg16(0x182, 0x0000)
	a.Custom.WriteReg16(0x184, 0x077C)
	a.Custom.WriteReg16(0x186, 0x0BBB)
	if err := saveBPL12PNG(a, "/tmp/bpl_3M_forced_palette.png", 0xA572, 0xC4B2); err != nil {
		t.Fatal(err)
	}
	t.Log("saved /tmp/bpl_3M_forced_palette.png")

	// Also render via Video for one frame with forced palette + pointers
	a.Custom.WriteReg16(0x100, 0x2302) // 2 planes
	a.Custom.WriteReg16(0x096, 0x03D0) // DMA
	// set BPL pointers in custom + video
	a.Custom.WriteReg16(0x0E0, 0x0000)
	a.Custom.WriteReg16(0x0E2, 0xA572)
	a.Custom.WriteReg16(0x0E4, 0x0000)
	a.Custom.WriteReg16(0x0E6, 0xC4B2)
	a.Video.bplpt[0] = 0xA572
	a.Video.bplpt[1] = 0xC4B2
	a.Video.DisplayActive = true
	for y := 0; y < 256; y++ {
		// re-snapshot each line (no modulo advance mid-frame issues)
		a.Video.bplpt[0] = 0xA572 + uint32(y*40)
		a.Video.bplpt[1] = 0xC4B2 + uint32(y*40)
		// RenderLine advances pointers - call carefully
	}
	// simpler: just use saveBPL12 which we already did

	// At 4.2M (after more IMAGE, before wipe)
	a2, _ := New(rom, nil)
	a2.Reset()
	for i := 0; i < 4_200_000; i++ {
		a2.Step()
	}
	a2.Custom.WriteReg16(0x180, 0x0FFF)
	a2.Custom.WriteReg16(0x182, 0x0000)
	a2.Custom.WriteReg16(0x184, 0x077C)
	a2.Custom.WriteReg16(0x186, 0x0BBB)
	saveBPL12PNG(a2, "/tmp/bpl_4.2M_forced_palette.png", 0xA572, 0xC4B2)
	t.Log("saved /tmp/bpl_4.2M_forced_palette.png")

	// Count bits
	buf := make([]byte, 2)
	for name, am := range map[string]*Amiga{"3M": a, "4.2M": a2} {
		b1, b2 := 0, 0
		for off := 0; off < 8000; off += 2 {
			am.ChipRAM.Read(0xA572+off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					b1++
				}
			}
			am.ChipRAM.Read(0xC4B2+off, buf)
			v = uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					b2++
				}
			}
		}
		t.Logf("%s: BPL1 bits=%d BPL2 bits=%d", name, b1, b2)
	}
	_ = image.Rect
	_ = png.Encode
	_ = os.Stderr
}
