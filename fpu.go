package m68k

import (
	"encoding/binary"
	"fmt"
	"math"
)

// The 68881/68882 floating-point coprocessor, attached to a 68020
// (Processor.FPU = FPU68881 or FPU68882). It is reached through F-line
// instructions with coprocessor ID 1 ($F2xx-$F3xx); without an FPU those
// take the F-line exception, which is how software (and exec) finds there
// is none.
//
// Registers are held as float64, so results carry 53 bits of mantissa
// rather than the FPU's 64; FPCR's rounding precision is ignored, its
// rounding mode is used for conversions to integer and FINT. Arithmetic
// exceptions are noted in FPSR but never trapped. Memory operands use all
// seven formats: byte, word, long, single, double, extended (96 bits) and
// packed decimal.

// FPU models.
const (
	FPUNone  = 0
	FPU68881 = 68881
	FPU68882 = 68882
)

// FPSR condition codes (bits 27-24) and exception bits.
const (
	fpccN   = 1 << 27
	fpccZ   = 1 << 26
	fpccI   = 1 << 25
	fpccNAN = 1 << 24
	fpexDZ  = 1 << 10 // divide by zero
	fpaeDZ  = 1 << 4  // accrued divide by zero
	fpexOP  = 1 << 13 // operand error
	fpaeIOP = 1 << 7
)

func (c *Processor) hasFPU() bool { return c.is020() && c.FPU != FPUNone }

// setFPCC sets the condition codes for a result.
func (c *Processor) setFPCC(v float64) {
	c.FPSR &^= fpccN | fpccZ | fpccI | fpccNAN
	switch {
	case math.IsNaN(v):
		c.FPSR |= fpccNAN
	case math.IsInf(v, 0):
		c.FPSR |= fpccI
	case v == 0:
		c.FPSR |= fpccZ
	}
	if math.Signbit(v) {
		c.FPSR |= fpccN
	}
}

// fpPredicate evaluates an FBcc/FScc/FDBcc/FTRAPcc condition (0-31; the
// upper 16 are the signalling forms of the lower, which differ only in
// raising BSUN on a NaN, not modelled).
func (c *Processor) fpPredicate(p uint16) bool {
	n, z, nan := c.FPSR&fpccN != 0, c.FPSR&fpccZ != 0, c.FPSR&fpccNAN != 0
	switch p & 0xF {
	case 0x0: // F
		return false
	case 0x1: // EQ
		return z
	case 0x2: // OGT
		return !(nan || z || n)
	case 0x3: // OGE
		return z || !(nan || n)
	case 0x4: // OLT
		return n && !(nan || z)
	case 0x5: // OLE
		return z || (n && !nan)
	case 0x6: // OGL
		return !(nan || z)
	case 0x7: // OR
		return !nan
	case 0x8: // UN
		return nan
	case 0x9: // UEQ
		return nan || z
	case 0xA: // UGT
		return nan || !(n || z)
	case 0xB: // UGE
		return nan || z || !n
	case 0xC: // ULT
		return nan || (n && !z)
	case 0xD: // ULE
		return nan || z || n
	case 0xE: // NE
		return !z
	}
	return true // T
}

// ---- Formats ----

// Operand formats (the source/destination format field).
const (
	fmtLong     = 0
	fmtSingle   = 1
	fmtExtended = 2
	fmtPacked   = 3
	fmtWord     = 4
	fmtDouble   = 5
	fmtByte     = 6
)

var fmtSize = [8]int{4, 4, 12, 12, 2, 8, 1, 12}

// toExtended encodes v in the 96-bit extended format: sign and 15-bit
// exponent (bias 16383), 16 zero bits, 64-bit mantissa with an explicit
// integer bit.
func toExtended(v float64) [12]byte {
	var b [12]byte
	bits := math.Float64bits(v)
	sign := uint16(bits>>63) << 15
	e := int(bits >> 52 & 0x7FF)
	m := bits & (1<<52 - 1)
	var exp uint16
	var mant uint64
	switch {
	case e == 0x7FF: // Inf or NaN
		exp = 0x7FFF
		if m != 0 {
			mant = 0xFFFFFFFFFFFFFFFF
		}
	case e == 0 && m == 0: // zero
	case e == 0: // denormal: normalise
		shift := uint(64 - 52)
		for m&(1<<52) == 0 {
			m <<= 1
			e--
		}
		exp = uint16(e + 1 - 1023 + 16383)
		mant = m << (shift - 1)
	default:
		exp = uint16(e - 1023 + 16383)
		mant = 1<<63 | m<<11
	}
	binary.BigEndian.PutUint16(b[0:], sign|exp)
	binary.BigEndian.PutUint64(b[4:], mant)
	return b
}

