package amiga

// Video handles Amiga bitplane rendering, converting planar data from
// chip RAM into an RGBA pixel framebuffer each frame.
type Video struct {
	amiga *Amiga

	// Pixels is the RGBA framebuffer (Width * Height * 4 bytes).
	Pixels []byte
	Width  int
	Height int

	// palette holds pre-converted RGBA colors (32 entries).
	palette [32][4]byte

	// bplpt holds working bitplane pointers, auto-incremented per line.
	bplpt [6]uint32

	buf [2]byte // scratch for chip RAM reads

	// Pre-allocated plane data buffers to avoid per-line allocations.
	// planeSlice holds the slice of plane buffers, planeBuf holds the raw data.
	planeBuf [6][80]byte // 80 bytes = 40 words max (hires)

	// DisplayActive controls whether RenderLine actually renders pixels.
	// Set to true when a display window is open. When false, rendering is
	// skipped for performance (headless/test mode).
	DisplayActive bool
}

const (
	PALVisibleLines = 256
	DisplayTop      = 44
	DisplayBottom   = 300

	LowResWidth = 320
	HiResWidth  = 640
)

// NewVideo allocates a Video subsystem for the given Amiga.
func NewVideo(a *Amiga) *Video {
	v := &Video{
		amiga:  a,
		Width:  LowResWidth,
		Height: PALVisibleLines,
		Pixels: make([]byte, LowResWidth*PALVisibleLines*4),
	}
	return v
}

// UpdatePalette reads COLOR00-31 from custom registers and converts
// 12-bit $0RGB to 8-bit RGBA.
func (v *Video) UpdatePalette() {
	for i := 0; i < 32; i++ {
		rgb := v.amiga.Custom.ReadReg16(0x180 + i*2)
		r := (rgb >> 8) & 0xF
		g := (rgb >> 4) & 0xF
		b := rgb & 0xF
		v.palette[i] = [4]byte{
			byte(r * 17), // 0x0→0, 0xF→255
			byte(g * 17),
			byte(b * 17),
			0xFF,
		}
	}
}

// SnapshotBitplanePointers reads BPL1PT-BPL6PT from custom registers.
// Each pointer is 32-bit split across two 16-bit registers (high + low word).
func (v *Video) SnapshotBitplanePointers() {
	for i := 0; i < 6; i++ {
		hi := v.amiga.Custom.ReadReg16(0x0E0 + i*4)
		lo := v.amiga.Custom.ReadReg16(0x0E2 + i*4)
		v.bplpt[i] = uint32(hi)<<16 | uint32(lo)
	}
}

// GetBplPt returns the current working bitplane pointer for plane p (0-5).
func (v *Video) GetBplPt(p int) uint32 {
	return v.bplpt[p]
}

// StartFrame is called at VBLANK to snapshot bitplane pointers for the new frame.
func (v *Video) StartFrame() {
	v.SnapshotBitplanePointers()
}

