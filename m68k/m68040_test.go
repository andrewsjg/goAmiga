package m68k

import (
	"math"
	"testing"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// The 68040 test tables, 4 KB pages: the root table at $20000 (SRP and
// URP), one pointer table at $20200, and the page table at $20400 for
// logical $000000-$03FFFF (all of RAM), mapped to itself. Logical
// $500000 has its own page table at $20500, empty until a test fills it.
const (
	root040   = 0x20000
	ptr040    = 0x20200
	page040   = 0x20400
	page040Hi = 0x20500 // logical $500000-$53FFFF
)

func new040(t *testing.T, code ...uint16) *Processor {
	t.Helper()
	c := &Processor{
		Model: Model68040,
		FPU:   FPU68040,
		PC:    0x1000,
		SR:    StatusSupervisor | StatusIPLMask,
		M:     &m68kmem.Decoder{M: m68kmem.NewRAM(0x40000)},
	}
	c.A[7], c.SSP = 0x8000, 0x8000
	for i, w := range code {
		c.physW(0x1000+uint32(2*i), w)
	}
	c.physL(root040, ptr040|2)
	c.physL(ptr040, page040|2)
	c.physL(ptr040+4*0x14, page040Hi|2)
	for p := uint32(0); p < 0x40; p++ {
		c.physL(page040+4*p, p<<12|1)
	}
	return c
}

// mmu040On loads SRP and URP, flushes the ATCs and turns translation on
// with 4 KB pages (12 words).
var mmu040On = []uint16{
	0x203C, 0x0002, 0x0000, // MOVE.L #root040,D0
	0x4E7B, 0x0807, // MOVEC D0,SRP
	0x4E7B, 0x0806, // MOVEC D0,URP
	0xF518,                 // PFLUSHA
	0x203C, 0x0000, 0x8000, // MOVE.L #$8000,D0 (E)
	0x4E7B, 0x0003, // MOVEC D0,TC
}

const mmu040OnSteps = 6

func TestMOVEC040(t *testing.T) {
	c := new040(t,
		0x4E7B, 0x0002, // MOVEC D0,CACR
		0x4E7A, 0x1002, // MOVEC CACR,D1
		0x4E7B, 0x6006, // MOVEC D6,DTT0
		0x4E7A, 0x2006, // MOVEC DTT0,D2
		0x4E7B, 0x0806, // MOVEC D0,URP
		0x4E7A, 0x3806, // MOVEC URP,D3
		0x4E7B, 0x6003, // MOVEC D6,TC
		0x4E7A, 0x4003, // MOVEC TC,D4
		0x4E7A, 0x5802, // MOVEC CAAR,D5: not on the 040
	)
	// All ones; for the TT register and TC, less the enables (which
	// would turn on translation through tables that aren't there).
	c.D[0], c.D[6] = 0xFFFFFFFF, 0xFFFF7FFF
	c.physL(VecIllegal*4, 0x3000)
	step(t, c, 9)
	for _, r := range []struct {
		name      string
		got, want uint32
	}{
		{"CACR", c.D[1], 0x80008000},
		{"DTT0", c.D[2], 0xFFFF6364},
		{"URP", c.D[3], 0xFFFFFE00},
		{"TC", c.D[4], 0x4000},
	} {
		if r.got != r.want {
			t.Errorf("%s reads $%08X, want $%08X", r.name, r.got, r.want)
		}
	}
	if c.PC != 0x3000 {
		t.Errorf("MOVEC CAAR went to $%X, want the illegal instruction vector", c.PC)
	}
	if !c.ICacheEnabled() {
		t.Errorf("CACR IE set but ICacheEnabled is false")
	}
}

// The 030's MMU instructions are F-line on the 040: that's how software
// tells them apart.
func Test030MMUOpcodesFLineOn040(t *testing.T) {
	c := new040(t, 0xF010, 0x4000) // PMOVE (A0),TC
	c.physL(VecLine1111*4, 0x3000)
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("PMOVE on the 040 went to $%X, want the F-line vector", c.PC)
	}
}