func fromExtended(b []byte) float64 {
	se := binary.BigEndian.Uint16(b[0:])
	mant := binary.BigEndian.Uint64(b[4:])
	exp := int(se & 0x7FFF)
	neg := se&0x8000 != 0
	var v float64
	switch {
	case exp == 0x7FFF && mant<<1 == 0:
		v = math.Inf(1)
	case exp == 0x7FFF:
		v = math.NaN()
	default:
		v = math.Ldexp(float64(mant), exp-16383-63)
	}
	if neg {
		v = -v
	}
	return v
}

// fromPacked decodes the 96-bit packed decimal format: sign of mantissa
// and exponent, a 3-digit BCD exponent, one integer digit and 16
// fraction digits.
func fromPacked(b []byte) float64 {
	if b[0]&0x30 == 0x30 { // Inf/NaN
		if binary.BigEndian.Uint64(b[4:]) == 0 {
			return math.Inf(1 - 2*int(b[0]>>7))
		}
		return math.NaN()
	}
	digit := func(i int) int { // BCD digit i of the 24 in the record
		v := b[i/2]
		if i%2 == 0 {
			return int(v >> 4)
		}
		return int(v & 0xF)
	}
	exp := digit(1)*100 + digit(2)*10 + digit(3)
	if b[0]&0x40 != 0 {
		exp = -exp
	}
	m := float64(digit(7))
	scale := 1.0
	for i := 8; i < 24; i++ {
		scale /= 10
		m += float64(digit(i)) * scale
	}
	v := m * math.Pow(10, float64(exp))
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}

// toPacked encodes v as packed decimal with 17 significant digits.
func toPacked(v float64) [12]byte {
	var b [12]byte
	if math.IsNaN(v) || math.IsInf(v, 0) {
		b[0] = 0x7F
		b[1] = 0xFF
		if math.IsNaN(v) {
			b[4] = 0xFF
		}
		if v < 0 {
			b[0] |= 0x80
		}
		return b
	}
	if math.Signbit(v) {
		b[0] |= 0x80
		v = -v
	}
	s := fmt.Sprintf("%.16e", v) // d.dddddddddddddddde±xx
	digits := s[0:1] + s[2:18]
	var exp int
	fmt.Sscanf(s[19:], "%d", &exp)
	if exp < 0 {
		b[0] |= 0x40
		exp = -exp
	}
	set := func(i, d int) {
		if i%2 == 0 {
			b[i/2] |= byte(d) << 4
		} else {
			b[i/2] |= byte(d)
		}
	}
	set(1, exp/100%10)
	set(2, exp/10%10)
	set(3, exp%10)
	for i, ch := range digits {
		set(7+i, int(ch-'0'))
	}
	return b
}

// roundInt rounds per FPCR's rounding mode (bits 5-4).
func (c *Processor) roundInt(v float64) float64 {
	switch c.FPCR >> 4 & 3 {
	case 1:
		return math.Trunc(v)
	case 2:
		return math.Floor(v)
	case 3:
		return math.Ceil(v)
	}
	return math.RoundToEven(v)
}

// toInt converts for an integer destination, saturating (and noting an
// operand error) when out of range.
func (c *Processor) toInt(v float64, min, max float64) int64 {
	r := c.roundInt(v)
	switch {
	case math.IsNaN(r):
		c.FPSR |= fpexOP | fpaeIOP
		return int64(max)
	case r < min:
		c.FPSR |= fpexOP | fpaeIOP
		return int64(min)
	case r > max:
		c.FPSR |= fpexOP | fpaeIOP
		return int64(max)
	}
	return int64(r)
}

// ---- Operand access ----

