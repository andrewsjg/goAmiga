package amiga

import "math"

// Sprite DMA / display states (OCS).
const (
	// sprFetchCtl: next DMA slot loads SPRxPOS/SPRxCTL from the pointer.
	sprFetchCtl = iota
	// sprWaitVStart: control words loaded; waiting for beam == VSTART (no DMA).
	sprWaitVStart
	// sprActive: between VSTART and VSTOP; fetch DATA/DATB each line and display.
	sprActive
	// sprIdle: terminated with (0,0) control words until SPRxPT is reloaded.
	sprIdle
)

// Sprite represents one of 8 Amiga hardware sprites.
type Sprite struct {
	pt    uint32 // SPRxPT working DMA pointer
	pos   uint16 // SPRxPOS
	ctl   uint16 // SPRxCTL
	data  uint16 // SPRxDATA (plane 0)
	datb  uint16 // SPRxDATB (plane 1)
	state int    // sprFetchCtl / sprWaitVStart / sprActive
}

// vstart returns the vertical start position (9 bits).
func (s *Sprite) vstart() int {
	v := int(s.pos >> 8)
	if s.ctl&0x0004 != 0 {
		v |= 0x100
	}
	return v
}

// vstop returns the vertical stop position (9 bits).
func (s *Sprite) vstop() int {
	v := int(s.ctl >> 8)
	if s.ctl&0x0002 != 0 {
		v |= 0x100
	}
	return v
}

// hstart returns the horizontal start in lores pixels (9 bits, half-color-clocks).
func (s *Sprite) hstart() int {
	h := int(s.pos&0xFF) << 1
	if s.ctl&0x0001 != 0 {
		h |= 1
	}
	return h
}

// SpriteEngine manages the 8 hardware sprites.
type SpriteEngine struct {
	sprites [8]Sprite
	px      [8][MaxOutputWidth]byte // per-sprite pixels of the current line
	amiga   *Amiga
	buf     [2]byte

	// start holds the sprites as the line began (after its DMA), and
	// events the register writes made during it, so paint can replay them
	// left to right: a sprite whose SPRxPOS and data are rewritten after it
	// has been shown appears again further along the same line.
	start  [8]Sprite
	events []sprEvent
}

// sprEvent is a write to a sprite register during the current line.
type sprEvent struct {
	col, sprite, reg int // output column; sprPOS, sprCTL, sprDATA or sprDATB
	val              uint16
}

const (
	sprPOS = iota
	sprCTL
	sprDATA
	sprDATB
)

// note records a sprite register write made at beam position hpos (CPU
// cycles) during the current line.
func (se *SpriteEngine) note(sprite, reg int, val uint16, hpos int) {
	v := se.amiga.Video
	if v == nil || !v.DisplayActive || sprite < 0 || sprite >= 8 {
		return
	}
	lores := 2*(hpos/CPUCyclesPerColorClock) + writeColumnOffset
	se.events = append(se.events, sprEvent{col: 2 * (lores - v.left), sprite: sprite, reg: reg, val: val})
}

// startLine records the sprites' state at the start of a line.
func (se *SpriteEngine) startLine() {
	se.start = se.sprites
	se.events = se.events[:0]
}

// NewSpriteEngine creates a new sprite engine.
func NewSpriteEngine(a *Amiga) *SpriteEngine {
	return &SpriteEngine{amiga: a}
}

// SetPT sets the DMA pointer for a sprite (high or low word).
// A write to the low word (completing the 32-bit pointer) restarts the sprite
// so the next DMA fetch reloads POS/CTL — matching copper pointer reloads
// each frame for the mouse pointer.
func (se *SpriteEngine) SetPT(sprite int, hi bool, val uint16) {
	if sprite < 0 || sprite >= 8 {
		return
	}
	s := &se.sprites[sprite]
	if hi {
		s.pt = (s.pt & 0x0000FFFF) | uint32(val)<<16
	} else {
		s.pt = (s.pt & 0xFFFF0000) | uint32(val)
		// Complete pointer write: restart DMA state for this sprite.
		s.state = sprFetchCtl
		s.data = 0
		s.datb = 0
	}
}

