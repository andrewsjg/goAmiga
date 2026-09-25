package amiga

import "testing"

// deniseAmiga returns a standard lores screen (see newTestAmigaVideo) with
// sprites, BPLCON0 set, and plane p's data at $1000+$100*p.
func deniseAmiga(t *testing.T, bplcon0 uint16, planes ...[]byte) *Amiga {
	t.Helper()
	a := newTestAmigaVideo()
	a.Sprites = NewSpriteEngine(a)
	a.Custom.WriteReg16(0x100, bplcon0)
	for p, data := range planes {
		a.ChipRAM.Write(0x1000+0x100*p, data)
		a.Video.bplpt[p] = uint32(0x1000 + 0x100*p)
	}
	return a
}

// rgbAt returns output column x of the first display line as 12-bit $RGB.
func rgbAt(a *Amiga, x int) uint16 {
	o := x * 4
	p := a.Video.Pixels
	return uint16(p[o]/17)<<8 | uint16(p[o+1]/17)<<4 | uint16(p[o+2]/17)
}

func setColors(a *Amiga, colors ...uint16) {
	for i, c := range colors {
		a.Custom.WriteReg16(0x180+2*i, c)
	}
}

func TestDIWClipsHorizontally(t *testing.T) {
	a := deniseAmiga(t, 0x1200, []byte{0xFF, 0xFF, 0xFF})
	setColors(a, 0x000, 0xF00)
	a.Custom.WriteReg16(0x08E, 0x2C91) // window starts 16 lores pixels in
	a.Custom.WriteReg16(0x090, 0x2CA1) // and stops at H $1A1
	a.Video.RenderLine(DisplayTop)
	for _, c := range []struct {
		x    int
		want uint16
	}{{0, 0x000}, {31, 0x000}, {32, 0xF00}, {47, 0xF00}, {48, 0x000}} {
		if got := rgbAt(a, c.x); got != c.want {
			t.Errorf("column %d = $%03X, want $%03X", c.x, got, c.want)
		}
	}
	// Columns past DIWSTOP ($1A1 = output column 2*($1A1-$81) = 576).
	a.ChipRAM.Write(0x1000, make([]byte, 40))
	for i := 0; i < 40; i++ {
		a.ChipRAM.Write(0x1000+i, []byte{0xFF})
	}
	a.Video.bplpt[0] = 0x1000
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 575); got != 0xF00 {
		t.Errorf("last column inside window = $%03X, want $F00", got)
	}
	if got := rgbAt(a, 576); got != 0x000 {
		t.Errorf("first column past DIWSTOP = $%03X, want $000", got)
	}
}

// DDFSTRT places the data: $30 starts fetching 8 colour clocks (16 lores
// pixels) earlier than the standard $38.
func TestDDFStartPositionsPlayfield(t *testing.T) {
	a := deniseAmiga(t, 0x1200, []byte{0x80, 0x00, 0x80, 0x00})
	setColors(a, 0x000, 0xFFF)
	a.Custom.WriteReg16(0x092, 0x0030)
	a.Custom.WriteReg16(0x08E, 0x2C71) // open the window to the left too
	a.Video.RenderLine(DisplayTop)
	// Word 0's first pixel is at H $71, left of the output; word 1's first
	// pixel (lores 16 after it) lands at H $81 = column 0.
	if got := rgbAt(a, 0); got != 0xFFF {
		t.Errorf("column 0 = $%03X, want word 1 pixel 0 ($FFF)", got)
	}
	if got := rgbAt(a, 2); got != 0x000 {
		t.Errorf("column 2 = $%03X, want $000", got)
	}
}

func TestHiresPixelsAreOneColumn(t *testing.T) {
	a := deniseAmiga(t, 0x9200, []byte{0xA0})
	setColors(a, 0x000, 0xFFF)
	a.Custom.WriteReg16(0x092, 0x003C)
	a.Custom.WriteReg16(0x094, 0x00D4)
	a.Video.RenderLine(DisplayTop)
	for x, want := range []uint16{0xFFF, 0x000, 0xFFF, 0x000} {
		if got := rgbAt(a, x); got != want {
			t.Errorf("hires column %d = $%03X, want $%03X", x, got, want)
		}
	}
}