// fpRead reads an operand in format f from <ea>.
func (c *Processor) fpRead(ea uint16, f int) (float64, string, error) {
	mode, reg := ea>>3&7, ea&7
	n := fmtSize[f]
	switch {
	case mode == 0: // Dn: byte, word, long, single only
		d := c.D[reg]
		opr := fmt.Sprintf("D%d", reg)
		switch f {
		case fmtByte:
			return float64(int8(d)), opr, nil
		case fmtWord:
			return float64(int16(d)), opr, nil
		case fmtLong:
			return float64(int32(d)), opr, nil
		case fmtSingle:
			return float64(math.Float32frombits(d)), opr, nil
		}
		return 0, "", errBadAddress
	case mode == 1:
		return 0, "", errBadAddress
	}
	var buf [12]byte
	var opr string
	if mode == 7 && reg == 4 { // immediate: bytes and words sit in a word
		k := n
		if n == 1 {
			k = 2
		}
		if _, err := c.M.Read(int(c.PC), buf[:k]); err != nil {
			return 0, "", err
		}
		if n == 1 {
			buf[0] = buf[1]
		}
		c.PC += uint32(k)
		opr = "#"
	} else {
		addr, o, err := c.eaAddr(ea, n)
		if err != nil {
			return 0, "", err
		}
		if _, err := c.M.Read(int(addr), buf[:n]); err != nil {
			return 0, "", err
		}
		opr = o
	}
	be := binary.BigEndian
	switch f {
	case fmtByte:
		return float64(int8(buf[0])), opr, nil
	case fmtWord:
		return float64(int16(be.Uint16(buf[:]))), opr, nil
	case fmtLong:
		return float64(int32(be.Uint32(buf[:]))), opr, nil
	case fmtSingle:
		return float64(math.Float32frombits(be.Uint32(buf[:]))), opr, nil
	case fmtDouble:
		return math.Float64frombits(be.Uint64(buf[:])), opr, nil
	case fmtExtended:
		return fromExtended(buf[:]), opr, nil
	case fmtPacked:
		return fromPacked(buf[:]), opr, nil
	}
	return 0, "", errBadOpcode
}

// fpWrite writes v in format f to <ea>.
func (c *Processor) fpWrite(ea uint16, f int, v float64) (string, error) {
	mode, reg := ea>>3&7, ea&7
	var buf [12]byte
	be := binary.BigEndian
	n := fmtSize[f]
	switch f {
	case fmtByte:
		buf[0] = byte(c.toInt(v, math.MinInt8, math.MaxInt8))
	case fmtWord:
		be.PutUint16(buf[:], uint16(c.toInt(v, math.MinInt16, math.MaxInt16)))
	case fmtLong:
		be.PutUint32(buf[:], uint32(c.toInt(v, math.MinInt32, math.MaxInt32)))
	case fmtSingle:
		be.PutUint32(buf[:], math.Float32bits(float32(v)))
	case fmtDouble:
		be.PutUint64(buf[:], math.Float64bits(v))
	case fmtExtended:
		buf = toExtended(v)
	case fmtPacked:
		buf = toPacked(v)
	default:
		return "", errBadOpcode
	}
	if mode == 0 {
		d := &c.D[reg]
		switch f {
		case fmtByte:
			*d = *d&^0xFF | uint32(buf[0])
		case fmtWord:
			*d = *d&^0xFFFF | uint32(be.Uint16(buf[:]))
		case fmtLong, fmtSingle:
			*d = be.Uint32(buf[:])
		default:
			return "", errBadAddress
		}
		return fmt.Sprintf("D%d", reg), nil
	}
	if mode == 1 || mode == 7 && reg >= 2 {
		return "", errBadAddress
	}
	addr, opr, err := c.eaAddr(ea, n)
	if err != nil {
		return "", err
	}
	_, err = c.M.Write(int(addr), buf[:n])
	return opr, err
}

// ---- Instructions ----

// opFPU dispatches an FPU instruction ($F200-$F3FF): the type field (bits
// 8-6) selects general, conditional, branch, save or restore.
func opFPU(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "f?", sz: noSize, n: 1}
	kind := c.op >> 6 & 7
	if kind < 4 { // FSAVE/FRESTORE don't count as using the FPU
		c.fpuUsed = true
	}
	switch kind {
	case 0:
		return c.fpGeneral(t)
	case 1:
		return c.fpCond(t)
	case 2, 3:
		return c.fpBranch(t)
	case 4:
		return c.fpSave(t)
	case 5:
		return c.fpRestore(t)
	}
	c.PC = t.addr
	t.err = c.lineF()
	return
}

