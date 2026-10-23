package m68k

import "fmt"

// opAndMulAbcdExg dispatches AND, MULU, MULS, ABCD, EXG in group 0xC
func opAndMulAbcdExg(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6

	// ABCD: 1100 xxx1 0000 xyyy
	if c.op&0x01F0 == 0x0100 {
		return opAbcd(c)
	}

	// EXG: 1100 xxx1 0100 0yyy or 1100 xxx1 0100 1yyy or 1100 xxx1 1000 1yyy
	opmode := (c.op & 0x01F8) >> 3
	if opmode == 0x28 || opmode == 0x29 || opmode == 0x31 {
		return opExgC(c)
	}

	// MULU: 1100 xxx0 11xx xyyy
	if sz == 3 && c.op&0x0100 == 0 {
		return opMulu(c)
	}

	// MULS: 1100 xxx1 11xx xyyy
	if sz == 3 && c.op&0x0100 != 0 {
		return opMuls(c)
	}

	return opAnd(c)
}

// opAnd implements AND (4-15)
func opAnd(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "and",
		sz:   sz,
	}
	c.PC += 2

	reg := (c.op & 0x0E00) >> 9
	dir := (c.op & 0x0100) >> 8
	ea := decodeEA(c.op)

	if dir == 0 { // <ea> AND Dn -> Dn
		var src Int
		src, t.src, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("D%d", reg)
		}
		result := NewInt(c.D[reg]&src.Long(), int(t.sz))

		// preserve upper bits of register for byte/word
		switch t.sz {
		case SizeByte:
			c.D[reg] = c.D[reg]&0xFFFFFF00 | uint32(result.Byte())
		case SizeWord:
			c.D[reg] = c.D[reg]&0xFFFF0000 | uint32(result.Word())
		case SizeLong:
			c.D[reg] = result.Long()
		}

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
		if result.Long()&sizeFilter[t.sz] == 0 {
			c.SR |= StatusZero
		}
		if result.IsNegative() {
			c.SR |= StatusNegative
		}
	} else { // Dn AND <ea> -> <ea>
		if c.TraceWriter != nil {
			t.src = fmt.Sprintf("D%d", reg)
		}
		s := c.saveRMW(ea)
		var dst Int
		dst, t.dst, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		result := NewInt(c.D[reg]&dst.Long(), int(t.sz))

		c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
		if result.Long()&sizeFilter[t.sz] == 0 {
			c.SR |= StatusZero
		}
		if result.IsNegative() {
			c.SR |= StatusNegative
		}

		c.restoreRMW(s)
		t.dst, t.err = c.writeInt(ea, result)
	}
	return
}

// opMulu implements MULU (4-139)
func opMulu(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "mulu",
		sz:   SizeWord,
		dst:  c.sprintf("D%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var src uint16
	src, t.src, t.err = c.readWord(ea)
	if t.err != nil {
		return
	}

	c.tim.count = popcount16(src)
	result := uint32(uint16(c.D[reg])) * uint32(src)
	c.D[reg] = result

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if result == 0 {
		c.SR |= StatusZero
	}
	if result&0x80000000 != 0 {
		c.SR |= StatusNegative
	}
	return
}

// opMuls implements MULS (4-136)
func opMuls(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "muls",
		sz:   SizeWord,
		dst:  c.sprintf("D%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var src uint16
	src, t.src, t.err = c.readWord(ea)
	if t.err != nil {
		return
	}

	c.tim.count = mulsPatterns(src)
	result := int32(int16(c.D[reg])) * int32(int16(src))
	c.D[reg] = uint32(result)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if result == 0 {
		c.SR |= StatusZero
	}
	if result < 0 {
		c.SR |= StatusNegative
	}
	return
}

// opExgC implements EXG in the 0xC group (4-105)
func opExgC(c *Processor) (t stepTrace) {
	rx := (c.op & 0x0E00) >> 9
	ry := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "exg",
		n:    1,
		sz:   SizeLong,
	}
	c.PC += 2

	opmode := (c.op & 0x00F8) >> 3
	switch opmode {
	case 0x08: // Dx, Dy
		if c.TraceWriter != nil {
			t.src = fmt.Sprintf("D%d", rx)
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("D%d", ry)
		}
		c.D[rx], c.D[ry] = c.D[ry], c.D[rx]
	case 0x09: // Ax, Ay
		if c.TraceWriter != nil {
			t.src = fmt.Sprintf("A%d", rx)
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("A%d", ry)
		}
		c.A[rx], c.A[ry] = c.A[ry], c.A[rx]
	case 0x11: // Dx, Ay
		if c.TraceWriter != nil {
			t.src = fmt.Sprintf("D%d", rx)
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("A%d", ry)
		}
		c.D[rx], c.A[ry] = c.A[ry], c.D[rx]
	default:
		t.err = errBadOpcode
	}
	return
}
