package amiga

// AGA (Alice and Lisa, the A1200/A4000 chipset), on top of ECS:
//
//   - Alice reports ID $22 in VPOSR; Lisa answers DENISEID $F8.
//   - 8 bitplanes (BPLCON0 bit 4, BPU3), BPL7PT/BPL8PT.
//   - 256 colour registers of 24 bits. COLOR00-31 address a bank of 32
//     chosen by BPLCON3 bits 15-13; a write sets both nibbles of each
//     component, and a write with BPLCON3 LOCT (bit 9) then sets the low
//     nibbles.
//   - HAM8 (8 planes: 6 data bits, 64 base colours); EHB only with 6 planes.
//   - Dual playfield with up to 4+4 planes; playfield 2's colours start at
//     the PF2OF offset (BPLCON3 bits 12-10: 0,2,4,8,16,32,64,128; 8 by
//     default).
//   - BPLCON4: bitplane colour XOR (bits 15-8) and the sprite colour banks
//     for even (bits 7-4) and odd (bits 3-0) sprites.
//   - FMODE ($1FC): bitplane fetches 16, 32 or 64 bits wide (bits 1-0),
//     sprites 16, 32 or 64 pixels wide (bits 3-2).
//   - 2 MB of chip RAM.
//
//   - Super-hires with up to 8 planes; BPLCON1's longer, finer scroll.
//   - Sprites: BPLCON3 SPRES resolution, SPRxCTL's two extra position
//     bits, and BRDSPRT sprites in the border.

// expand12 widens a 12-bit OCS colour to 24 bits.
func expand12(c uint16) uint32 {
	r, g, b := uint32(c>>8&0xF), uint32(c>>4&0xF), uint32(c&0xF)
	return r*17<<16 | g*17<<8 | b*17
}

// rgba converts a 24-bit colour to the framebuffer's RGBA.
func rgba(c uint32) [4]byte { return [4]byte{byte(c >> 16), byte(c >> 8), byte(c), 0xFF} }

// halfBrite is EHB's half-brightness colour: OCS/ECS halve the 12-bit
// components, AGA the 8-bit ones.
func halfBrite(c uint32, aga bool) uint32 {
	if aga {
		return c >> 1 & 0x7F7F7F
	}
	half := func(b uint32) uint32 { return (b / 17 >> 1) * 17 }
	return half(c>>16&0xFF)<<16 | half(c>>8&0xFF)<<8 | half(c&0xFF)
}

// planesOf is the number of bitplanes BPLCON0 enables.
func planesOf(bplcon0 uint16, aga bool) int {
	n := int(bplcon0 >> 12 & 7)
	if aga {
		n |= int(bplcon0>>4&1) << 3
		return min(n, 8)
	}
	return min(n, 6)
}

// fetchFactor is how many words each bitplane fetch takes: 1, 2 or 4
// (FMODE bits 1-0 on AGA; always 1 before).
func (v *Video) fetchFactor() int {
	if !v.amiga.AGA {
		return 1
	}
	return [4]int{1, 2, 2, 4}[v.amiga.Custom.ReadReg16(0x1FC)&3]
}

// fetchUnitCCK is the length in colour clocks of one fetch unit: the time
// one plane's fetch covers in pixels (16 per word).
func fetchUnitCCK(hires, shres bool, factor int) int {
	switch {
	case shres:
		return 2 * factor
	case hires:
		return 4 * factor
	}
	return 8 * factor
}

// agaColorWrite handles a write to COLORxx on AGA: the bank in BPLCON3
// picks which of the 256 registers; LOCT picks the high or low nibbles.
// It returns the register and its old and new values for mid-line
// changes.
func (v *Video) agaColorWrite(n int, val uint16) (idx int, old, new uint32) {
	bplcon3 := v.amiga.Custom.ReadReg16(0x106)
	idx = int(bplcon3>>13)<<5 | n
	old = v.agaPal[idx]
	r, g, b := uint32(val>>8&0xF), uint32(val>>4&0xF), uint32(val&0xF)
	if bplcon3&0x0200 != 0 { // LOCT: the low nibbles
		new = old&0xF0F0F0 | r<<16 | g<<8 | b
	} else { // both nibbles
		new = r*17<<16 | g*17<<8 | b*17
	}
	v.agaPal[idx] = new
	return
}

// agaReset sets the registers whose reset values matter on AGA: BPLCON3
// (PF2OF = 3) and BPLCON4 (sprite banks at colour 16), and clears the
// palette.
func (c *CustomChips) agaReset() {
	c.WriteReg16(0x106, 0x0C00)
	c.WriteReg16(0x10C, 0x0011)
	if c.amiga.Video != nil {
		c.amiga.Video.agaPal = [256]uint32{}
	}
}