func TestMMU040Translates(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x243C, 0x1234, 0x5678, // MOVE.L #$12345678,D2
		0x23C2, 0x0050, 0x0010, // MOVE.L D2,$500010
		0x2239, 0x0050, 0x0010, // MOVE.L $500010,D1
	})...)
	c.physL(page040Hi, 0x30000|1) // logical $500000 -> physical $30000
	step(t, c, mmu040OnSteps+3)
	if !c.mmu4.enabled {
		t.Fatal("MMU not enabled")
	}
	if got := c.physPeekL(0x30010); got != 0x12345678 {
		t.Errorf("physical $30010 = $%08X, want the value written at logical $500010", got)
	}
	if c.D[1] != 0x12345678 {
		t.Errorf("D1 = $%08X, read back through the MMU", c.D[1])
	}
	if d := c.physPeekL(page040Hi); d&0x18 != 0x18 {
		t.Errorf("page descriptor $%08X: U and M should be set", d)
	}
	if d := c.physPeekL(page040 + 4*1); d&0x08 == 0 || d&0x10 != 0 {
		t.Errorf("code page descriptor $%08X: U set (fetched), M clear (not written)", d)
	}
	if d := c.physPeekL(ptr040 + 4*0x14); d&0x08 == 0 {
		t.Errorf("pointer descriptor $%08X: U should be set", d)
	}
}

// An 8 KB page: the page index is address bits 17-13.
func TestMMU0408KPages(t *testing.T) {
	c := new040(t, code(mmu040On[:len(mmu040On)-5], []uint16{
		0x203C, 0x0000, 0xC000, // MOVE.L #$C000,D0 (E, P)
		0x4E7B, 0x0003, // MOVEC D0,TC
		0x23FC, 0x0000, 0x00AB, 0x0050, 0x3004, // MOVE.L #$AB,$503004
	})...)
	// RAM as 8 KB pages: 32 of them.
	for p := uint32(0); p < 0x20; p++ {
		c.physL(page040+4*p, p<<13|1)
	}
	c.physL(page040Hi+4*1, 0x34000|1) // logical $502000 -> physical $34000
	step(t, c, mmu040OnSteps+1)
	if got := c.physPeekL(0x35004); got != 0xAB {
		t.Errorf("physical $35004 = $%X, want the write to logical $503004", got)
	}
}

// An indirect page descriptor points to the real one.
func TestMMU040Indirect(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x23FC, 0x0000, 0x00CD, 0x0050, 0x0020, // MOVE.L #$CD,$500020
	})...)
	c.physL(page040Hi, 0x600|2) // indirect, to $600
	c.physL(0x600, 0x31000|1)
	step(t, c, mmu040OnSteps+1)
	if got := c.physPeekL(0x31020); got != 0xCD {
		t.Errorf("physical $31020 = $%X", got)
	}
	if d := c.physPeekL(0x600); d&0x18 != 0x18 {
		t.Errorf("the indirect target $%08X should have U and M set", d)
	}
}

// A write to an invalid page takes an access error with a format $7
// frame; the handler maps the page and RTE runs the instruction again.
func TestMMU040AccessErrorRestarts(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x243C, 0xCAFE, 0xF00D, // MOVE.L #$CAFEF00D,D2
		0x267C, 0x0050, 0x0004, // MOVEA.L #$500004,A3
		0x26C2, // MOVE.L D2,(A3)+
		0x4E71, // NOP
	})...)
	c.physL(VecBusError*4, 0x3000)
	for i, w := range []uint16{
		0x3A2F, 0x000C, // MOVE.W 12(A7),D5: the special status word
		0x2C2F, 0x0014, // MOVE.L 20(A7),D6: the fault address
		0x2E0B,                                 // MOVE.L A3,D7: A3 as the handler sees it
		0x23FC, 0x0003, 0x1001, 0x0002, 0x0500, // MOVE.L #$31001,$20500: map the page
		0xF518, // PFLUSHA
		0x4E73, // RTE
	} {
		c.physW(0x3000+uint32(2*i), w)
	}
	step(t, c, mmu040OnSteps+3)
	if c.PC != 0x3000 {
		t.Fatalf("PC $%X after the faulting MOVE, want the access error handler", c.PC)
	}
	if fv := c.peekW(c.A[7] + 6); fv != 0x7008 {
		t.Errorf("frame format/vector $%04X, want $7008", fv)
	}
	if pc := c.physPeekL(c.A[7] + 2); pc != 0x1026 {
		t.Errorf("stacked PC $%X, want the faulting MOVE at $1026", pc)
	}
	sp := c.A[7]
	step(t, c, 6) // the handler, RTE
	if c.D[5] != 0x0405 {
		t.Errorf("SSW $%04X: want ATC, a write, long, supervisor data ($0405)", c.D[5])
	}
	if c.D[6] != 0x500004 {
		t.Errorf("fault address $%X, want $500004", c.D[6])
	}
	if c.D[7] != 0x500004 {
		t.Errorf("A3 in the handler = $%X: the (A3)+ should have been undone", c.D[7])
	}
	if c.A[7] != sp+60 {
		t.Errorf("RTE left A7 at $%X, want $%X (a 60-byte frame)", c.A[7], sp+60)
	}
	if c.PC != 0x1026 {
		t.Fatalf("RTE returned to $%X, want the MOVE at $1026", c.PC)
	}
	step(t, c, 1)
	if got := c.physPeekL(0x31004); got != 0xCAFEF00D {
		t.Errorf("physical $31004 = $%08X after the rerun", got)
	}
	if c.A[3] != 0x500008 {
		t.Errorf("A3 = $%X after the rerun, want $500008 (incremented once)", c.A[3])
	}
}

