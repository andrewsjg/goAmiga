package amiga

import (
	"fmt"
	"testing"
)

// Trace each native Flood and bitmap bit counts after the insert-disk script.
func TestFloodSeries(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	countBits := func() (b1, b2 int) {
		buf := make([]byte, 2)
		for off := 0; off < 8000; off += 2 {
			a.ChipRAM.Read(0xA572+off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					b1++
				}
			}
			a.ChipRAM.Read(0xC4B2+off, buf)
			v = uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					b2++
				}
			}
		}
		return
	}

	n := 0
	for i := 0; i < 5_000_000 && n < 20; i++ {
		if a.CPU.PC == floodEntryPC {
			fg := a.readByteChip(a.CPU.A[1] + 25)
			x, y, mode := int(a.CPU.D[0]), int(a.CPU.D[1]), a.CPU.D[2]&0xFF
			b1a, b2a := countBits()
			// Let native flood run via Step
			a.Step()
			b1b, b2b := countBits()
			n++
			t.Logf("Flood#%d pen=%d mode=%d @(%d,%d) BPL1 %d→%d (Δ%d) BPL2 %d→%d (Δ%d)",
				n, fg, mode, x, y, b1a, b1b, b1b-b1a, b2a, b2b, b2b-b2a)
			continue
		}
		a.Step()
	}
	fb1, fb2 := countBits()
	t.Logf("total floods=%d final BPL1/BPL2 bits=%d/%d", n, fb1, fb2)
	_ = fmt.Sprintf
}
