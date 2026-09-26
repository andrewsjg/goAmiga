package amiga

import (
	"testing"
)

// newTestAmigaVideo creates a minimal Amiga with chip RAM and video for testing.
func newTestAmigaVideo() *Amiga {
	a := &Amiga{
		ChipRAM: newChipRAM(ChipRAMSize),
	}
	a.Custom = NewCustomChips(nil, a)
	a.Video = NewVideo(a)
	a.Video.DisplayActive = true
	// Enable bitplane DMA: DMAEN (bit 9) + BPLEN (bit 8) + SET (bit 15)
	a.Custom.Write(0x096, []byte{0x83, 0x00})
	// Standard PAL lores screen, as Kickstart sets up: display window
	// $2C81-$2CC1 and data fetch $38-$D0 (20 words per plane per line).
	a.Custom.WriteReg16(0x08E, 0x2C81)
	a.Custom.WriteReg16(0x090, 0x2CC1)
	a.Custom.WriteReg16(0x092, 0x0038)
	a.Custom.WriteReg16(0x094, 0x00D0)
	return a
}

func TestPaletteConversion(t *testing.T) {
	a := newTestAmigaVideo()

	tests := []struct {
		name       string
		color      uint16
		r, g, b, a byte
	}{
		{"red $0F00", 0x0F00, 255, 0, 0, 255},
		{"green $00F0", 0x00F0, 0, 255, 0, 255},
		{"blue $000F", 0x000F, 0, 0, 255, 255},
		{"white $0FFF", 0x0FFF, 255, 255, 255, 255},
		{"black $0000", 0x0000, 0, 0, 0, 255},
		{"gray $0888", 0x0888, 136, 136, 136, 255},
		{"amiga gray $0AAA", 0x0AAA, 170, 170, 170, 255},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Write color to COLOR00
			a.Custom.WriteReg16(0x180, tt.color)
			a.Video.UpdatePalette()

			c := a.Video.palette[0]
			if c[0] != tt.r || c[1] != tt.g || c[2] != tt.b || c[3] != tt.a {
				t.Errorf("COLOR00=$%04X: got RGBA(%d,%d,%d,%d), want (%d,%d,%d,%d)",
					tt.color, c[0], c[1], c[2], c[3], tt.r, tt.g, tt.b, tt.a)
			}
		})
	}
}

func TestRenderLine_NoBitplanes(t *testing.T) {
	a := newTestAmigaVideo()

	// Set BPLCON0 to 0 planes
	a.Custom.WriteReg16(0x100, 0x0000)
	// Set COLOR00 to blue
	a.Custom.WriteReg16(0x180, 0x000F)

	// Render a visible line
	a.Video.RenderLine(DisplayTop)

	// Every pixel should be COLOR00 = blue
	for x := 0; x < LowResWidth; x++ {
		off := x * 4
		if a.Video.Pixels[off] != 0 || a.Video.Pixels[off+1] != 0 ||
			a.Video.Pixels[off+2] != 255 || a.Video.Pixels[off+3] != 255 {
			t.Errorf("pixel %d: got RGBA(%d,%d,%d,%d), want (0,0,255,255)",
				x, a.Video.Pixels[off], a.Video.Pixels[off+1],
				a.Video.Pixels[off+2], a.Video.Pixels[off+3])
			break
		}
	}
}

func TestRenderLine_SingleBitplane(t *testing.T) {
	a := newTestAmigaVideo()

	// Set 1 bitplane in BPLCON0
	a.Custom.WriteReg16(0x100, 0x1000) // 1 plane

	// COLOR00 = black, COLOR01 = white
	a.Custom.WriteReg16(0x180, 0x0000)
	a.Custom.WriteReg16(0x182, 0x0FFF)

	// Write checkerboard pattern ($AA = 10101010) into chip RAM at address 0
	for i := 0; i < 40; i++ {
		a.ChipRAM.Write(i, []byte{0xAA})
	}

	// Set BPL1PT to address 0
	a.Video.bplpt[0] = 0

	a.Video.RenderLine(DisplayTop)

	// Check first 8 pixels: 1,0,1,0,1,0,1,0 ($AA = 10101010). Lores
	// pixel x covers output columns 2x and 2x+1.
	expected := []byte{1, 0, 1, 0, 1, 0, 1, 0}
	for x, exp := range expected {
		off := 2 * x * 4
		var got byte
		if a.Video.Pixels[off] == 255 { // white = color 1
			got = 1
		}
		if got != exp {
			t.Errorf("pixel %d: got color %d, want %d", x, got, exp)
		}
	}
}

