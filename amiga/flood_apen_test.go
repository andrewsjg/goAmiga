package amiga

import (
	"fmt"
	"testing"
)

// Confirm: APen=2 is intentional from boot script FE-cmd, and measure
// what first Flood(pen=2) does to bitplanes.
func TestFloodAPenIntentional(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	const flood = uint32(0xFC5AD0)
	const bpl1, bpl2 = 0xA572, 0xC4B2

	countBits := func(base int) int {
		buf := make([]byte, 2)
		bits := 0
		for off := 0; off < 8000; off += 2 {
			a.ChipRAM.Read(base+off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					bits++
				}
			}
		}
		return bits
	}

	// Run to just before first Flood, snapshot, then after Flood returns
	preFlood := false
	for i := 0; i < 5_000_000; i++ {
		if a.CPU.PC == flood && !preFlood {
			preFlood = true
			fg := make([]byte, 1)
			a.ChipRAM.Read(0xA4D2+25, fg)
			t.Logf("step=%d ENTER Flood#1 pen(Fg)=%d D0=%d D1=%d D2=%d",
				i, fg[0], a.CPU.D[0], a.CPU.D[1], a.CPU.D[2])
			t.Logf("  before: BPL1 bits=%d BPL2 bits=%d", countBits(bpl1), countBits(bpl2))
		}
		// Detect return from first flood: FE8908 is bra back to script loop after Flood
		if preFlood && a.CPU.PC == 0xFE8908 {
			fg := make([]byte, 1)
			a.ChipRAM.Read(0xA4D2+25, fg)
			b1, b2 := countBits(bpl1), countBits(bpl2)
			t.Logf("step=%d after Flood#1 return: Fg=%d BPL1 bits=%d BPL2 bits=%d",
				i, fg[0], b1, b2)
			if b2 > 50000 {
				t.Logf("  DIAGNOSIS: Flood#1 filled nearly entire BPL2 (pen 2 blue) — flood is unbounded/wrong")
			} else if b2 > 100 && b2 < 10000 {
				t.Logf("  DIAGNOSIS: Flood#1 filled a bounded region — APen=2 working as intended for disk body")
			}
			// Continue to see SetAPen sequence is from script
			break
		}
		a.Step()
	}

	// Re-summarize script pen sequence
	t.Log("Boot script pen commands (from ROM FE89D0):")
	t.Log("  FF 01 ...  outline polylines in pen 1 (black)")
	t.Log("  FE 02 25 45  SetAPen(2)+Flood — blue disk body (INTENTIONAL)")
	t.Log("  FF 01 ...  more black outlines")
	t.Log("  FE 03 ...  pen 3 gray floods")
	t.Log("  FE 02 ...  more blue")
	t.Log("  FE 01 ...  black region floods (hand fill)")
	_ = fmt.Sprintf
}
