package m68k

import (
	"encoding/binary"
	"fmt"
)

// The 68020. Setting Processor.Model to Model68020 adds what it has over
// the 68000 (as the 68EC020: 24-bit address bus, no MMU, no FPU):
//
//   - Control registers VBR, CACR, CAAR, SFC, DFC and MSP, reached with
//     MOVEC. Exception vectors are read relative to VBR.
//   - Exception stack frames start with a format/vector word; format 2
//     frames (CHK, CHK2, TRAPcc, TRAPV, trace, divide by zero) also hold the
//     address of the instruction that caused them. RTE reads the format to
//     know how much to pop.
//   - No address errors for data: words and longs may be at odd addresses.
//     (Instruction fetches from an odd address still fault; that frame is
//     still the 68000's.)
//   - MOVE from SR is privileged; MOVE from CCR exists.
//   - New instructions: MOVEC, RTD, EXTB.L, LINK.L, TRAPcc, CHK.L, MULS.L,
//     MULU.L, DIVS.L, DIVU.L, DIVSL.L, DIVUL.L, Bcc/BRA/BSR with a 32-bit
//     displacement, bit fields (bitfield.go), CAS, CAS2, CHK2, CMP2, PACK,
//     UNPK and MOVES (m68020_misc.go).
//   - Scaled indexing and the full extension word addressing modes,
//     including memory indirect (decodeBriefExt).
//
// The instruction cache is not emulated (CACR is stored); CALLM/RTM (68020
// only, never used by the Amiga OS) and coprocessor instructions are not
// implemented: they take the F-line or illegal instruction exception.

// Model selects the processor.
type Model int

const (
	Model68000 Model = iota
	Model68020
)

func (m Model) String() string {
	switch m {
	case Model68020:
		return "68020"
	case Model68030:
		return "68030"
	}
	return "68000"
}

func (c *Processor) is020() bool { return c.Model >= Model68020 }

// ICacheEnabled reports whether the 68020's instruction cache is on (CACR
// bit 0). Instruction fetches that hit it make no bus cycles; the
// processor doesn't model the cache itself, but a memory system can use
// this with Decoder.Fetch.
func (c *Processor) ICacheEnabled() bool { return c.is020() && c.CACR&1 != 0 }

// opcodes020 are the 68020's additions, matched before the 68000 table.
var opcodes020 = []opcode{
	{0x4E7A, 0xFFFE, opMovec},
	{0x4E74, 0xFFFF, opRtd},
	{0x49C0, 0xFFF8, opExtb},
	{0x4808, 0xFFF8, opLinkL},
	{0x42C0, 0xFFC0, opMoveFromCcr},
	{0x40C0, 0xFFC0, opMoveFromSr020},
	{0x50FA, 0xF0FE, opTrapcc},
	{0x50FC, 0xF0FF, opTrapcc},
	{0x4100, 0xF1C0, opChkL},
	{0x4C00, 0xFFC0, opMulL},
	{0x4C40, 0xFFC0, opDivL},
}

func dispatch020(op uint16) stepFunc {
	for _, o := range opcodes020 {
		if op&o.mask == o.base {
			return o.f
		}
	}
	for _, o := range opcodes020Misc {
		if op&o.mask == o.base {
			return o.f
		}
	}
	return nil
}

func (c *Processor) push32(v uint32) error {
	c.A[7] -= 4
	binary.BigEndian.PutUint32(c.buf[:4], v)
	_, err := c.M.Write(int(c.A[7]), c.buf[:4])
	return err
}

func (c *Processor) push16(v uint16) error {
	c.A[7] -= 2
	binary.BigEndian.PutUint16(c.buf[:2], v)
	_, err := c.M.Write(int(c.A[7]), c.buf[:2])
	return err
}

// frameFormat2 reports whether a vector's 68020 exception frame is format
// 2, which adds the address of the instruction that caused it.
func frameFormat2(vector int) bool {
	switch vector {
	case VecDivideByZero, VecCHK, VecTRAPV, VecTrace:
		return true
	}
	return false
}

// pushFrame020 pushes the format/vector word (and for format 2 the
// instruction address) that the 68020 puts below the PC and SR.
func (c *Processor) pushFrame020(vector int) error {
	format := uint16(0)
	if frameFormat2(vector) {
		if err := c.push32(c.instrAddr); err != nil {
			return err
		}
		format = 2
	}
	return c.push16(format<<12 | uint16(vector*4)&0x0FFF)
}

// rte020 pops the rest of a 68020 frame after SR and PC: the format word
// says how long it is.
func (c *Processor) rte020() error {
	fw, err := c.M.Word(int(c.A[7]))
	if err != nil {
		return err
	}
	switch fw >> 12 {
	case 0, 1:
		c.A[7] += 2
	case 2:
		c.A[7] += 6
	case 9:
		c.A[7] += 14
	case 0xA:
		c.A[7] += 26
	case 0xB:
		c.A[7] += 86
	default:
		return errFormat
	}
	return nil
}

