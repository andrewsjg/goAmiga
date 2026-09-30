package m68k

import (
	"encoding/binary"
	"testing"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

// new020 returns a 68020 in supervisor mode with code at $1000 and the
// supervisor stack at $8000.
func new020(t *testing.T, code ...uint16) *Processor {
	t.Helper()
	c := &Processor{
		Model: Model68020,
		PC:    0x1000,
		SR:    StatusSupervisor | StatusIPLMask,
		M:     &m68kmem.Decoder{M: m68kmem.NewRAM(0x40000)},
	}
	c.A[7], c.SSP = 0x8000, 0x8000
	for i, w := range code {
		c.pokeW(0x1000+uint32(2*i), w)
	}
	return c
}

func (c *Processor) pokeW(addr uint32, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	c.M.Write(int(addr), b[:])
}

func (c *Processor) pokeL(addr uint32, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	c.M.Write(int(addr), b[:])
}

func (c *Processor) peekW(addr uint32) uint16 { v, _ := c.M.Word(int(addr)); return v }
func (c *Processor) peekL(addr uint32) uint32 { v, _ := c.M.Long(int(addr)); return v }

func step(t *testing.T, c *Processor, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := c.Step(); err != nil {
			t.Fatalf("step %d at $%X: %v", i, c.PC, err)
		}
	}
}

func TestMOVECAndVBR(t *testing.T) {
	c := new020(t,
		0x4E7B, 0x0801, // MOVEC D0,VBR
		0x4E7A, 0x1801, // MOVEC VBR,D1
		0x4E40, // TRAP #0
	)
	c.D[0] = 0x2000
	c.pokeL(0x2000+32*4, 0x3000) // TRAP #0 vector relative to VBR
	c.pokeW(0x3000, 0x4E73)      // RTE
	step(t, c, 2)
	if c.VBR != 0x2000 || c.D[1] != 0x2000 {
		t.Fatalf("VBR=$%X D1=$%X", c.VBR, c.D[1])
	}
	sr := c.SR
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Fatalf("TRAP went to $%X, want the VBR-relative vector $3000", c.PC)
	}
	// Format 0 frame: SR, PC, format/vector word.
	sp := c.A[7]
	if sp != 0x8000-8 || c.peekW(sp) != uint16(sr) || c.peekL(sp+2) != 0x100A || c.peekW(sp+6) != 0x0080 {
		t.Errorf("frame at $%X: SR $%04X PC $%X fmt $%04X; want SR $%04X PC $100A fmt $0080",
			sp, c.peekW(sp), c.peekL(sp+2), c.peekW(sp+6), sr)
	}
	step(t, c, 1) // RTE pops all 8 bytes
	if c.PC != 0x100A || c.A[7] != 0x8000 {
		t.Errorf("after RTE PC=$%X SP=$%X, want $100A $8000", c.PC, c.A[7])
	}
}

// Divide by zero takes a format 2 frame with the instruction's address.
func TestFormat2Frame(t *testing.T) {
	c := new020(t,
		0x4E71,                         // NOP
		0x4C7C, 0x0000, 0x0000, 0x0000, // DIVU.L #0,D0
	)
	c.pokeL(5*4, 0x3000)
	c.pokeW(0x3000, 0x4E73) // RTE
	step(t, c, 2)
	sp := c.A[7]
	if c.PC != 0x3000 || sp != 0x8000-12 {
		t.Fatalf("PC=$%X SP=$%X", c.PC, sp)
	}
	if fw, ia := c.peekW(sp+6), c.peekL(sp+8); fw != 0x2014 || ia != 0x1002 {
		t.Errorf("format word $%04X, instruction address $%X; want $2014, $1002", fw, ia)
	}
	step(t, c, 1)
	if c.A[7] != 0x8000 {
		t.Errorf("RTE left SP $%X, want $8000 (12-byte frame popped)", c.A[7])
	}
}

func TestNoDataAddressErrors020(t *testing.T) {
	c := new020(t, 0x2010) // MOVE.L (A0),D0
	c.A[0] = 0x3001
	c.pokeL(0x3001, 0x12345678)
	step(t, c, 1)
	if c.D[0] != 0x12345678 || c.PC != 0x1002 {
		t.Errorf("D0=$%X PC=$%X: odd long read should just work", c.D[0], c.PC)
	}
}

