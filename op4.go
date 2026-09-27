package m68k

import (
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
	if t.sz > SizeLong {
		t.err = errBadOpSize
		return
	}

	// The 68000's CLR reads its destination before writing zero (so an
	// odd address faults on the read).
	ea := decodeEA(c.op)
	rmw := c.saveRMW(ea)
	if _, _, t.err = c.readInt(ea, t.sz); t.err != nil {
		return
	}
	c.restoreRMW(rmw)
	t.dst, t.err = c.writeInt(ea, NewInt(0, int(t.sz)))
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

// opMovem implements MOVEM (4-128), one register at a time as the 68000
// does, so an address error happens at the right transfer:
//   - registers to memory: D0..A7 upwards from the address, or for -(An)
//     A7..D0 downwards from An (mask bit 0 = A7); -(An) stores An's value
//     from before the instruction;
//   - memory to registers: D0..A7 upwards; words are sign-extended.
//
// An is written back (with the final address) only once the transfer is
// done, so for (An)+ it overrides a value loaded into An. If the first
// transfer faults, -(An) is unchanged and (An)+ is left advanced by 2.
func opMovem(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   "movem",
		sz:   []uint16{SizeWord, SizeLong}[c.op&0x40>>6],
	}
	c.PC += 2
	regl, _, _ := c.readImmWord() // register list
	c.tim.count = popcount16(regl)
	toRegs := c.op&0x0400 != 0
	mode, reg := c.op>>3&7, c.op&7
	size := uint32(2)
	if t.sz == SizeLong {
		size = 4
	}

	var addr uint32
	switch mode {
	case 3, 4:
		addr = c.A[reg]
		if mode == 3 {
			t.src = fmt.Sprintf("(A%d)+", reg)
			// A fault on the first transfer leaves An advanced by 2.
			defer func() {
				if r := recover(); r != nil {
					c.A[reg] = addr + 2
					panic(r)
				}
			}()
		} else {
			t.dst = fmt.Sprintf("-(A%d)", reg)
		}
	default:
		var opr string
		if addr, opr, t.err = c.controlAddr(c.op & 0x3F); t.err != nil {
			return
		}
		if toRegs {
			t.src = opr
		} else {
			t.dst = opr
		}
	}
	if !toRegs {
		t.src = fmt.Sprintf("$%X", regl)
		regs := append(c.D[:8:8], c.A[:]...) // values before the transfer
		for i := uint16(0); i < 16; i++ {
			if regl&(1<<i) == 0 {
				continue
			}
			var v uint32
			if mode == 4 {
				v = regs[15-i] // bit 0 = A7 ... bit 15 = D0
				addr -= size
			} else {
				v = regs[i]
			}
			if mode == 4 && size == 4 {
				// A long is written low word first, going down.
				if t.err = c.writeAt(addr+2, NewInt(v&0xFFFF, SizeWord)); t.err != nil {
					return
				}
				if t.err = c.writeAt(addr, NewInt(v>>16, SizeWord)); t.err != nil {
					return
				}
			} else if t.err = c.writeAt(addr, NewInt(v, int(t.sz))); t.err != nil {
				return
			}
			if mode != 4 {
				addr += size
			}
		}
		if mode == 4 {
			c.A[reg] = addr
		}
		return
	}

	t.dst = fmt.Sprintf("$%X", regl)
	for i := uint16(0); i < 16; i++ {
		if regl&(1<<i) == 0 {
			continue
		}
		var v uint32
		if size == 2 {
			var w uint16
			if w, t.err = c.M.Word(int(addr)); t.err != nil {
				return
			}
			v = uint32(wordToInt32(w))
		} else if v, t.err = c.M.Long(int(addr)); t.err != nil {
			return
		}
		addr += size
		if i < 8 {
			c.D[i] = v
		} else {
			c.A[i-8] = v
		}
	}
	if mode == 3 {
		c.A[reg] = addr
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

	// An odd target faults on the prefetch from it, before the return
	// address is pushed.
	if jmp&1 != 0 && !c.NoAddressErrors {
		// Timed as JSR less its push and final prefetch, plus 50.
		cyc := 50 + controlCycles(c.op&0x3F, [7]int{16, 18, 22, 18, 20, 18, 22}) - 16
		panic(&addressError{addr: jmp, program: true, pc: jmp - 4, sr: c.SR, cycles: cyc})
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

	// SP = An, then pop An. The pop is written last, so UNLK A7 leaves
	// A7 = the popped value.
	c.A[7] = c.A[reg]
	var v uint32
	if v, t.err = c.M.Long(int(c.A[7])); t.err != nil {
		return
	}
	c.A[7] += 4
	c.A[reg] = v
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
		c.tim.taken = true
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

	// Like CLR, the 68000 reads the destination before writing it.
	ea := decodeEA(c.op)
	sr := uint16(c.SR)
	rmw := c.saveRMW(ea)
	if _, _, t.err = c.readWord(ea); t.err != nil {
		return
	}
	c.restoreRMW(rmw)
	t.dst, t.err = c.writeWord(ea, sr)
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
	rmw := c.saveRMW(ea) // one address calculation for the read and the write
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
	c.restoreRMW(rmw)
	t.dst, t.err = c.writeByte(ea, b)
	return
}

// opChk implements CHK.W (4-69): trap (vector 6) if Dn < 0 or Dn > <ea>.
// Flags as on the 68000: Z from Dn, V and C cleared, and on a trap N set if
// Dn < 0 and cleared if Dn > <ea>.
func opChk(c *Processor) (t stepTrace) {
	reg := (c.op >> 9) & 7
	t = stepTrace{addr: c.PC, op: "chk", sz: SizeWord, n: 1, dst: fmt.Sprintf("D%d", reg)}
	c.PC += 2
	var bound uint16
	bound, t.src, t.err = c.readWord(c.op)
	if t.err != nil {
		return
	}
	dn, ub := int16(c.D[reg]), int16(bound)
	c.SR &^= StatusZero | StatusOverflow | StatusCarry
	if dn == 0 {
		c.SR |= StatusZero
	}
	if dn < 0 && dn <= ub {
		c.tim.count = 1 // trapped on the sign test, not the bound (2 more cycles)
	}
	switch {
	case dn < 0:
		c.SR |= StatusNegative
	case dn > ub:
		c.SR &^= StatusNegative
	default:
		return
	}
	t.err = c.RaiseException(VecCHK)
	return
}

// opTrapv implements TRAPV (4-191): trap (vector 7) if V is set.
func opTrapv(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "trapv", sz: noSize, n: 1}
	c.PC += 2
	if c.SR&StatusOverflow != 0 {
		t.err = c.RaiseException(VecTRAPV)
	}
	return
}

// opRtr implements RTR (4-170): pop the CCR, then the PC.
func opRtr(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "rtr", sz: noSize, n: 1}
	var ccr uint16
	if ccr, t.err = c.M.Word(int(c.A[7])); t.err != nil {
		return
	}
	var pc uint32
	if pc, t.err = c.M.Long(int(c.A[7] + 2)); t.err != nil {
		return
	}
	c.A[7] += 6
	c.SR = c.SR&0xFF00 | uint32(ccr&0x1F)
	c.PC = pc
	return
}
