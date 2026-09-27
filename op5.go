package m68k

import "fmt"

// opAddq implements ADDQ (4-11)
func opAddq(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "addq",
		sz:   sz,
	}
	c.PC += 2

	data := uint32((c.op & 0x0E00) >> 9)
	if data == 0 {
		data = 8
	}
	t.src = fmt.Sprintf("#%d", data)

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3

	// address register: entire 32-bit, no flags
	if mod == 0x01 {
		reg := ea & 0x07
		c.A[reg] += data
		t.dst = fmt.Sprintf("A%d", reg)
		return
	}

	s := c.saveRMW(ea)
	var val Int
	val, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	addend := NewInt(data, int(t.sz))
	result := val.Add(addend)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
	c.SR |= result.Status()
	if result.Status()&StatusCarry != 0 {
		c.SR |= StatusExtend
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, result)
	return
}

// opSubq implements SUBQ (4-181)
func opSubq(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "subq",
		sz:   sz,
	}
	c.PC += 2

	data := uint32((c.op & 0x0E00) >> 9)
	if data == 0 {
		data = 8
	}
	t.src = fmt.Sprintf("#%d", data)

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3

	// address register: entire 32-bit, no flags
	if mod == 0x01 {
		reg := ea & 0x07
		c.A[reg] -= data
		t.dst = fmt.Sprintf("A%d", reg)
		return
	}

	s := c.saveRMW(ea)
	var val Int
	val, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	sub := NewInt(data, int(t.sz))
	result := val.Sub(sub)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
	c.SR |= result.Status()
	if result.Status()&StatusCarry != 0 {
		c.SR |= StatusExtend
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, result)
	return
}

// opDBcc implements DBcc (pg. 4-90)
// Test, decrement and branch.
func opDBcc(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	cc := c.op & 0x0F00 >> 8
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   fmt.Sprintf("db%s", conditionStrings[cc]),
		src:  fmt.Sprintf("D%d", reg),
		sz:   noSize,
	}
	c.PC += 2

	// compute displacement
	var dw uint16
	dw, _, t.err = c.readImmWord()
	if t.err != nil {
		return
	}
	disp := wordToInt32(dw)
	t.dst = fmt.Sprintf("($%X,PC)", disp)

	// test condition and return if true
	if testCond(c, cc) {
		return
	}

	// decrement register
	var n uint16
	n = uint16(c.D[reg]) - 1
	c.D[reg] = c.D[reg]&0xFFFF0000 | uint32(n)

	// branch if n != -1
	c.tim.expired = n == 0xFFFF
	c.tim.taken = !c.tim.expired
	if n != 0xFFFF {
		c.PC = uint32(int32(c.PC) + disp - 2)
	}
	return
}
