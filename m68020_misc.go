package m68k

import (
	"fmt"
)

// opcodes020Misc: the 68020's remaining additions (bit fields, CAS, CAS2,
// CHK2, CMP2, PACK, UNPK, MOVES). Order matters: CAS2 before CAS.L, and
// both before MOVES (whose size-3 slot they occupy).
var opcodes020Misc = []opcode{
	{0xE8C0, 0xF8C0, opBitField},
	{0x0CFC, 0xFDFF, opCas2}, // CAS2.W $0CFC, CAS2.L $0EFC
	{0x0AC0, 0xFFC0, opCas},  // CAS.B
	{0x0CC0, 0xFFC0, opCas},  // CAS.W
	{0x0EC0, 0xFFC0, opCas},  // CAS.L
	{0x00C0, 0xFFC0, opChk2}, // CHK2/CMP2.B
	{0x02C0, 0xFFC0, opChk2}, // .W
	{0x04C0, 0xFFC0, opChk2}, // .L
	{0x0E00, 0xFF00, opMoves},
	{0x8140, 0xF1F0, opPack},
	{0x8180, 0xF1F0, opUnpk},
}

// casSize maps CAS's size field (bits 10-9: 1 byte, 2 word, 3 long).
func casSize(op uint16) (sz uint16, n int, ok bool) {
	switch op >> 9 & 3 {
	case 1:
		return SizeByte, 1, true
	case 2:
		return SizeWord, 2, true
	case 3:
		return SizeLong, 4, true
	}
	return 0, 0, false
}

// chk2Size maps CHK2/CMP2's size field (bits 10-9: 0 byte, 1 word, 2 long).
func chk2Size(op uint16) (sz uint16, n int, ok bool) {
	switch op >> 9 & 3 {
	case 0:
		return SizeByte, 1, true
	case 1:
		return SizeWord, 2, true
	case 2:
		return SizeLong, 4, true
	}
	return 0, 0, false
}

func (c *Processor) readN(addr uint32, n int) (uint32, error) {
	switch n {
	case 1:
		b, err := c.M.Byte(int(addr))
		return uint32(b), err
	case 2:
		w, err := c.M.Word(int(addr))
		return uint32(w), err
	}
	return c.M.Long(int(addr))
}

func (c *Processor) writeN(addr uint32, n int, v uint32) error {
	switch n {
	case 1:
		c.buf[0] = byte(v)
	case 2:
		c.buf[0], c.buf[1] = byte(v>>8), byte(v)
	default:
		c.buf[0], c.buf[1], c.buf[2], c.buf[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	_, err := c.M.Write(int(addr), c.buf[:n])
	return err
}

func byteMask(n int) uint32 {
	if n == 4 {
		return 0xFFFFFFFF
	}
	return uint32(1)<<(8*n) - 1
}

// cmpFlags sets N Z V C as CMP does for dst - src at n bytes.
func (c *Processor) cmpFlags(dst, src uint32, n int) {
	m := byteMask(n)
	d, s := dst&m, src&m
	r := (d - s) & m
	top := uint32(1) << (8*uint(n) - 1)
	c.SR &^= StatusNegative | StatusZero | StatusOverflow | StatusCarry
	if r&top != 0 {
		c.SR |= StatusNegative
	}
	if r == 0 {
		c.SR |= StatusZero
	}
	if (d^s)&(d^r)&top != 0 {
		c.SR |= StatusOverflow
	}
	if s > d {
		c.SR |= StatusCarry
	}
}

func signExtend(v uint32, n int) uint32 {
	switch n {
	case 1:
		return uint32(int32(int8(v)))
	case 2:
		return uint32(int32(int16(v)))
	}
	return v
}

// eaAddr computes the address of a memory operand of n bytes for modes
// (An), (An)+, -(An) and the control modes, updating An as the mode does.
func (c *Processor) eaAddr(ea uint16, n int) (uint32, string, error) {
	reg := ea & 7
	step := uint32(n)
	if reg == 7 && n == 1 {
		step = 2 // A7 stays even
	}
	switch ea >> 3 & 7 {
	case 3: // (An)+
		a := c.A[reg]
		c.A[reg] += step
		return a, fmt.Sprintf("(A%d)+", reg), nil
	case 4: // -(An)
		c.A[reg] -= step
		return c.A[reg], fmt.Sprintf("-(A%d)", reg), nil
	}
	return c.controlAddr(ea)
}

// opCas implements CAS Dc,Du,<ea>: if the operand equals Dc, write Du to
// it; otherwise load it into Dc. Flags as CMP <ea>-Dc.
func opCas(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "cas", n: 1}
	sz, n, _ := casSize(c.op)
	t.sz = sz
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	dc, du := ext&7, ext>>6&7
	addr, opr, err := c.eaAddr(decodeEA(c.op), n)
	if err != nil {
		t.err = err
		return
	}
	v, err := c.readN(addr, n)
	if err != nil {
		t.err = err
		return
	}
	c.cmpFlags(v, c.D[dc], n)
	if c.SR&StatusZero != 0 {
		t.err = c.writeN(addr, n, c.D[du])
	} else {
		m := byteMask(n)
		c.D[dc] = c.D[dc]&^m | v&m
	}
	t.src, t.dst = fmt.Sprintf("D%d,D%d", dc, du), opr
	return
}

// opCas2 implements CAS2 Dc1:Dc2,Du1:Du2,(Rn1):(Rn2).
func opCas2(c *Processor) (t stepTrace) {
	n := 2
	t = stepTrace{addr: c.PC, op: "cas2", sz: SizeWord, n: 1}
	if c.op&0x0200 != 0 {
		n, t.sz = 4, SizeLong
	}
	c.PC += 2
	ext1, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	ext2, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	reg := func(e uint16) uint32 {
		if e&0x8000 != 0 {
			return c.A[e>>12&7]
		}
		return c.D[e>>12&7]
	}
	a1, a2 := reg(ext1), reg(ext2)
	v1, err := c.readN(a1, n)
	if err != nil {
		t.err = err
		return
	}
	v2, err := c.readN(a2, n)
	if err != nil {
		t.err = err
		return
	}
	dc1, dc2 := ext1&7, ext2&7
	c.cmpFlags(v1, c.D[dc1], n)
	if c.SR&StatusZero != 0 {
		c.cmpFlags(v2, c.D[dc2], n)
	}
	if c.SR&StatusZero != 0 {
		if t.err = c.writeN(a1, n, c.D[ext1>>6&7]); t.err != nil {
			return
		}
		t.err = c.writeN(a2, n, c.D[ext2>>6&7])
		return
	}
	m := byteMask(n)
	c.D[dc1] = c.D[dc1]&^m | v1&m
	c.D[dc2] = c.D[dc2]&^m | v2&m
	return
}

// opChk2 implements CHK2 and CMP2 <ea>,Rn: Rn against the bounds pair at
// <ea> (lower, then upper). Z if Rn equals a bound, C if it is outside
// them; CHK2 then takes the CHK exception. For an address register the
// bounds are sign-extended and the whole register compared. Bounds whose
// lower is above the upper wrap around.
func opChk2(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "cmp2", n: 1}
	sz, n, _ := chk2Size(c.op)
	t.sz = sz
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	chk := ext&0x0800 != 0
	if chk {
		t.op = "chk2"
	}
	addr, opr, err := c.controlAddr(decodeEA(c.op))
	if err != nil {
		t.err = err
		return
	}
	lower, err := c.readN(addr, n)
	if err != nil {
		t.err = err
		return
	}
	upper, err := c.readN(addr+uint32(n), n)
	if err != nil {
		t.err = err
		return
	}
	r := ext >> 12 & 7
	var v uint32
	if ext&0x8000 != 0 {
		v = c.A[r]
		lower, upper = signExtend(lower, n), signExtend(upper, n)
	} else {
		m := byteMask(n)
		v, lower, upper = c.D[r]&m, lower&m, upper&m
	}
	c.SR &^= StatusZero | StatusCarry
	if v == lower || v == upper {
		c.SR |= StatusZero
	}
	var out bool
	if lower <= upper {
		out = v < lower || v > upper
	} else {
		out = v > upper && v < lower
	}
	if out {
		c.SR |= StatusCarry
	}
	t.src = opr
	if out && chk {
		t.err = c.RaiseException(VecCHK)
	}
	return
}

