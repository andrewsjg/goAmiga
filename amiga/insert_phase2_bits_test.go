package amiga

import (
	"testing"
)

func TestInsertPhase2Bits(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	count := func() (int, int) {
		buf := make([]byte, 2)
		b1, b2 := 0, 0
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
		return b1, b2
	}

	pre1, pre2 := 0, 0
	gotPhase2 := false
	for i := 0; i < 5_000_000; i++ {
		if a.CPU.PC == 0xFE891E && !gotPhase2 {
			pre1, pre2 = count()
			t.Logf("step=%d before phase2 BPL1=%d BPL2=%d", i, pre1, pre2)
			gotPhase2 = true
		}
		if gotPhase2 && a.CPU.PC == 0xFE896C {
			// end of phase2 loop (bmi)
			p1, p2 := count()
			t.Logf("step=%d after phase2 BPL1=%d BPL2=%d Δ1=%d Δ2=%d", i, p1, p2, p1-pre1, p2-pre2)
			if p1 == pre1 && p2 == pre2 {
				t.Error("phase2 blits had ZERO effect on bitplanes — BltPattern broken")
			}
			return
		}
		a.Step()
	}
	t.Fatal("did not complete phase2")
}
