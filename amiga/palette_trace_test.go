package amiga

import (
	"fmt"
	"testing"
)

func TestPaletteTrace(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true

	last := [4]uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}
	loadRGBHits := 0
	const loadRGB = uint32(0xFC5B4C)

	for i := 0; i < 8_000_000; i++ {
		pc := a.CPU.PC
		if pc == loadRGB {
			loadRGBHits++
			// A0=vp A1=colors D0=count
			t.Logf("step=%d LoadRGB4 count=%d colors=$%08X vp=$%08X",
				i, a.CPU.D[0]&0xFFFF, a.CPU.A[1], a.CPU.A[0])
			// dump first 4 colors from source
			if a.CPU.A[1] < ChipRAMSize {
				// might be ROM address
			}
			src := a.CPU.A[1]
			for c := 0; c < 4; c++ {
				var w uint16
				if src >= 0xFC0000 {
					// read via CPU mapper
					buf := make([]byte, 2)
					a.CPU.M.Read(int(src)+c*2, buf)
					w = uint16(buf[0])<<8 | uint16(buf[1])
				} else {
					buf := make([]byte, 2)
					a.ChipRAM.Read(int(src)+c*2, buf)
					w = uint16(buf[0])<<8 | uint16(buf[1])
				}
				t.Logf("  src[%d]=$%04X", c, w)
			}
		}
		// Track COLOR register changes
		for c := 0; c < 4; c++ {
			v := a.Custom.ReadReg16(0x180 + c*2)
			if v != last[c] {
				t.Logf("step=%d COLOR%02d $%04X→$%04X PC=$%08X",
					i, c, last[c], v, pc)
				last[c] = v
			}
		}
		a.Step()
	}
	t.Logf("LoadRGB4 hits=%d final COLOR %04X %04X %04X %04X",
		loadRGBHits, last[0], last[1], last[2], last[3])

	// Dump copper list colors
	cop2 := a.Copper.GetCop2LC()
	t.Logf("COP2LC=$%08X", cop2)
	if cop2 > 0 && cop2 < ChipRAMSize {
		buf := make([]byte, 4)
		addr := cop2
		for n := 0; n < 30; n++ {
			a.ChipRAM.Read(int(addr), buf)
			ir1 := uint16(buf[0])<<8 | uint16(buf[1])
			ir2 := uint16(buf[2])<<8 | uint16(buf[3])
			if ir1 == 0xFFFF && ir2 == 0xFFFE {
				t.Logf("  END")
				break
			}
			if ir1&1 == 0 {
				reg := ir1 & 0x1FE
				name := customRegNames[int(reg)]
				if name == "" {
					name = fmt.Sprintf("$%03X", reg)
				}
				if reg >= 0x180 && reg <= 0x1BE {
					t.Logf("  MOVE %s=$%04X", name, ir2)
				}
			}
			addr += 4
		}
	}
}