var errFormat = fmt.Errorf("bad exception stack frame format")

// ---- Control registers ----

func (c *Processor) controlReg(n uint16) (*uint32, uint32, bool) {
	switch n {
	case 0x000:
		return &c.SFC, 7, true
	case 0x001:
		return &c.DFC, 7, true
	case 0x002: // CACR: enable and freeze; the clear bits read as 0
		if c.is030() { // and the 68030's data cache, burst and write allocate
			return &c.CACR, 0x3313, true
		}
		return &c.CACR, 0x03, true
	case 0x800:
		return &c.USP, 0xFFFFFFFF, true
	case 0x801:
		return &c.VBR, 0xFFFFFFFF, true
	case 0x802:
		return &c.CAAR, 0xFC, true
	case 0x803:
		return &c.MSP, 0xFFFFFFFF, true
	case 0x804: // ISP: the supervisor stack pointer (A7 while in supervisor mode)
		return &c.A[7], 0xFFFFFFFF, true
	}
	return nil, 0, false
}

// opMovec implements MOVEC Rc,Rn ($4E7A) and MOVEC Rn,Rc ($4E7B).
func opMovec(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "movec", sz: SizeLong, n: 1}
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
	reg := &c.D[ext>>12&7]
	name := fmt.Sprintf("D%d", ext>>12&7)
	if ext&0x8000 != 0 {
		reg, name = &c.A[ext>>12&7], fmt.Sprintf("A%d", ext>>12&7)
	}
	cr, mask, ok := c.controlReg(ext & 0x0FFF)
	if !ok {
		c.PC = t.addr
		t.err = c.illegal()
		return
	}
	crName := fmt.Sprintf("$%03X", ext&0xFFF)
	if c.op&1 == 0 {
		*reg = *cr & mask
		t.src, t.dst = crName, name
	} else {
		*cr = *reg & mask
		t.src, t.dst = name, crName
	}
	return
}

// illegal takes the illegal instruction exception for the instruction at
// PC.
func (c *Processor) illegal() error {
	return c.RaiseException(VecIllegal)
}

// opRtd implements RTD #d16: return and deallocate parameters.
func opRtd(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "rtd", sz: noSize, n: 1}
	c.PC += 2
	d, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	pc, err := c.M.Long(int(c.A[7]))
	if err != nil {
		t.err = err
		return
	}
	c.A[7] = uint32(int32(c.A[7]+4) + int32(int16(d)))
	c.PC = pc
	t.src = fmt.Sprintf("#%d", int16(d))
	return
}

// opExtb implements EXTB.L Dn: byte sign-extended to long.
func opExtb(c *Processor) (t stepTrace) {
	reg := c.op & 7
	t = stepTrace{addr: c.PC, op: "extb", sz: SizeLong, n: 1, dst: fmt.Sprintf("D%d", reg)}
	c.PC += 2
	c.D[reg] = uint32(int32(int8(c.D[reg])))
	c.setStatusForLong(c.D[reg])
	return
}

// opLinkL implements LINK.L An,#d32.
func opLinkL(c *Processor) (t stepTrace) {
	reg := c.op & 7
	t = stepTrace{addr: c.PC, op: "link", sz: SizeLong, n: 1, src: fmt.Sprintf("A%d", reg)}
	c.PC += 2
	d, _, err := c.readImmLong()
	if err != nil {
		t.err = err
		return
	}
	if t.err = c.push32(c.A[reg]); t.err != nil {
		return
	}
	c.A[reg] = c.A[7]
	c.A[7] = uint32(int32(c.A[7]) + int32(d))
	t.dst = fmt.Sprintf("#$%X", int32(d))
	return
}

// opMoveFromCcr implements MOVE CCR,<ea> (68010 and later).
func opMoveFromCcr(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "move", sz: SizeWord, n: 1, src: "CCR"}
	c.PC += 2
	t.dst, t.err = c.writeWord(decodeEA(c.op), uint16(c.SR&0x1F))
	return
}

// opMoveFromSr020 is MOVE SR,<ea> on the 68020: privileged, and no read
// before the write.
func opMoveFromSr020(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "move", sz: SizeWord, n: 1, src: "SR"}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	t.dst, t.err = c.writeWord(decodeEA(c.op), uint16(c.SR))
	return
}

// opTrapcc implements TRAPcc, TRAPcc.W #d16 and TRAPcc.L #d32: the TRAPV
// exception if the condition holds.
func opTrapcc(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "trapcc", sz: noSize, n: 1}
	c.PC += 2
	switch c.op & 7 {
	case 2:
		c.PC += 2
	case 3:
		c.PC += 4
	}
	if testCond(c, c.op>>8&0xF) {
		t.err = c.RaiseException(VecTRAPV)
	}
	return
}

