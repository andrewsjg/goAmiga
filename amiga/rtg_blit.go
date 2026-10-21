package amiga

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// The card's blitter: the drawing calls uaegfx.card passes on before
// falling back to Picasso96's own routines. Each works on the card's
// memory directly and answers 1; anything it doesn't handle (memory off
// the card, a mask or minterm it doesn't do) answers 0, and Picasso96
// draws it in software as before.
//
// The register conventions are UAE's: A0 BoardInfo, A1 the RenderInfo (or
// for the planar calls the BitMap, with the RenderInfo in A2), D0-D5 the
// coordinates and sizes, D7 the pixel format (or a plane mask).
//
// With Verify set, each call is worked out on the side and the real
// drawing left to Picasso96; the next call compares the two and counts
// the differences (Mismatches), which is how the conventions were checked.

// RenderInfo, Template and Pattern, as Picasso96 passes them.
const (
	riMemory      = 0
	riBytesPerRow = 4
	riRGBFormat   = 8

	tmplMemory      = 0
	tmplBytesPerRow = 4
	tmplXOffset     = 6
	tmplDrawMode    = 7
	tmplFgPen       = 8
	tmplBgPen       = 12

	patMemory   = 0
	patXOffset  = 4
	patYOffset  = 6
	patFgPen    = 8
	patBgPen    = 12
	patSize     = 16
	patDrawMode = 17

	bmBytesPerRow = 0
	bmDepth       = 5
	bmPlanes      = 8

	cimColors = 4 // ColorIndexMapping: ColorMask, then 256 pixel values
)

// Draw modes.
const (
	drawJAM2       = 1
	drawComplement = 2
	drawInversVid  = 4
)

// rtgSurface is a rectangle's place in the card's memory.
type rtgSurface struct {
	off int // the rectangle's first byte in vram
	bpr int // bytes per row
	bpp int
}

// surface finds a RenderInfo's x, y, w×h rectangle in the card's memory;
// ok is false if any of it is elsewhere.
func (r *rtgBoard) surface(ri uint32, x, y, w, h, format int) (s rtgSurface, ok bool) {
	if format <= 0 || format >= len(rgbfBytes) || w <= 0 || h <= 0 {
		return s, false
	}
	a := r.amiga
	mem := a.peek32(ri + riMemory)
	s.bpr = int(int16(a.peek16(ri + riBytesPerRow)))
	s.bpp = rgbfBytes[format]
	if mem < r.base || s.bpr <= 0 {
		return s, false
	}
	s.off = int(mem-r.base) + y*s.bpr + x*s.bpp
	end := s.off + (h-1)*s.bpr + w*s.bpp
	if s.off < 0 || end > len(r.vram.b) || x < 0 || y < 0 {
		return s, false
	}
	return s, true
}

// row returns row y of a w-pixel-wide rectangle.
func (r *rtgBoard) row(s rtgSurface, y, w int) []byte {
	o := s.off + y*s.bpr
	return r.vram.b[o : o+w*s.bpp]
}

// pixel is a pen as the bytes of one pixel, as Picasso96 makes them for
// the format: the low bpp bytes of the value, most significant first,
// except that a 24-bit pen is stored least significant first.
func pixel(pen uint32, bpp int) []byte {
	b := make([]byte, bpp)
	for i := range b {
		if bpp == 3 {
			b[i] = byte(pen >> (8 * i))
		} else {
			b[i] = byte(pen >> (8 * (bpp - 1 - i)))
		}
	}
	return b
}

// minterm combines source and destination bits by a blitter function,
// Picasso96's opcode: the high nibble of an Amiga minterm (bit 3 src&dst, 2 src&^dst,
// 1 ^src&dst, 0 ^src&^dst).
func minterm(op, s, d byte) byte {
	var v byte
	if op&8 != 0 {
		v |= s & d
	}
	if op&4 != 0 {
		v |= s &^ d
	}
	if op&2 != 0 {
		v |= ^s & d
	}
	if op&1 != 0 {
		v |= ^s &^ d
	}
	return v
}