// Write-protected and supervisor-only pages fault; PTEST reports what it
// finds in MMUSR.
func TestMMU040ProtectionAndPTEST(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x7005,         // MOVEQ #5,D0
		0x4E7B, 0x0001, // MOVEC D0,DFC (supervisor data)
		0x43F9, 0x0050, 0x0000, // LEA $500000,A1
		0xF569,         // PTESTR (A1)
		0x4E7A, 0x2805, // MOVEC MMUSR,D2
		0x43F9, 0x0050, 0x1000, // LEA $501000,A1
		0xF569,         // PTESTR (A1): invalid
		0x4E7A, 0x3805, // MOVEC MMUSR,D3
		0x43F9, 0x0050, 0x0000, // LEA $500000,A1
		0x2211,                 // MOVE.L (A1),D1: reading is allowed
		0x22BC, 0x0000, 0x0001, // MOVE.L #1,(A1): a write-protected page
	})...)
	c.physL(VecBusError*4, 0x3000)
	c.physW(0x3000, 0x4E71)
	c.physL(page040Hi, 0x32000|0x400|0x80|4|1) // global, supervisor-only, write-protected
	step(t, c, mmu040OnSteps+10)
	if c.D[2] != 0x32000|0x400|0x80|4|1 {
		t.Errorf("MMUSR $%08X: want the page, G, S, W and R", c.D[2])
	}
	if c.D[3] != 0 {
		t.Errorf("MMUSR $%08X for an invalid page, want 0", c.D[3])
	}
	if c.PC == 0x3000 {
		t.Fatalf("a read from a write-protected page faulted")
	}
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("a write to a write-protected page should take an access error (PC $%X)", c.PC)
	}
}

// A user access to a supervisor-only page faults.
func TestMMU040SupervisorOnly(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x46FC, 0x0000, // MOVE #0,SR: user mode
		0x2239, 0x0050, 0x0000, // MOVE.L $500000,D1
	})...)
	c.physL(VecBusError*4, 0x3000)
	c.physW(0x3000, 0x4E71)
	c.USP = 0x7000
	c.physL(page040Hi, 0x32000|0x80|1)
	step(t, c, mmu040OnSteps+2)
	if c.PC != 0x3000 {
		t.Fatalf("a user read of a supervisor page went on to $%X", c.PC)
	}
	if ssw := c.peekW(c.A[7] + 12); ssw&7 != 1 || ssw&0x100 == 0 {
		t.Errorf("SSW $%04X: want a user data read", ssw)
	}
}

// DTT0 maps a block transparently for data, even where the tables
// don't; ITT0 doesn't apply to data; a write-protected TT block faults.
func TestMMU040TransparentTranslation(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x203C, 0x0000, 0xC000, // MOVE.L #$C000,D0: any mode, enabled, block $00
		0x4E7B, 0x0006, // MOVEC D0,DTT0
		0x23FC, 0x0000, 0x00AB, 0x0003, 0x8000, // MOVE.L #$AB,$38000
	})...)
	// Unmap $38000 in the tables: only DTT0 can reach it.
	c.physL(page040+4*0x38, 0)
	step(t, c, mmu040OnSteps+3)
	if c.PC == 0 || c.mmu4.dtt[0] != 0xC000 {
		t.Fatalf("DTT0 = $%X", c.mmu4.dtt[0])
	}
	if got := c.physPeekL(0x38000); got != 0xAB {
		t.Errorf("physical $38000 = $%X: DTT0 should have mapped it", got)
	}

	// Write protection in a TT register, with the MMU off: user
	// accesses to block $00 (so not the exception frame's writes).
	c = new040(t,
		0x203C, 0x0000, 0x8004, // MOVE.L #$8004,D0: block $00, user, W
		0x4E7B, 0x0006, // MOVEC D0,DTT0
		0x46FC, 0x0000, // MOVE #0,SR
		0x23FC, 0x0000, 0x00AB, 0x0003, 0x8000, // MOVE.L #$AB,$38000
	)
	c.USP = 0x7000
	c.physL(VecBusError*4, 0x3000)
	step(t, c, 4)
	if c.PC != 0x3000 {
		t.Errorf("a write to a write-protected TT block went on to $%X", c.PC)
	}
}

