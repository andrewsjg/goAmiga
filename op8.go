package m68k

import "fmt"

// opOr implements OR (4-150)
func opOr(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	if sz == 3 {
		return opDivu(c)
	}

	// Check for SBCD: 0xC1xx pattern with specific bits
	if c.op&0x01F0 == 0x0100 {
		return opSbcd(c)
	}

	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "or",
		sz:   sz,
	}
	c.PC += 2

	reg := (c.op & 0x0E00) >> 9
	dir := (c.op & 0x0100) >> 8
	ea := decodeEA(c.op)

	if dir == 0 { // <ea> OR Dn -> Dn
		var a, b Int
		a = NewInt(c.D[reg], int(t.sz))
		b, t.src, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		result := NewInt(a.Long()|b.Long(), int(t.sz))
		t.dst = fmt.Sprintf("D%d", reg)

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
	} else { // Dn OR <ea> -> <ea>
		t.src = fmt.Sprintf("D%d", reg)
		s := c.saveRMW(ea)
		var b Int
		b, t.dst, t.err = c.readInt(ea, t.sz)
		if t.err != nil {
			return
		}
		result := NewInt(c.D[reg]|b.Long(), int(t.sz))

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

// opDivu implements DIVU (4-97)
func opDivu(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "divu",
		sz:   SizeWord,
		dst:  fmt.Sprintf("D%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var divisor uint16
	divisor, t.src, t.err = c.readWord(ea)
	if t.err != nil {
		return
	}

	if divisor == 0 {
		// divide by zero — for now set error
		t.err = fmt.Errorf("divide by zero")
		return
	}

	dividend := c.D[reg]
	quotient := dividend / uint32(divisor)
	remainder := dividend % uint32(divisor)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)

	if quotient > 0xFFFF {
		c.SR |= StatusOverflow
		return
	}

	c.D[reg] = (remainder&0xFFFF)<<16 | (quotient & 0xFFFF)

	if quotient == 0 {
		c.SR |= StatusZero
	}
	if quotient&0x8000 != 0 {
		c.SR |= StatusNegative
	}
	return
}

// opDivs implements DIVS (4-93)
func opDivs(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "divs",
		sz:   SizeWord,
		dst:  fmt.Sprintf("D%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var src uint16
	src, t.src, t.err = c.readWord(ea)
	if t.err != nil {
		return
	}

	divisor := int32(int16(src))
	if divisor == 0 {
		t.err = fmt.Errorf("divide by zero")
		return
	}

	dividend := int32(c.D[reg])
	quotient := dividend / divisor
	remainder := dividend % divisor

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)

	if quotient > 32767 || quotient < -32768 {
		c.SR |= StatusOverflow
		return
	}

	c.D[reg] = (uint32(remainder)&0xFFFF)<<16 | (uint32(quotient) & 0xFFFF)

	if uint16(quotient) == 0 {
		c.SR |= StatusZero
	}
	if quotient < 0 {
		c.SR |= StatusNegative
	}
	return
}

// opSbcd implements SBCD (4-171)
func opSbcd(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "sbcd",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	rx := (c.op & 0x0E00) >> 9
	ry := c.op & 0x07
	x := byte(0)
	if c.SR&StatusExtend != 0 {
		x = 1
	}
	mod := (c.op & 0x08) >> 3

	var vx, vy byte
	if mod == 0 {
		t.src = fmt.Sprintf("D%d", ry)
		t.dst = fmt.Sprintf("D%d", rx)
		vx = byte(c.D[rx])
		vy = byte(c.D[ry])
	} else {
		t.src = fmt.Sprintf("-(A%d)", ry)
		t.dst = fmt.Sprintf("-(A%d)", rx)
		c.A[rx]--
		c.A[ry]--
		vx, t.err = c.M.Byte(int(c.A[rx]))
		if t.err != nil {
			return
		}
		vy, t.err = c.M.Byte(int(c.A[ry]))
		if t.err != nil {
			return
		}
	}

	c.SR &= ^(StatusCarry | StatusExtend)

	// packed BCD subtract
	lo := int16(vx&0x0F) - int16(vy&0x0F) - int16(x)
	if lo < 0 {
		lo += 10
	}
	hi := int16(vx>>4) - int16(vy>>4)
	if lo > 9 {
		hi--
	}
	if hi < 0 {
		hi += 10
		c.SR |= StatusCarry | StatusExtend
	}
	v := byte(hi<<4) | byte(lo&0x0F)

	if v != 0 {
		c.SR &= ^StatusZero
	}

	if mod == 0 {
		c.D[rx] = (c.D[rx] & 0xFFFFFF00) | uint32(v)
	} else {
		_, t.err = c.M.Write(int(c.A[rx]), []byte{v})
	}
	return
}
