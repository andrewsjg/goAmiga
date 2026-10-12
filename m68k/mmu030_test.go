package m68k

import (
	"encoding/binary"
	"testing"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// The test MMU setup: 4 KB pages, an initial shift of 8 and one 12-bit
// table level of short descriptors at $20000. Logical pages 0-$3F (all
// 256 KB of RAM: vectors, code, stack, the table itself) map to
// themselves; the tests map others beyond.
const (
	mmuTable = 0x20000
	mmuTC    = 0x80C8C000 // E, PS 12, IS 8, TIA 12
)

func new030(t *testing.T, code ...uint16) *Processor {
	t.Helper()
	c := &Processor{
		Model: Model68030,
		PC:    0x1000,
		SR:    StatusSupervisor | StatusIPLMask,
		M:     &m68kmem.Decoder{M: m68kmem.NewRAM(0x40000)},
	}
	c.A[7], c.SSP = 0x8000, 0x8000
	for i, w := range code {
		c.physW(0x1000+uint32(2*i), w)
	}
	for p := uint32(0); p < 0x40; p++ {
		c.physL(mmuTable+4*p, p<<12|1)
	}
	// TC and CRP for the PMOVEs at $1000 to load.
	c.physL(0x900, mmuTC)
	c.physL(0x908, 0x7FFF0002)
	c.physL(0x90C, mmuTable)
	return c
}

// physW and physL write physical memory, past the MMU.
func (c *Processor) physW(addr uint32, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	c.M.M.Write(int(addr), b[:])
}

func (c *Processor) physL(addr uint32, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	c.M.M.Write(int(addr), b[:])
}

func (c *Processor) physPeekL(addr uint32) uint32 {
	var b [4]byte
	c.M.M.Read(int(addr), b[:])
	return binary.BigEndian.Uint32(b[:])
}

// mmuOn is the code that turns the MMU on: CRP then TC, from $908 and
// $900 (8 words).
var mmuOn = []uint16{
	0x41F8, 0x0908, // LEA $908.W,A0
	0xF010, 0x4C00, // PMOVE (A0),CRP
	0x41F8, 0x0900, // LEA $900.W,A0
	0xF010, 0x4000, // PMOVE (A0),TC
}

func code(parts ...[]uint16) []uint16 {
	var out []uint16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestMMUTranslates(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x243C, 0x1234, 0x5678, // MOVE.L #$12345678,D2
		0x23C2, 0x0050, 0x0010, // MOVE.L D2,$500010
		0x2239, 0x0050, 0x0010, // MOVE.L $500010,D1
	})...)
	c.physL(mmuTable+4*0x500, 0x30000|1) // logical $500000 -> physical $30000
	step(t, c, 7)
	if !c.mmu.enabled {
		t.Fatal("MMU not enabled")
	}
	if got := c.physPeekL(0x30010); got != 0x12345678 {
		t.Errorf("physical $30010 = $%08X, want the value written at logical $500010", got)
	}
	if c.D[1] != 0x12345678 {
		t.Errorf("D1 = $%08X, read back through the MMU", c.D[1])
	}
	if d := c.physPeekL(mmuTable + 4*0x500); d&0x18 != 0x18 {
		t.Errorf("page descriptor $%08X: U and M should be set", d)
	}
	if d := c.physPeekL(mmuTable + 4*1); d&0x08 == 0 || d&0x10 != 0 {
		t.Errorf("code page descriptor $%08X: U set (fetched), M clear (not written)", d)
	}
}

