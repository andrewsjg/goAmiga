package amiga

// The card's mouse pointer: a hardware sprite, drawn over the picture
// rather than into the framebuffer. uaegfx.card has no sprite calls, so
// InitCard puts routines of its own in BoardInfo's SetSprite,
// SetSpritePosition, SetSpriteImage and SetSpriteColor, which call the
// emulator as the card's do, and sets BIF_HARDWARESPRITE: rtg.library then
// stops drawing the pointer into the screen itself (saving and restoring
// what is under it on every move) and tells the card where it is.

// Function numbers for the sprite routines (the card's own go up to 34).
const (
	rtgSetSprite         = 36
	rtgSetSpritePosition = 37
	rtgSetSpriteImage    = 38
	rtgSetSpriteColor    = 39
)

// BoardInfo's sprite fields (offsets checked in rtg.library's code).
const (
	biSetSprite         = 0x206
	biSetSpritePosition = 0x20A
	biSetSpriteImage    = 0x20E
	biSetSpriteColor    = 0x212
	biMouseX            = 0x566
	biMouseY            = 0x568
	biMouseWidth        = 0x56A
	biMouseHeight       = 0x56B
	biMouseXOffset      = 0x56C
	biMouseYOffset      = 0x56D
	biMouseImage        = 0x56E

	bifHardwareSprite = 1 << 0
)

// rtAreaSprite holds the four routines, 16 bytes each:
// MOVE.L #fn,-(SP); JSR $F0FF60; ADDQ.L #4,SP; RTS.
const rtAreaSprite = 0xFFA0

func spriteRoutine(fn byte) []byte {
	return []byte{0x2F, 0x3C, 0, 0, 0, fn, 0x4E, 0xB9, 0x00, 0xF0, 0xFF, 0x60, 0x58, 0x8F, 0x4E, 0x75}
}

// rtgSprite is the pointer's state.
type rtgSprite struct {
	on     bool
	x, y   int // where rtg.library puts it, on the screen's bitmap
	w, h   int
	img    []byte     // colour numbers 0-3, w×h
	colors [3][3]byte // colours 1-3
}

// installSprite gives rtg.library the card's sprite routines.
func (r *rtgBoard) installSprite(bi uint32) {
	a := r.amiga
	for i, fn := range []byte{rtgSetSprite, rtgSetSpritePosition, rtgSetSpriteImage, rtgSetSpriteColor} {
		at := rtAreaSprite + 16*i
		copy(r.area.mem[at:], spriteRoutine(fn))
		a.poke32(bi+biSetSprite+uint32(4*i), rtAreaBase+uint32(at))
	}
	a.poke32(bi+biFlags, a.peek32(bi+biFlags)|bifHardwareSprite)
	a.poke16(bi+biSoftSpriteFlags, 0)
}

// spriteCall handles the sprite routines' calls.
func (r *rtgBoard) spriteCall(fn uint32, bi uint32) {
	a := r.amiga
	c := a.CPU
	s := &r.sprite
	switch fn {
	case rtgSetSprite:
		s.on = c.D[0]&0xFFFF != 0
	case rtgSetSpritePosition:
		s.x, s.y = int(int16(c.D[0])), int(int16(c.D[1]))
	case rtgSetSpriteColor:
		if i := c.D[0] & 0xFF; i < 3 {
			s.colors[i] = [3]byte{byte(c.D[1]), byte(c.D[2]), byte(c.D[3])}
		}
	case rtgSetSpriteImage:
		r.spriteImage(bi)
	}
}

// spriteImage reads the pointer's image: MouseImage is in Amiga sprite
// form, two control words and then for each line two words (each bit of
// the first gives bit 0 of a pixel's colour number, of the second bit 1).
func (r *rtgBoard) spriteImage(bi uint32) {
	a := r.amiga
	s := &r.sprite
	s.w, s.h = int(a.peek8(bi+biMouseWidth)), int(a.peek8(bi+biMouseHeight))
	s.w = min(s.w, 16)
	img := a.peek32(bi + biMouseImage)
	s.img = make([]byte, s.w*s.h)
	for y := 0; y < s.h; y++ {
		p0 := a.peek16(img + 4 + uint32(4*y))
		p1 := a.peek16(img + 6 + uint32(4*y))
		for x := 0; x < s.w; x++ {
			bit := uint16(0x8000) >> x
			var v byte
			if p0&bit != 0 {
				v |= 1
			}
			if p1&bit != 0 {
				v |= 2
			}
			s.img[y*s.w+x] = v
		}
	}
	r.tracef("SetSpriteImage %dx%d offset %d,%d", s.w, s.h, int8(a.peek8(bi+biMouseXOffset)), int8(a.peek8(bi+biMouseYOffset)))
}

// drawSprite draws the pointer over the rendered picture.
func (r *rtgBoard) drawSprite() {
	s := &r.sprite
	if !s.on || len(s.img) == 0 {
		return
	}
	w, h := r.width, r.height
	for y := 0; y < s.h; y++ {
		py := s.y - r.yoff + y
		if py < 0 || py >= h {
			continue
		}
		for x := 0; x < s.w; x++ {
			px := s.x - r.xoff + x
			v := s.img[y*s.w+x]
			if v == 0 || px < 0 || px >= w {
				continue
			}
			o := 4 * (py*w + px)
			c := s.colors[v-1]
			r.pix[o], r.pix[o+1], r.pix[o+2], r.pix[o+3] = c[0], c[1], c[2], 0xFF
		}
	}
}
