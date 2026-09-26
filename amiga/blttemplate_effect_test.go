package amiga

import (
	"testing"
)

func TestBltTemplateEffect(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	countBits := func() (int, int) {
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

	n := 0
	for i := 0; i < 5_000_000 && n < 8; i++ {
		if a.CPU.PC == 0xFE8966 {
			b1a, b2a := countBits()
			// step until return to FE896A
			for j := 0; j < 500000; j++ {
				a.Step()
				if a.CPU.PC == 0xFE896A {
					break
				}
			}
			b1b, b2b := countBits()
			n++
			t.Logf("BltTemplate#%d D1(mod)=%d D2=%d D3=%d D4=%d D5=%d A0=$%05X  BPL1 %d→%d (Δ%d) BPL2 %d→%d (Δ%d)",
				n, a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.D[4], a.CPU.D[5], a.CPU.A[0],
				b1a, b1b, b1b-b1a, b2a, b2b, b2b-b2a)
			continue
		}
		a.Step()
	}
}
