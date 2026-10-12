package m68k

import "fmt"

// Packed BCD arithmetic (ABCD, SBCD, NBCD).
//
// The formulas are flamewing's, measured on real 68000s (and used by MAME and
// the SingleStepTests generator). They reproduce the hardware's handling of
// invalid BCD digits and its officially undefined N and V flags: V is the
// overflow of the decimal correction step, N is bit 7 of the result.

// bcdAdd returns dst + src + x in packed BCD, with the carry and overflow.
func bcdAdd(dst, src, x uint8) (res uint8, carry, overflow bool) {
	ss := dst + src + x
	bc := ((dst & src) | (^ss & (dst | src))) & 0x88 // binary carries out of bits 3, 7
	dc := uint8((((int(ss) + 0x66) ^ int(ss)) & 0x110) >> 1)
	corf := (bc | dc) - ((bc | dc) >> 2) // $06, $60 or $66
	rr := ss + corf
	return rr, (bc|(ss&^rr))&0x80 != 0, (^ss&rr)&0x80 != 0
}

// bcdSub returns dst - src - x in packed BCD, with the borrow and overflow.
func bcdSub(dst, src, x uint8) (res uint8, borrow, overflow bool) {
	dd := dst - src - x
	bc := ((^dst & src) | (dd & ^dst) | (dd & src)) & 0x88 // binary borrows
	corf := bc - (bc >> 2)
	rr := dd - corf
	return rr, (bc|(^dd&rr))&0x80 != 0, (dd&^rr)&0x80 != 0
}

// setBCDFlags sets X, C, V and N and clears Z if res is non-zero (Z is
// otherwise left alone, so a multi-byte operation can test the whole result).
func (c *Processor) setBCDFlags(res uint8, carry, overflow bool) {
	c.SR &^= StatusExtend | StatusCarry | StatusOverflow | StatusNegative
	if carry {
		c.SR |= StatusExtend | StatusCarry
	}
	if overflow {
		c.SR |= StatusOverflow
	}
	if res&0x80 != 0 {
		c.SR |= StatusNegative
	}
	if res != 0 {
		c.SR &^= StatusZero
	}
}

// predecByte returns -(An) for a byte access: A7 moves by 2 to keep the
// stack pointer even.
func (c *Processor) predecByte(reg uint16) uint32 {
	if reg == 7 {
		c.A[7] -= 2
	} else {
		c.A[reg]--
	}
	return c.A[reg]
}

// bcdOperands reads the operands of ABCD/SBCD: Dy,Dx or -(Ay),-(Ax) (source
// first). It returns src, dst and a function that stores the result.
func (c *Processor) bcdOperands(t *stepTrace) (src, dst uint8, store func(uint8)) {
	rx := (c.op >> 9) & 7
	ry := c.op & 7
	if c.op&0x08 == 0 {
		t.src, t.dst = fmt.Sprintf("D%d", ry), fmt.Sprintf("D%d", rx)
		return uint8(c.D[ry]), uint8(c.D[rx]), func(v uint8) {
			c.D[rx] = c.D[rx]&0xFFFFFF00 | uint32(v)
		}
	}
	t.src, t.dst = fmt.Sprintf("-(A%d)", ry), fmt.Sprintf("-(A%d)", rx)
	src, _ = c.M.Byte(int(c.predecByte(ry)))
	addr := c.predecByte(rx)
	dst, _ = c.M.Byte(int(addr))
	return src, dst, func(v uint8) { c.M.Write(int(addr), []byte{v}) }
}

func (c *Processor) extendBit() uint8 {
	if c.SR&StatusExtend != 0 {
		return 1
	}
	return 0
}

// opAbcd implements ABCD (4-3).
func opAbcd(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "abcd", sz: noSize, n: 1}
	c.PC += 2
	src, dst, store := c.bcdOperands(&t)
	res, carry, overflow := bcdAdd(dst, src, c.extendBit())
	c.setBCDFlags(res, carry, overflow)
	store(res)
	return
}

// opSbcd implements SBCD (4-176).
func opSbcd(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "sbcd", sz: noSize, n: 1}
	c.PC += 2
	src, dst, store := c.bcdOperands(&t)
	res, borrow, overflow := bcdSub(dst, src, c.extendBit())
	c.setBCDFlags(res, borrow, overflow)
	store(res)
	return
}

// opNbcd implements NBCD (4-132): 0 - <ea> - X in packed BCD.
func opNbcd(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "nbcd", sz: SizeByte, n: 1}
	c.PC += 2
	ea := decodeEA(c.op)
	rmw := c.saveRMW(ea)
	var v Int
	v, t.dst, t.err = c.readInt(ea, SizeByte)
	if t.err != nil {
		return
	}
	res, borrow, overflow := bcdSub(0, uint8(v.Long()), c.extendBit())
	c.setBCDFlags(res, borrow, overflow)
	c.restoreRMW(rmw)
	t.dst, t.err = c.writeInt(ea, NewInt(uint32(res), int(SizeByte)))
	return
}