func TestRenderLine_TwoBitplanes(t *testing.T) {
	a := newTestAmigaVideo()

	// Set 2 bitplanes in BPLCON0
	a.Custom.WriteReg16(0x100, 0x2000)

	// COLOR00=black, COLOR01=red, COLOR02=green, COLOR03=blue
	a.Custom.WriteReg16(0x180, 0x0000)
	a.Custom.WriteReg16(0x182, 0x0F00)
	a.Custom.WriteReg16(0x184, 0x00F0)
	a.Custom.WriteReg16(0x186, 0x000F)

	// Plane 0: $FF (all 1s for first byte) then $00
	// Plane 1: $F0 (1111 0000) then $00
	// Result: pixels 0-3 = both bits set = color 3 (blue)
	//         pixels 4-7 = plane0 only   = color 1 (red)
	a.ChipRAM.Write(0, []byte{0xFF, 0x00})  // plane 0 data
	a.ChipRAM.Write(40, []byte{0xF0, 0x00}) // plane 1 data (40 bytes per line)

	a.Video.bplpt[0] = 0
	a.Video.bplpt[1] = 40

	a.Video.RenderLine(DisplayTop)

	// Pixel 0: plane0=1, plane1=1 → color 3 (blue)
	off := 0
	if a.Video.Pixels[off+2] != 255 { // blue component
		t.Errorf("pixel 0: expected color 3 (blue), got RGBA(%d,%d,%d,%d)",
			a.Video.Pixels[off], a.Video.Pixels[off+1],
			a.Video.Pixels[off+2], a.Video.Pixels[off+3])
	}

	// Pixel 4 (output column 8): plane0=1, plane1=0 → color 1 (red)
	off = 8 * 4
	if a.Video.Pixels[off] != 255 { // red component
		t.Errorf("pixel 4: expected color 1 (red), got RGBA(%d,%d,%d,%d)",
			a.Video.Pixels[off], a.Video.Pixels[off+1],
			a.Video.Pixels[off+2], a.Video.Pixels[off+3])
	}

	// Pixel 8 (output column 16): plane0=0, plane1=0 → color 0 (black)
	off = 16 * 4
	if a.Video.Pixels[off] != 0 || a.Video.Pixels[off+1] != 0 || a.Video.Pixels[off+2] != 0 {
		t.Errorf("pixel 8: expected color 0 (black), got RGBA(%d,%d,%d,%d)",
			a.Video.Pixels[off], a.Video.Pixels[off+1],
			a.Video.Pixels[off+2], a.Video.Pixels[off+3])
	}
}

func TestBitplanePointerIncrement(t *testing.T) {
	a := newTestAmigaVideo()

	// 1 bitplane, no modulo
	a.Custom.WriteReg16(0x100, 0x1000)
	a.Custom.WriteReg16(0x108, 0x0000) // BPL1MOD = 0
	a.Custom.WriteReg16(0x180, 0x0000)

	// Start at address 0x1000
	a.Video.bplpt[0] = 0x1000

	a.Video.RenderLine(DisplayTop)

	// After rendering, pointer should advance by 40 bytes (20 words = 320 pixels)
	expected := uint32(0x1000 + 40)
	if a.Video.bplpt[0] != expected {
		t.Errorf("bplpt[0] after RenderLine: got $%08X, want $%08X",
			a.Video.bplpt[0], expected)
	}
}

func TestBitplanePointerIncrementWithModulo(t *testing.T) {
	a := newTestAmigaVideo()

	// 1 bitplane with modulo of 8
	a.Custom.WriteReg16(0x100, 0x1000)
	a.Custom.WriteReg16(0x108, 0x0008) // BPL1MOD = 8
	a.Custom.WriteReg16(0x180, 0x0000)

	a.Video.bplpt[0] = 0x1000

	a.Video.RenderLine(DisplayTop)

	// Advance by 40 (bytes per line) + 8 (modulo) = 48
	expected := uint32(0x1000 + 40 + 8)
	if a.Video.bplpt[0] != expected {
		t.Errorf("bplpt[0] after RenderLine with modulo: got $%08X, want $%08X",
			a.Video.bplpt[0], expected)
	}
}

func TestRenderLine_OutsideDisplayWindow(t *testing.T) {
	a := newTestAmigaVideo()

	// Set COLOR00 to red
	a.Custom.WriteReg16(0x180, 0x0F00)

	// Clear framebuffer
	for i := range a.Video.Pixels {
		a.Video.Pixels[i] = 0
	}

	// Render a line above the display window — should not modify pixels
	a.Video.RenderLine(0)

	allZero := true
	for _, b := range a.Video.Pixels {
		if b != 0 {
			allZero = false
			break
		}
	}
	if !allZero {
		t.Error("RenderLine outside display window should not modify pixels")
	}
}

