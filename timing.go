package m68k

// Instruction timing, from the 68000 User's Manual tables (cycle counts
// include instruction prefetch and operand fetches) and Jorge Cwik's exact
// DIVU/DIVS algorithms. Instructions record the few facts timing depends on
// in c.tim during execution; Step then sets LastCycles from cycles().

// timingInfo holds what an instruction's timing depends on beyond its
// opcode.
type timingInfo struct {
	taken     bool // branch or DBcc branch taken; Scc condition true
	expired   bool // DBcc counter expired
	count     int  // shift count; MOVEM register count; MULx pattern count
	divCycles int  // DIVU/DIVS execution time
	exception int  // vector of an exception the instruction took (0: none)
	trapped   bool // CHK/TRAPV/zero divide raised its exception
}

// eaCycles is the effective address calculation time for mode/reg ea.
func eaCycles(ea uint16, long bool) int {
	n := 0
	switch ea >> 3 & 7 {
	case 0, 1:
		return 0
	case 2, 3:
		n = 4
	case 4:
		n = 6
	case 5:
		n = 8
	case 6:
		n = 10
	case 7:
		switch ea & 7 {
		case 0, 2: // (xxx).W, (d16,PC)
			n = 8
		case 1: // (xxx).L
			n = 12
		case 3: // (d8,PC,Xn)
			n = 10
		case 4: // #imm
			n = 4
		}
	}
	if long {
		n += 4
	}
	return n
}

// moveDstCycles is MOVE's destination write time: like eaCycles, except
// that -(An) costs no more than (An).
func moveDstCycles(ea uint16, long bool) int {
	if ea>>3&7 == 4 {
		ea = ea&7 | 2<<3
	}
	return eaCycles(ea, long)
}

// controlCycles returns the per-mode time for JMP, JSR, LEA and PEA:
// (An), (d16,An), (d8,An,Xn), (xxx).W, (xxx).L, (d16,PC), (d8,PC,Xn).
func controlCycles(ea uint16, times [7]int) int {
	switch ea >> 3 & 7 {
	case 2:
		return times[0]
	case 5:
		return times[1]
	case 6:
		return times[2]
	case 7:
		switch ea & 7 {
		case 0:
			return times[3]
		case 1:
			return times[4]
		case 2:
			return times[5]
		case 3:
			return times[6]
		}
	}
	return 4
}

func isRegDirect(ea uint16) bool { return ea>>3&7 < 2 }

