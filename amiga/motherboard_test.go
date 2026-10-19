package amiga

import "testing"

// With Gary's bus timeout enabled ($DE0001 bit 7), reading nothing at
// $D00000 takes a bus error, with the 030's format $B or the 040's format
// $7 frame (no ATC bit: the bus, not the MMU); the Autoconfig area still
// reads quietly; with the timeout disabled, so does $D00000.
func TestGaryBusTimeout(t *testing.T) {
	for _, cpu := range []string{"68030", "68040"} {
		a := createTestAmiga(t)
		if err := a.SetCPU(cpu, 0); err != nil {
			t.Fatal(err)
		}
		if err := a.EnableMotherboard("a4000", 0); err != nil {
			t.Fatal(err)
		}
		a.Reset()
		a.wireMemoryMap(false) // chip RAM at 0 for the vectors and code
		for i, w := range []uint16{
			0x13FC, 0x0080, 0x00DE, 0x0001, // MOVE.B #$80,$DE0001: timeout on
			0x2039, 0x00E8, 0x0000, // MOVE.L $E80000,D0: Autoconfig, quiet
			0x2239, 0x00D0, 0x0000, // MOVE.L $D00000,D1: a bus error
		} {
			a.poke16(0x1000+uint32(2*i), w)
		}
		for i, w := range []uint16{
			0x13FC, 0x0000, 0x00DE, 0x0001, // MOVE.B #0,$DE0001: timeout off
			0x2439, 0x00D0, 0x0000, // MOVE.L $D00000,D2: reads nothing
			0x4E71, // NOP
		} {
			a.poke16(0x2000+uint32(2*i), w)
		}
		a.poke32(2*4, 0x2000) // the bus error vector
		c := a.CPU
		c.PC, c.SR, c.A[7] = 0x1000, 0x2700, 0x8000
		for i := 0; i < 3; i++ {
			if err := c.Step(); err != nil {
				t.Fatalf("%s: step %d: %v", cpu, i, err)
			}
		}
		if c.PC != 0x2000 {
			t.Fatalf("%s: PC $%X after reading $D00000, want the bus error handler", cpu, c.PC)
		}
		fv := a.peek16(c.A[7] + 6)
		switch cpu {
		case "68030":
			if fv != 0xB008 || a.peek32(c.A[7]+0x10) != 0xD00000 {
				t.Errorf("030 frame: format/vector $%04X, fault address $%X", fv, a.peek32(c.A[7]+0x10))
			}
		case "68040":
			ssw := a.peek16(c.A[7] + 0x0C)
			if fv != 0x7008 || a.peek32(c.A[7]+0x14) != 0xD00000 || ssw&0x0400 != 0 || ssw&0x0100 == 0 {
				t.Errorf("040 frame: format/vector $%04X, FA $%X, SSW $%04X (want a read, not ATC)",
					fv, a.peek32(c.A[7]+0x14), ssw)
			}
		}
		if pc := a.peek32(c.A[7] + 2); pc != 0x100E {
			t.Errorf("%s: stacked PC $%X, want the MOVE at $100E", cpu, pc)
		}
		for i := 0; i < 3; i++ {
			if err := c.Step(); err != nil {
				t.Fatalf("%s: handler step %d: %v", cpu, i, err)
			}
		}
		if c.PC != 0x2010 {
			t.Errorf("%s: with the timeout off, reading $D00000 went to $%X", cpu, c.PC)
		}
		if c.BusErrors {
			t.Errorf("%s: BusErrors still on after the timeout was disabled", cpu)
		}
	}
}