func TestRenderLine_DMADisabled(t *testing.T) {
	a := newTestAmigaVideo()

	// Set 1 bitplane and some non-zero data
	a.Custom.WriteReg16(0x100, 0x1000) // 1 plane
	a.Custom.WriteReg16(0x180, 0x000F) // COLOR00 = blue

	// Write non-zero bitplane data
	for i := 0; i < 40; i++ {
		a.ChipRAM.Write(i, []byte{0xFF})
	}
	a.Video.bplpt[0] = 0

	// DMACON: clear all DMA (BPLEN off, DMAEN off)
	a.Custom.WriteReg16(0x096, 0x0000)

	a.Video.RenderLine(DisplayTop)

	// Every pixel should be COLOR00 = blue (DMA disabled = no bitplane fetch)
	for x := 0; x < LowResWidth; x++ {
		off := x * 4
		if a.Video.Pixels[off] != 0 || a.Video.Pixels[off+1] != 0 ||
			a.Video.Pixels[off+2] != 255 || a.Video.Pixels[off+3] != 255 {
			t.Errorf("pixel %d: got RGBA(%d,%d,%d,%d), want (0,0,255,255) with DMA disabled",
				x, a.Video.Pixels[off], a.Video.Pixels[off+1],
				a.Video.Pixels[off+2], a.Video.Pixels[off+3])
			break
		}
	}
}

func TestVPOSR_V8Bit(t *testing.T) {
	a := newTestAmigaVideo()

	// Set vpos to 260 (> 255, so V8 should be set)
	a.vpos = 260

	buf := make([]byte, 2)
	a.Custom.Read(0x004, buf)
	val := uint16(buf[0])<<8 | uint16(buf[1])

	// V8 is bit 0 of the result (bit 8 of the register = low byte bit 0)
	if val&0x0001 == 0 {
		t.Errorf("VPOSR with vpos=260: got $%04X, want V8 bit set (bit 0)", val)
	}

	// Set vpos to 100 (< 256, so V8 should be clear)
	a.vpos = 100
	a.Custom.Read(0x004, buf)
	val = uint16(buf[0])<<8 | uint16(buf[1])

	if val&0x0001 != 0 {
		t.Errorf("VPOSR with vpos=100: got $%04X, want V8 bit clear", val)
	}
}

func TestVPOSR_LOF(t *testing.T) {
	a := newTestAmigaVideo()

	// LOF starts as false
	a.lof = false
	buf := make([]byte, 2)
	a.Custom.Read(0x004, buf)
	val := uint16(buf[0])<<8 | uint16(buf[1])
	if val&0x8000 != 0 {
		t.Errorf("VPOSR with lof=false: got $%04X, want LOF bit clear", val)
	}

	// Toggle LOF
	a.lof = true
	a.Custom.Read(0x004, buf)
	val = uint16(buf[0])<<8 | uint16(buf[1])
	if val&0x8000 == 0 {
		t.Errorf("VPOSR with lof=true: got $%04X, want LOF bit set (bit 15)", val)
	}
}

// Agnus fetches in 8-colour-clock units, finishing the unit that reaches
// DDFSTOP plus one more; a unit is one word per plane in lores, two in
// hires.
func TestFetchWords(t *testing.T) {
	for _, c := range []struct {
		hires       bool
		start, stop uint16
		want        int
	}{
		{false, 0x38, 0xD0, 20}, // standard lores
		{false, 0x30, 0xD0, 21}, // lores with one word for scrolling
		{true, 0x3C, 0xD4, 40},  // standard hires (HRM)
		{true, 0x3C, 0xD0, 40},  // Kickstart 1.3 Workbench
		{true, 0x38, 0xD8, 42},  // Kickstart 3.x Workbench (with BPLxMOD -4)
		{true, 0x18, 0xD8, 50},  // widest overscan
	} {
		if got := fetchWords(c.hires, c.start, c.stop); got != c.want {
			t.Errorf("hires=%v DDF $%02X/$%02X: %d words, want %d", c.hires, c.start, c.stop, got, c.want)
		}
	}
}

// A 42-word hires line advances the pointers by 84 bytes plus the modulo
// (the fetch used to be capped at 40 words, shearing KS3.x's Workbench).
func TestWideFetchAdvancesPointers(t *testing.T) {
	a := newTestAmigaVideo()
	a.Custom.WriteReg16(0x100, 0xA200) // hires, 2 planes
	a.Custom.WriteReg16(0x092, 0x0038)
	a.Custom.WriteReg16(0x094, 0x00D8)
	a.Custom.WriteReg16(0x108, 0xFFFC) // -4
	a.Custom.WriteReg16(0x10A, 0xFFFC)
	a.Video.bplpt[0], a.Video.bplpt[1] = 0x1000, 0x8000
	a.Video.RenderLine(DisplayTop)
	if a.Video.bplpt[0] != 0x1000+80 || a.Video.bplpt[1] != 0x8000+80 {
		t.Errorf("pointers $%X, $%X after one line; want +80 (84 fetched - 4)", a.Video.bplpt[0], a.Video.bplpt[1])
	}
}

// OCS Denise has no DENISEID; the read floats high. Anything that looks
// like an ECS ($FC) or AGA ($F8) ID makes Kickstart 3.x use those modes.
func TestDENISEIDReadsAsOCS(t *testing.T) {
	a := newTestAmigaVideo()
	b := make([]byte, 2)
	a.Custom.Read(0x07C, b)
	if b[0] != 0xFF || b[1] != 0xFF {
		t.Errorf("DENISEID = $%02X%02X, want $FFFF", b[0], b[1])
	}
}