// BPLCON1 PF1H delays odd planes, PF2H even planes, in lores pixels.
func TestBPLCON1Scroll(t *testing.T) {
	a := deniseAmiga(t, 0x2200, []byte{0x80}, []byte{0x80})
	setColors(a, 0x000, 0xF00, 0x0F0, 0x00F)
	a.Custom.WriteReg16(0x102, 0x0053) // PF1H=3, PF2H=5
	a.Video.RenderLine(DisplayTop)
	for _, c := range []struct {
		x    int
		want uint16
	}{{0, 0x000}, {6, 0xF00}, {7, 0xF00}, {8, 0x000}, {10, 0x0F0}, {12, 0x000}} {
		if got := rgbAt(a, c.x); got != c.want {
			t.Errorf("column %d = $%03X, want $%03X", c.x, got, c.want)
		}
	}
}

func TestDualPlayfield(t *testing.T) {
	// PF1 = planes 1,3 (odd), PF2 = planes 2,4 (even). Pixel 0: both
	// playfields set; pixel 1: PF2 only; pixel 2: PF1 only.
	a := deniseAmiga(t, 0x4600, // 4 planes, DBLPF, COLOR
		[]byte{0xA0}, []byte{0xC0}, []byte{0x00}, []byte{0x00})
	setColors(a, 0x000, 0xF00, 0, 0, 0, 0, 0, 0, 0x000, 0x0F0)
	a.Video.RenderLine(DisplayTop)
	check := func(label string, want ...uint16) {
		for i, w := range want {
			if got := rgbAt(a, 2*i); got != w {
				t.Errorf("%s: pixel %d = $%03X, want $%03X", label, i, got, w)
			}
		}
	}
	check("PF1 in front", 0xF00, 0x0F0, 0xF00, 0x000)

	a.Custom.WriteReg16(0x104, 0x0040) // PF2PRI
	for p := 0; p < 4; p++ {
		a.Video.bplpt[p] = uint32(0x1000 + 0x100*p)
	}
	a.Video.RenderLine(DisplayTop)
	check("PF2 in front", 0x0F0, 0x0F0, 0xF00, 0x000)
}

func TestExtraHalfBrite(t *testing.T) {
	// Pixel 0 = colour 1, pixel 1 = colour 33 (half-bright colour 1).
	a := deniseAmiga(t, 0x6200,
		[]byte{0xC0}, []byte{0}, []byte{0}, []byte{0}, []byte{0}, []byte{0x40})
	setColors(a, 0x000, 0xE8A)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 0); got != 0xE8A {
		t.Errorf("colour 1 = $%03X, want $E8A", got)
	}
	if got := rgbAt(a, 2); got != 0x745 {
		t.Errorf("colour 33 = $%03X, want half-bright $745", got)
	}
}

func TestHoldAndModify(t *testing.T) {
	// Pixels: set COLOR02, modify blue to $F, modify red to $3, modify green
	// to $9, hold nothing (set COLOR00).
	// Pixel values (bits 5..0): %000010, %01_1111, %10_0011, %11_1001, 0.
	px := []int{0x02, 0x1F, 0x23, 0x39, 0x00}
	planes := make([][]byte, 6)
	for p := range planes {
		planes[p] = make([]byte, 2)
		for i, v := range px {
			if v>>p&1 != 0 {
				planes[p][0] |= 0x80 >> i
			}
		}
	}
	a := deniseAmiga(t, 0x6A00, planes...) // 6 planes, HAM
	setColors(a, 0x123, 0x000, 0x480)
	a.Video.RenderLine(DisplayTop)
	for i, want := range []uint16{0x480, 0x48F, 0x38F, 0x39F, 0x123} {
		if got := rgbAt(a, 2*i); got != want {
			t.Errorf("HAM pixel %d = $%03X, want $%03X", i, got, want)
		}
	}
}

// setSprite puts sprite n on the first display line at lores output pixel x.
func setSprite(a *Amiga, n, x int, data, datb uint16, attach bool) {
	s := &a.Sprites.sprites[n]
	h := OutputLeft + x
	s.state = sprActive
	s.pos = uint16(h >> 1)
	s.ctl = uint16(h & 1)
	if attach {
		s.ctl |= 0x80
	}
	s.data, s.datb = data, datb
}