// PFLUSHN leaves global entries; PFLUSH (An) removes the page.
func TestPFLUSH040(t *testing.T) {
	c := new040(t)
	c.mmu4.tc = 0x8000
	c.mmu040Changed()
	c.physL(page040Hi, 0x30000|0x400|1) // global
	c.physL(page040Hi+4, 0x31000|1)
	c.SetMMU040State(MMU040State{TC: 0x8000, URP: root040, SRP: root040})
	c.translate040(0x500000, 1, false)
	c.translate040(0x501000, 1, false)
	valid := func(la uint32) bool {
		page := la >> 12
		e := c.mmu4.datc[(page^0x80)%atc040Size]
		return e.valid && e.logical == page
	}
	if !valid(0x500000) || !valid(0x501000) {
		t.Fatal("translations not cached")
	}
	c.flushATC040(func(e *atc040Entry) bool { return !e.g }) // PFLUSHAN
	if !valid(0x500000) || valid(0x501000) {
		t.Errorf("PFLUSHAN: the global page should stay, the other go")
	}

	// PFLUSH (A0) with DFC supervisor.
	c.physW(0x1000, 0xF508) // PFLUSH (A0)
	c.A[0], c.DFC, c.PC = 0x500000, 5, 0x1000
	step(t, c, 1)
	if valid(0x500000) {
		t.Errorf("PFLUSH (A0) left the page")
	}
}

func TestMOVE16(t *testing.T) {
	c := new040(t,
		0xF620, 0x9000, // MOVE16 (A0)+,(A1)+
		0xF600|1<<3|2, 0x0000, 0x3000, // MOVE16 $3000,(A2)+
	)
	for i := uint32(0); i < 16; i++ {
		c.physW(0x2000+2*i, uint16(0x1100+i))
	}
	c.A[0], c.A[1], c.A[2] = 0x2008, 0x3004, 0x4000 // low bits ignored
	step(t, c, 2)
	for i := uint32(0); i < 8; i++ {
		if got := c.peekW(0x3000 + 2*i); got != uint16(0x1100+i) {
			t.Errorf("$%X = $%04X after MOVE16 (A0)+,(A1)+", 0x3000+2*i, got)
		}
		if got := c.peekW(0x4000 + 2*i); got != uint16(0x1100+i) {
			t.Errorf("$%X = $%04X after MOVE16 $3000,(A2)+", 0x4000+2*i, got)
		}
	}
	if c.A[0] != 0x2018 || c.A[1] != 0x3014 || c.A[2] != 0x4010 {
		t.Errorf("A0-A2 = $%X $%X $%X, want each advanced by 16", c.A[0], c.A[1], c.A[2])
	}
}

// CINV and CPUSH are privileged and otherwise do nothing.
func TestCINVCPUSH(t *testing.T) {
	c := new040(t,
		0xF4D8,         // CINVA BC
		0xF4F8,         // CPUSHA BC
		0x46FC, 0x0000, // MOVE #0,SR
		0xF4D8, // CINVA BC in user mode
	)
	c.USP = 0x7000
	c.physL(VecPrivilege*4, 0x3000)
	step(t, c, 4)
	if c.PC != 0x3000 {
		t.Errorf("CINV in user mode went to $%X, want a privilege violation", c.PC)
	}
}

// A jump to an odd address takes an address error with a format $2
// frame holding the address.
func TestAddressError040(t *testing.T) {
	c := new040(t, 0x4EF9, 0x0000, 0x2001) // JMP $2001
	c.physL(VecAddressError*4, 0x3000)
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Fatalf("PC $%X, want the address error handler", c.PC)
	}
	if fv := c.peekW(c.A[7] + 6); fv != 0x200C {
		t.Errorf("format/vector $%04X, want $200C", fv)
	}
	if a := c.physPeekL(c.A[7] + 8); a != 0x2000 {
		t.Errorf("frame address $%X, want $2000", a)
	}
	if pc := c.physPeekL(c.A[7] + 2); pc != 0x1000 {
		t.Errorf("stacked PC $%X, want the JMP", pc)
	}
}

