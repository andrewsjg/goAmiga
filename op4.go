package m68k

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// opLea implements LEA (pg. 4-110)
func opLea(c *Processor) (t stepTrace) {
	an := (c.op & 0x0E00) >> 9
	t = stepTrace{
		addr: c.PC,
		op:   "lea",
		sz:   noSize,
		dst:  fmt.Sprintf("A%d", an),
	}
	c.PC += 2

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		t.err = errBadAddress
		return

	case 0x02: // memory address
		c.A[an] = c.A[reg]
		t.src = fmt.Sprintf("(A%d)", reg)

	case 0x05: // (d16,An)
		var d int16
		d, t.err = c.M.Sword(int(c.PC))
		if t.err != nil {
			return
		}
		c.PC += 2
		c.A[an] = uint32(int32(c.A[reg]) + int32(d))
		t.src = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // (d8,An,Xn)
		c.A[an], t.src, t.err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))

	case 0x07: // other
		switch reg {
		default:
			t.err = errBadAddress
			return

		case 0x00: // absolute word
			var n uint16
			n, _, t.err = c.readImmWord()
			c.A[an] = uint32(wordToInt32(n))
			t.src = fmt.Sprintf("$%X", int32(n))

		case 0x01: // absolute long
			var n uint32
			n, _, t.err = c.readImmLong()
			c.A[an] = n
			t.src = fmt.Sprintf("$%X", int32(n))

		case 0x02: // program counter with displacement
			var n uint16
			n, _, t.err = c.readImmWord()
			disp := wordToInt32(n)
			c.A[an] = uint32(int32(c.PC) + disp - 2)
			t.src = fmt.Sprintf("($%X,PC)", disp)

		case 0x03: // program counter with index
			c.A[an], t.src, t.err = c.decodeBriefExt(c.PC, "PC")
		}
	}

	return
}

// opClr implements CLR (4-73).
// Clear operand.
func opClr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "clr",
		sz:   c.op >> 6 & 0x3,
	}
	c.PC += 2

	switch t.sz {
	default:
		t.err = errBadOpSize

	case SizeByte:
		t.dst, t.err = c.writeByte(c.op, 0)

	case SizeWord:
		t.dst, t.err = c.writeWord(c.op, 0)

	case SizeLong:
		t.dst, t.err = c.writeLong(c.op, 0)
	}
	if t.err != nil {
		return
	}
	c.SR &= 0xFFFFFFF0
	c.SR |= StatusZero
	return
}

// opMoveToSr implements MOVE to SR (6-19). Privileged instruction.
func opMoveToSr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "move",
		sz:   noSize,
		dst:  "SR",
	}

	var ok bool
	ok, t.err = c.checkPrivilege(t.addr)
	if !ok {
		return
	}

	c.PC += 2

	var n uint16
	n, t.src, t.err = c.readWord(c.op)
	if t.err != nil {
		return
	}

	c.setSR(uint32(n) & StatusMask)
	return
}

