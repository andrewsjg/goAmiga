package amiga

import (
	"encoding/binary"
	"testing"
)

func TestCIAAccessWait(t *testing.T) {
	a := createTestAmiga(t)
	cases := []struct {
		phase, n, elapsed, want int
	}{
		{0, 1, 4, 6},  // starts on an E boundary: 10 cycles total
		{1, 1, 4, 15}, // just missed it: 19 cycles total
		{6, 1, 4, 10}, // 4 to the boundary + 10 - 4
		{0, 2, 8, 12}, // second access starts on a boundary: 20 total
		{8, 1, 16, 6}, // access at cycle 12 of the instruction: phase 0
		{0, 0, 16, 0}, // no CIA access
	}
	for _, c := range cases {
		a.eClockCounter = c.phase
		if got := a.ciaAccessWait(c.n, c.elapsed); got != c.want {
			t.Errorf("phase %d, %d accesses, %d cycles: wait %d, want %d",
				c.phase, c.n, c.elapsed, got, c.want)
		}
	}
}

// MOVE.B $BFE001,D0 run by the CPU: whatever the E clock phase, the CIA
// read ends on an E cycle boundary and the access takes 10-19 cycles.
func TestCIAReadSyncsToEClock(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	prog := make([]byte, 6)
	binary.BigEndian.PutUint16(prog[0:], 0x1039) // MOVE.B abs.L,D0
	binary.BigEndian.PutUint32(prog[2:], 0x00BFE001)
	a.ChipRAM.Write(0x1000, prog)

	seen := map[int]bool{}
	for phase := 0; phase < eClockCycles; phase++ {
		a.CPU.PC = 0x1000
		a.cpuStopped = false
		a.eClockCounter = phase
		start, dmaWait := a.cycles, a.DMAWaitCycles
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		// Less any wait for a chip bus slot for the fetch (the program is
		// in chip RAM; with ExactBus the 68000 waits for an even colour
		// clock).
		extra := int(a.cycles-start) - a.CPU.LastCycles - int(a.DMAWaitCycles-dmaWait)
		if a.eClockCounter != 0 {
			t.Errorf("phase %d: access ended %d cycles into an E cycle", phase, a.eClockCounter)
		}
		if extra < 6 || extra > 15 {
			t.Errorf("phase %d: %d extra cycles, want 6-15", phase, extra)
		}
		seen[extra] = true
	}
	// (With ExactBus the fetch's wait for an even colour clock moves the
	// phase the CIA access starts at, so phases can share a wait.)
	if !a.ExactBus && len(seen) != eClockCycles {
		t.Errorf("extra cycles took %d distinct values over 10 phases, want 10", len(seen))
	}
	if a.CIAWaitCycles == 0 {
		t.Error("CIAWaitCycles not accumulated")
	}
}
