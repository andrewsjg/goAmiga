package amiga

import (
	"encoding/binary"
	"testing"
)

// copperBurstGap runs a CPU loop storing VHPOSR to chip RAM while the
// Copper waits for line 100 and then runs 40 MOVEs (80 words), and
// returns the longest time, in colour clocks, between two samples.
func copperBurstGap(t *testing.T, exact bool) int {
	t.Helper()
	prog := []uint16{0xFFFF, 0xFFFE}
	moves := []uint16{0x6441, 0xFFFE} // WAIT line 100, H $40
	for i := range 40 {
		moves = append(moves, 0x0180, uint16(i)) // MOVE COLOR00
	}
	prog = append(moves, prog...)
	a := startCopper(t, prog, 99)
	a.wireMemoryMap(false)
	a.ExactBus = exact
	code := []uint16{
		0x41F9, 0x0000, 0x4000, // LEA $4000,A0
		0x30F9, 0x00DF, 0xF006, // MOVE.W $DFF006,(A0)+
		0x60F8, // BRA.S back to the MOVE
	}
	buf := make([]byte, 2*len(code))
	for i, w := range code {
		binary.BigEndian.PutUint16(buf[2*i:], w)
	}
	a.ChipRAM.Write(0x1000, buf)
	a.CPU.PC = 0x1000
	a.cpuStopped = false
	for a.vpos < 102 {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	n := int(a.CPU.A[0]-0x4000) / 2
	samples := make([]byte, 2*n)
	a.ChipRAM.Read(0x4000, samples)
	gap := 0
	prev := -1
	for i := range n {
		v := binary.BigEndian.Uint16(samples[2*i:])
		at := int(v>>8)*227 + int(v&0xFF) // colour clocks into the frame
		if prev >= 0 {
			gap = max(gap, at-prev)
		}
		prev = at
	}
	return gap
}

// A Copper that wakes from a WAIT takes every even colour clock while it
// runs, so the CPU, which only uses even ones, waits for the whole burst.
// (The forecast sees this too, charging the instruction the Copper is
// running at; exact timing stops the CPU at the access itself.)
func TestExactBusCopperBurstStallsCPU(t *testing.T) {
	forecast, exact := copperBurstGap(t, false), copperBurstGap(t, true)
	t.Logf("longest gap between samples: %d colour clocks with the forecast, %d exact", forecast, exact)
	if exact < 160 {
		t.Errorf("exact: the CPU went on during the Copper's 80-word burst (longest gap %d colour clocks, want 160 or more)", exact)
	}
}

// A register is read when the instruction's bus cycle for it comes, not
// when the instruction starts: MOVE.W $DFF006,$4000 started at H 0 reads
// VHPOSR after fetching its source address.
func TestExactBusReadTakesItsPlace(t *testing.T) {
	read := func(exact bool) int {
		a := createTestAmiga(t)
		a.wireMemoryMap(false)
		a.ExactBus = exact
		code := []uint16{0x33F9, 0x00DF, 0xF006, 0x0000, 0x4000, 0x4E71} // MOVE.W $DFF006,$4000; NOP
		buf := make([]byte, 2*len(code))
		for i, w := range code {
			binary.BigEndian.PutUint16(buf[2*i:], w)
		}
		a.ChipRAM.Write(0x1000, buf)
		a.CPU.PC = 0x1000
		a.cpuStopped = false
		a.vpos, a.hpos = 100, 0
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		var b [2]byte
		a.ChipRAM.Read(0x4000, b[:])
		return int(b[1])
	}
	forecast, exact := read(false), read(true)
	t.Logf("VHPOSR read at H %d with the forecast, H %d exact", forecast, exact)
	if exact < 4 {
		t.Errorf("exact: VHPOSR read at H %d, want after the two address words' fetches (H 4 or later)", exact)
	}
}

// The 68000 starts chip bus cycles on even colour clocks only. BRA.S
// takes 10 cycles, so the fetch after it waits 2: ADDQ.L #1,D0 (8) and
// BRA.S (10) take 20 cycles a pass from chip RAM, not 18.
func TestExactBusEvenSlotAlignment(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	a.ExactBus = true
	code := []byte{0x52, 0x80, 0x60, 0xFC} // ADDQ.L #1,D0; BRA.S back
	a.ChipRAM.Write(0x1000, code)
	a.CPU.PC = 0x1000
	a.cpuStopped = false
	a.CPU.D[0] = 0
	start := a.cycles
	for a.cycles-start < 100000 {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	per := float64(a.cycles-start) / float64(a.CPU.D[0])
	t.Logf("%.2f cycles a pass", per)
	if per < 19.9 || per > 20.1 {
		t.Errorf("%.2f cycles a pass, want 20", per)
	}
}
