package m68k

import "fmt"

// opCmpEor dispatches CMP, CMPA, CMPM, and EOR in the 0xB group
func opCmpEor(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	dir := (c.op & 0x0100) >> 8

	if sz == 3 {
		return opCmpa(c)
	}

	if dir == 1 {
		ea := decodeEA(c.op)
		mod := ea & 0x38 >> 3
		if mod == 0x01 {
			return opCmpm(c)
		}
		return opEor(c)
	}

	return opCmp(c)
}

// opCmp implements CMP (pg. 4-75)
func opCmp(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "cmp",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2

	ea := decodeEA(c.op)
	reg := c.op & 0x0E00 >> 9

	var a, b Int
	a = NewInt(c.D[reg], int(t.sz))
	b, t.src, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}
	if c.TraceWriter != nil {
		t.dst = fmt.Sprintf("D%d", reg)
	}

	result := a.Sub(b)
	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	c.SR |= result.Status()
	return
}

// opCmpa implements CMPA (4-77)
func opCmpa(c *Processor) (t stepTrace) {
	reg := (c.op & 0x0E00) >> 9
	szBit := (c.op & 0x0100) >> 8
	t = stepTrace{
		addr: c.PC,
		op:   "cmpa",
		n:    1,
		dst:  c.sprintf("A%d", reg),
	}
	c.PC += 2

	ea := decodeEA(c.op)

	var src int32
	if szBit == 0 { // word
		t.sz = SizeWord
		var n uint16
		n, t.src, t.err = c.readWord(ea)
		if t.err != nil {
			return
		}
		src = int32(int16(n))
	} else { // long
		t.sz = SizeLong
		var n uint32
		n, t.src, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		src = int32(n)
	}

	// always compare as long
	a := NewInt(c.A[reg], SizeLong)
	b := NewInt(uint32(src), SizeLong)
	result := a.Sub(b)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	c.SR |= result.Status()
	return
}

// opCmpm implements CMPM (4-81)
func opCmpm(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		op:   "cmpm",
		n:    1,
		sz:   sz,
	}
	c.PC += 2

	rx := (c.op & 0x0E00) >> 9
	ry := c.op & 0x07
	if c.TraceWriter != nil {
		t.src = fmt.Sprintf("(A%d)+", ry)
	}
	if c.TraceWriter != nil {
		t.dst = fmt.Sprintf("(A%d)+", rx)
	}

	// read source (Ay)+
	srcEA := uint16(0x18 | ry) // mode 3 (post-increment) + register
	var src Int
	src, _, t.err = c.readInt(srcEA, t.sz)
	if t.err != nil {
		return
	}

	// read destination (Ax)+
	dstEA := uint16(0x18 | rx)
	var dst Int
	dst, _, t.err = c.readInt(dstEA, t.sz)
	if t.err != nil {
		return
	}

	result := dst.Sub(src)
	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	c.SR |= result.Status()
	return
}

// opEor implements EOR (4-100)
func opEor(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		op:   "eor",
		n:    1,
		sz:   sz,
	}
	c.PC += 2

	reg := (c.op & 0x0E00) >> 9
	ea := decodeEA(c.op)
	if c.TraceWriter != nil {
		t.src = fmt.Sprintf("D%d", reg)
	}

	s := c.saveRMW(ea)
	var dst Int
	dst, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	result := NewInt(c.D[reg]^dst.Long(), int(t.sz))

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if result.Long()&sizeFilter[t.sz] == 0 {
		c.SR |= StatusZero
	}
	if result.IsNegative() {
		c.SR |= StatusNegative
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, result)
	return
}