func TestMoveFromSRPrivileged020(t *testing.T) {
	c := new020(t, 0x40C0, 0x42C1) // MOVE SR,D0 ; MOVE CCR,D1
	c.pokeL(8*4, 0x3000)
	c.SR = 0x0015 // user mode, X Z C
	c.USP, c.A[7] = 0x7000, 0x7000
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Fatalf("MOVE SR in user mode went to $%X, want the privilege vector", c.PC)
	}
	c = new020(t, 0x42C1)
	c.SR = 0x2015
	step(t, c, 1)
	if c.D[1]&0xFFFF != 0x15 {
		t.Errorf("MOVE CCR,D1 gave $%X, want $15", c.D[1]&0xFFFF)
	}
}

func TestSmall020Instructions(t *testing.T) {
	c := new020(t,
		0x49C2,                 // EXTB.L D2
		0x480E, 0xFFFF, 0x0000, // LINK.L A6,#-$10000
		0x57FC,                 // TRAPEQ (Z clear: not taken)
		0x60FF, 0x0000, 0x0100, // BRA.L +$100
	)
	c.D[2] = 0x12345680
	step(t, c, 1)
	if c.D[2] != 0xFFFFFF80 || c.SR&StatusNegative == 0 {
		t.Errorf("EXTB.L: D2=$%X SR=$%X", c.D[2], c.SR)
	}
	c.A[6], c.A[7] = 0xAAAA, 0x30000
	step(t, c, 1)
	if c.A[6] != 0x30000-4 || c.A[7] != 0x30000-4-0x10000 || c.peekL(0x30000-4) != 0xAAAA {
		t.Errorf("LINK.L: A6=$%X SP=$%X", c.A[6], c.A[7])
	}
	c.SR &^= StatusZero
	step(t, c, 1)
	if c.PC != 0x100A {
		t.Errorf("TRAPEQ not taken: PC=$%X", c.PC)
	}
	step(t, c, 1)
	if c.PC != 0x100C+0x100 {
		t.Errorf("BRA.L went to $%X, want $%X", c.PC, 0x100C+0x100)
	}

	// RTD pops the return address, then the parameters.
	c = new020(t, 0x4E74, 0x0008)
	c.A[7] = 0x7000
	c.pokeL(0x7000, 0x2468)
	step(t, c, 1)
	if c.PC != 0x2468 || c.A[7] != 0x7000+4+8 {
		t.Errorf("RTD: PC=$%X SP=$%X", c.PC, c.A[7])
	}

	// TRAPcc taken: TRAPV vector, format 2.
	c = new020(t, 0x57FC)
	c.pokeL(7*4, 0x3000)
	c.SR |= StatusZero
	step(t, c, 1)
	if c.PC != 0x3000 || c.peekW(c.A[7]+6) != 0x201C {
		t.Errorf("TRAPEQ taken: PC=$%X format $%04X", c.PC, c.peekW(c.A[7]+6))
	}

	// CHK.L D1,D0 out of bounds.
	c = new020(t, 0x4101)
	c.pokeL(6*4, 0x3000)
	c.D[0], c.D[1] = 0x20000, 0x10000
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("CHK.L didn't trap: PC=$%X", c.PC)
	}
}

