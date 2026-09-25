package amiga

import "image"

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

	// planeBuf holds the current line's bitplane data.
	planeBuf [6][80]byte // 80 bytes = 40 words max (hires)

	// sprCol and sprPair hold the current line's sprite pixels: palette
	// index (16-31, 0 = none) and sprite pair (0-3) per output column.
	sprCol, sprPair [OutputWidth]byte

	// DisplayActive controls whether RenderLine actually renders pixels.
	// Set to true when a display window is open. When false, rendering is
	// skipped for performance (headless/test mode).
	DisplayActive bool
}

const (
	PALVisibleLines = 256
	LowResWidth     = 320
	HiResWidth      = 640

	// The framebuffer shows a fixed part of the beam: lines DisplayTop to
	// DisplayBottom and, horizontally, OutputWidth hires pixels starting at
	// lores beam position OutputLeft. That is exactly the standard PAL
	// display window (DIWSTRT $2C81, DIWSTOP $2CC1); screens are placed in
	// it by their own DIW/DDF values, so overscan beyond it is cropped.
	DisplayTop    = 44
	DisplayBottom = DisplayTop + PALVisibleLines
	OutputLeft    = 0x81
	OutputWidth   = HiResWidth
)

// NewVideo allocates a Video subsystem for the given Amiga.
func NewVideo(a *Amiga) *Video {
	v := &Video{
		amiga:  a,
		Width:  OutputWidth,
		Height: PALVisibleLines,
		Pixels: make([]byte, OutputWidth*PALVisibleLines*4),
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

// fetchWords is the number of words fetched per plane per line for the
// given DDFSTRT/DDFSTOP.
func fetchWords(hires bool, ddfstrt, ddfstop uint16) int {
	if ddfstrt == 0 || ddfstop == 0 || ddfstop <= ddfstrt {
		if hires {
			return 40
		}
		return 20
	}
	// HRM: lores  DDFSTRT = DDFSTOP - 8*(words-1)  → words = (STOP-STRT)/8 + 1
	//      hires DDFSTRT = DDFSTOP - 4*(words-2)  → words = (STOP-STRT)/4 + 2
	if !hires {
		return int((ddfstop-ddfstrt)/8) + 1
	}
	words := int((ddfstop-ddfstrt)/4) + 2
	// Standard full-width hires copper (e.g. KS1.3 WB DDF $3C/$D0) is
	// documented as 39 by the pure HRM identity, but Denise/fetch and
	// graphics.library treat it as 40 words (80 bytes) with BPLMOD=0
	// matching BitMap BytesPerRow=80. Using 39 shears the framebuffer
	// by 2 bytes per line against drawn bitmaps.
	if words == 39 {
		words = 40
	}
	return words
}

// displayWindow is the DIWSTRT/DIWSTOP window in beam coordinates: lores
// pixels horizontally, lines vertically. Both are [start, stop).
type displayWindow struct {
	hstart, hstop, vstart, vstop int
}

// window decodes DIWSTRT/DIWSTOP. The start is H/V bits 7-0 (H8=V8=0); the
// stop has H8=1 and V8 = NOT V7 (HRM), so $2CC1 means H $1C1, V $12C.
func (v *Video) window() displayWindow {
	strt := v.amiga.Custom.ReadReg16(0x08E)
	stop := v.amiga.Custom.ReadReg16(0x090)
	w := displayWindow{
		hstart: int(strt & 0xFF),
		vstart: int(strt >> 8),
		hstop:  int(stop&0xFF) | 0x100,
		vstop:  int(stop >> 8),
	}
	if w.vstop&0x80 == 0 {
		w.vstop |= 0x100
	}
	return w
}

// bitplaneFetchLine reports whether Agnus fetches bitplanes on line vpos:
// bitplane DMA on, at least one plane, and inside the vertical window.
func (v *Video) bitplaneFetchLine(vpos int) bool {
	if v.amiga.Custom.ReadReg16(0x096)&0x0300 != 0x0300 {
		return false
	}
	if v.amiga.Custom.ReadReg16(0x100)>>12&7 == 0 {
		return false
	}
	w := v.window()
	return vpos >= w.vstart && vpos < w.vstop
}

// RenderLine fetches the bitplane data for scanline vpos (advancing the
// bitplane pointers as the DMA does) and, when the display is active and
// the line is in the output window, draws it into Pixels.
func (v *Video) RenderLine(vpos int) {
	bplcon0 := v.amiga.Custom.ReadReg16(0x100)
	numPlanes := int((bplcon0 >> 12) & 0x7)
	if numPlanes > 6 {
		numPlanes = 6
	}
	hires := bplcon0&0x8000 != 0
	ddfstrt := v.amiga.Custom.ReadReg16(0x092)
	words := fetchWords(hires, ddfstrt, v.amiga.Custom.ReadReg16(0x094))
	if words > 40 {
		words = 40 // cap to the pre-allocated buffer
	}

	fetch := v.bitplaneFetchLine(vpos)
	draw := v.DisplayActive && vpos >= DisplayTop && vpos < DisplayBottom
	if fetch && draw {
		for p := 0; p < numPlanes; p++ {
			v.amiga.ChipRAM.Read(int(v.bplpt[p]&0x7FFFF), v.planeBuf[p][:2*words])
		}
	}
	if fetch {
		bpl1mod := int32(int16(v.amiga.Custom.ReadReg16(0x108)))
		bpl2mod := int32(int16(v.amiga.Custom.ReadReg16(0x10A)))
		for p := 0; p < numPlanes; p++ {
			mod := bpl1mod // odd planes (BPL1, 3, 5)
			if p%2 == 1 {
				mod = bpl2mod
			}
			v.bplpt[p] = uint32(int32(v.bplpt[p]) + int32(2*words) + mod)
		}
	}
	if !draw {
		return
	}
	if !fetch {
		numPlanes = 0
	}
	v.drawLine(vpos, numPlanes, hires, int(ddfstrt&0xFC), words)
}

// drawLine composes one output row the way Denise does: playfield pixels
// placed by DDFSTRT and BPLCON1, colour modes (dual playfield, EHB, HAM),
// sprites by BPLCON2 priority, and COLOR00 outside the display window.
//
// Horizontal positions are in hires pixels: output column X is beam
// position 2*OutputLeft+X, and lores pixels are two columns wide.
func (v *Video) drawLine(vpos, numPlanes int, hires bool, ddfstrt, words int) {
	v.UpdatePalette()
	c := v.amiga.Custom
	bplcon0 := c.ReadReg16(0x100)
	bplcon1 := c.ReadReg16(0x102)
	bplcon2 := c.ReadReg16(0x104)
	win := v.window()
	lineVisible := vpos >= win.vstart && vpos < win.vstop

	dpf := bplcon0&0x0400 != 0
	ham := bplcon0&0x0800 != 0 && !hires && numPlanes >= 5
	ehb := numPlanes == 6 && !dpf && !ham
	pf2pri := bplcon2&0x0040 != 0
	pf1p := int(bplcon2 & 7)
	pf2p := int(bplcon2 >> 3 & 7)

	// First playfield pixel, in hires beam units, before scrolling. DDFSTRT
	// is in colour clocks; the fetch-to-display pipeline adds 17 lores
	// pixels in lores and 9 in hires (HRM: DIWSTRT = 2*DDFSTRT + 17 / + 9,
	// so the standard $38/$3C both line up with DIWSTRT $81).
	origin := 2 * (2*ddfstrt + 17)
	pixW := 2 // hires columns per playfield pixel
	if hires {
		origin = 2 * (2*ddfstrt + 9)
		pixW = 1
	}
	// BPLCON1 delays odd planes by PF1H and even planes by PF2H, in lores
	// pixels in both resolutions.
	delay := [2]int{2 * int(bplcon1&0xF), 2 * int(bplcon1>>4&0xF)}
	pixels := 16 * words

	anySprite := false
	if v.amiga.Sprites != nil && lineVisible {
		anySprite = v.amiga.Sprites.lineBuffer(&v.sprCol, &v.sprPair)
	}

	row := v.Pixels[(vpos-DisplayTop)*OutputWidth*4:]
	var hold uint16 = c.ReadReg16(0x180) // HAM hold register
	for x := 0; x < OutputWidth; x++ {
		lores := OutputLeft + x/2
		if !lineVisible || lores < win.hstart || lores >= win.hstop {
			v.put(row, x, v.palette[0])
			hold = c.ReadReg16(0x180)
			continue
		}

		// Playfield bits for this column.
		var idx, pf1, pf2 int
		for p := 0; p < numPlanes; p++ {
			n := (2*OutputLeft + x - origin - delay[p&1]) / pixW
			if 2*OutputLeft+x-origin-delay[p&1] < 0 || n >= pixels {
				continue
			}
			if v.planeBuf[p][n>>3]&(0x80>>(n&7)) != 0 {
				idx |= 1 << p
				if p&1 == 0 {
					pf1 |= 1 << (p >> 1)
				} else {
					pf2 |= 1 << (p >> 1)
				}
			}
		}

		// Sprites: shown where every non-transparent playfield at this
		// pixel has a lower priority code than the sprite's pair.
		if anySprite && v.sprCol[x] != 0 {
			pair := int(v.sprPair[x])
			if (pf1 == 0 || pair < pf1p) && (pf2 == 0 || pair < pf2p) {
				v.put(row, x, v.palette[v.sprCol[x]])
				if ham {
					hold = v.hamStep(hold, idx)
				}
				continue
			}
		}

		switch {
		case ham:
			hold = v.hamStep(hold, idx)
			v.put(row, x, rgb12(hold))
		case dpf:
			front, back := pf1, pf2+8
			if pf2pri {
				front, back = pf2+8, pf1
			}
			switch {
			case front&7 != 0:
				v.put(row, x, v.palette[front])
			case back&7 != 0:
				v.put(row, x, v.palette[back])
			default:
				v.put(row, x, v.palette[0])
			}
		case ehb && idx&0x20 != 0:
			v.put(row, x, rgb12(c.ReadReg16(0x180+2*(idx&0x1F))>>1&0x777))
		default:
			v.put(row, x, v.palette[idx&0x1F])
		}
	}
}

// hamStep applies one HAM pixel to the held colour. Bits 5-4 (planes 6
// and 5) select: 00 load COLOR[bits 3-0], 01 set blue, 10 set red, 11 set
// green to bits 3-0.
func (v *Video) hamStep(hold uint16, idx int) uint16 {
	val := uint16(idx & 0xF)
	switch idx >> 4 & 3 {
	case 0:
		return v.amiga.Custom.ReadReg16(0x180 + 2*int(val))
	case 1:
		return hold&0xFF0 | val
	case 2:
		return hold&0x0FF | val<<8
	default:
		return hold&0xF0F | val<<4
	}
}

// rgb12 converts a 12-bit $RGB colour to RGBA.
func rgb12(c uint16) [4]byte {
	return [4]byte{byte(c >> 8 & 0xF * 17), byte(c >> 4 & 0xF * 17), byte(c & 0xF * 17), 0xFF}
}

func (v *Video) put(row []byte, x int, c [4]byte) {
	copy(row[x*4:x*4+4], c[:])
}

// ScreenImage returns an RGBA image snapshot of the current video output.
func (v *Video) ScreenImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	copy(img.Pix, v.Pixels)
	return img
}