// blit handles a drawing call, answering whether it drew.
func (r *rtgBoard) blit(fn uint32) bool {
	c := r.amiga.CPU
	d := func(i int) int { return int(c.D[i] & 0xFFFF) }
	switch fn {
	case rtgFillRect:
		return r.fillRect(c.A[1], d(0), d(1), d(2), d(3), c.D[4], byte(c.D[5]), int(c.D[7]))
	case rtgInvertRect:
		return r.invertRect(c.A[1], d(0), d(1), d(2), d(3), byte(c.D[4]), int(c.D[7]))
	case rtgBlitRect:
		return r.blitRect(c.A[1], c.A[1], d(0), d(1), d(2), d(3), d(4), d(5), 0xC, byte(c.D[6]), int(c.D[7]))
	case rtgBlitRectNoMaskComplete:
		return r.blitRect(c.A[1], c.A[2], d(0), d(1), d(2), d(3), d(4), d(5), byte(c.D[6])&0xF, 0xFF, int(c.D[7]))
	case rtgBlitTemplate:
		return r.blitTemplate(c.A[1], c.A[2], d(0), d(1), d(2), d(3), byte(c.D[4]), int(c.D[7]))
	case rtgBlitPattern:
		return r.blitPattern(c.A[1], c.A[2], d(0), d(1), d(2), d(3), byte(c.D[4]), int(c.D[7]))
	case rtgBlitPlanar2Chunky:
		return r.planar2Chunky(c.A[1], c.A[2], 0, d(0), d(1), d(2), d(3), d(4), d(5), byte(c.D[6]), byte(c.D[7]))
	case rtgBlitPlanar2Direct:
		return r.planar2Chunky(c.A[1], c.A[2], c.A[3], d(0), d(1), d(2), d(3), d(4), d(5), byte(c.D[6]), byte(c.D[7]))
	}
	return false
}

// put writes a pen into a pixel: in a CLUT through the mask, else whole.
func put(dst, pen []byte, mask byte) {
	if len(dst) == 1 && mask != 0xFF {
		dst[0] = pen[0]&mask | dst[0]&^mask
		return
	}
	copy(dst, pen)
}

func (r *rtgBoard) fillRect(ri uint32, x, y, w, h int, pen uint32, mask byte, format int) bool {
	s, ok := r.surface(ri, x, y, w, h, format)
	if !ok {
		return false
	}
	p := pixel(pen, s.bpp)
	for j := 0; j < h; j++ {
		row := r.row(s, j, w)
		if s.bpp == 1 && mask == 0xFF {
			for i := range row {
				row[i] = p[0]
			}
			continue
		}
		for i := 0; i < len(row); i += s.bpp {
			put(row[i:i+s.bpp], p, mask)
		}
	}
	return true
}

func (r *rtgBoard) invertRect(ri uint32, x, y, w, h int, mask byte, format int) bool {
	s, ok := r.surface(ri, x, y, w, h, format)
	if !ok {
		return false
	}
	// Every bit of the colour, through the mask in a CLUT; the alpha
	// byte of a 32-bit pixel and the spare top bit of a 15-bit one (in
	// the second byte of the PC formats' little-endian word) stay as
	// they were.
	inv := make([]byte, s.bpp)
	for i := range inv {
		inv[i] = 0xFF
	}
	switch format {
	case rgbfCLUT:
		inv[0] = mask
	case rgbfA8R8G8B8, rgbfA8B8G8R8:
		inv[0] = 0
	case rgbfR8G8B8A8, rgbfB8G8R8A8:
		inv[3] = 0
	case rgbfR5G5B5:
		inv[0] = 0x7F
	case rgbfR5G5B5PC, rgbfB5G5R5PC:
		inv[1] = 0x7F
	}
	for j := 0; j < h; j++ {
		row := r.row(s, j, w)
		for i := range row {
			row[i] ^= inv[i%s.bpp]
		}
	}
	return true
}

