package m68k

import (
	"math"
	"testing"
)

func newFPU(t *testing.T, model int, code ...uint16) *Processor {
	c := new020(t, code...)
	c.FPU = model
	return c
}

func TestExtendedFormat(t *testing.T) {
	x := toExtended(1.0)
	if want := [12]byte{0x3F, 0xFF, 0, 0, 0x80, 0, 0, 0, 0, 0, 0, 0}; x != want {
		t.Errorf("1.0 = % X, want % X", x, want)
	}
	for _, v := range []float64{1, -2.5, math.Pi, 1e300, 1e-300, 5e-324, 0} {
		x := toExtended(v)
		if got := fromExtended(x[:]); got != v {
			t.Errorf("%g round-trips to %g", v, got)
		}
	}
	if x := toExtended(math.Inf(-1)); !math.IsInf(fromExtended(x[:]), -1) {
		t.Error("-Inf")
	}
	if x := toExtended(math.NaN()); !math.IsNaN(fromExtended(x[:])) {
		t.Error("NaN")
	}
	p := toPacked(-123.456)
	if got := fromPacked(p[:]); math.Abs(got+123.456) > 1e-12 {
		t.Errorf("packed -123.456 round-trips to %g", got)
	}
}

func TestFPUArithmetic(t *testing.T) {
	c := newFPU(t, FPU68881,
		0xF23C, 0x4000, 0x0000, 0x0003, // FMOVE.L #3,FP0
		0xF23C, 0x5480, 0x4002, 0x0000, 0x0000, 0x0000, // FMOVE.D #2.25,FP1
		0xF200, 0x0422, // FADD.X FP1,FP0      -> 5.25
		0xF200, 0x0023, // FMUL.X FP0,FP0      -> 27.5625
		0xF200, 0x0004, // FSQRT.X FP0         -> 5.25
		0xF200, 0x6000, // FMOVE.L FP0,D0      -> 5 (nearest even)
	)
	step(t, c, 6)
	if c.FP[0] != 5.25 || c.D[0] != 5 {
		t.Errorf("FP0=%g D0=%d, want 5.25 and 5", c.FP[0], c.D[0])
	}
	// Round towards zero via FPCR, then convert -2.7.
	c = newFPU(t, FPU68881,
		0xF200, 0x9000, // FMOVE.L D0,FPCR
		0xF23C, 0x5480, 0xC005, 0x9999, 0x9999, 0x999A, // FMOVE.D #-2.7,FP1
		0xF201, 0x6080, // FMOVE.L FP1,D1
	)
	c.D[0] = 0x10 // RZ
	step(t, c, 3)
	if int32(c.D[1]) != -2 {
		t.Errorf("RZ conversion of -2.7 = %d, want -2", int32(c.D[1]))
	}
	// Divide by zero: +Inf and DZ.
	c = newFPU(t, FPU68881,
		0xF23C, 0x4000, 0x0000, 0x0001, // FMOVE.L #1,FP0
		0xF23C, 0x4020, 0x0000, 0x0000, // FDIV.L #0,FP0
	)
	step(t, c, 2)
	if !math.IsInf(c.FP[0], 1) || c.FPSR&fpexDZ == 0 || c.FPSR&fpccI == 0 {
		t.Errorf("1/0: FP0=%g FPSR=$%X", c.FP[0], c.FPSR)
	}
	// FMOVECR #0 is pi.
	c = newFPU(t, FPU68881, 0xF200, 0x5C00)
	step(t, c, 1)
	if c.FP[0] != math.Pi {
		t.Errorf("FMOVECR #0 = %g", c.FP[0])
	}
}

func TestFPUMemoryAndMovem(t *testing.T) {
	c := newFPU(t, FPU68881,
		0xF210, 0x6800, // FMOVE.X FP0,(A0)
		0xF210, 0x4900, // FMOVE.X (A0),FP2
		0xF227, 0xE0C0, // FMOVEM.X FP0/FP1,-(A7)
		0xF21F, 0xD030, // FMOVEM.X (A7)+,FP2/FP3
	)
	c.A[0] = 0x3000
	c.FP[0], c.FP[1] = -1.5, 1e10
	step(t, c, 2)
	if c.FP[2] != -1.5 || c.peekW(0x3000) != 0xBFFF {
		t.Errorf("FMOVE.X via memory: FP2=%g, exponent word $%04X", c.FP[2], c.peekW(0x3000))
	}
	step(t, c, 2)
	if c.FP[2] != -1.5 || c.FP[3] != 1e10 || c.A[7] != 0x8000 {
		t.Errorf("FMOVEM round trip: FP2=%g FP3=%g SP=$%X", c.FP[2], c.FP[3], c.A[7])
	}
}

func TestFPUConditions(t *testing.T) {
	c := newFPU(t, FPU68881,
		0xF23C, 0x4038, 0x0000, 0x0005, // FCMP.L #5,FP0
		0xF282, 0x0010, // FBOGT +$10
	)
	c.FP[0] = 7
	step(t, c, 2)
	if c.PC != 0x100A+0x10 { // relative to the extension word
		t.Errorf("FBOGT with 7 > 5 went to $%X", c.PC)
	}
	c = newFPU(t, FPU68881, 0xF241, 0x0001) // FSEQ D1
	c.FPSR = fpccZ
	step(t, c, 1)
	if c.D[1]&0xFF != 0xFF {
		t.Errorf("FSEQ with Z: D1=$%X", c.D[1])
	}
	c.FP[0] = math.NaN()
	c = newFPU(t, FPU68881, 0xF23C, 0x4038, 0, 1, 0xF282, 0x0010) // NaN compares unordered
	c.FP[0] = math.NaN()
	step(t, c, 2)
	if c.PC != 0x100C {
		t.Errorf("FBOGT with a NaN went to $%X, want to fall through to $100C", c.PC)
	}
}

// FSAVE gives a null frame until the FPU is used, then an idle frame
// whose size says 68881 ($18) or 68882 ($38).
func TestFSaveFRestore(t *testing.T) {
	for _, m := range []struct {
		model int
		size  byte
	}{{FPU68881, 0x18}, {FPU68882, 0x38}} {
		c := newFPU(t, m.model,
			0xF327,         // FSAVE -(A7)
			0xF200, 0x0000, // FMOVE.X FP0,FP0
			0xF327,         // FSAVE -(A7)
			0xF35F,         // FRESTORE (A7)+
		)
		step(t, c, 1)
		if c.A[7] != 0x8000-4 || c.peekL(c.A[7]) != 0 {
			t.Errorf("%d: first FSAVE not a null frame (SP=$%X, $%08X)", m.model, c.A[7], c.peekL(c.A[7]))
		}
		step(t, c, 2)
		sp := c.A[7]
		if hdr := c.peekW(sp); hdr != 0x1F00|uint16(m.size) || sp != 0x8000-4-4-uint32(m.size) {
			t.Errorf("%d: idle frame header $%04X at SP=$%X", m.model, hdr, sp)
		}
		step(t, c, 1)
		if c.A[7] != 0x8000-4 {
			t.Errorf("%d: FRESTORE left SP=$%X", m.model, c.A[7])
		}
	}
}

// Without an FPU the instructions take the F-line exception.
func TestNoFPULineF(t *testing.T) {
	c := new020(t, 0xF200, 0x0000)
	c.pokeL(11*4, 0x3000)
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("FPU opcode without an FPU went to $%X, want the F-line vector", c.PC)
	}
}
