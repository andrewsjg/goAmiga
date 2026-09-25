package m68k

import "fmt"

// opAdd implements ADD (pg. 4-4)
func opAdd(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	if sz == 3 {
		return opAdda(c)
	}

	// Check for ADDX: direction bit set and EA mode matches
	if c.op&0x0130 == 0x0100 {
		ea := c.op & 0x38 >> 3
		if ea == 0x00 || ea == 0x01 {
			return opAddx(c)
		}
	}

	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "add",
		sz:   sz,
	}
	c.PC += 2
	reg := (c.op & 0x0E00) >> 9
	dir := (c.op & 0x0100) >> 8
	ea := decodeEA(c.op)

	if dir == 0 { // <ea> + Dn -> Dn
		var src Int
		src, t.src, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		t.dst = fmt.Sprintf("D%d", reg)
		dst := NewInt(c.D[reg], int(t.sz))
		result := dst.Add(src)

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
		c.SR |= result.Status()
		if result.Status()&StatusCarry != 0 {
			c.SR |= StatusExtend
		}

		switch t.sz {
		case SizeByte:
			c.D[reg] = c.D[reg]&0xFFFFFF00 | uint32(result.Byte())
		case SizeWord:
			c.D[reg] = c.D[reg]&0xFFFF0000 | uint32(result.Word())
		case SizeLong:
			c.D[reg] = result.Long()
		}
	} else { // Dn + <ea> -> <ea>
		t.src = fmt.Sprintf("D%d", reg)
		s := c.saveRMW(ea)
		var dst Int
		dst, t.dst, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		src := NewInt(c.D[reg], int(t.sz))
		result := dst.Add(src)

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
		c.SR |= result.Status()
		if result.Status()&StatusCarry != 0 {
			c.SR |= StatusExtend
		}

		c.restoreRMW(s)
		t.dst, t.err = c.writeInt(ea, result)
	}
	return
}

// opAdda implements ADDA (pg. 4-7)
func opAdda(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	szBit := (c.op & 0x0100) >> 8
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "adda",
		dst:  fmt.Sprintf("A%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)

	if szBit == 0 { // word
		t.sz = SizeWord
		var src uint16
		src, t.src, t.err = c.readWord(ea)
		if t.err != nil {
			return
		}
		c.A[reg] = uint32(int32(c.A[reg]) + int32(int16(src)))
	} else { // long
		t.sz = SizeLong
		var src uint32
		src, t.src, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		c.A[reg] += src
	}
	// ADDA does not affect condition codes
	return
}

// opAddx implements ADDX (4-9)
func opAddx(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		op:   "addx",
		n:    1,
		sz:   sz,
	}
	c.PC += 2

	rx := (c.op & 0x0E00) >> 9
	ry := c.op & 0x07
	mod := (c.op & 0x08) >> 3

	x := uint32(0)
	if c.SR&StatusExtend != 0 {
		x = 1
	}

	if mod == 0 { // data register
		t.src = fmt.Sprintf("D%d", ry)
		t.dst = fmt.Sprintf("D%d", rx)

		// compute dst + src + X in one step to avoid flag accumulation
		a := c.D[rx] & sizeFilter[sz]
		b := c.D[ry] & sizeFilter[sz]
		full := uint64(a) + uint64(b) + uint64(x)
		n := uint32(full) & sizeFilter[sz]
		result := NewInt(n, int(t.sz))

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusNegative | StatusExtend)
		if n&signMask[sz] != 0 {
			c.SR |= StatusNegative
		}
		if full > uint64(sizeFilter[sz]) {
			c.SR |= StatusCarry | StatusExtend
		}
		as, bs, ns := a&signMask[sz], b&signMask[sz], n&signMask[sz]
		if as == bs && as != ns {
			c.SR |= StatusOverflow
		}
		// Z is only cleared, never set
		if n != 0 {
			c.SR &= ^StatusZero
		}

		switch t.sz {
		case SizeByte:
			c.D[rx] = c.D[rx]&0xFFFFFF00 | uint32(result.Byte())
		case SizeWord:
			c.D[rx] = c.D[rx]&0xFFFF0000 | uint32(result.Word())
		case SizeLong:
			c.D[rx] = result.Long()
		}
	} else { // memory with pre-decrement
		t.src = fmt.Sprintf("-(A%d)", ry)
		t.dst = fmt.Sprintf("-(A%d)", rx)

		switch t.sz {
		case SizeByte:
			c.A[ry]--
			c.A[rx]--
		case SizeWord:
			c.A[ry] -= 2
			c.A[rx] -= 2
		case SizeLong:
			c.A[ry] -= 4
			c.A[rx] -= 4
		}

		var srcVal, dstVal Int
		srcVal, _, t.err = c.readInt(0x10|ry, t.sz)
		if t.err != nil {
			return
		}
		dstVal, _, t.err = c.readInt(0x10|rx, t.sz)
		if t.err != nil {
			return
		}

		// compute dst + src + X in one step to avoid flag accumulation
		a := dstVal.Long() & sizeFilter[sz]
		b := srcVal.Long() & sizeFilter[sz]
		full := uint64(a) + uint64(b) + uint64(x)
		n := uint32(full) & sizeFilter[sz]
		result := NewInt(n, int(t.sz))

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusNegative | StatusExtend)
		if n&signMask[sz] != 0 {
			c.SR |= StatusNegative
		}
		if full > uint64(sizeFilter[sz]) {
			c.SR |= StatusCarry | StatusExtend
		}
		as, bs, ns := a&signMask[sz], b&signMask[sz], n&signMask[sz]
		if as == bs && as != ns {
			c.SR |= StatusOverflow
		}
		if n != 0 {
			c.SR &= ^StatusZero
		}

		_, t.err = c.writeInt(0x10|rx, result)
	}
	return
}