// blitRect copies a rectangle, combining it with the destination by the
// minterm op and, in a CLUT, through the mask. Source and destination
// may overlap.
func (r *rtgBoard) blitRect(sri, dri uint32, sx, sy, dx, dy, w, h int, op, mask byte, format int) bool {
	src, ok1 := r.surface(sri, sx, sy, w, h, format)
	dst, ok2 := r.surface(dri, dx, dy, w, h, format)
	if !ok1 || !ok2 {
		return false
	}
	if dst.bpp > 1 {
		mask = 0xFF
	}
	n := w * src.bpp
	tmp := make([]byte, n*h)
	for j := 0; j < h; j++ {
		copy(tmp[j*n:], r.row(src, j, w))
	}
	for j := 0; j < h; j++ {
		row, from := r.row(dst, j, w), tmp[j*n:(j+1)*n]
		if op == 0xC && mask == 0xFF {
			copy(row, from)
			continue
		}
		for i := range row {
			v := minterm(op, from[i], row[i])
			row[i] = v&mask | row[i]&^mask
		}
	}
	return true
}

// blitTemplate draws a one-bit template (text, mostly) in the foreground
// pen, and in JAM2 the background pen where it is clear.
func (r *rtgBoard) blitTemplate(ri, t uint32, x, y, w, h int, mask byte, format int) bool {
	s, ok := r.surface(ri, x, y, w, h, format)
	if !ok {
		return false
	}
	a := r.amiga
	mem := a.peek32(t + tmplMemory)
	bpr := int(int16(a.peek16(t + tmplBytesPerRow)))
	xoff := int(a.peek8(t + tmplXOffset))
	mode := a.peek8(t + tmplDrawMode)
	fg, bg := pixel(a.peek32(t+tmplFgPen), s.bpp), pixel(a.peek32(t+tmplBgPen), s.bpp)
	bits := make([]byte, (xoff+w+7)/8)
	for j := 0; j < h; j++ {
		a.peekBytes(mem+uint32(j*bpr), bits)
		r.drawBits(r.row(s, j, w), s.bpp, func(i int) bool {
			b := xoff + i
			return bits[b>>3]&(0x80>>(b&7)) != 0
		}, mode, fg, bg, mask)
	}
	return true
}

// drawBits draws a row of one-bit pixels in a draw mode.
func (r *rtgBoard) drawBits(row []byte, bpp int, bit func(int) bool, mode byte, fg, bg []byte, mask byte) {
	cmask := mask
	if bpp > 1 {
		cmask = 0xFF
	}
	for i := 0; i < len(row)/bpp; i++ {
		set := bit(i)
		if mode&drawInversVid != 0 {
			set = !set
		}
		px := row[i*bpp : (i+1)*bpp]
		switch {
		case mode&drawComplement != 0:
			if set {
				for k := range px {
					px[k] ^= cmask
				}
			}
		case set:
			put(px, fg, mask)
		case mode&drawJAM2 != 0:
			put(px, bg, mask)
		}
	}
}

// blitPattern fills a rectangle with a 16-pixel-wide pattern of 2^Size
// rows, as BlitTemplate draws.
func (r *rtgBoard) blitPattern(ri, p uint32, x, y, w, h int, mask byte, format int) bool {
	s, ok := r.surface(ri, x, y, w, h, format)
	if !ok {
		return false
	}
	a := r.amiga
	mem := a.peek32(p + patMemory)
	xoff, yoff := int(a.peek16(p+patXOffset)), int(a.peek16(p+patYOffset))
	rows := 1 << (a.peek8(p+patSize) & 15)
	mode := a.peek8(p + patDrawMode)
	fg, bg := pixel(a.peek32(p+patFgPen), s.bpp), pixel(a.peek32(p+patBgPen), s.bpp)
	for j := 0; j < h; j++ {
		word := a.peek16(mem + uint32(2*((yoff+j)%rows)))
		r.drawBits(r.row(s, j, w), s.bpp, func(i int) bool {
			return word&(0x8000>>((xoff+i)&15)) != 0
		}, mode, fg, bg, mask)
	}
	return true
}

