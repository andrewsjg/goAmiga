package amiga

import "image"

// The picture: the chipset's (Video), or the RTG card's while Picasso96
// has switched the display to it. The window, screenshots and the mouse
// go by these rather than by Video directly.

// Picture returns the frame to show: RGBA pixels, w×h.
func (a *Amiga) Picture() (pix []byte, w, h int) {
	if r := a.RTG; r.showing() {
		r.render()
		return r.pix, r.width, r.height
	}
	v := a.Video
	return v.Pixels, v.Width, v.Height
}

// PictureSize is the size to show the picture at on the host: the card's
// mode as it is, or the chipset's frame as Video.DisplaySize has it.
func (a *Amiga) PictureSize() (w, h int) {
	if r := a.RTG; r.showing() {
		return r.width, r.height
	}
	return a.Video.DisplaySize()
}

// PictureImage returns the picture as an image.
func (a *Amiga) PictureImage() *image.RGBA {
	pix, w, h := a.Picture()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, pix)
	return img
}

// showing reports whether the card has the display: switched on, in a
// mode, with a framebuffer in its memory.
func (r *rtgBoard) showing() bool {
	return r != nil && r.on && r.width > 0 && r.height > 0 && r.rowPixels > 0 &&
		r.fb >= r.base && r.fb < r.base+uint32(len(r.vram.b)) &&
		r.format > 0 && r.format < len(rgbfBytes)
}

// render converts the visible part of the framebuffer, in the card's
// pixel format, to RGBA in r.pix.
func (r *rtgBoard) render() {
	w, h := r.width, r.height
	if len(r.pix) != w*h*4 {
		r.pix = make([]byte, w*h*4)
	}
	bpp := rgbfBytes[r.format]
	stride := r.rowPixels * bpp
	vram := r.vram.b
	start := int(r.fb-r.base) + r.yoff*stride + r.xoff*bpp
	for y := 0; y < h; y++ {
		out := r.pix[y*w*4 : (y+1)*w*4]
		from := start + y*stride
		if from < 0 || from >= len(vram) {
			clear(out)
			continue
		}
		row := vram[from:min(from+w*bpp, len(vram))]
		n := len(row) / bpp
		r.convertRow(out[:n*4], row[:n*bpp])
		clear(out[n*4:])
	}
}

// convertRow converts one row of pixels in the card's format to RGBA.
func (r *rtgBoard) convertRow(out, in []byte) {
	// 16-bit formats: a word, big-endian or (PC) little-endian, with
	// 5 or 6 bits of green and red or blue on top.
	rgb16 := func(pc, six, bgr bool) {
		for i, o := 0, 0; i+1 < len(in); i, o = i+2, o+4 {
			v := uint16(in[i])<<8 | uint16(in[i+1])
			if pc {
				v = v>>8 | v<<8
			}
			var hi, g, lo uint16
			if six {
				hi, g, lo = v>>11, v>>5&0x3F, v&0x1F
				g = g<<2 | g>>4
			} else {
				hi, g, lo = v>>10&0x1F, v>>5&0x1F, v&0x1F
				g = g<<3 | g>>2
			}
			hi, lo = hi<<3|hi>>2, lo<<3|lo>>2
			if bgr {
				hi, lo = lo, hi
			}
			out[o], out[o+1], out[o+2], out[o+3] = byte(hi), byte(g), byte(lo), 0xFF
		}
	}
	// Byte formats: where red, green and blue are in each pixel.
	bytes := func(bpp, ri, gi, bi int) {
		for i, o := 0, 0; i+bpp <= len(in); i, o = i+bpp, o+4 {
			out[o], out[o+1], out[o+2], out[o+3] = in[i+ri], in[i+gi], in[i+bi], 0xFF
		}
	}
	switch r.format {
	case rgbfCLUT:
		for i, c := range in {
			p := &r.palette[c]
			out[4*i], out[4*i+1], out[4*i+2], out[4*i+3] = p[0], p[1], p[2], 0xFF
		}
	case rgbfR8G8B8:
		bytes(3, 0, 1, 2)
	case rgbfB8G8R8:
		bytes(3, 2, 1, 0)
	case rgbfA8R8G8B8:
		bytes(4, 1, 2, 3)
	case rgbfA8B8G8R8:
		bytes(4, 3, 2, 1)
	case rgbfR8G8B8A8:
		bytes(4, 0, 1, 2)
	case rgbfB8G8R8A8:
		bytes(4, 2, 1, 0)
	case rgbfR5G6B5PC:
		rgb16(true, true, false)
	case rgbfR5G5B5PC:
		rgb16(true, false, false)
	case rgbfR5G6B5:
		rgb16(false, true, false)
	case rgbfR5G5B5:
		rgb16(false, false, false)
	case rgbfB5G6R5PC:
		rgb16(true, true, true)
	case rgbfB5G5R5PC:
		rgb16(true, false, true)
	}
}
