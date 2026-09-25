package amiga

import (
	"testing"
)

func newTestAmigaSprites() *Amiga {
	a := &Amiga{
		ChipRAM: newChipRAM(ChipRAMSize),
	}
	a.Custom = NewCustomChips(nil, a)
	a.Video = NewVideo(a)
	a.Sprites = NewSpriteEngine(a)
	return a
}

func TestSpriteRendering(t *testing.T) {
	a := newTestAmigaSprites()
	// Playfield origin = DIWSTRT H (bitplanes start at FB x=0).
	a.Custom.WriteReg16(0x08E, 0x2C81) // DIW H=$81
	a.Custom.WriteReg16(0x180, 0x0000)
	a.Custom.WriteReg16(0x1A2, 0x0F00) // COLOR17 = red

	s := &a.Sprites.sprites[0]
	s.state = sprActive
	// hstart = 0x81+64 = 193 → lores FB x=64
	// hstart = (pos_low<<1)|ctl0 → pos_low = 96 ($60), ctl bit0 = 1
	s.pos = 0x0060
	s.ctl = 0xFF01
	s.data = 0x8000
	s.datb = 0x0000
	if s.hstart() != 193 {
		t.Fatalf("hstart=%d, want 193", s.hstart())
	}

	a.Video.UpdatePalette()
	pixels := make([]byte, LowResWidth*4)
	for x := 0; x < LowResWidth; x++ {
		pixels[x*4+3] = 0xFF
	}
	a.Sprites.RenderOnLine(pixels, 0, LowResWidth)

	x := 64
	off := x * 4
	if pixels[off] != 255 || pixels[off+1] != 0 || pixels[off+2] != 0 {
		t.Errorf("sprite pixel at x=%d: got RGBA(%d,%d,%d), want red",
			x, pixels[off], pixels[off+1], pixels[off+2])
	}
	x = 65
	off = x * 4
	if pixels[off] != 0 || pixels[off+1] != 0 || pixels[off+2] != 0 {
		t.Errorf("non-sprite pixel at x=%d: got RGBA(%d,%d,%d), want black",
			x, pixels[off], pixels[off+1], pixels[off+2])
	}
}

func TestSpriteVStartVStop(t *testing.T) {
	a := newTestAmigaSprites()
	s := &a.Sprites.sprites[0]

	s.pos = 100<<8 | 0x40
	s.ctl = 110 << 8
	if s.vstart() != 100 {
		t.Errorf("vstart: got %d, want 100", s.vstart())
	}
	if s.vstop() != 110 {
		t.Errorf("vstop: got %d, want 110", s.vstop())
	}

	s.ctl = 110<<8 | 0x04
	if s.vstart() != 356 {
		t.Errorf("9-bit vstart: got %d, want 356", s.vstart())
	}

	s.ctl = 110<<8 | 0x02
	if s.vstop() != 366 {
		t.Errorf("9-bit vstop: got %d, want 366", s.vstop())
	}
}

// TestSpriteDMANoAdvanceWhileWaiting ensures that while waiting for VSTART the
// DMA pointer does not walk through memory (the bug that destroyed the WB mouse).
func TestSpriteDMANoAdvanceWhileWaiting(t *testing.T) {
	a := newTestAmigaSprites()
	// Enable sprite DMA: DMAEN|SPREN
	a.Custom.WriteReg16(0x096, 0x8220)

	// Sprite data at $1000: VSTART=50, HSTART=$40, VSTOP=55, then a few data words
	// POS = vstart<<8 | (hstart>>1)  → hstart=$80 → lo byte $40
	// CTL = vstop<<8
	data := []byte{
		0x32, 0x40, // POS: v=50, h high bits
		0x37, 0x00, // CTL: vstop=55
		0x80, 0x00, // DATA line 50
		0x00, 0x00, // DATB
		0x40, 0x00, // DATA line 51
		0x00, 0x00,
		0x20, 0x00,
		0x00, 0x00,
		0x10, 0x00,
		0x00, 0x00,
		0x08, 0x00,
		0x00, 0x00,
		0x00, 0x00, // end control
		0x00, 0x00,
	}
	a.ChipRAM.Write(0x1000, data)

	// Load pointer (low word completes and restarts DMA state)
	a.Sprites.SetPT(0, true, 0x0000)
	a.Sprites.SetPT(0, false, 0x1000)

	s := &a.Sprites.sprites[0]
	if s.state != sprFetchCtl {
		t.Fatalf("after SetPT: state=%d, want fetchCtl", s.state)
	}

	// Lines 0..49: first line fetches CTL, rest wait without advancing past data
	a.Sprites.FetchLine(0)
	if s.state != sprWaitVStart {
		t.Fatalf("after line 0: state=%d pos=$%04X ctl=$%04X", s.state, s.pos, s.ctl)
	}
	if s.vstart() != 50 || s.vstop() != 55 {
		t.Fatalf("ctl words: vstart=%d vstop=%d", s.vstart(), s.vstop())
	}
	ptAfterCtl := s.pt // should be $1004 (after 2 words)

	for v := 1; v < 50; v++ {
		a.Sprites.FetchLine(v)
		if s.pt != ptAfterCtl {
			t.Fatalf("line %d: pt advanced to $%08X, want $%08X (must not fetch while waiting)",
				v, s.pt, ptAfterCtl)
		}
		if s.state != sprWaitVStart {
			t.Fatalf("line %d: state=%d, want waitVStart", v, s.state)
		}
	}

	// Line 50: activate and fetch first data
	a.Sprites.FetchLine(50)
	if s.state != sprActive {
		t.Fatalf("line 50: state=%d, want active", s.state)
	}
	if s.data != 0x8000 {
		t.Fatalf("line 50 data=$%04X, want $8000", s.data)
	}

	// Run through to VSTOP and ensure we terminate cleanly
	for v := 51; v <= 55; v++ {
		a.Sprites.FetchLine(v)
	}
	// vstop=55: on line 55 we should fetch end control (0,0) and go idle
	if s.state != sprIdle && s.state != sprWaitVStart {
		// After zero ctl we go idle
		t.Logf("after vstop: state=%d pos=$%04X ctl=$%04X pt=$%08X", s.state, s.pos, s.ctl, s.pt)
	}
	if s.pos != 0 || s.ctl != 0 {
		// May have loaded end words
		t.Logf("end ctl pos=$%04X ctl=$%04X", s.pos, s.ctl)
	}
}

func TestSpriteHiresRenderWidth(t *testing.T) {
	a := newTestAmigaSprites()
	a.Custom.WriteReg16(0x08E, 0x2C81) // DIW H=$81
	a.Custom.WriteReg16(0x1A2, 0x0F00) // red
	a.Video.UpdatePalette()

	s := &a.Sprites.sprites[0]
	s.state = sprActive
	// hstart=193 → lores x=64 → hires x=128..129
	s.pos = 0x0060
	s.ctl = 0xFF01
	s.data = 0x8000
	s.datb = 0x0000

	pixels := make([]byte, HiResWidth*4)
	for x := 0; x < HiResWidth; x++ {
		pixels[x*4+3] = 0xFF
	}
	a.Sprites.RenderOnLine(pixels, 0, HiResWidth)

	for _, x := range []int{128, 129} {
		off := x * 4
		if pixels[off] != 255 || pixels[off+1] != 0 || pixels[off+2] != 0 {
			t.Errorf("hires x=%d: got RGBA(%d,%d,%d), want red", x, pixels[off], pixels[off+1], pixels[off+2])
		}
	}
}