// SetPOS writes SPRxPOS (CPU path).
func (se *SpriteEngine) SetPOS(sprite int, val uint16) {
	if sprite < 0 || sprite >= 8 {
		return
	}
	se.sprites[sprite].pos = val
}

// SetCTL writes SPRxCTL (CPU path) and arms the sprite to wait for VSTART.
// Used when software pokes sprite registers directly without DMA.
func (se *SpriteEngine) SetCTL(sprite int, val uint16) {
	if sprite < 0 || sprite >= 8 {
		return
	}
	s := &se.sprites[sprite]
	s.ctl = val
	s.state = sprWaitVStart
	s.data = 0
	s.datb = 0
}

// SetDATA writes SPRxDATA for the given sprite.
func (se *SpriteEngine) SetDATA(sprite int, val uint16) {
	if sprite < 0 || sprite >= 8 {
		return
	}
	se.sprites[sprite].data = val
	// CPU-loaded sprite: writing DATA enables display for the current line.
	if se.sprites[sprite].state != sprActive {
		se.sprites[sprite].state = sprActive
	}
}

// SetDATB writes SPRxDATB for the given sprite.
func (se *SpriteEngine) SetDATB(sprite int, val uint16) {
	if sprite < 0 || sprite >= 8 {
		return
	}
	se.sprites[sprite].datb = val
}

func (se *SpriteEngine) readWord(addr uint32) uint16 {
	se.amiga.ChipRAM.Read(int(addr), se.buf[:])
	return uint16(se.buf[0])<<8 | uint16(se.buf[1])
}

func (se *SpriteEngine) fetchCtl(s *Sprite) {
	s.pos = se.readWord(s.pt)
	s.pt += 2
	s.ctl = se.readWord(s.pt)
	s.pt += 2
}

func (se *SpriteEngine) fetchData(s *Sprite) {
	s.data = se.readWord(s.pt)
	s.pt += 2
	s.datb = se.readWord(s.pt)
	s.pt += 2
}

// FetchLine performs sprite DMA for the given vertical position.
//
// OCS rules used here:
//   - While waiting for VSTART, the pointer does NOT advance each line
//     (the old code re-fetched POS/CTL every armed line and destroyed the
//     mouse pointer data stream).
//   - On VSTART, fetch data and display that line.
//   - On VSTOP, stop displaying and fetch the next control word pair
//     (sprite multiplexing); (0,0) ends the sprite until SPRxPT is reloaded.
func (se *SpriteEngine) FetchLine(vpos int) {
	defer se.startLine()
	dmacon := se.amiga.Custom.ReadReg16(0x096)
	if dmacon&0x0220 != 0x0220 {
		return
	}

	for i := range se.sprites {
		s := &se.sprites[i]

		switch s.state {
		case sprIdle:
			// Terminated until copper/CPU reloads SPRxPT (SetPT low word).

		case sprFetchCtl:
			se.fetchCtl(s)
			s.data = 0
			s.datb = 0
			// End-of-list: both words zero — idle until next PT reload.
			if s.pos == 0 && s.ctl == 0 {
				s.state = sprIdle
				break
			}
			// If VSTART is this line, go active and fetch data immediately.
			if s.vstart() == vpos {
				s.state = sprActive
				se.fetchData(s)
			} else {
				s.state = sprWaitVStart
			}

		case sprWaitVStart:
			// No DMA while waiting — pointer must not advance each line.
			if s.vstart() == vpos {
				s.state = sprActive
				se.fetchData(s)
			}

		case sprActive:
			if vpos >= s.vstop() {
				// VSTOP line is not displayed; fetch next control pair
				// (sprite multiplexing further down the frame).
				s.data = 0
				s.datb = 0
				se.fetchCtl(s)
				if s.pos == 0 && s.ctl == 0 {
					s.state = sprIdle
				} else if s.vstart() == vpos {
					s.state = sprActive
					se.fetchData(s)
				} else {
					s.state = sprWaitVStart
				}
			} else {
				se.fetchData(s)
			}
		}
	}
}