// cycles returns the clock periods for the instruction just executed.
func (c *Processor) cycles() int {
	op := c.op
	ea := op & 0x3F
	size := op >> 6 & 3
	long := size == 2

	// Exceptions an instruction raised (TRAP, CHK, TRAPV, zero divide).
	if c.tim.trapped {
		switch c.tim.exception {
		case VecCHK: // 2 more when Dn is negative (and within the bound)
			return 38 + 2*c.tim.count + eaCycles(ea, false)
		case VecDivideByZero:
			return 38 + eaCycles(ea, false)
		default:
			return 34
		}
	}

	switch op >> 12 {
	case 0x0:
		switch {
		case op&0xF138 == 0x0108: // MOVEP
			if op&0x40 != 0 {
				return 24
			}
			return 16
		case op&0xFF00 == 0x0800: // BTST/BCHG/BCLR/BSET #imm
			kind := op >> 6 & 3
			if isRegDirect(ea) {
				// Changing a bit in the low word of Dn is 2 cycles faster.
				if kind != 0 && c.tim.count < 16 {
					return [4]int{10, 10, 12, 10}[kind]
				}
				return [4]int{10, 12, 14, 12}[kind]
			}
			if kind == 0 {
				return 8 + eaCycles(ea, false)
			}
			return 12 + eaCycles(ea, false)
		case op&0xF100 == 0x0100: // BTST/BCHG/BCLR/BSET Dn
			kind := op >> 6 & 3
			if isRegDirect(ea) {
				if kind != 0 && c.tim.count < 16 {
					return [4]int{6, 6, 8, 6}[kind]
				}
				return [4]int{6, 8, 10, 8}[kind]
			}
			if kind == 0 {
				if ea == 0x3C { // BTST Dn,#imm
					return 10
				}
				return 4 + eaCycles(ea, false)
			}
			return 8 + eaCycles(ea, false)
		case ea == 0x3C: // ORI/ANDI/EORI to CCR/SR
			return 20
		default: // ORI ANDI SUBI ADDI EORI CMPI
			cmp := op&0xFF00 == 0x0C00
			if isRegDirect(ea) {
				switch {
				case long && cmp:
					return 14
				case long:
					return 16
				}
				return 8
			}
			if cmp {
				if long {
					return 12 + eaCycles(ea, true)
				}
				return 8 + eaCycles(ea, false)
			}
			if long {
				return 20 + eaCycles(ea, true)
			}
			return 12 + eaCycles(ea, false)
		}

	case 0x1, 0x2, 0x3: // MOVE, MOVEA
		l := op>>12 == 2
		return 4 + eaCycles(ea, l) + moveDstCycles(decodeExEA(op), l)

	case 0x4:
		switch {
		case op == 0x4E70: // RESET
			return 132
		case op == 0x4E71, op == 0x4E72: // NOP, STOP
			return 4
		case op == 0x4E73, op == 0x4E77: // RTE, RTR
			return 20
		case op == 0x4E75: // RTS
			return 16
		case op == 0x4E76: // TRAPV (no trap)
			return 4
		case op&0xFFF0 == 0x4E40: // TRAP
			return 34
		case op&0xFFF8 == 0x4E50: // LINK
			return 16
		case op&0xFFF8 == 0x4E58: // UNLK
			return 12
		case op&0xFFF0 == 0x4E60: // MOVE USP
			return 4
		case op&0xFFC0 == 0x4E80: // JSR
			return controlCycles(ea, [7]int{16, 18, 22, 18, 20, 18, 22})
		case op&0xFFC0 == 0x4EC0: // JMP
			return controlCycles(ea, [7]int{8, 10, 14, 10, 12, 10, 14})
		case op&0xF1C0 == 0x41C0: // LEA
			return controlCycles(ea, [7]int{4, 8, 12, 8, 12, 8, 12})
		case op&0xFFF8 == 0x4840: // SWAP
			return 4
		case op&0xFFC0 == 0x4840: // PEA
			return controlCycles(ea, [7]int{12, 16, 20, 16, 20, 16, 20})
		case op&0xFFB8 == 0x4880: // EXT
			return 4
		case op&0xFB80 == 0x4880: // MOVEM
			perReg := 4
			if op&0x40 != 0 {
				perReg = 8
			}
			n := perReg * c.tim.count
			if m := ea >> 3 & 7; m == 3 || m == 4 { // (An)+, -(An): as (An)
				ea = ea&7 | 2<<3
			}
			if op&0x0400 != 0 { // memory to registers
				return controlCycles(ea, [7]int{12, 16, 18, 16, 20, 16, 18}) + n
			}
			return controlCycles(ea, [7]int{8, 12, 14, 12, 16, 12, 14}) + n
		case op&0xF1C0 == 0x4180: // CHK (no trap)
			return 10 + eaCycles(ea, false)
		case op&0xFFC0 == 0x4800: // NBCD
			if isRegDirect(ea) {
				return 6
			}
			return 8 + eaCycles(ea, false)
		case op&0xFFC0 == 0x40C0: // MOVE from SR
			if isRegDirect(ea) {
				return 6
			}
			return 8 + eaCycles(ea, false)
		case op&0xFFC0 == 0x44C0, op&0xFFC0 == 0x46C0: // MOVE to CCR/SR
			return 12 + eaCycles(ea, false)
		case op&0xFFC0 == 0x4AC0: // TAS
			if isRegDirect(ea) {
				return 4
			}
			return 10 + eaCycles(ea, false)
		case op&0xFF00 == 0x4A00: // TST
			return 4 + eaCycles(ea, long)
		default: // NEGX CLR NEG NOT
			if isRegDirect(ea) {
				if long {
					return 6
				}
				return 4
			}
			if long {
				return 12 + eaCycles(ea, true)
			}
			return 8 + eaCycles(ea, false)
		}

	case 0x5:
		switch {
		case op&0xF0F8 == 0x50C8: // DBcc
			switch {
			case !c.tim.taken && !c.tim.expired: // condition true
				return 12
			case c.tim.expired:
				return 14
			}
			return 10
		case op&0xF0C0 == 0x50C0: // Scc
			if isRegDirect(ea) {
				if c.tim.taken {
					return 6
				}
				return 4
			}
			return 8 + eaCycles(ea, false)
		default: // ADDQ SUBQ
			switch {
			case ea>>3&7 == 1:
				if long {
					return 6 // SingleStepTests; the manual says 8
				}
				return 8
			case isRegDirect(ea):
				if long {
					return 8
				}
				return 4
			case long:
				return 12 + eaCycles(ea, true)
			}
			return 8 + eaCycles(ea, false)
		}

	case 0x6:
		switch {
		case op&0xFF00 == 0x6100: // BSR
			return 18
		case c.tim.taken:
			return 10
		case op&0xFF == 0:
			return 12
		}
		return 8

	case 0x7: // MOVEQ
		return 4

	case 0x8, 0xC:
		switch {
		case op&0xF1C0 == 0x80C0, op&0xF1C0 == 0x81C0: // DIVU DIVS
			return c.tim.divCycles + eaCycles(ea, false)
		case op&0xF1C0 == 0xC0C0, op&0xF1C0 == 0xC1C0: // MULU MULS
			return 38 + 2*c.tim.count + eaCycles(ea, false)
		case op&0xF1F0 == 0x8100, op&0xF1F0 == 0xC100: // SBCD ABCD
			if op&0x08 != 0 {
				return 18
			}
			return 6
		case op&0xF130 == 0xC100: // EXG
			return 6
		}
		return aluCycles(op, ea, size)

	case 0x9, 0xD:
		switch {
		case op&0xC0 == 0xC0: // ADDA SUBA
			if op&0x100 == 0 {
				return 8 + eaCycles(ea, false)
			}
			if isRegDirect(ea) || ea == 0x3C {
				return 8 + eaCycles(ea, true)
			}
			return 6 + eaCycles(ea, true)
		case op&0x130 == 0x100: // ADDX SUBX
			if op&0x08 != 0 {
				if long {
					return 30
				}
				return 18
			}
			if long {
				return 8
			}
			return 4
		}
		return aluCycles(op, ea, size)

	case 0xB:
		switch {
		case op&0xC0 == 0xC0: // CMPA
			return 6 + eaCycles(ea, op&0x100 != 0)
		case op&0x138 == 0x108: // CMPM
			if long {
				return 20
			}
			return 12
		case op&0x100 != 0: // EOR Dn,<ea>
			if isRegDirect(ea) {
				if long {
					return 8
				}
				return 4
			}
			if long {
				return 12 + eaCycles(ea, true)
			}
			return 8 + eaCycles(ea, false)
		}
		// CMP <ea>,Dn
		if long {
			return 6 + eaCycles(ea, true)
		}
		return 4 + eaCycles(ea, false)

	case 0xE:
		if size == 3 { // memory shift, one bit
			return 8 + eaCycles(ea, false)
		}
		if long {
			return 8 + 2*c.tim.count
		}
		return 6 + 2*c.tim.count
	}
	return 4
}

