package amiga

import "testing"

// agaAmiga is deniseAmiga with the AGA chipset (and its reset values).
func agaAmiga(t *testing.T, bplcon0 uint16, planes ...[]byte) *Amiga {
	t.Helper()
	a := deniseAmiga(t, bplcon0, planes...)
	a.AGA, a.ECSAgnus, a.ECSDenise = true, true, true
	a.Custom.agaReset()
	return a
}

// rgbAt24 is output column x of the first display line as 24-bit RGB.
func rgbAt24(a *Amiga, x int) uint32 {
	p := a.Video.Pixels[4*x:]
	return uint32(p[0])<<16 | uint32(p[1])<<8 | uint32(p[2])
}

// setColor24 writes AGA palette entry i through COLORxx with the bank and
// LOCT, the way graphics.library does.
func setColor24(a *Amiga, i int, rgb uint32) {
	c := a.Custom
	con3 := c.ReadReg16(0x106) &^ 0xE200
	hi := uint16(rgb>>20&0xF)<<8 | uint16(rgb>>12&0xF)<<4 | uint16(rgb>>4&0xF)
	lo := uint16(rgb>>16&0xF)<<8 | uint16(rgb>>8&0xF)<<4 | uint16(rgb&0xF)
	writeCustom16(a, 0x106, con3|uint16(i>>5)<<13)
	writeCustom16(a, 0x180+2*(i&31), hi)
	writeCustom16(a, 0x106, con3|uint16(i>>5)<<13|0x0200)
	writeCustom16(a, 0x180+2*(i&31), lo)
	writeCustom16(a, 0x106, con3)
}

func TestAGAChipIDs(t *testing.T) {
	a := createTestAmiga(t)
	a.SetChipset("aga")
	var b [2]byte
	a.Custom.Read(0x004, b[:])
	if b[0]&0x7F != 0x22 {
		t.Errorf("VPOSR Agnus ID $%02X, want $22 (Alice)", b[0]&0x7F)
	}
	a.Custom.Read(0x07C, b[:])
	if b[1] != 0xF8 {
		t.Errorf("DENISEID low byte $%02X, want $F8 (Lisa)", b[1])
	}
}

// COLORxx writes go to the bank BPLCON3 selects; LOCT writes the low
// nibbles.
func TestAGAPaletteBanksAndLOCT(t *testing.T) {
	a := agaAmiga(t, 0x0200)
	writeCustom16(a, 0x106, 0x4C00) // bank 2
	writeCustom16(a, 0x18A, 0x0F80) // COLOR05 -> entry 69
	if got := a.Video.agaPal[69]; got != 0xFF8800 {
		t.Errorf("entry 69 = $%06X, want $FF8800", got)
	}
	writeCustom16(a, 0x106, 0x4E00) // bank 2, LOCT
	writeCustom16(a, 0x18A, 0x0123)
	if got := a.Video.agaPal[69]; got != 0xF18203 {
		t.Errorf("entry 69 after LOCT = $%06X, want $F18203", got)
	}
	if a.Video.agaPal[5] != 0 {
		t.Error("bank 0 entry 5 changed")
	}
}

// Eight bitplanes address all 256 colours.
func TestAGAEightPlanes(t *testing.T) {
	// Pixel 0 of each plane: index $C5 = planes 1, 3, 7, 8.
	planes := make([][]byte, 8)
	for p := range planes {
		planes[p] = make([]byte, 64)
		if 0xC5>>p&1 != 0 {
			planes[p][0] = 0x80
		}
	}
	a := agaAmiga(t, 0x0210, planes...) // BPU3: 8 planes
	setColor24(a, 0xC5, 0x123456)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 0); got != 0x123456 {
		t.Errorf("8-plane pixel = $%06X, want colour $C5 ($123456)", got)
	}
}

// HAM8: control bits in planes 1-2, six data bits in planes 3-8.
func TestAGAHAM8(t *testing.T) {
	// Pixel 0: control 00, data 5 -> palette 5. Pixel 1: control 10 (red),
	// data $3F -> red's top six bits all set.
	idx := []int{5 << 2, 0x3F<<2 | 2}
	planes := make([][]byte, 8)
	for p := range planes {
		planes[p] = make([]byte, 64)
		for px, v := range idx {
			if v>>p&1 != 0 {
				planes[p][0] |= 0x80 >> px
			}
		}
	}
	a := agaAmiga(t, 0x0A10, planes...) // HAM, 8 planes
	setColor24(a, 5, 0x011223)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 0); got != 0x011223 {
		t.Errorf("HAM8 base pixel = $%06X, want $011223", got)
	}
	if got := rgbAt24(a, 2); got != 0xFD1223 {
		t.Errorf("HAM8 red modify = $%06X, want $FD1223 (top 6 bits set, low 2 kept)", got)
	}
}