// A write to an invalid page takes a bus error with the registers as they
// were; the handler maps the page and the instruction runs again.
func TestMMUBusErrorRestarts(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x243C, 0xCAFE, 0xF00D, // MOVE.L #$CAFEF00D,D2
		0x267C, 0x0060, 0x0004, // MOVEA.L #$600004,A3
		0x26C2, // MOVE.L D2,(A3)+
		0x4E71, // NOP
	})...)
	c.physL(VecBusError*4, 0x3000)
	for i, w := range []uint16{
		0x3A2F, 0x000A, // MOVE.W 10(A7),D5: the special status word
		0x2C2F, 0x0010, // MOVE.L 16(A7),D6: the fault address
		0x2E0B,                                 // MOVE.L A3,D7: A3 as the handler sees it
		0x23FC, 0x0003, 0x1001, 0x0002, 0x1800, // MOVE.L #$31001,$21800: map page $600
		0xF000, 0x2400, // PFLUSHA
		0x4E73, // RTE
	} {
		c.physW(0x3000+uint32(2*i), w)
	}
	step(t, c, 7) // MMU on, the two MOVEs, then the faulting MOVE
	if c.PC != 0x3000 {
		t.Fatalf("PC $%X after the faulting MOVE, want the bus error handler", c.PC)
	}
	if fv := c.peekW(c.A[7] + 6); fv != 0xB008 {
		t.Errorf("frame format/vector $%04X, want $B008", fv)
	}
	step(t, c, 6) // the handler, RTE
	if c.D[5]&0x0140 != 0x0100 || c.D[5]&7 != 5 {
		t.Errorf("SSW $%04X: want DF, a write, supervisor data", c.D[5])
	}
	if c.D[6] != 0x600004 {
		t.Errorf("fault address $%X, want $600004", c.D[6])
	}
	if c.D[7] != 0x600004 {
		t.Errorf("A3 in the handler = $%X: the (A3)+ should have been undone", c.D[7])
	}
	if c.PC != 0x101C {
		t.Fatalf("RTE returned to $%X, want the MOVE at $101C", c.PC)
	}
	step(t, c, 1)
	if got := c.physPeekL(0x31004); got != 0xCAFEF00D {
		t.Errorf("physical $31004 = $%08X after the rerun", got)
	}
	if c.A[3] != 0x600008 {
		t.Errorf("A3 = $%X after the rerun, want $600008 (incremented once)", c.A[3])
	}
}

// Write protection and supervisor-only pages fault; PTEST reports why.
func TestMMUProtectionAndPTEST(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x43F9, 0x0070, 0x0000, // LEA $700000,A1
		0xF011, 0x9F15, // PTESTW #5,(A1),#7,A0
		0x45F8, 0x0A00, // LEA $A00.W,A2
		0xF012, 0x6200, // PMOVE MMUSR,(A2)
		0x22BC, 0x0000, 0x0001, // MOVE.L #1,(A1)
	})...)
	c.physL(VecBusError*4, 0x3000)
	c.physW(0x3000, 0x4E71)
	c.physL(mmuTable+4*0x700, 0x32000|4|1) // write-protected
	step(t, c, 8)
	if sr := c.peekW(0xA00); sr&0x0800 == 0 || sr&7 != 1 {
		t.Errorf("MMUSR $%04X: want W and 1 level", sr)
	}
	if c.A[0] != mmuTable+4*0x700 {
		t.Errorf("PTEST put $%X in A0, want the descriptor's address", c.A[0])
	}
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("a write to a write-protected page should take a bus error (PC $%X)", c.PC)
	}
}

// TT0 maps a 16 MB block transparently, even where the tables don't.
func TestMMUTransparentTranslation(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x41F8, 0x0910, // LEA $910.W,A0
		0xF010, 0x0800, // PMOVE (A0),TT0
		0x23FC, 0x0000, 0x00AB, 0x0003, 0x8000, // MOVE.L #$AB,$38000
	})...)
	c.physL(0x910, 0x00008707) // TT0: block $00, enabled, RWM, any FC
	step(t, c, 7)
	if got := c.physPeekL(0x38000); got != 0xAB {
		t.Errorf("physical $38000 = $%X: TT0 should have mapped logical $38000 to it", got)
	}
}