// aluCycles times ADD, SUB, AND and OR (<ea>,Dn or Dn,<ea>).
func aluCycles(op, ea, size uint16) int {
	long := size == 2
	if op&0x100 == 0 { // <ea>,Dn
		if long {
			if isRegDirect(ea) || ea == 0x3C {
				return 8 + eaCycles(ea, true)
			}
			return 6 + eaCycles(ea, true)
		}
		return 4 + eaCycles(ea, false)
	}
	if long { // Dn,<ea>
		return 12 + eaCycles(ea, true)
	}
	return 8 + eaCycles(ea, false)
}

// divuCycles is DIVU's execution time (Jorge Cwik's algorithm).
func divuCycles(dividend uint32, divisor uint16) int {
	if dividend>>16 >= uint32(divisor) {
		return 10 // overflow detected early
	}
	mcycles := 38
	hdivisor := uint32(divisor) << 16
	for i := 0; i < 15; i++ {
		temp := dividend
		dividend <<= 1
		if int32(temp) < 0 {
			dividend -= hdivisor
		} else {
			mcycles += 2
			if dividend >= hdivisor {
				dividend -= hdivisor
				mcycles--
			}
		}
	}
	return mcycles * 2
}

// divsCycles is DIVS's execution time (Jorge Cwik's algorithm).
func divsCycles(dividend int32, divisor int16) int {
	mcycles := 6
	if dividend < 0 {
		mcycles++
	}
	absDividend := uint32(dividend)
	if dividend < 0 {
		absDividend = uint32(-int64(dividend))
	}
	absDivisor := uint16(divisor)
	if divisor < 0 {
		absDivisor = uint16(-int32(divisor))
	}
	if absDividend>>16 >= uint32(absDivisor) {
		return (mcycles + 2) * 2
	}
	aquot := absDividend / uint32(absDivisor)
	mcycles += 55
	if divisor >= 0 {
		if dividend >= 0 {
			mcycles--
		} else {
			mcycles++
		}
	}
	for i := 0; i < 15; i++ {
		if int16(aquot) >= 0 {
			mcycles++
		}
		aquot <<= 1
	}
	return mcycles * 2
}

// mulsPatterns counts MULS's timing-relevant bit transitions: in the
// 17-bit value source:0, the number of 01 and 10 pairs.
func mulsPatterns(src uint16) int {
	v := uint32(src) << 1
	n := 0
	for i := 0; i < 16; i++ {
		if (v>>i)&1 != (v>>(i+1))&1 {
			n++
		}
	}
	return n
}

func popcount16(v uint16) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}