// lineF takes the F-line exception for the instruction at PC.
func (c *Processor) lineF() error {
	c.PC += 2
	return c.RaiseException(VecLine1111)
}

var fpConstants = map[uint16]float64{
	0x00: math.Pi, 0x0B: math.Log10(2), 0x0C: math.E, 0x0D: math.Log2E, 0x0E: math.Log10E,
	0x0F: 0, 0x30: math.Ln2, 0x31: math.Ln10, 0x32: 1, 0x33: 10, 0x34: 100, 0x35: 1e4,
	0x36: 1e8, 0x37: 1e16, 0x38: 1e32, 0x39: 1e64, 0x3A: 1e128, 0x3B: 1e256,
	0x3C: math.Inf(1), 0x3D: math.Inf(1), 0x3E: math.Inf(1), 0x3F: math.Inf(1),
}

// fpGeneral is the general instruction: a command word whose opclass
// (bits 15-13) chooses register/memory arithmetic, FMOVE out, control
// register moves or FMOVEM.
func (c *Processor) fpGeneral(t stepTrace) stepTrace {
	c.FPIAR = t.addr
	c.PC += 2
	cmd, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return t
	}
	ea := c.op & 0x3F
	class := cmd >> 13
	switch class {
	case 0, 2: // FPm or <ea> to FPn, operation by opmode
		dst := cmd >> 7 & 7
		var src float64
		if class == 0 {
			src = c.FP[cmd>>10&7]
			t.src = fmt.Sprintf("FP%d", cmd>>10&7)
		} else if cmd>>10&7 == 7 { // FMOVECR #offset,FPn
			c.FP[dst] = fpConstants[cmd&0x7F]
			c.setFPCC(c.FP[dst])
			t.op, t.src, t.dst = "fmovecr", fmt.Sprintf("#$%X", cmd&0x7F), fmt.Sprintf("FP%d", dst)
			c.tim.fixed = 29
			return t
		} else {
			if src, t.src, t.err = c.fpRead(ea, int(cmd>>10&7)); t.err != nil {
				return t
			}
		}
		t.dst = fmt.Sprintf("FP%d", dst)
		t.op = c.fpOperate(cmd&0x7F, src, dst, cmd&7)
		return t
	case 3: // FMOVE FPn,<ea>
		f := int(cmd >> 10 & 7)
		if f == 7 { // packed with dynamic k-factor
			f = fmtPacked
		}
		src := cmd >> 7 & 7
		t.op, t.src = "fmove", fmt.Sprintf("FP%d", src)
		t.dst, t.err = c.fpWrite(ea, f, c.FP[src])
		c.tim.fixed = 33
		return t
	case 4, 5: // FMOVE(M) control registers
		return c.fpMoveControl(t, cmd, class == 5)
	case 6, 7: // FMOVEM data registers
		return c.fpMovem(t, cmd, class == 7)
	}
	c.PC = t.addr
	t.err = c.lineF()
	return t
}