// The 040's FSAVE idle frame, which is how software recognises its FPU.
func TestFSAVE040(t *testing.T) {
	c := new040(t,
		0xF327,         // FSAVE -(A7): null (FPU unused)
		0xF200, 0x0422, // FADD.X FP0,FP0
		0xF327, // FSAVE -(A7): idle
	)
	step(t, c, 3)
	if got := c.physPeekL(c.A[7]); got != 0x41000000 {
		t.Errorf("idle frame $%08X, want $41000000", got)
	}
	if got := c.physPeekL(c.A[7] + 4); got != 0 {
		t.Errorf("null frame $%08X, want 0", got)
	}
}

// FSADD rounds to single precision; FDADD and FADD don't.
func TestFSADD(t *testing.T) {
	c := new040(t,
		0xF200, 0x0462, // FSADD.X FP1,FP0
		0xF200, 0x05E6, // FDADD.X FP1,FP3
	)
	third := 1.0 / 3
	c.FP[0], c.FP[1], c.FP[3] = 0, third, 0
	step(t, c, 2)
	if c.FP[0] != float64(float32(third)) {
		t.Errorf("FSADD gave %v, want %v", c.FP[0], float64(float32(third)))
	}
	if c.FP[3] != third || math.IsNaN(c.FP[3]) {
		t.Errorf("FDADD gave %v, want %v", c.FP[3], third)
	}
}

// State and SetState carry the 040's MMU, and translation resumes.
func TestState040(t *testing.T) {
	c := new040(t, code(mmu040On, []uint16{
		0x203C, 0x0000, 0xC000, // MOVE.L #$C000,D0
		0x4E7B, 0x0006, // MOVEC D0,DTT0
	})...)
	step(t, c, mmu040OnSteps+2)
	st := c.State()
	d := new040(t)
	d.SetState(st)
	if got := d.MMU040State(); got != c.MMU040State() {
		t.Errorf("restored MMU %+v, want %+v", got, c.MMU040State())
	}
	if d.M.Translate == nil {
		t.Errorf("translation not resumed")
	}
}

// On the 68LC040 every FP instruction takes the F-line vector with a
// format $4 frame: the next instruction's PC, the effective address and
// the instruction's own PC; registers are untouched and RTE goes on past
// it.
func TestLC040FLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		code []uint16
		ea   uint32
	}{
		{"FADD.X FP0,FP1", []uint16{0xF200, 0x00A2}, 0},
		{"FMOVE.D (A0)+,FP1", []uint16{0xF218, 0x5480}, 0x2000},
		{"FMOVE.D -(A0),FP1", []uint16{0xF220, 0x5480}, 0x2000 - 8},
		{"FMOVE.L #imm,FP0", []uint16{0xF23C, 0x4000, 0x1234, 0x5678}, 0},
		{"FMOVE.X FP2,($10,A0)", []uint16{0xF228, 0x6900, 0x0010}, 0x2010},
		{"FBEQ.W", []uint16{0xF281, 0x0010}, 0},
		{"FSEQ (A0)", []uint16{0xF250, 0x0001}, 0x2000},
		{"FSAVE -(A7)", []uint16{0xF327}, 0x8000 - 4},
	} {
		c := new040(t, append(tc.code, 0x4E71)...) // then a NOP
		c.FPU = FPUNone
		c.A[0] = 0x2000
		c.physL(VecLine1111*4, 0x3000)
		c.physW(0x3000, 0x4E73) // RTE
		next := 0x1000 + 2*uint32(len(tc.code))
		step(t, c, 1)
		if c.PC != 0x3000 {
			t.Fatalf("%s: PC $%X, want the F-line handler", tc.name, c.PC)
		}
		sp := c.A[7]
		if fv := c.peekW(sp + 6); fv != 0x402C {
			t.Errorf("%s: format/vector $%04X, want $402C", tc.name, fv)
		}
		if pc := c.physPeekL(sp + 2); pc != next {
			t.Errorf("%s: stacked PC $%X, want the next instruction at $%X", tc.name, pc, next)
		}
		if ea := c.physPeekL(sp + 8); ea != tc.ea {
			t.Errorf("%s: effective address $%X, want $%X", tc.name, ea, tc.ea)
		}
		if pc := c.physPeekL(sp + 12); pc != 0x1000 {
			t.Errorf("%s: faulted instruction's PC $%X, want $1000", tc.name, pc)
		}
		if c.A[0] != 0x2000 {
			t.Errorf("%s: A0 = $%X: the instruction mustn't run", tc.name, c.A[0])
		}
		step(t, c, 1) // RTE
		if c.PC != next || c.A[7] != 0x8000 {
			t.Errorf("%s: RTE to $%X with A7 $%X, want $%X and $8000", tc.name, c.PC, c.A[7], next)
		}
	}
}