// lineBuffer fills the current line's sprite pixels for the compositor:
// col[x] is the palette index (16-31, 0 = transparent) and pair[x] the
// sprite pair (0-3) shown at output column x (column 0 is lores beam
// position left; width columns), and present[x] has bit i set
// where sprite i has a non-transparent pixel (for collision detection).
// Lower-numbered sprites are in front of higher ones. An odd sprite with
// ATTACH (SPRxCTL bit 7) joins its even partner as one 15-colour sprite
// using COLOR16-31. It reports whether any sprite pixel was drawn.
//
// Sprite HSTART is in lores beam pixels, the same units as DIWSTRT's
// horizontal position; sprites are lores, two output columns per pixel.
func (se *SpriteEngine) lineBuffer(col, pair, present *[MaxOutputWidth]byte, left, width int) bool {
	var painted [8]bool
	anyPainted := false
	for i := range se.sprites {
		painted[i] = se.paint(i, &se.px[i], left, width)
		anyPainted = anyPainted || painted[i]
	}
	if !anyPainted {
		return false
	}
	*col = [MaxOutputWidth]byte{}
	*present = [MaxOutputWidth]byte{}
	for pr := 3; pr >= 0; pr-- {
		ev, od := 2*pr, 2*pr+1
		if !painted[ev] && !painted[od] {
			continue
		}
		attached := se.sprites[od].ctl&0x80 != 0
		for x := 0; x < width; x++ {
			var e, o byte
			if painted[ev] {
				e = se.px[ev][x]
			}
			if painted[od] {
				o = se.px[od][x]
			}
			if e|o == 0 {
				continue
			}
			if e != 0 {
				present[x] |= 1 << ev
			}
			if o != 0 {
				present[x] |= 1 << od
			}
			switch {
			case attached:
				col[x] = 16 + (o<<2 | e)
			case e != 0:
				col[x] = byte(16+4*pr) + e
			default:
				col[x] = byte(16+4*pr) + o
			}
			pair[x] = byte(pr)
		}
	}
	return true
}

// paint draws sprite i's 2-bit pixels on the current line into bits,
// reporting false (and leaving bits untouched) if it shows nothing.
//
// It replays the line: the sprite starts as it was after the line's DMA,
// and each write during the line takes effect from its column. An armed
// sprite (DATA written, by DMA or otherwise, and not since disarmed by a
// CTL write) is shown when the beam reaches its HSTART; if HSTART is then
// moved further right and the sprite rearmed, it is shown again there.
func (se *SpriteEngine) paint(i int, bits *[MaxOutputWidth]byte, left, width int) bool {
	// With no writes this line, start and current state are the same (or
	// the registers were set outside emulated time, as tests do).
	s := se.sprites[i]
	for _, ev := range se.events {
		if ev.sprite == i {
			s = se.start[i]
			break
		}
	}
	armed := s.state == sprActive
	painted := false
	draw := func(from, to int) {
		if !armed || s.data|s.datb == 0 {
			return
		}
		x0 := 2 * (s.hstart() - left)
		if x0 < from || x0 >= to {
			return
		}
		if !painted {
			*bits = [MaxOutputWidth]byte{}
			painted = true
		}
		for b := 0; b < 16; b++ {
			v := byte(s.data>>(15-b)&1 | s.datb>>(15-b)&1<<1)
			if v == 0 {
				continue
			}
			for _, x := range [2]int{x0 + 2*b, x0 + 2*b + 1} {
				if x >= 0 && x < width {
					bits[x] = v
				}
			}
		}
	}
	from := math.MinInt
	for _, ev := range se.events {
		if ev.sprite != i {
			continue
		}
		draw(from, ev.col)
		from = ev.col
		switch ev.reg {
		case sprPOS:
			s.pos = ev.val
		case sprCTL:
			s.ctl, armed = ev.val, false
		case sprDATA:
			s.data, armed = ev.val, true
		case sprDATB:
			s.datb = ev.val
		}
	}
	draw(from, math.MaxInt)
	return painted
}