// The 68030's CACR keeps its data cache bits; the 68020's doesn't.
func TestCACR030(t *testing.T) {
	c := new030(t, 0x4E7B, 0x0002, 0x4E7A, 0x1002) // MOVEC D0,CACR; MOVEC CACR,D1
	c.D[0] = 0xFFFFFFFF
	step(t, c, 2)
	if c.D[1] != 0x3313 {
		t.Errorf("CACR reads $%X, want $3313", c.D[1])
	}
}

// translateFor runs one translation directly, as the Decoder would, and
// reports the physical address or the fault.
func translateFor(c *Processor, addr uint32, fc uint8, write bool) (phys uint32, fault bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*busFault); !ok {
				panic(r)
			}
			fault = true
		}
	}()
	c.mmu.fcOverride = int(fc) | 8
	defer func() { c.mmu.fcOverride = 0 }()
	return uint32(c.translate(int(addr), 4, write)), false
}

// Two levels of long descriptors: limits, supervisor-only tables, early
// termination and indirect descriptors.
func TestMMUWalkLongDescriptors(t *testing.T) {
	c := new030(t)
	// IS 0, TIA 8, TIB 12, PS 12. Root: long table at $22000, limit 3
	// (indexes 0-3 only).
	c.mmu.crp = uint64(0x00030003)<<32 | 0x22000
	c.setTC(0x80C08C00)
	// Level A, long descriptors: index 0 -> level B at $23000 (long);
	// index 1 -> supervisor-only level B at $24000 (short);
	// index 2 -> an early-terminating page descriptor (16 MB at $00000000).
	c.physL(0x22000, 0x7FFF0003) // long table, no limit
	c.physL(0x22004, 0x23000)
	c.physL(0x22008, 0x7FFF0102) // S, short table
	c.physL(0x2200C, 0x24000)
	c.physL(0x22010, 0x00000001) // page descriptor
	c.physL(0x22014, 0x00000000)
	// Level B under index 0 (long): page 5 -> $35000; page 6 indirect ->
	// a short page descriptor at $25000 (-> $36000).
	c.physL(0x23000+8*5, 0x00000001)
	c.physL(0x23000+8*5+4, 0x35000)
	c.physL(0x23000+8*6, 0x00000002) // indirect (at the last level) to a short descriptor
	c.physL(0x23000+8*6+4, 0x25000)
	c.physL(0x25000, 0x36000|1)
	// Under index 1 (short): page 0 -> $37000.
	c.physL(0x24000, 0x37000|1)

	for _, tc := range []struct {
		addr      uint32
		fc        uint8
		want      uint32
		wantFault bool
	}{
		{0x00005123, 5, 0x35123, false},
		{0x00006010, 1, 0x36010, false},
		{0x01000444, 5, 0x37444, false}, // supervisor: allowed
		{0x01000444, 1, 0, true},        // user: supervisor-only
		{0x02ABCDEF, 1, 0x00ABCDEF, false},
		{0x04000000, 5, 0, true}, // index 4: over the root's limit
		{0x00007000, 5, 0, true}, // invalid page
	} {
		phys, fault := translateFor(c, tc.addr, tc.fc, false)
		if fault != tc.wantFault || !fault && phys != tc.want {
			t.Errorf("$%08X FC %d: phys $%X fault %v, want $%X fault %v", tc.addr, tc.fc, phys, fault, tc.want, tc.wantFault)
		}
	}
	if c.physPeekL(0x23000+8*6)&8 == 0 || c.physPeekL(0x25000)&8 == 0 {
		t.Error("the indirect descriptor and its page descriptor should both have U set")
	}
}