// opChkL implements CHK.L <ea>,Dn.
func opChkL(c *Processor) (t stepTrace) {
	reg := c.op >> 9 & 7
	t = stepTrace{addr: c.PC, op: "chk", sz: SizeLong, n: 1, dst: fmt.Sprintf("D%d", reg)}
	c.PC += 2
	var bound uint32
	if bound, t.src, t.err = c.readLong(c.op); t.err != nil {
		return
	}
	dn, ub := int32(c.D[reg]), int32(bound)
	c.SR &^= StatusZero | StatusOverflow | StatusCarry
	if dn == 0 {
		c.SR |= StatusZero
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

// opMulL implements MULU.L and MULS.L: 32 x 32 bits to 32 (V on overflow)
// or 64 bits (Dh:Dl).
func opMulL(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "mulu", sz: SizeLong, n: 1}
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	var src uint32
	if src, t.src, t.err = c.readLong(c.op); t.err != nil {
		return
	}
	dl, dh := ext>>12&7, ext&7
	signed, wide := ext&0x0800 != 0, ext&0x0400 != 0
	var hi, lo uint64
	overflow := false
	if signed {
		t.op = "muls"
		p := int64(int32(c.D[dl])) * int64(int32(src))
		lo, hi = uint64(uint32(p)), uint64(uint32(p>>32))
		overflow = p != int64(int32(p))
	} else {
		p := uint64(c.D[dl]) * uint64(src)
		lo, hi = p&0xFFFFFFFF, p>>32
		overflow = hi != 0
	}
	c.SR &^= StatusNegative | StatusZero | StatusOverflow | StatusCarry
	c.D[dl] = uint32(lo)
	if wide {
		c.D[dh] = uint32(hi)
		if hi&0x80000000 != 0 {
			c.SR |= StatusNegative
		}
		if hi|lo == 0 {
			c.SR |= StatusZero
		}
		t.dst = fmt.Sprintf("D%d:D%d", dh, dl)
		return
	}
	t.dst = fmt.Sprintf("D%d", dl)
	if lo&0x80000000 != 0 {
		c.SR |= StatusNegative
	}
	if lo == 0 {
		c.SR |= StatusZero
	}
	if overflow {
		c.SR |= StatusOverflow
	}
	return
}

// opDivL implements DIVU.L, DIVS.L, DIVUL.L and DIVSL.L: a 32- or 64-bit
// dividend (Dr:Dq) divided by 32 bits, quotient to Dq and remainder to Dr
// (when Dr differs from Dq, or the dividend is 64 bits). On overflow V is
// set and the registers are unchanged; dividing by zero takes the
// exception.
func opDivL(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "divu", sz: SizeLong, n: 1}
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	var divisor uint32
	if divisor, t.src, t.err = c.readLong(c.op); t.err != nil {
		return
	}
	dq, dr := ext>>12&7, ext&7
	signed, wide := ext&0x0800 != 0, ext&0x0400 != 0
	t.dst = fmt.Sprintf("D%d:D%d", dr, dq)
	if divisor == 0 {
		c.SR &^= StatusCarry
		t.err = c.RaiseException(VecDivideByZero)
		return
	}
	c.SR &^= StatusOverflow | StatusCarry
	var q, r uint32
	if signed {
		t.op = "divs"
		var dividend int64
		if wide {
			dividend = int64(uint64(c.D[dr])<<32 | uint64(c.D[dq]))
		} else {
			dividend = int64(int32(c.D[dq]))
		}
		d := int64(int32(divisor))
		if dividend == -1<<63 && d == -1 {
			c.SR |= StatusOverflow
			return
		}
		qq, rr := dividend/d, dividend%d
		if qq != int64(int32(qq)) {
			c.SR |= StatusOverflow
			return
		}
		q, r = uint32(qq), uint32(rr)
	} else {
		if wide {
			dividend := uint64(c.D[dr])<<32 | uint64(c.D[dq])
			qq := dividend / uint64(divisor)
			if qq > 0xFFFFFFFF { // the quotient won't fit in 32 bits
				c.SR |= StatusOverflow
				return
			}
			q, r = uint32(qq), uint32(dividend%uint64(divisor))
		} else {
			q, r = c.D[dq]/divisor, c.D[dq]%divisor
		}
	}
	if wide || dr != dq {
		c.D[dr] = r
	}
	c.D[dq] = q
	c.SR &^= StatusNegative | StatusZero
	if q&0x80000000 != 0 {
		c.SR |= StatusNegative
	}
	if q == 0 {
		c.SR |= StatusZero
	}
	return
}
