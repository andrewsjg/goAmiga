package amiga

import (
	"fmt"
	"testing"
)

// Compare BPL state with DisplayActive (copper runs during STOP) vs without.
func TestBPLWithDisplayActive(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}

	run := func(display bool, steps int) (ff, zero, other, bits int, c0, c1, c2, c3 uint16, bpl2bits int) {
		a, _ := New(rom, nil)
		a.Reset()
		a.Video.DisplayActive = display
		const bpl1, bpl2 = 0xA572, 0xC4B2
		for i := 0; i < steps; i++ {
			a.Step()
		}
		// a few frames to let copper run
		if display {
			for i := 0; i < 5; i++ {
				a.RunFrame()
			}
		}
		buf := make([]byte, 2)
		for off := 0; off < 8000; off += 2 {
			a.ChipRAM.Read(bpl1+off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			switch v {
			case 0xFFFF:
				ff++
			case 0:
				zero++
			default:
				other++
			}
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					bits++
				}
			}
			a.ChipRAM.Read(bpl2+off, buf)
			v2 := uint16(buf[0])<<8 | uint16(buf[1])
			for i := 0; i < 16; i++ {
				if v2&(1<<uint(i)) != 0 {
					bpl2bits++
				}
			}
		}
		c0 = a.Custom.ReadReg16(0x180)
		c1 = a.Custom.ReadReg16(0x182)
		c2 = a.Custom.ReadReg16(0x184)
		c3 = a.Custom.ReadReg16(0x186)
		_ = saveBPL12PNG(a, fmt.Sprintf("/tmp/bpl_disp_%v_steps_%d.png", display, steps), bpl1, bpl2)
		return
	}

	for _, steps := range []int{3_000_000, 5_000_000, 8_000_000, 12_000_000} {
		for _, disp := range []bool{false, true} {
			ff, zero, other, bits, c0, c1, c2, c3, b2 := run(disp, steps)
			t.Logf("steps=%d display=%v: BPL1 FF=%d zero=%d other=%d bits=%d | BPL2bits=%d | COLOR %04X %04X %04X %04X",
				steps, disp, ff, zero, other, bits, b2, c0, c1, c2, c3)
		}
	}
}