// With FCL the function code picks the first table: user and supervisor
// see different memory at the same address.
func TestMMUFunctionCodeLookup(t *testing.T) {
	c := new030(t)
	c.mmu.crp = uint64(0x7FFF0002)<<32 | 0x22000
	c.setTC(0x81C8C000)             // E, FCL, PS 12, IS 8, TIA 12
	c.physL(0x22000+4*1, 0x23000|2) // user data -> table at $23000
	c.physL(0x22000+4*5, 0x24000|2) // supervisor data -> table at $24000
	c.physL(0x23000+4*0x100, 0x30000|1)
	c.physL(0x24000+4*0x100, 0x31000|1)
	if p, _ := translateFor(c, 0x100010, 1, false); p != 0x30010 {
		t.Errorf("user data at $100010 -> $%X, want $30010", p)
	}
	if p, _ := translateFor(c, 0x100010, 5, false); p != 0x31010 {
		t.Errorf("supervisor data at $100010 -> $%X, want $31010", p)
	}
}

// A fault fetching an instruction marks pipe stage B, with its address in
// the frame (AMIX reads it from there).
func TestMMUInstructionFault(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x4EF9, 0x0060, 0x0000, // JMP $600000
	})...)
	c.physL(VecBusError*4, 0x3000)
	step(t, c, 6) // MMU on, JMP, then the fetch at $600000 faults
	if c.PC != 0x3000 {
		t.Fatalf("PC $%X: the fetch from an unmapped page should take a bus error", c.PC)
	}
	sp := c.A[7]
	if ssw := c.peekW(sp + 0xA); ssw&0x5000 != 0x5000 || ssw&7 != 6 {
		t.Errorf("SSW $%04X: want FB and RB, supervisor program", ssw)
	}
	if b := c.peekL(sp + 0x24); b != 0x600000 {
		t.Errorf("stage B address $%X, want $600000", b)
	}
	if pc := c.peekL(sp + 2); pc != 0x600000 {
		t.Errorf("stacked PC $%X, want $600000 (the instruction to fetch again)", pc)
	}
}

// A long write straddling two pages, the second invalid, faults before
// writing anything.
func TestMMUPageCrossingWrite(t *testing.T) {
	c := new030(t, code(mmuOn, []uint16{
		0x23FC, 0x1122, 0x3344, 0x0050, 0x0FFE, // MOVE.L #$11223344,$500FFE
	})...)
	c.physL(VecBusError*4, 0x3000)
	c.physL(mmuTable+4*0x500, 0x30000|1) // page $501 left invalid
	step(t, c, 5)
	if c.PC != 0x3000 {
		t.Fatalf("PC $%X: the write into page $501 should fault", c.PC)
	}
	if got := c.physPeekL(0x30FFC); got != 0 {
		t.Errorf("physical $30FFC = $%08X: nothing should be written before the fault", got)
	}
	if fa := c.peekL(c.A[7] + 0x10); fa != 0x501000 {
		t.Errorf("fault address $%X, want $501000 (the first byte refused)", fa)
	}
}

// MOVES uses SFC/DFC: supervisor code writes user memory.
func TestMMUMOVES(t *testing.T) {
	c := new030(t)
	c.mmu.crp = uint64(0x7FFF0002)<<32 | 0x22000
	c.setTC(0x81C8C000) // FCL: user and supervisor tables differ
	for p := uint32(0); p < 0x40; p++ {
		c.physL(0x24000+4*p, p<<12|1) // supervisor: identity
	}
	c.physL(0x22000+4*5, 0x24000|2)
	c.physL(0x22000+4*6, 0x24000|2)
	c.physL(0x22000+4*1, 0x23000|2)
	c.physL(0x23000+4*0x100, 0x30000|1) // user data $100000 -> $30000
	for i, w := range []uint16{
		0x7001,         // MOVEQ #1,D0
		0x4E7B, 0x0001, // MOVEC D0,DFC
		0x207C, 0x0010, 0x0000, // MOVEA.L #$100000,A0
		0x243C, 0x0BAD, 0xBEEF, // MOVE.L #$BADBEEF,D2
		0x0E90, 0x2800, // MOVES.L D2,(A0)
	} {
		c.physW(0x1000+uint32(2*i), w)
	}
	step(t, c, 5)
	if got := c.physPeekL(0x30000); got != 0x0BADBEEF {
		t.Errorf("physical $30000 = $%08X: MOVES should have written user data space", got)
	}
}