// opMovem implements MOVEM (pg. 4-128)
func opMovem(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "movem",
		sz:   []uint16{SizeWord, SizeLong}[c.op&0x40>>6],
	}
	c.PC += 2
	regl, _, _ := c.readImmWord() // register list
	dir := c.op & 0x0400 >> 10    // direction

	if dir == 0 {
		// register to memory
		// For the predecrement addressing mode (-(An), EA mode bits = 100),
		// the register list mask is encoded in REVERSE order:
		//   bit 0 = A7, bit 1 = A6, ..., bit 7 = A0, bit 8 = D7, ..., bit 15 = D0
		// Registers are stored from highest-numbered to lowest-numbered, so the
		// highest-numbered register ends up at the HIGHEST memory address and the
		// lowest-numbered register (last pushed) ends up at the LOWEST address.
		// We iterate bits 0→15 (last→first push order), then REVERSE the buffer
		// so the first-pushed (highest-numbered) value sits at the highest address.
		//
		// For all other addressing modes (control, absolute etc.), the mask is
		// in normal order (bit 0=D0 ... bit 15=A7) and the buffer is written
		// sequentially starting at the EA address.
		mod := c.op & 0x38 >> 3 // EA mode field
		isPredec := mod == 0x04 // predecrement -(An)

		t.src = fmt.Sprintf("$%X", regl)
		b := &bytes.Buffer{}
		for i := uint16(0); i < 16; i++ {
			ok := regl & (1 << i)
			if ok == 0 {
				continue // skip unset registers
			}

			// read register value
			// For predecrement: bit 0=A7, ..., bit 7=A0, bit 8=D7, ..., bit 15=D0
			// For other modes:  bit 0=D0, ..., bit 7=D7, bit 8=A0, ..., bit 15=A7
			var v uint32
			if isPredec {
				if i < 8 {
					v = c.A[7-i]
				} else {
					v = c.D[7-(i-8)]
				}
			} else {
				if i < 8 {
					v = c.D[i]
				} else {
					v = c.A[i-8]
				}
			}

			// write value to buffer
			binary.BigEndian.PutUint32(c.buf[:4], v)
			if t.sz == SizeWord { // movem.w <list>,<ea>
				b.Write(c.buf[2:4])
			} else { // movem.l <list>,<ea>
				b.Write(c.buf[:4])
			}
		}

		buf := b.Bytes()
		if isPredec {
			// Reverse the buffer so the last-pushed register (lowest-numbered,
			// first in our iteration) ends up at the LOWEST memory address.
			stride := 4
			if t.sz == SizeWord {
				stride = 2
			}
			// Reverse in chunks of stride bytes
			for lo, hi := 0, len(buf)-stride; lo < hi; lo, hi = lo+stride, hi-stride {
				for k := 0; k < stride; k++ {
					buf[lo+k], buf[hi+k] = buf[hi+k], buf[lo+k]
				}
			}
		}

		// flush buffer to memory
		t.dst, t.err = c.writeBytes(c.op, buf)
		return
	}

	// memory to register
	t.dst = fmt.Sprintf("$%X", regl)

	// count registers
	regc := 0
	for i := uint16(0); i < 16; i++ {
		if regl&(1<<i) != 0 {
			regc++
		}
	}

	// read values from memory
	buflen := regc * 2
	if t.sz == SizeLong {
		buflen = regc * 4
	}
	t.src, t.err = c.readBytes(c.op, c.buf[:buflen])
	if t.err != nil {
		return
	}

	// set register values
	x := 0
	for i := uint16(0); i < 16; i++ {
		ok := regl & (1 << i)
		if ok == 0 {
			continue // skip unset registers
		}
		var v uint32
		if t.sz == SizeWord { // movem.w <ea>,<list>
			v = uint32(wordToInt32(binary.BigEndian.Uint16(c.buf[x : x+2])))
			x += 2
		} else { // movem.l <ea>,<list>
			v = binary.BigEndian.Uint32(c.buf[x : x+4])
			x += 4
		}
		if i < 8 {
			c.D[i] = v
		} else {
			c.A[i-8] = v
		}
	}

	return
}

// opTrap implements TRAP (4-188)
func opTrap(c *Processor) (t stepTrace) {
	v := c.op & 0x000F
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "trap",
		sz:   noSize,
		src:  fmt.Sprintf("#%d", v),
	}
	c.PC += 2
	v += 32
	if c.handlers[v] != nil {
		// Custom Go handler registered — call it inline
		t.err = c.handlers[v].Trap(c, int(v))
	} else {
		// No Go handler — vector through exception table like real 68000
		t.err = c.RaiseException(int(v))
	}
	return
}

// opMoveUsp implements MOVE USP (pg. 6-21)
func opMoveUsp(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "move",
		n:    1,
		sz:   noSize,
	}

	var ok bool
	ok, t.err = c.checkPrivilege(t.addr)
	if !ok {
		return
	}

	c.PC += 2
	dir := c.op & 0x08 >> 3
	if dir == 0 { // An to USP
		t.src, t.dst = fmt.Sprintf("A%d", reg), "USP"
		c.USP = c.A[reg]
	} else { // USP to An
		t.src, t.dst = "USP", fmt.Sprintf("A%d", reg)
		c.A[reg] = c.USP
	}
	return
}

// opStop implements STOP (6-85). Privileged instruction.
func opStop(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "stop",
		sz:   noSize,
	}

	var ok bool
	ok, t.err = c.checkPrivilege(t.addr)
	if !ok {
		return
	}

	c.PC += 2

	var n uint16
	n, t.err = c.M.Word(int(c.PC))
	if t.err != nil {
		return
	}
	c.PC += 2
	t.dst = fmt.Sprintf("#$%X", n)
	c.setSR(uint32(n) & StatusMask)
	t.err = c.Stop()
	return
}

// opRts implements RTS (Return from Subroutine) (4-169)
func opRts(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "rts",
		sz:   noSize,
		n:    1,
	}
	// move program counter to the address at the top of the stack
	c.PC, t.err = c.M.Long(int(c.A[7]))
	if t.err != nil {
		return
	}
	// increment stack pointer
	c.A[7] += 4
	return
}