func TestSpritePlayfieldPriority(t *testing.T) {
	a := deniseAmiga(t, 0x1200, []byte{0xFF})
	setColors(a, 0x000, 0xFFF)
	a.Custom.WriteReg16(0x1A2, 0xF00)     // COLOR17: sprite 0/1 colour 1
	a.Custom.WriteReg16(0x1AA, 0x0F0)     // COLOR21: sprite 2/3 colour 1
	setSprite(a, 0, 4, 0xFFFF, 0, false)  // pair 0: px 4-19 (4-7 over the playfield)
	setSprite(a, 2, 24, 0xFFFF, 0, false) // pair 1: px 24-39, transparent playfield

	render := func(bplcon2 uint16) {
		a.Custom.WriteReg16(0x104, bplcon2)
		a.Video.bplpt[0] = 0x1000
		a.Video.RenderLine(DisplayTop)
	}
	// Single playfield, all bits in plane 1 (odd = PF1). PF1P=1: pair 0
	// in front of the playfield, pair 1 behind it.
	render(0x0001)
	if got := rgbAt(a, 2*4); got != 0xF00 {
		t.Errorf("pair 0 with PF1P=1 = $%03X, want sprite $F00", got)
	}
	if got := rgbAt(a, 2*24); got != 0x0F0 {
		t.Errorf("pair 1 over transparent playfield = $%03X, want sprite $0F0", got)
	}
	setSprite(a, 2, 0, 0x8000, 0, false) // pair 1 at px 0, over the playfield
	render(0x0001)
	if got := rgbAt(a, 0); got != 0xFFF {
		t.Errorf("pair 1 with PF1P=1 = $%03X, want playfield $FFF", got)
	}
	render(0x0000) // playfield in front of all sprites
	if got := rgbAt(a, 2*4); got != 0xFFF {
		t.Errorf("pair 0 with PF1P=0 = $%03X, want playfield $FFF", got)
	}
	render(0x0024) // Workbench: sprites in front
	if got := rgbAt(a, 2*4); got != 0xF00 {
		t.Errorf("pair 0 with BPLCON2 $24 = $%03X, want sprite $F00", got)
	}
}

func TestSpriteNumberPriorityAndAttach(t *testing.T) {
	a := deniseAmiga(t, 0x0200)
	for i := 16; i < 32; i++ {
		a.Custom.WriteReg16(0x180+2*i, uint16(i)) // COLORn = n, to identify
	}
	setSprite(a, 0, 0, 0x8000, 0, false) // colour 1 of pair 0 → COLOR17
	setSprite(a, 1, 0, 0x8000, 0x8000, false)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 0); got != 17 {
		t.Errorf("sprite 0 over sprite 1 = COLOR%d, want COLOR17", got)
	}

	// Attached: even sprite supplies bits 0-1, odd sprite bits 2-3.
	setSprite(a, 1, 0, 0x8000, 0x8000, true)  // bits 2,3 → 12
	setSprite(a, 0, 0, 0x0000, 0x8000, false) // bit 1 → 2
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 0); got != 16+14 {
		t.Errorf("attached sprite = COLOR%d, want COLOR30", got)
	}
}

// Bitplane DMA follows the vertical display window.
func TestVerticalWindowGatesFetch(t *testing.T) {
	a := deniseAmiga(t, 0x1200, []byte{0xFF})
	setColors(a, 0x00F, 0xFFF)
	a.Custom.WriteReg16(0x08E, 0x3081) // window starts at line $30
	a.Video.RenderLine(DisplayTop)     // line $2C: above the window
	if a.Video.bplpt[0] != 0x1000 {
		t.Errorf("pointer advanced above the window: $%X", a.Video.bplpt[0])
	}
	if got := rgbAt(a, 0); got != 0x00F {
		t.Errorf("line above window = $%03X, want COLOR00 $00F", got)
	}
	a.Video.RenderLine(0x30)
	if a.Video.bplpt[0] != 0x1000+40 {
		t.Errorf("pointer after first window line = $%X, want $1028", a.Video.bplpt[0])
	}
}