// fpOperate performs a general operation on src into FPn (or for FCMP and
// FTST just compares), sets the condition codes, and returns its name.
func (c *Processor) fpOperate(opmode uint16, src float64, dst uint16, cosReg uint16) string {
	d := c.FP[dst]
	result := d
	name := "f?"
	cyc := 51
	set := true
	switch opmode {
	case 0x00, 0x40, 0x44:
		name, result, cyc = "fmove", src, 21
	case 0x01:
		name, result = "fint", c.roundInt(src)
	case 0x02:
		name, result, cyc = "fsinh", math.Sinh(src), 300
	case 0x03:
		name, result = "fintrz", math.Trunc(src)
	case 0x04, 0x41, 0x45:
		name, result, cyc = "fsqrt", math.Sqrt(src), 107
	case 0x06:
		name, result, cyc = "flognp1", math.Log1p(src), 300
	case 0x08:
		name, result, cyc = "fetoxm1", math.Expm1(src), 300
	case 0x09:
		name, result, cyc = "ftanh", math.Tanh(src), 300
	case 0x0A:
		name, result, cyc = "fatan", math.Atan(src), 300
	case 0x0C:
		name, result, cyc = "fasin", math.Asin(src), 300
	case 0x0D:
		name, result, cyc = "fatanh", math.Atanh(src), 300
	case 0x0E:
		name, result, cyc = "fsin", math.Sin(src), 300
	case 0x0F:
		name, result, cyc = "ftan", math.Tan(src), 300
	case 0x10:
		name, result, cyc = "fetox", math.Exp(src), 300
	case 0x11:
		name, result, cyc = "ftwotox", math.Exp2(src), 300
	case 0x12:
		name, result, cyc = "ftentox", math.Pow(10, src), 300
	case 0x14:
		name, result, cyc = "flogn", math.Log(src), 300
	case 0x15:
		name, result, cyc = "flog10", math.Log10(src), 300
	case 0x16:
		name, result, cyc = "flog2", math.Log2(src), 300
	case 0x18, 0x58, 0x5C:
		name, result, cyc = "fabs", math.Abs(src), 21
	case 0x19:
		name, result, cyc = "fcosh", math.Cosh(src), 300
	case 0x1A, 0x5A, 0x5E:
		name, result, cyc = "fneg", -src, 21
	case 0x1C:
		name, result, cyc = "facos", math.Acos(src), 300
	case 0x1D:
		name, result, cyc = "fcos", math.Cos(src), 300
	case 0x1E: // FGETEXP: the unbiased exponent
		name = "fgetexp"
		if src == 0 || math.IsInf(src, 0) || math.IsNaN(src) {
			result = src
			if math.IsInf(src, 0) {
				result = math.NaN()
				c.FPSR |= fpexOP | fpaeIOP
			}
		} else {
			_, e := math.Frexp(src)
			result = float64(e - 1)
		}
	case 0x1F: // FGETMAN: the mantissa, 1 <= |m| < 2
		name = "fgetman"
		if src == 0 || math.IsNaN(src) {
			result = src
		} else if math.IsInf(src, 0) {
			result = math.NaN()
		} else {
			f, _ := math.Frexp(src)
			result = f * 2
		}
	case 0x20, 0x60, 0x64:
		name, result, cyc = "fdiv", d/src, 103
		if src == 0 {
			c.FPSR |= fpexDZ | fpaeDZ
		}
	case 0x21: // FMOD: remainder of a truncated quotient
		name, cyc = "fmod", 70
		result = math.Mod(d, src)
		c.fpQuotient(math.Trunc(d / src))
	case 0x22, 0x62, 0x66:
		name, result = "fadd", d+src
	case 0x23, 0x63, 0x67:
		name, result, cyc = "fmul", d*src, 71
	case 0x24:
		name, result, cyc = "fsgldiv", float64(float32(d/src)), 69
	case 0x25: // FREM: IEEE remainder (quotient rounded to nearest)
		name, cyc = "frem", 70
		result = math.Remainder(d, src)
		c.fpQuotient(math.RoundToEven(d / src))
	case 0x26:
		name, result, cyc = "fscale", math.Ldexp(d, int(math.Trunc(src))), 41
	case 0x27:
		name, result, cyc = "fsglmul", float64(float32(d*src)), 59
	case 0x28, 0x68, 0x6C:
		name, result = "fsub", d-src
	case 0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37:
		name, result, cyc = "fsincos", math.Sin(src), 450
		c.FP[cosReg] = math.Cos(src)
	case 0x38: // FCMP: flags from FPn - src, nothing stored
		name, cyc = "fcmp", 33
		r := d - src
		if d == src {
			r = 0 // equal infinities compare equal
		}
		c.setFPCC(r)
		set = false
		result = d
	case 0x3A: // FTST
		name, cyc = "ftst", 17
		c.setFPCC(src)
		set = false
		result = d
	default:
		name = "f?"
	}
	c.FP[dst] = result
	if set {
		c.setFPCC(result)
	}
	c.tim.fixed = cyc
	return name
}

// fpQuotient puts the sign and low 7 bits of an FMOD/FREM quotient in
// FPSR bits 23-16.
func (c *Processor) fpQuotient(q float64) {
	c.FPSR &^= 0xFF << 16
	if math.Signbit(q) {
		c.FPSR |= 0x80 << 16
		q = -q
	}
	if !math.IsInf(q, 0) && !math.IsNaN(q) {
		c.FPSR |= uint32(int64(math.Mod(q, 128))) << 16
	}
}