// opJsr implements JSR (4-109)
func opJsr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "jsr",
		n:    1,
		sz:   noSize,
	}
	c.PC += 2

	// compute jump address and mutate PC to return address
	var jmp uint32
	mod := c.op & 0x38 >> 3
	reg := c.op & 0x07
	switch mod {
	default:
		t.err = errBadAddress
		return

	case 0x02: // address register indirect
		jmp = c.A[reg]
		t.src = fmt.Sprintf("(A%d)", reg)

	case 0x05: // (d16,An)
		var d int16
		d, t.err = c.M.Sword(int(c.PC))
		if t.err != nil {
			return
		}
		c.PC += 2
		jmp = uint32(int32(c.A[reg]) + int32(d))
		t.src = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // (d8,An,Xn)
		jmp, t.src, t.err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if t.err != nil {
			return
		}

	case 0x07: // other
		switch reg {
		default:
			t.err = errBadAddress
			return

		case 0x00: // absolute word
			var n uint16
			n, _, t.err = c.readImmWord()
			jmp = uint32(wordToInt32(n))
			t.src = fmt.Sprintf("$%X", int32(n))

		case 0x01: // absolute long
			var n uint32
			n, _, t.err = c.readImmLong()
			jmp = n
			t.src = fmt.Sprintf("$%X", int32(n))

		case 0x02: // program counter with displacement
			var n uint16
			n, _, t.err = c.readImmWord()
			disp := wordToInt32(n)
			jmp = uint32(int32(c.PC) + disp - 2)
			t.src = fmt.Sprintf("($%X,PC)", disp)

		case 0x03: // program counter with index
			jmp, t.src, t.err = c.decodeBriefExt(c.PC, "PC")
			if t.err != nil {
				return
			}
		}
	}

	// push return address (current PC) to stack
	c.A[7] -= 4
	binary.BigEndian.PutUint32(c.buf[:4], c.PC)
	_, t.err = c.M.Write(int(c.A[7]), c.buf[:4])
	if t.err != nil {
		return
	}

	// jump
	c.PC = jmp
	return
}

// opTst implements TST (4-192)
func opTst(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "tst",
		sz:   (c.op & 0xC0) >> 6,
		n:    1,
	}
	c.PC += 2
	c.SR &= 0xFFFFFFF0 // reset last 4 bits only
	switch t.sz {
	case SizeByte:
		var b byte
		b, t.src, t.err = c.readByte(c.op)
		c.setStatusForByte(b)

	case SizeWord:
		var n uint16
		n, t.src, t.err = c.readWord(c.op)
		c.setStatusForWord(n)

	case SizeLong:
		var n uint32
		n, t.src, t.err = c.readLong(c.op)
		c.setStatusForLong(n)

	default:
		t.err = errBadOpSize
	}
	return
}

// opJmp implements JMP (4-108)
func opJmp(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "jmp",
		n:    1,
		sz:   noSize,
	}
	c.PC += 2

	mod := c.op & 0x38 >> 3
	reg := c.op & 0x07
	switch mod {
	default:
		t.err = errBadAddress
		return

	case 0x02: // address register indirect
		c.PC = c.A[reg]
		t.src = fmt.Sprintf("(A%d)", reg)

	case 0x05: // address register indirect with displacement
		var d int16
		d, t.err = c.M.Sword(int(c.PC))
		if t.err != nil {
			return
		}
		c.PC = uint32(int32(c.A[reg]) + int32(d))
		t.src = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, t.src, t.err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if t.err != nil {
			return
		}
		c.PC = addr

	case 0x07:
		switch reg {
		default:
			t.err = errBadAddress
			return

		case 0x00: // absolute word
			var n uint16
			n, _, t.err = c.readImmWord()
			c.PC = uint32(wordToInt32(n))
			t.src = fmt.Sprintf("$%X", wordToInt32(n))

		case 0x01: // absolute long
			var n uint32
			n, _, t.err = c.readImmLong()
			c.PC = n
			t.src = fmt.Sprintf("$%X", int32(n))

		case 0x02: // program counter with displacement
			var n uint16
			n, _, t.err = c.readImmWord()
			disp := wordToInt32(n)
			c.PC = uint32(int32(c.PC) + disp - 2)
			t.src = fmt.Sprintf("($%X,PC)", disp)

		case 0x03: // program counter with index
			var addr uint32
			addr, t.src, t.err = c.decodeBriefExt(c.PC, "PC")
			if t.err != nil {
				return
			}
			c.PC = addr
		}
	}
	return
}

