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
	planeBuf [6][2 * maxFetchWords]byte

	// changes lists this line's writes to Denise registers (colours,
	// BPLCON1, BPLCON2) in beam order, for drawLine to apply mid-line.
	changes []regChange

	// lineColors holds COLOR00-31 as drawLine sweeps the line.
	lineColors [32]uint16

	// sprCol and sprPair hold the current line's sprite pixels: palette
	// index (16-31, 0 = none) and sprite pair (0-3) per output column.
	sprCol, sprPair, sprPresent [MaxOutputWidth]byte

	// pfIdx holds the current line's playfield colour index per column.
	pfIdx [MaxOutputWidth]byte

	// clxdat accumulates collisions until CLXDAT is read. clxPF caches the
	// sprite-free collision bits per playfield index for CLXCON clxPFCon.
	clxdat     uint16
	clxPF      [64]uint16
	clxPFCon   uint16
	clxPFValid bool

	// The part of the beam the framebuffer shows: lores beam position of
	// column 0, first line, and rows per field. Width is the column count.
	left, top, rows int

	// laced is set for frames displayed with BPLCON0 LACE: the framebuffer
	// is then 512 rows and each field fills alternate rows.
	laced bool

	// DisplayActive controls whether RenderLine actually renders pixels.
	// Set to true when a display window is open. When false, rendering is
	// skipped for performance (headless/test mode).
	DisplayActive bool
}

const (
	PALVisibleLines = 256
	LowResWidth     = 320
	HiResWidth      = 640

	// The framebuffer shows a fixed part of the beam. By default that is
	// exactly the standard PAL display window (DIWSTRT $2C81, DIWSTOP
	// $2CC1): lines DisplayTop to DisplayBottom and OutputWidth hires
	// columns starting at lores beam position OutputLeft. Screens are
	// placed in it by their own DIW/DDF values; anything outside is cropped.
	DisplayTop    = 44
	DisplayBottom = DisplayTop + PALVisibleLines
	OutputLeft    = 0x81
	OutputWidth   = HiResWidth

	// Overscan view (SetOverscan): 20 lores pixels more on each side and
	// lines 28-311, about as much of the PAL picture as a monitor shows.
	OverscanLeft  = OutputLeft - 20
	OverscanWidth = OutputWidth + 80
	OverscanTop   = 28
	OverscanRows  = 284

	// MaxOutputWidth sizes the per-line buffers for either view.
	MaxOutputWidth = OverscanWidth
)

// NewVideo allocates a Video subsystem for the given Amiga.
func NewVideo(a *Amiga) *Video {
	v := &Video{
		amiga: a,
	}
	v.SetOverscan(false)
	return v
}

// SetOverscan selects the standard view (640x256 from H $81, line 44) or
// the overscan view (720x284 from H $6D, line 28). Interlace doubles the
// rows in either.
func (v *Video) SetOverscan(on bool) {
	v.left, v.top, v.rows, v.Width = OutputLeft, DisplayTop, PALVisibleLines, OutputWidth
	if on {
		v.left, v.top, v.rows, v.Width = OverscanLeft, OverscanTop, OverscanRows, OverscanWidth
	}
	v.resize()
}

// resize allocates the framebuffer for the current view and interlace.
func (v *Video) resize() {
	v.Height = v.rows
	if v.laced {
		v.Height *= 2
	}
	v.Pixels = make([]byte, v.Width*v.Height*4)
}