// RenderLine renders a single scanline to the Pixels framebuffer.
// vpos is the absolute vertical position (0-311).
func (v *Video) RenderLine(vpos int) {
	if vpos < DisplayTop || vpos >= DisplayBottom {
		return
	}

	bplcon0 := v.amiga.Custom.ReadReg16(0x100)
	numPlanes := int((bplcon0 >> 12) & 0x7)
	hires := bplcon0&0x8000 != 0

	// Check bitplane DMA: requires both DMAEN (bit 9) and BPLEN (bit 8)
	dmacon := v.amiga.Custom.ReadReg16(0x096)
	if dmacon&0x0300 != 0x0300 || numPlanes == 0 {
		if v.DisplayActive {
			v.UpdatePalette()
			// Fill entire line with COLOR00
			screenY := vpos - DisplayTop
			if screenY >= 0 && screenY < v.Height {
				c := v.palette[0]
				lineOffset := screenY * v.Width * 4
				for x := 0; x < v.Width; x++ {
					pixOff := lineOffset + x*4
					if pixOff+3 < len(v.Pixels) {
						v.Pixels[pixOff] = c[0]
						v.Pixels[pixOff+1] = c[1]
						v.Pixels[pixOff+2] = c[2]
						v.Pixels[pixOff+3] = c[3]
					}
				}
			}
		}
		return
	}

	// Determine pixel width and words per line
	width := LowResWidth
	if hires {
		width = HiResWidth
	}

	wordsPerLine := 20
	if hires {
		wordsPerLine = 40
	}

	ddfstrt := v.amiga.Custom.ReadReg16(0x092)
	ddfstop := v.amiga.Custom.ReadReg16(0x094)
	if ddfstrt != 0 && ddfstop != 0 && ddfstop > ddfstrt {
		// HRM: lores  DDFSTRT = DDFSTOP - 8*(words-1)  → words = (STOP-STRT)/8 + 1
		//      hires DDFSTRT = DDFSTOP - 4*(words-2)  → words = (STOP-STRT)/4 + 2
		if hires {
			wordsPerLine = int((ddfstop-ddfstrt)/4) + 2
			// Standard full-width hires copper (e.g. KS1.3 WB DDF $3C/$D0) is
			// documented as 39 by the pure HRM identity, but Denise/fetch and
			// graphics.library treat it as 40 words (80 bytes) with BPLMOD=0
			// matching BitMap BytesPerRow=80. Using 39 shears the framebuffer
			// by 2 bytes per line against drawn bitmaps.
			if wordsPerLine == 39 {
				wordsPerLine = 40
			}
		} else {
			wordsPerLine = int((ddfstop-ddfstrt)/8) + 1
		}
	}

	bytesPerLine := wordsPerLine * 2
	if bytesPerLine > 80 {
		bytesPerLine = 80 // cap to pre-allocated buffer size
	}

	// Always advance bitplane pointers (DMA fetch happens regardless of display)
	defer func() {
		bpl1mod := int16(v.amiga.Custom.ReadReg16(0x108))
		bpl2mod := int16(v.amiga.Custom.ReadReg16(0x10A))
		for p := 0; p < numPlanes; p++ {
			v.bplpt[p] += uint32(bytesPerLine)
			if p%2 == 0 {
				v.bplpt[p] = uint32(int32(v.bplpt[p]) + int32(bpl1mod))
			} else {
				v.bplpt[p] = uint32(int32(v.bplpt[p]) + int32(bpl2mod))
			}
		}
	}()

	if !v.DisplayActive {
		return // skip pixel rendering in headless mode
	}

	v.UpdatePalette()

	pixelsPerLine := wordsPerLine * 16
	if pixelsPerLine > width {
		pixelsPerLine = width
	}

	screenY := vpos - DisplayTop
	if screenY < 0 || screenY >= v.Height {
		return
	}

	if width != v.Width {
		v.Width = width
		v.Pixels = make([]byte, v.Width*v.Height*4)
	}

	// Fetch bitplane data using pre-allocated buffers
	for p := 0; p < numPlanes; p++ {
		for w := 0; w < bytesPerLine; w += 2 {
			addr := v.bplpt[p] + uint32(w)
			v.amiga.ChipRAM.Read(int(addr&0x7FFFF), v.buf[:2])
			v.planeBuf[p][w] = v.buf[0]
			v.planeBuf[p][w+1] = v.buf[1]
		}
	}

	// Planar-to-chunky conversion
	lineOffset := screenY * v.Width * 4
	for x := 0; x < pixelsPerLine; x++ {
		byteIdx := x / 8
		bitIdx := uint(7 - (x % 8))

		var colorIdx byte
		for p := 0; p < numPlanes; p++ {
			if v.planeBuf[p][byteIdx]&(1<<bitIdx) != 0 {
				colorIdx |= 1 << uint(p)
			}
		}

		pixOff := lineOffset + x*4
		if pixOff+3 < len(v.Pixels) {
			c := v.palette[colorIdx&0x1F]
			v.Pixels[pixOff] = c[0]
			v.Pixels[pixOff+1] = c[1]
			v.Pixels[pixOff+2] = c[2]
			v.Pixels[pixOff+3] = c[3]
		}
	}

	// Fill remaining pixels with COLOR00
	if pixelsPerLine < v.Width {
		c := v.palette[0]
		for x := pixelsPerLine; x < v.Width; x++ {
			pixOff := lineOffset + x*4
			if pixOff+3 < len(v.Pixels) {
				v.Pixels[pixOff] = c[0]
				v.Pixels[pixOff+1] = c[1]
				v.Pixels[pixOff+2] = c[2]
				v.Pixels[pixOff+3] = c[3]
			}
		}
	}

	// Overlay sprites
	if v.amiga.Sprites != nil {
		v.amiga.Sprites.RenderOnLine(v.Pixels, lineOffset, v.Width)
	}
}