// opPea implements PEA (4-149)
func opPea(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "pea",
		n:    1,
		sz:   noSize,
	}
	c.PC += 2

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	var addr uint32
	switch mod {
	default:
		t.err = errBadAddress
		return

	case 0x02: // (An)
		addr = c.A[reg]
		t.src = fmt.Sprintf("(A%d)", reg)

	case 0x05: // (d16,An)
		var d int16
		d, t.err = c.M.Sword(int(c.PC))
		if t.err != nil {
			return
		}
		c.PC += 2
		addr = uint32(int32(c.A[reg]) + int32(d))
		t.src = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // (d8,An,Xn)
		addr, t.src, t.err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if t.err != nil {
			return
		}

	case 0x07:
		switch reg {
		default:
			t.err = errBadAddress
			return
		case 0x00:
			var n uint16
			n, _, t.err = c.readImmWord()
			addr = uint32(wordToInt32(n))
			t.src = fmt.Sprintf("$%X", wordToInt32(n))
		case 0x01:
			addr, _, t.err = c.readImmLong()
			t.src = fmt.Sprintf("$%X", int32(addr))
		case 0x02:
			var n uint16
			n, _, t.err = c.readImmWord()
			disp := wordToInt32(n)
			addr = uint32(int32(c.PC) + disp - 2)
			t.src = fmt.Sprintf("($%X,PC)", disp)
		case 0x03:
			addr, t.src, t.err = c.decodeBriefExt(c.PC, "PC")
		}
	}
	if t.err != nil {
		return
	}

	// push address to stack
	c.A[7] -= 4
	binary.BigEndian.PutUint32(c.buf[:4], addr)
	_, t.err = c.M.Write(int(c.A[7]), c.buf[:4])
	return
}

// opNop implements NOP (4-147)
func opNop(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "nop",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2
	return
}

// opReset implements RESET - asserts RESET line to external devices (privileged)
func opReset(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "reset",
		sz:   noSize,
		n:    1,
	}

	var ok bool
	ok, t.err = c.checkPrivilege(t.addr)
	if !ok {
		return
	}

	c.PC += 2
	if c.ResetHandler != nil {
		c.ResetHandler()
	}
	return
}

// opLink implements LINK (4-112)
func opLink(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "link",
		sz:   noSize,
		n:    1,
		src:  fmt.Sprintf("A%d", reg),
	}
	c.PC += 2

	var disp uint16
	disp, _, t.err = c.readImmWord()
	if t.err != nil {
		return
	}
	t.dst = fmt.Sprintf("#$%X", wordToInt32(disp))

	// push An onto stack
	c.A[7] -= 4
	binary.BigEndian.PutUint32(c.buf[:4], c.A[reg])
	_, t.err = c.M.Write(int(c.A[7]), c.buf[:4])
	if t.err != nil {
		return
	}

	// An = SP
	c.A[reg] = c.A[7]

	// SP = SP + displacement
	c.A[7] = uint32(int32(c.A[7]) + int32(int16(disp)))
	return
}

// opUnlk implements UNLK (4-194)
func opUnlk(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "unlk",
		sz:   noSize,
		n:    1,
		src:  fmt.Sprintf("A%d", reg),
	}
	c.PC += 2

	// SP = An
	c.A[7] = c.A[reg]

	// pop An from stack
	c.A[reg], t.err = c.M.Long(int(c.A[7]))
	if t.err != nil {
		return
	}
	c.A[7] += 4
	return
}

// opRte implements RTE (6-84)
func opRte(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "rte",
		sz:   noSize,
		n:    1,
	}

	var ok bool
	ok, t.err = c.checkPrivilege(t.addr)
	if !ok {
		return
	}

	// pop SR from stack
	var sr uint16
	sr, t.err = c.M.Word(int(c.A[7]))
	if t.err != nil {
		return
	}
	c.A[7] += 2

	// pop PC from stack
	c.PC, t.err = c.M.Long(int(c.A[7]))
	if t.err != nil {
		return
	}
	c.A[7] += 4

	// apply SR (may switch from supervisor to user mode, swapping stacks)
	c.setSR(uint32(sr) & StatusMask)
	return
}

// opNeg implements NEG (4-144)
func opNeg(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "neg",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	s := c.saveRMW(ea)
	var val Int
	val, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	zero := NewInt(0, int(t.sz))
	result := zero.Sub(val)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
	c.SR |= result.Status()
	if result.Status()&StatusCarry != 0 {
		c.SR |= StatusExtend
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, result)
	return
}