// DisplaySize is the size to show the framebuffer at on the host: full
// width, and two host rows per non-interlaced line so pixels keep the
// Amiga's aspect ratio.
func (v *Video) DisplaySize() (w, h int) {
	return v.Width, 2 * v.rows
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

// StartFrame is called at VBLANK to snapshot bitplane pointers for the new
// frame, and switches the framebuffer between 256 rows and 512 interlaced
// rows when BPLCON0 LACE changes.
func (v *Video) StartFrame() {
	v.SnapshotBitplanePointers()
	laced := v.amiga.Custom.ReadReg16(0x100)&0x0004 != 0
	if laced != v.laced {
		v.laced = laced
		v.resize()
	}
}

// outputRow is the framebuffer row for display line vpos. Interlaced, the
// long frame (LOF=1) fills the even rows and the short frame the odd rows,
// and the other field's rows are kept, weaving the two into one picture.
func (v *Video) outputRow(vpos int) int {
	y := vpos - v.top
	if !v.laced {
		return y
	}
	if v.amiga.lof {
		return 2 * y
	}
	return 2*y + 1
}

// maxFetchWords is the most words a plane can fetch per line: DDF from
// $18 to $D8 in hires.
const maxFetchWords = 50

// fetchWords is the number of words fetched per plane per line for the
// given DDFSTRT/DDFSTOP. Agnus fetches in units of 8 colour clocks from
// DDFSTRT; it finishes the unit in which DDFSTOP is reached and one more.
// A unit fetches one word per plane in lores and two in hires. So the
// HRM's standard values give 20 lores words ($38/$D0) and 40 hires words
// ($3C/$D4, and KS1.3's $3C/$D0); KS3.x's hires $38/$D8 gives 42, with
// BPLxMOD -4 bringing the pointers back to an 80-byte row.
func fetchWords(hires bool, ddfstrt, ddfstop uint16) int {
	start, stop := int(ddfstrt&0xFC), int(ddfstop&0xFC)
	if start == 0 || stop <= start {
		start, stop = 0x38, 0xD0
		if hires {
			start = 0x3C
		}
	}
	units := (stop-start+7)/8 + 1
	words := units
	if hires {
		words *= 2
	}
	if words > maxFetchWords {
		words = maxFetchWords
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
	return v.amiga.Custom.ReadReg16(0x100)>>12&7 != 0 && v.bitplaneFetchWindow(vpos)
}

// bitplaneFetchWindow reports whether bitplane DMA is on and vpos is
// inside the vertical window.
func (v *Video) bitplaneFetchWindow(vpos int) bool {
	if v.amiga.Custom.ReadReg16(0x096)&0x0300 != 0x0300 {
		return false
	}
	w := v.window()
	return vpos >= w.vstart && vpos < w.vstop
}

// RenderLine fetches the bitplane data for scanline vpos (advancing the
// bitplane pointers as the DMA does) and, when the display is active and
// the line is in the output window, draws it into Pixels.
func (v *Video) RenderLine(vpos int) {
	ddfstrt := v.amiga.Custom.ReadReg16(0x092)
	// Agnus fetches with the BPLCON0 in force when the fetch starts; a
	// write later in the line (a Copper list setting up the next part of
	// the screen) changes only what Denise shows from its column on.
	bplcon0 := v.amiga.Custom.ReadReg16(0x100)
	for i := len(v.changes) - 1; i >= 0; i-- {
		if ch := v.changes[i]; ch.reg == 0x100 && ch.cck >= int(ddfstrt&0xFC) {
			bplcon0 = ch.old
		}
	}
	numPlanes := int((bplcon0 >> 12) & 0x7)
	if numPlanes > 6 {
		numPlanes = 6
	}
	hires := bplcon0&0x8000 != 0
	words := fetchWords(hires, ddfstrt, v.amiga.Custom.ReadReg16(0x094))

	fetch := numPlanes > 0 && v.bitplaneFetchWindow(vpos)
	draw := v.DisplayActive && vpos >= v.top && vpos < v.top+v.rows
	if fetch && draw {
		for p := 0; p < numPlanes; p++ {
			v.amiga.ChipRAM.Read(int(v.bplpt[p]), v.planeBuf[p][:2*words])
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
	defer func() { v.changes = v.changes[:0] }()
	if !draw {
		return
	}
	if !fetch {
		numPlanes = 0
	}
	v.drawLine(vpos, numPlanes, hires, int(ddfstrt&0xFC), words)
}

// regChange is a write to a Denise register during the current line.
type regChange struct {
	col      int // first output column showing the new value
	cck      int // colour clock of the write
	reg      int
	old, new uint16
}

// deniseLineReg reports whether writes to reg are applied mid-line.
func deniseLineReg(reg int) bool {
	return reg >= 0x180 && reg < 0x1C0 || reg == 0x100 || reg == 0x102 || reg == 0x104
}

// writeColumnOffset places a register write in the picture: a write at
// colour clock c takes effect at lores beam position 2*c + 1, the same
// relationship as bitplane data (the standard DDFSTRT $38 completes its
// first fetch at colour clock $40 and its first pixel is H $81).
const writeColumnOffset = 1

// noteWrite records a Denise register write at beam position hpos (CPU
// cycles) so drawLine can apply it from the right column.
func (v *Video) noteWrite(reg int, old, new uint16, hpos int) {
	if !v.DisplayActive || old == new {
		return
	}
	cck := hpos / CPUCyclesPerColorClock
	lores := 2*cck + writeColumnOffset
	v.changes = append(v.changes, regChange{col: 2 * (lores - v.left), cck: cck, reg: reg, old: old, new: new})
}

// drawLine composes one output row the way Denise does: playfield pixels
// placed by DDFSTRT and BPLCON1, colour modes (dual playfield, EHB, HAM),
// sprites by BPLCON2 priority, collisions, and COLOR00 outside the display
// window. BPLCON0 (planes shown and colour mode), BPLCON1, BPLCON2 and
// colour writes made during the line take effect from their column (see
// noteWrite).
//
// Horizontal positions are in hires pixels: output column X is beam
// position 2*v.left+X, and lores pixels are two columns wide.
func (v *Video) drawLine(vpos, numPlanes int, hires bool, ddfstrt, words int) {
	c := v.amiga.Custom
	bplcon0 := c.ReadReg16(0x100)
	bplcon1 := c.ReadReg16(0x102)
	bplcon2 := c.ReadReg16(0x104)
	for i := range v.lineColors {
		v.lineColors[i] = c.ReadReg16(0x180+2*i) & 0xFFF
	}
	// The registers hold their end-of-line values; rewind this line's
	// writes to get the values it started with, then replay them by column.
	for i := len(v.changes) - 1; i >= 0; i-- {
		ch := v.changes[i]
		switch ch.reg {
		case 0x100:
			bplcon0 = ch.old
		case 0x102:
			bplcon1 = ch.old
		case 0x104:
			bplcon2 = ch.old
		default:
			v.lineColors[(ch.reg-0x180)/2] = ch.old & 0xFFF
		}
	}
	for i, col := range v.lineColors {
		v.palette[i] = rgb12(col)
	}

	win := v.window()
	lineVisible := vpos >= win.vstart && vpos < win.vstop
	// Denise shows the planes and colour mode BPLCON0 names now, which a
	// write during the line can change; planes it names but Agnus did not
	// fetch show as 0 (the resolution stays as fetched).
	var planeMask int
	var dpf, ham, ehb bool
	deniseMode := func() {
		shown := min(int(bplcon0>>12&7), numPlanes)
		planeMask = 1<<shown - 1
		dpf = bplcon0&0x0400 != 0
		ham = bplcon0&0x0800 != 0 && !hires && shown >= 5
		ehb = shown == 6 && !dpf && !ham
	}
	deniseMode()

	// First playfield pixel, in output columns, before scrolling. DDFSTRT
	// is in colour clocks; the fetch-to-display pipeline adds 17 lores
	// pixels in lores and 9 in hires (HRM: DIWSTRT = 2*DDFSTRT + 17 / + 9,
	// so the standard $38/$3C both line up with DIWSTRT $81).
	pf := playfieldLayout{planes: numPlanes, words: words, pixW: 2,
		origin: 2*(2*ddfstrt+17) - 2*v.left}
	if hires {
		pf.pixW, pf.origin = 1, 2*(2*ddfstrt+9)-2*v.left
	}
	v.fillPlayfield(pf, 0, bplcon1)

	anySprite := false
	if v.amiga.Sprites != nil && lineVisible {
		anySprite = v.amiga.Sprites.lineBuffer(&v.sprCol, &v.sprPair, &v.sprPresent, v.left, v.Width)
	}

	// Collisions without sprites depend only on the playfield bits and
	// CLXCON: a table, rebuilt when CLXCON changes.
	clxcon := c.ReadReg16(0x098)
	if !v.clxPFValid || v.clxPFCon != clxcon {
		for i := range v.clxPF {
			v.clxPF[i] = collisions(clxcon, i, 0)
		}
		v.clxPFCon, v.clxPFValid = clxcon, true
	}

	pf2pri := bplcon2&0x0040 != 0
	pf1p, pf2p := int(bplcon2&7), int(bplcon2>>3&7)
	row := v.Pixels[v.outputRow(vpos)*v.Width*4:]
	hold := v.lineColors[0] // HAM hold register
	next := 0
	nextCol := v.Width // column of the next pending change
	if len(v.changes) > 0 {
		nextCol = v.changes[0].col
	}
	for x := 0; x < v.Width; x++ {
		for x >= nextCol {
			ch := v.changes[next]
			next++
			nextCol = v.Width
			if next < len(v.changes) {
				nextCol = v.changes[next].col
			}
			switch ch.reg {
			case 0x100:
				bplcon0 = ch.new
				deniseMode()
			case 0x102:
				bplcon1 = ch.new
				v.fillPlayfield(pf, x, bplcon1)
			case 0x104:
				bplcon2 = ch.new
				pf2pri = bplcon2&0x0040 != 0
				pf1p, pf2p = int(bplcon2&7), int(bplcon2>>3&7)
			default:
				i := (ch.reg - 0x180) / 2
				v.lineColors[i] = ch.new & 0xFFF
				v.palette[i] = rgb12(ch.new)
			}
		}

		lores := v.left + x/2
		if !lineVisible || lores < win.hstart || lores >= win.hstop {
			v.put(row, x, v.palette[0])
			hold = v.lineColors[0]
			continue
		}

		idx := int(v.pfIdx[x]) & planeMask
		pf1, pf2 := oddPlaneBits[idx], evenPlaneBits[idx]
		if anySprite && v.sprPresent[x] != 0 {
			v.clxdat |= collisions(clxcon, idx, v.sprPresent[x])
			// Sprites: shown where every non-transparent playfield at this
			// pixel has a lower priority code than the sprite's pair.
			pair := int(v.sprPair[x])
			if (pf1 == 0 || pair < pf1p) && (pf2 == 0 || pair < pf2p) {
				v.put(row, x, v.palette[v.sprCol[x]])
				if ham {
					hold = v.hamStep(hold, idx)
				}
				continue
			}
		} else {
			v.clxdat |= v.clxPF[idx]
		}

		switch {
		case ham:
			hold = v.hamStep(hold, idx)
			v.put(row, x, rgb12(hold))
		case dpf:
			front, back := int(pf1), int(pf2)+8
			if pf2pri {
				front, back = int(pf2)+8, int(pf1)
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
			v.put(row, x, rgb12(v.lineColors[idx&0x1F]>>1&0x777))
		default:
			v.put(row, x, v.palette[idx&0x1F])
		}
	}
}

// playfieldLayout is where a line's fetched bitplane data lands.
type playfieldLayout struct {
	planes, words int
	pixW          int // output columns per playfield pixel (2 lores, 1 hires)
	origin        int // output column of the first pixel, before scrolling
}

// fillPlayfield sets pfIdx[from:] to the playfield colour index (plane
// bits) of each output column, with BPLCON1 scroll bplcon1. BPLCON1 delays
// odd planes by PF1H and even planes by PF2H, in lores pixels in both
// resolutions.
func (v *Video) fillPlayfield(pf playfieldLayout, from int, bplcon1 uint16) {
	clear(v.pfIdx[from:])
	if from < 0 {
		from = 0
	}
	pixels := 16 * pf.words
	for p := 0; p < pf.planes; p++ {
		delay := int(bplcon1 & 0xF)
		if p&1 == 1 {
			delay = int(bplcon1 >> 4 & 0xF)
		}
		start := pf.origin + 2*delay // column of pixel 0
		// Pixels whose columns lie in [from, v.Width).
		nMin := (from - start + pf.pixW - 1) / pf.pixW
		if from-start < 0 {
			nMin = 0
		}
		nMax := (v.Width - start + pf.pixW - 1) / pf.pixW
		if nMax > pixels {
			nMax = pixels
		}
		bit := byte(1) << p
		data := v.planeBuf[p][:2*pf.words]
		for n := nMin; n < nMax; n++ {
			if n&7 == 0 && data[n>>3] == 0 {
				n += 7 // whole byte clear
				continue
			}
			if data[n>>3]&(0x80>>(n&7)) == 0 {
				continue
			}
			x := start + n*pf.pixW
			v.pfIdx[x] |= bit
			if pf.pixW == 2 && x+1 < v.Width {
				v.pfIdx[x+1] |= bit
			}
		}
	}
}

// oddPlaneBits and evenPlaneBits split a 6-bit colour index into the
// playfield 1 (planes 1, 3, 5) and playfield 2 (planes 2, 4, 6) values.
var oddPlaneBits, evenPlaneBits [64]byte

func init() {
	for i := range oddPlaneBits {
		oddPlaneBits[i] = byte(i&1 | i>>1&2 | i>>2&4)
		evenPlaneBits[i] = byte(i>>1&1 | i>>2&2 | i>>3&4)
	}
}

// collisions returns the CLXDAT bits for one pixel with playfield bits idx
// and sprites present (bit i = sprite i). CLXCON selects which planes take
// part (ENBP) and the value they must have (MVBP); PF1 is the odd planes
// and PF2 the even planes. Even sprites always take part, odd sprites only
// with ENSP.
func collisions(clxcon uint16, idx int, present byte) uint16 {
	enbp := int(clxcon >> 6 & 0x3F)
	mvbp := int(clxcon & 0x3F)
	pf1 := (idx^mvbp)&enbp&0x15 == 0
	pf2 := (idx^mvbp)&enbp&0x2A == 0
	var bits uint16
	if pf1 && pf2 {
		bits |= 1 << 0
	}
	if present == 0 {
		return bits
	}
	ensp := byte(clxcon >> 12 & 0xF)
	var pairs byte // bit k = sprite pair k present
	for k := 0; k < 4; k++ {
		if present>>(2*k)&1 != 0 || present>>(2*k+1)&1 != 0 && ensp>>k&1 != 0 {
			pairs |= 1 << k
		}
	}
	for k := 0; k < 4; k++ {
		if pairs>>k&1 == 0 {
			continue
		}
		if pf1 {
			bits |= 1 << (1 + k)
		}
		if pf2 {
			bits |= 1 << (5 + k)
		}
	}
	// Sprite pair vs pair: bits 9-14 for (0,1) (0,2) (0,3) (1,2) (1,3) (2,3).
	bit := 9
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if pairs>>i&1 != 0 && pairs>>j&1 != 0 {
				bits |= 1 << bit
			}
			bit++
		}
	}
	return bits
}

// ReadCLXDAT returns the collisions seen since the last read and clears
// them. Bit 15 always reads as 1.
func (v *Video) ReadCLXDAT() uint16 {
	val := v.clxdat | 0x8000
	v.clxdat = 0
	return val
}

// hamStep applies one HAM pixel to the held colour. Bits 5-4 (planes 6
// and 5) select: 00 load COLOR[bits 3-0], 01 set blue, 10 set red, 11 set
// green to bits 3-0.
func (v *Video) hamStep(hold uint16, idx int) uint16 {
	val := uint16(idx & 0xF)
	switch idx >> 4 & 3 {
	case 0:
		return v.lineColors[val]
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
	o := x * 4
	row[o], row[o+1], row[o+2], row[o+3] = c[0], c[1], c[2], c[3]
}

// ScreenImage returns an RGBA image snapshot of the current video output.
func (v *Video) ScreenImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	copy(img.Pix, v.Pixels)
	return img
}