// Dual playfield: playfield 2's colours start at PF2OF; BPLCON4 XORs the
// colour index.
func TestAGADualPlayfieldAndXOR(t *testing.T) {
	plane2 := make([]byte, 64)
	plane2[0] = 0x80                                   // playfield 2 colour 1
	a := agaAmiga(t, 0x2600, make([]byte, 64), plane2) // 2 planes, DPF
	writeCustom16(a, 0x106, 0x1000)                    // PF2OF = 4: offset 16
	setColor24(a, 17, 0xABCDEF)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 0); got != 0xABCDEF {
		t.Errorf("PF2 colour 1 with offset 16 = $%06X, want colour 17", got)
	}
	plane1 := make([]byte, 64)
	plane1[0] = 0x80
	a = agaAmiga(t, 0x1200, plane1)
	writeCustom16(a, 0x10C, 0xFF11) // BPLAM $FF
	setColor24(a, 0xFE, 0x445566)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 0); got != 0x445566 {
		t.Errorf("index 1 XOR $FF = $%06X, want colour $FE", got)
	}
}

// FMODE 3 fetches 64 bits at a time: DDF $38-$B8 is 5 units of 4 words
// per plane in lores, the same 20 words as normal width's $38-$D0.
func TestAGAFMODEFetch(t *testing.T) {
	a := agaAmiga(t, 0x1200, make([]byte, 64))
	writeCustom16(a, 0x1FC, 0x0003)
	writeCustom16(a, 0x094, 0x00B8)
	a.Video.RenderLine(DisplayTop)
	if got := a.Video.bplpt[0]; got != 0x1000+40 {
		t.Errorf("plane pointer advanced to $%X, want $%X (20 words)", got, 0x1000+40)
	}
}

// Sprite colours come from the banks BPLCON4 picks; FMODE makes sprites
// 32 pixels wide.
func TestAGASprites(t *testing.T) {
	a := agaAmiga(t, 0x0200)
	a.Sprites = NewSpriteEngine(a)
	writeCustom16(a, 0x10C, 0x0022) // even and odd sprite banks at 32
	setColor24(a, 33, 0x00FF00)
	setSprite(a, 0, 10, 0x8000, 0, false)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 20); got != 0x00FF00 {
		t.Errorf("sprite pixel = $%06X, want colour 33 (bank 32)", got)
	}
	// 32-pixel-wide sprite: the second word's pixel 0 is the sprite's
	// pixel 16.
	writeCustom16(a, 0x10C, 0x0011)
	writeCustom16(a, 0x1FC, 0x0004) // SPR32
	setColor24(a, 17, 0xFF0000)
	setSprite(a, 0, 10, 0x0000, 0, false)
	a.Sprites.sprites[0].dataX[0] = 0x8000
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt24(a, 2*(10+16)); got != 0xFF0000 {
		t.Errorf("32-wide sprite pixel 16 = $%06X, want colour 17", got)
	}
}

// OCS/ECS ignore all of it: BPU3, the palette banks, FMODE.
func TestNotAGAIgnores(t *testing.T) {
	a := deniseAmiga(t, 0x0210) // BPU3 set, but not AGA: 0 planes
	if n := planesOf(a.Custom.ReadReg16(0x100), a.AGA); n != 0 {
		t.Errorf("BPU3 gave %d planes on OCS", n)
	}
	if a.Video.fetchFactor() != 1 {
		t.Error("FMODE honoured on OCS")
	}
}

// The A1200 Kickstart finds AGA (Alice and Lisa) and boots Workbench.
func TestBootAGA(t *testing.T) {
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 2048)
	if err := a.AttachHardfile(hardfileFromADF(t, "../disks/Install3.2.adf"), false); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start")
	}
	if got := gfxChipRevBits(a) & 0x0F; got != 0x0F {
		t.Errorf("GfxBase ChipRevBits0 = $%02X, want $0F (ECS Agnus+Denise, AA Alice+Lisa)", got)
	}
}

// Words fetched per plane per line with AGA's wide fetches: the unit
// under way at DDFSTOP completes and one more follows, as on the original
// chips. The hires cases are Kickstart 3.2's 640-pixel screen (modulo -8)
// and Worms' title (modulo 0).
func TestFetchWordsFactor(t *testing.T) {
	for _, tc := range []struct {
		hires            bool
		ddfstrt, ddfstop uint16
		factor, want     int
	}{
		{true, 0x38, 0xD8, 4, 44},
		{true, 0x38, 0xC0, 4, 40},
		{false, 0x38, 0xA0, 4, 20},
		{false, 0x38, 0xD0, 1, 20},
		{true, 0x3C, 0xD4, 1, 40},
	} {
		if got := fetchWordsFactor(tc.hires, tc.ddfstrt, tc.ddfstop, tc.factor); got != tc.want {
			t.Errorf("hires %v DDF $%02X-$%02X factor %d: %d words, want %d", tc.hires, tc.ddfstrt, tc.ddfstop, tc.factor, got, tc.want)
		}
	}
}
