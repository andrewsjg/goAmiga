package m68k

import "testing"

func new68000(t *testing.T, code ...uint16) *Processor {
	c := new020(t, code...)
	c.Model = Model68000
	c.Prefetch = true
	return c
}

// SDI's decryption loop: the first EOR decrypts the DBRA's displacement,
// but the DBRA was prefetched with the encrypted one and branches there.
func TestPrefetchSelfModifyingBranch(t *testing.T) {
	for _, prefetch := range []bool{true, false} {
		c := new68000(t,
			0x3018,         // loop: MOVE.W (A0)+,D0
			0xB159,         //       EOR.W  D0,(A1)+
			0x51C9, 0x0100, //       DBRA   D1,loop (displacement encrypted)
		)
		c.Prefetch = prefetch
		c.pokeW(0x2000, 0x0100^0xFFFA) // key: decrypts to -6, back to loop
		c.A[0], c.A[1], c.D[1] = 0x2000, 0x1006, 5
		step(t, c, 3)
		want := uint32(0x1106) // the encrypted displacement
		if !prefetch {
			want = 0x1000
		}
		if c.PC != want {
			t.Errorf("prefetch %v: DBRA went to $%X, want $%X", prefetch, c.PC, want)
		}
		if w := c.peekW(0x1006); w != 0xFFFA {
			t.Errorf("prefetch %v: displacement in memory $%04X, want $FFFA", prefetch, w)
		}
	}
}

// An instruction's final prefetch comes before its write, except MOVE's
// (which writes first). Here each writes the immediate word of the
// MOVE.W #imm,D2 after it.
func TestPrefetchWriteOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   uint16
		want uint16
	}{
		{"ADD.W D0,(A1)", 0xD151, 0x5678},  // read, prefetch, write: old word
		{"MOVE.W D0,(A1)", 0x3280, 0x0001}, // write, prefetch: new word
	} {
		c := new68000(t, tc.op, 0x343C, 0x5678) // op ; MOVE.W #$5678,D2
		c.A[1], c.D[0] = 0x1004, 1              // ADD makes the word $5679, MOVE $0001
		step(t, c, 2)
		if uint16(c.D[2]) != tc.want {
			t.Errorf("%s: MOVE.W #imm,D2 loaded $%04X, want $%04X", tc.name, uint16(c.D[2]), tc.want)
		}
	}
}

// A word already in the queue stays there: EOR changes the instruction
// two words on, which it prefetched before writing; the NOP after it
// passes it along, and it runs unchanged.
func TestPrefetchCarriesOver(t *testing.T) {
	c := new68000(t,
		0xB151, // EOR.W D0,(A1)
		0x4E71, // NOP
		0x7605, // MOVEQ #5,D3 (EOR makes it MOVEQ #6,D3)
		0x4E71, // NOP
		0x7607, // MOVEQ #7,D4: the queue has moved on, so this one is fresh
	)
	c.A[1], c.D[0] = 0x1004, 3
	step(t, c, 3)
	if c.D[3] != 5 {
		t.Errorf("D3 = %d, want 5 (the prefetched instruction)", c.D[3])
	}
	if w := c.peekW(0x1004); w != 0x7606 {
		t.Errorf("memory $%04X, want $7606", w)
	}
	// Running it again from memory sees the change.
	c.PC = 0x1004
	step(t, c, 1)
	if c.D[3] != 6 {
		t.Errorf("D3 = %d on a fresh fetch, want 6", c.D[3])
	}
}

// Illegal instructions and line A/F stack the address of the instruction
// itself (handlers that emulate or skip it add its length).
func TestIllegalStacksInstructionAddress(t *testing.T) {
	for _, op := range []uint16{0x4AFC, 0xA123, 0xF123} {
		c := new68000(t, 0x4E71, op)
		c.pokeL(VecIllegal*4, 0x3000)
		c.pokeL(VecLine1010*4, 0x3000)
		c.pokeL(VecLine1111*4, 0x3000)
		step(t, c, 2)
		if c.PC != 0x3000 {
			t.Fatalf("$%04X: PC $%X, want the handler", op, c.PC)
		}
		if pc := c.peekL(c.A[7] + 2); pc != 0x1002 {
			t.Errorf("$%04X: stacked PC $%X, want $1002", op, pc)
		}
	}
}