// opMoves implements MOVES: a privileged move to or from the address space
// SFC/DFC name. With no MMU there is one address space, so it is a move.
func opMoves(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "moves", n: 1}
	var n int
	switch c.op >> 6 & 3 {
	case 0:
		n, t.sz = 1, SizeByte
	case 1:
		n, t.sz = 2, SizeWord
	case 2:
		n, t.sz = 4, SizeLong
	}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	addr, opr, err := c.eaAddr(decodeEA(c.op), n)
	if err != nil {
		t.err = err
		return
	}
	r := ext >> 12 & 7
	regp := &c.D[r]
	if ext&0x8000 != 0 {
		regp = &c.A[r]
	}
	t.src = opr
	if ext&0x0800 != 0 { // register to memory
		t.err = c.writeN(addr, n, *regp)
		return
	}
	v, err := c.readN(addr, n)
	if err != nil {
		t.err = err
		return
	}
	if ext&0x8000 != 0 {
		*regp = signExtend(v, n)
	} else {
		m := byteMask(n)
		*regp = *regp&^m | v&m
	}
	return
}

// opPack implements PACK Dx,Dy,#adj and PACK -(Ax),-(Ay),#adj: a word of
// two unpacked BCD digits (plus the adjustment) packed into a byte.
func opPack(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "pack", sz: noSize, n: 1}
	c.PC += 2
	adj, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	x, y := c.op&7, c.op>>9&7
	pack := func(w uint16) byte { w += adj; return byte(w>>4&0xF0 | w&0x0F) }
	if c.op&8 == 0 {
		c.D[y] = c.D[y]&^0xFF | uint32(pack(uint16(c.D[x])))
		return
	}
	c.A[x] -= 2
	w, err := c.M.Word(int(c.A[x]))
	if err != nil {
		t.err = err
		return
	}
	c.A[y]--
	if y == 7 {
		c.A[y]--
	}
	t.err = c.writeN(c.A[y], 1, uint32(pack(w)))
	return
}

// opUnpk implements UNPK Dx,Dy,#adj and UNPK -(Ax),-(Ay),#adj: a packed BCD
// byte spread into a word (one digit per byte), plus the adjustment.
func opUnpk(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "unpk", sz: noSize, n: 1}
	c.PC += 2
	adj, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	x, y := c.op&7, c.op>>9&7
	unpk := func(b byte) uint16 { return (uint16(b&0xF0)<<4 | uint16(b&0x0F)) + adj }
	if c.op&8 == 0 {
		c.D[y] = c.D[y]&^0xFFFF | uint32(unpk(byte(c.D[x])))
		return
	}
	c.A[x]--
	if x == 7 {
		c.A[x]--
	}
	b, err := c.M.Byte(int(c.A[x]))
	if err != nil {
		t.err = err
		return
	}
	c.A[y] -= 2
	t.err = c.writeN(c.A[y], 2, uint32(unpk(b)))
	return
}