// fpMoveControl moves FPCR, FPSR and/or FPIAR (register list in bits
// 12-10) to or from <ea>, in that order.
func (c *Processor) fpMoveControl(t stepTrace, cmd uint16, toMemory bool) stepTrace {
	t.op = "fmovem"
	regs := []*uint32{}
	for i, r := range []*uint32{&c.FPCR, &c.FPSR, &c.FPIAR} {
		if cmd&(0x1000>>i) != 0 {
			regs = append(regs, r)
		}
	}
	ea := c.op & 0x3F
	mode := ea >> 3 & 7
	if len(regs) == 1 && (mode == 0 || mode == 1) { // single register to/from Dn/An
		r := &c.D[ea&7]
		if mode == 1 {
			r = &c.A[ea&7]
		}
		if toMemory {
			*r = *regs[0]
		} else {
			*regs[0] = *r
		}
		c.tim.fixed = 23
		return t
	}
	if len(regs) == 1 && mode == 7 && ea&7 == 4 && !toMemory { // immediate
		v, _, err := c.readImmLong()
		*regs[0], t.err = v, err
		c.tim.fixed = 23
		return t
	}
	addr, opr, err := c.eaAddr(ea, 4*len(regs))
	if err != nil {
		t.err = err
		return t
	}
	t.dst = opr
	for i, r := range regs {
		a := addr + uint32(4*i)
		if toMemory {
			if t.err = c.writeN(a, 4, *r); t.err != nil {
				return t
			}
		} else {
			if *r, t.err = c.readN(a, 4); t.err != nil {
				return t
			}
		}
	}
	c.tim.fixed = 23 + 4*len(regs)
	return t
}

// fpMovem moves a list of FP registers (static in bits 7-0 or dynamic in
// Dn) to or from memory as extended values. For -(An) the list is
// reversed (bit 0 = FP0) and stored downwards.
func (c *Processor) fpMovem(t stepTrace, cmd uint16, toMemory bool) stepTrace {
	t.op = "fmovem"
	list := cmd & 0xFF
	if cmd&0x0800 != 0 { // dynamic: list in Dn
		list = uint16(c.D[cmd>>4&7] & 0xFF)
	}
	ea := c.op & 0x3F
	mode, reg := ea>>3&7, ea&7
	predec := cmd&0x1000 == 0 // modes 00/01: predecrement
	var regs []int
	for i := 0; i < 8; i++ {
		if predec {
			if list&(1<<i) != 0 { // bit 0 = FP7 ... stored downwards from FP7
				regs = append(regs, 7-i)
			}
		} else if list&(0x80>>i) != 0 { // bit 7 = FP0
			regs = append(regs, i)
		}
	}
	c.tim.fixed = 35 + 25*len(regs)
	if mode == 4 { // -(An): FP7 first at the top, downwards
		for _, r := range regs {
			c.A[reg] -= 12
			x := toExtended(c.FP[r])
			if _, t.err = c.M.Write(int(c.A[reg]), x[:]); t.err != nil {
				return t
			}
		}
		return t
	}
	var addr uint32
	if mode == 3 { // (An)+
		addr = c.A[reg]
		c.A[reg] += uint32(12 * len(regs))
	} else {
		a, opr, err := c.controlAddr(ea)
		if err != nil {
			t.err = err
			return t
		}
		addr, t.dst = a, opr
	}
	var x [12]byte
	for i, r := range regs {
		a := addr + uint32(12*i)
		if toMemory {
			x = toExtended(c.FP[r])
			if _, t.err = c.M.Write(int(a), x[:]); t.err != nil {
				return t
			}
		} else {
			if _, t.err = c.M.Read(int(a), x[:]); t.err != nil {
				return t
			}
			c.FP[r] = fromExtended(x[:])
		}
	}
	return t
}