// opNegx implements NEGX (4-146)
func opNegx(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "negx",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	s := c.saveRMW(ea)
	var val Int
	val, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	x := uint32(0)
	if c.SR&StatusExtend != 0 {
		x = 1
	}

	// compute 0 - val - X in one step to avoid flag accumulation
	sz := int(t.sz)
	src := val.Long() & sizeFilter[sz]
	n := (0 - src - x) & sizeFilter[sz]
	result := NewInt(n, sz)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusNegative | StatusExtend)
	if n&signMask[sz] != 0 {
		c.SR |= StatusNegative
	}
	// C = X = borrow: set if src or x is nonzero
	if src != 0 || x != 0 {
		c.SR |= StatusCarry | StatusExtend
	}
	// V = source negative AND result negative
	if src&signMask[sz] != 0 && n&signMask[sz] != 0 {
		c.SR |= StatusOverflow
	}
	// Z: only cleared, never set
	if n != 0 {
		c.SR &= ^StatusZero
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, result)
	return
}

// opNot implements NOT (4-148)
func opNot(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "not",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	s := c.saveRMW(ea)
	var val Int
	val, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}

	result := NewInt(^val.Long()&sizeFilter[t.sz], int(t.sz))
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

// opExt implements EXT (4-106)
func opExt(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "ext",
		n:    1,
		dst:  fmt.Sprintf("D%d", reg),
	}
	c.PC += 2

	opmode := (c.op & 0x01C0) >> 6
	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)

	switch opmode {
	case 0x02: // byte to word
		t.sz = SizeWord
		v := int8(c.D[reg])
		result := uint16(int16(v))
		c.D[reg] = c.D[reg]&0xFFFF0000 | uint32(result)
		if result == 0 {
			c.SR |= StatusZero
		}
		if result&0x8000 != 0 {
			c.SR |= StatusNegative
		}
	case 0x03: // word to long
		t.sz = SizeLong
		v := int16(c.D[reg])
		result := uint32(int32(v))
		c.D[reg] = result
		if result == 0 {
			c.SR |= StatusZero
		}
		if result&0x80000000 != 0 {
			c.SR |= StatusNegative
		}
	default:
		t.err = errBadOpcode
	}
	return
}

// opSwap implements SWAP (4-185)
func opSwap(c *Processor) (t stepTrace) {
	reg := c.op & 0x07
	t = stepTrace{
		addr: c.PC,
		op:   "swap",
		n:    1,
		sz:   SizeWord,
		dst:  fmt.Sprintf("D%d", reg),
	}
	c.PC += 2

	c.D[reg] = (c.D[reg]>>16)&0xFFFF | (c.D[reg]&0xFFFF)<<16

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if c.D[reg] == 0 {
		c.SR |= StatusZero
	}
	if c.D[reg]&0x80000000 != 0 {
		c.SR |= StatusNegative
	}
	return
}

// opScc implements Scc (4-173)
func opScc(c *Processor) (t stepTrace) {
	cc := (c.op & 0x0F00) >> 8
	t = stepTrace{
		addr: c.PC,
		op:   fmt.Sprintf("s%s", conditionStrings[cc]),
		n:    1,
		sz:   SizeByte,
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var val byte
	if testCond(c, cc) {
		val = 0xFF
	}
	t.dst, t.err = c.writeByte(ea, val)
	return
}

// opMoveFromSr implements MOVE from SR (6-17)
func opMoveFromSr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "move",
		n:    1,
		sz:   noSize,
		src:  "SR",
	}
	c.PC += 2

	ea := decodeEA(c.op)
	t.dst, t.err = c.writeWord(ea, uint16(c.SR))
	return
}

// opMoveToCcr implements MOVE to CCR (4-123)
func opMoveToCcr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "move",
		n:    1,
		sz:   noSize,
		dst:  "CCR",
	}
	c.PC += 2

	var n uint16
	n, t.src, t.err = c.readWord(c.op)
	if t.err != nil {
		return
	}

	c.SR = (c.SR & 0xFFFFFF00) | (uint32(n) & 0x1F)
	return
}

// opTas implements TAS (4-186)
func opTas(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "tas",
		n:    1,
		sz:   SizeByte,
	}
	c.PC += 2

	ea := decodeEA(c.op)
	var b byte
	b, t.dst, t.err = c.readByte(ea)
	if t.err != nil {
		return
	}

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if b == 0 {
		c.SR |= StatusZero
	}
	if b&0x80 != 0 {
		c.SR |= StatusNegative
	}

	b |= 0x80
	t.dst, t.err = c.writeByte(ea, b)
	return
}