func TestMulDivLong(t *testing.T) {
	// MULU.L #$10000,D1:D0 (64-bit)
	c := new020(t, 0x4C3C, 0x0401, 0x0001, 0x0000)
	c.D[0] = 0x00123456
	step(t, c, 1)
	if c.D[1] != 0x12 || c.D[0] != 0x34560000 {
		t.Errorf("MULU.L 64: D1:D0 = $%X:%08X", c.D[1], c.D[0])
	}
	// MULS.L #2,D0 (32-bit): +2^31 doesn't fit, so V
	c = new020(t, 0x4C3C, 0x0800, 0x0000, 0x0002)
	c.D[0] = 0x40000000
	step(t, c, 1)
	if c.D[0] != 0x80000000 || c.SR&StatusOverflow == 0 {
		t.Errorf("MULS.L overflow: D0=$%X SR=$%X", c.D[0], c.SR)
	}
	// DIVU.L #7,D1:D0 (64-bit dividend $1_00000000)
	c = new020(t, 0x4C7C, 0x0401, 0x0000, 0x0007)
	c.D[1], c.D[0] = 1, 0
	step(t, c, 1)
	if c.D[0] != 0x24924924 || c.D[1] != 4 {
		t.Errorf("DIVU.L 64/32: q=$%X r=%d", c.D[0], c.D[1])
	}
	// DIVSL.L #3,D1:D0: -7 / 3 = -2 remainder -1
	c = new020(t, 0x4C7C, 0x0801, 0x0000, 0x0003)
	c.D[0] = uint32(0xFFFFFFF9)
	step(t, c, 1)
	if int32(c.D[0]) != -2 || int32(c.D[1]) != -1 {
		t.Errorf("DIVSL.L: q=%d r=%d", int32(c.D[0]), int32(c.D[1]))
	}
	// DIVU.L 64/32 overflow leaves the registers.
	c = new020(t, 0x4C7C, 0x0401, 0x0000, 0x0001)
	c.D[1], c.D[0] = 5, 9
	step(t, c, 1)
	if c.SR&StatusOverflow == 0 || c.D[1] != 5 || c.D[0] != 9 {
		t.Errorf("DIVU.L overflow: SR=$%X D1:D0=$%X:%X", c.SR, c.D[1], c.D[0])
	}
}

func TestAddressing020(t *testing.T) {
	// Brief format with scale: LEA (4,A0,D1.L*4),A2
	c := new020(t, 0x45F0, 0x1C04)
	c.A[0], c.D[1] = 0x3000, 3
	step(t, c, 1)
	if c.A[2] != 0x3000+4+12 {
		t.Errorf("scaled brief: A2=$%X", c.A[2])
	}
	// Full format, long base displacement: LEA ($12345,A0,D1.W*2),A2
	c = new020(t, 0x45F0, 0x1330, 0x0001, 0x2345)
	c.A[0], c.D[1] = 0x3000, 0xFFFF0002 // word index 2
	step(t, c, 1)
	if c.A[2] != 0x3000+0x12345+4 {
		t.Errorf("full format: A2=$%X", c.A[2])
	}
	// Base register suppressed: LEA ($100,ZA0,D1.L*1),A2
	c = new020(t, 0x45F0, 0x19A0, 0x0100)
	c.A[0], c.D[1] = 0x3000, 5
	step(t, c, 1)
	if c.A[2] != 0x105 {
		t.Errorf("base suppressed: A2=$%X", c.A[2])
	}
	// PC-relative full format: LEA ($10,PC,D1.L*2),A2 (PC = extension word)
	c = new020(t, 0x45FB, 0x1B20, 0x0010)
	c.D[1] = 4
	step(t, c, 1)
	if c.A[2] != 0x1002+0x10+8 {
		t.Errorf("PC full format: A2=$%X", c.A[2])
	}
	// Memory indirect pre-indexed: MOVE.L ([8,A0,D1.L*4],12),D0
	c = new020(t, 0x2030, 0x1D22, 0x0008, 0x000C)
	c.A[0], c.D[1] = 0x3000, 2
	c.pokeL(0x3010, 0x4000)
	c.pokeL(0x400C, 0xCAFEBABE)
	step(t, c, 1)
	if c.D[0] != 0xCAFEBABE {
		t.Errorf("pre-indexed: D0=$%X", c.D[0])
	}
	// Memory indirect post-indexed: MOVE.L ([8,A0],D1.L*4,12),D0
	c = new020(t, 0x2030, 0x1D26, 0x0008, 0x000C)
	c.A[0], c.D[1] = 0x3000, 2
	c.pokeL(0x3008, 0x5000)
	c.pokeL(0x5014, 0x0BADF00D)
	step(t, c, 1)
	if c.D[0] != 0x0BADF00D {
		t.Errorf("post-indexed: D0=$%X", c.D[0])
	}
}

// On the 68000 none of this exists: MOVEC is illegal and $FF branch
// displacements are -1.
func TestModel68000Unchanged(t *testing.T) {
	c := new020(t, 0x4E7A, 0x1801)
	c.Model = Model68000
	c.pokeL(4*4, 0x3000)
	step(t, c, 1)
	if c.PC != 0x3000 {
		t.Errorf("MOVEC on the 68000 went to $%X, want the illegal vector", c.PC)
	}
}