// fpCond handles FScc, FDBcc and FTRAPcc (a condition word follows).
func (c *Processor) fpCond(t stepTrace) stepTrace {
	c.PC += 2
	cond, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return t
	}
	ea := c.op & 0x3F
	ok := c.fpPredicate(cond & 0x3F)
	c.tim.fixed = 30
	switch {
	case ea>>3 == 1: // FDBcc Dn,label
		t.op = "fdbcc"
		disp, _, err := c.readImmWord()
		if err != nil {
			t.err = err
			return t
		}
		if ok {
			return t
		}
		r := &c.D[ea&7]
		cnt := uint16(*r) - 1
		*r = *r&^0xFFFF | uint32(cnt)
		if cnt != 0xFFFF {
			c.PC = uint32(int32(t.addr+4) + int32(int16(disp)))
		}
		return t
	case ea == 0x3A || ea == 0x3B || ea == 0x3C: // FTRAPcc
		t.op = "ftrapcc"
		switch ea {
		case 0x3A:
			c.PC += 2
		case 0x3B:
			c.PC += 4
		}
		if ok {
			t.err = c.RaiseException(VecTRAPV)
		}
		return t
	}
	t.op = "fscc" // FScc <ea>
	v := byte(0)
	if ok {
		v = 0xFF
	}
	t.dst, t.err = c.writeByte(ea, v)
	return t
}

// fpBranch handles FBcc with a 16- or 32-bit displacement.
func (c *Processor) fpBranch(t stepTrace) stepTrace {
	t.op = "fbcc"
	c.PC += 2
	var disp int32
	if c.op&0x40 != 0 {
		n, _, err := c.readImmLong()
		if err != nil {
			t.err = err
			return t
		}
		disp = int32(n)
	} else {
		n, _, err := c.readImmWord()
		if err != nil {
			t.err = err
			return t
		}
		disp = int32(int16(n))
	}
	c.tim.fixed = 22
	if c.fpPredicate(c.op & 0x3F) {
		c.PC = uint32(int32(t.addr+2) + disp)
	}
	return t
}

// fpFrameSize is the idle state frame's size after its header word: 24
// bytes for the 68881, 56 for the 68882 (how exec tells them apart).
func (c *Processor) fpFrameSize() int {
	if c.FPU == FPU68882 {
		return 0x38
	}
	return 0x18
}

// fpSave implements FSAVE <ea>: a null frame (one zero longword) if the
// FPU has not been used since reset, else an idle frame: version $1F,
// size, then that many bytes (the internal state, which here is empty).
func (c *Processor) fpSave(t stepTrace) stepTrace {
	t.op = "fsave"
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return t
	}
	c.PC += 2
	frame := []byte{0, 0, 0, 0}
	if c.fpuUsed {
		size := c.fpFrameSize()
		frame = make([]byte, 4+size)
		frame[0], frame[1] = 0x1F, byte(size)
	}
	ea := c.op & 0x3F
	var addr uint32
	if ea>>3 == 4 { // -(An)
		c.A[ea&7] -= uint32(len(frame))
		addr = c.A[ea&7]
	} else {
		a, opr, err := c.controlAddr(ea)
		if err != nil {
			t.err = err
			return t
		}
		addr, t.dst = a, opr
	}
	_, t.err = c.M.Write(int(addr), frame)
	c.tim.fixed = 70
	return t
}

// fpRestore implements FRESTORE <ea>: a null frame resets the FPU; an
// idle frame is skipped over (its internal state needs nothing here).
func (c *Processor) fpRestore(t stepTrace) stepTrace {
	t.op = "frestore"
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return t
	}
	c.PC += 2
	ea := c.op & 0x3F
	var addr uint32
	postinc := ea>>3 == 3
	if postinc {
		addr = c.A[ea&7]
	} else {
		a, opr, err := c.controlAddr(ea)
		if err != nil {
			t.err = err
			return t
		}
		addr, t.src = a, opr
	}
	hdr, err := c.M.Long(int(addr))
	if err != nil {
		t.err = err
		return t
	}
	size := 0
	if hdr>>24 == 0 { // null frame: reset
		for i := range c.FP {
			c.FP[i] = math.NaN()
		}
		c.FPCR, c.FPSR, c.FPIAR = 0, 0, 0
		c.fpuUsed = false
	} else {
		size = int(hdr >> 16 & 0xFF)
		c.fpuUsed = true
	}
	if postinc {
		c.A[ea&7] += uint32(4 + size)
	}
	c.tim.fixed = 70
	return t
}