// planar2Chunky draws part of a planar BitMap (an Amiga bitmap: icons,
// mostly) on the card. Into a CLUT the pixel is the colour number,
// through the plane mask; into any other format (with a
// ColorIndexMapping, cim) it is the mapping's pixel value for it.
func (r *rtgBoard) planar2Chunky(bm, ri, cim uint32, sx, sy, dx, dy, w, h int, mt, mask byte) bool {
	a := r.amiga
	format := int(a.peek32(ri + riRGBFormat))
	if (cim == 0) != (format == rgbfCLUT) {
		return false
	}
	s, ok := r.surface(ri, dx, dy, w, h, format)
	if !ok {
		return false
	}
	op := mt // Picasso96's 4-bit opcode; an Amiga minterm byte has it on top
	if op > 0xF {
		op >>= 4
	}
	if cim != 0 && op != 0xC {
		return false // only a copy into the direct formats
	}
	bpr := int(a.peek16(bm + bmBytesPerRow))
	depth := min(int(a.peek8(bm+bmDepth)), 8)
	var planes [8]uint32
	for i := 0; i < depth; i++ {
		planes[i] = a.peek32(bm + bmPlanes + uint32(4*i))
	}
	var colors [256][]byte
	if cim != 0 {
		for i := range colors {
			colors[i] = pixel(a.peek32(cim+cimColors+uint32(4*i)), s.bpp)
		}
	}
	first, last := sx>>3, (sx+w-1)>>3
	line := make([]byte, last-first+1)
	idx := make([]byte, w)
	for j := 0; j < h; j++ {
		clear(idx)
		for p := 0; p < depth; p++ {
			bit := byte(1) << p
			switch planes[p] {
			case 0:
				continue
			case 0xFFFFFFFF:
				for i := range idx {
					idx[i] |= bit
				}
				continue
			}
			a.peekBytes(planes[p]+uint32((sy+j)*bpr+first), line)
			for i := range idx {
				b := sx + i - first*8
				if line[b>>3]&(0x80>>(b&7)) != 0 {
					idx[i] |= bit
				}
			}
		}
		row := r.row(s, j, w)
		if cim != 0 {
			for i, c := range idx {
				copy(row[i*s.bpp:], colors[c&mask])
			}
			continue
		}
		for i, c := range idx {
			v := minterm(op, c, row[i])
			row[i] = v&mask | row[i]&^mask
		}
	}
	return true
}

// rtgCheck is a verified call's expected result, the bytes it should
// have changed, and where its software drawing returns to.
type rtgCheck struct {
	what string
	off  int
	want []byte
	ret  uint32
}

// verifyCall works a call out on the side: it draws, keeps the bytes it
// changed, puts the memory back and answers that it didn't, so Picasso96
// draws it too. The software routine then returns through rtAreaVerify,
// which calls checkPending to compare the two: the caller's return
// address, above the stub's saved registers (15 longwords), the function
// number and uaelib's own return address, is swapped for it. Picasso96's
// routines call the card again (BlitRect goes through
// BlitRectNoMaskComplete), so the checks nest.
func (r *rtgBoard) verifyCall(fn uint32) {
	a := r.amiga
	c := a.CPU
	saved := bytes.Clone(r.vram.b)
	if !r.blit(fn) {
		return
	}
	lo, hi := -1, -1
	for i := range saved {
		if saved[i] != r.vram.b[i] {
			if lo < 0 {
				lo = i
			}
			hi = i
		}
	}
	if lo >= 0 {
		ret := c.A[7] + 8 + 60
		r.pending = append(r.pending, &rtgCheck{
			what: fmt.Sprintf("call %d D %08X A1 %08X A2 %08X", fn, c.D, c.A[1], c.A[2]),
			off:  lo, want: bytes.Clone(r.vram.b[lo : hi+1]), ret: a.peek32(ret),
		})
		a.poke32(ret, rtAreaBase+rtAreaVerify)
	}
	copy(r.vram.b, saved)
	r.Verified++
}

// checkPending compares what Picasso96 drew with the innermost call
// worked out, and gives rtAreaVerify the address to return to.
func (r *rtgBoard) checkPending() {
	if len(r.pending) == 0 {
		return
	}
	p := r.pending[len(r.pending)-1]
	r.pending = r.pending[:len(r.pending)-1]
	binary.BigEndian.PutUint32(r.area.mem[rtAreaVerifyRet:], p.ret)
	got := r.vram.b[p.off : p.off+len(p.want)]
	if !bytes.Equal(got, p.want) {
		r.Mismatches++
		i := 0
		for got[i] == p.want[i] {
			i++
		}
		r.tracef("MISMATCH %s: at +%d got % X want % X", p.what, p.off+i-int(r.fb-r.base),
			got[i:min(i+8, len(got))], p.want[i:min(i+8, len(got))])
	}
}
