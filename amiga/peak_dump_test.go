package amiga

import (
	"fmt"
	"testing"
)

func TestPeakBPL1Dump(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	const bpl1 = 0xA572
	const bpl2 = 0xC4B2
	peakOther := 0
	peakStep := 0
	prevBusy := false
	buf := make([]byte, 2)
	for step := 0; step < 5_000_000; step++ {
		a.Step()
		if prevBusy && !a.Blitter.busy {
			other := 0
			for off := 0; off < 8000; off += 2 {
				a.ChipRAM.Read(bpl1+off, buf)
				v := uint16(buf[0])<<8 | uint16(buf[1])
				if v != 0 && v != 0xFFFF {
					other++
				}
			}
			if other > peakOther {
				peakOther = other
				peakStep = step
			}
		}
		prevBusy = a.Blitter.busy
	}
	t.Logf("Peak at step %d other=%d — re-running to that step", peakStep, peakOther)

	a2, _ := New(rom, nil)
	a2.Reset()
	for step := 0; step <= peakStep; step++ {
		a2.Step()
	}
	for i := 0; i < 4; i++ {
		t.Logf("COLOR%02d=$%04X", i, a2.Custom.ReadReg16(0x180+i*2))
	}
	bits := 0
	nonzeroLines := 0
	lineBuf := make([]byte, 40)
	for y := 0; y < 200; y++ {
		a2.ChipRAM.Read(bpl1+y*40, lineBuf)
		lineBits := 0
		for _, b := range lineBuf {
			for i := 0; i < 8; i++ {
				if b&(1<<uint(i)) != 0 {
					bits++
					lineBits++
				}
			}
		}
		if lineBits > 0 {
			nonzeroLines++
			if nonzeroLines <= 20 {
				hex := ""
				for i := 0; i < 20; i++ {
					hex += fmt.Sprintf(" %02X", lineBuf[i])
				}
				t.Logf("BPL1 y=%3d bits=%3d:%s", y, lineBits, hex)
			}
		}
	}
	t.Logf("BPL1 total bits set=%d nonzeroLines=%d", bits, nonzeroLines)
	bits2 := 0
	nz2 := 0
	for y := 0; y < 200; y++ {
		a2.ChipRAM.Read(bpl2+y*40, lineBuf)
		lb := 0
		for _, b := range lineBuf {
			for i := 0; i < 8; i++ {
				if b&(1<<uint(i)) != 0 {
					bits2++
					lb++
				}
			}
		}
		if lb > 0 {
			nz2++
		}
	}
	t.Logf("BPL2 total bits set=%d nonzeroLines=%d", bits2, nz2)

	// Also dump at step 4201906 (last IMAGE before solid fills) and 4301000
	for _, target := range []int{4201906, 4301000, 4991853} {
		a3, _ := New(rom, nil)
		a3.Reset()
		for step := 0; step <= target; step++ {
			a3.Step()
		}
		bits = 0
		other := 0
		ff := 0
		for off := 0; off < 8000; off += 2 {
			a3.ChipRAM.Read(bpl1+off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			switch v {
			case 0xFFFF:
				ff++
			case 0:
			default:
				other++
			}
			for i := 0; i < 16; i++ {
				if v&(1<<uint(i)) != 0 {
					bits++
				}
			}
		}
		t.Logf("step %d: BPL1 bits=%d words FF=%d other=%d COLOR00=$%04X COLOR01=$%04X",
			target, bits, ff, other, a3.Custom.ReadReg16(0x180), a3.Custom.ReadReg16(0x182))
		_ = saveBPL12PNG(a3, fmt.Sprintf("/tmp/bpl1_step_%d.png", target), bpl1, bpl2)
	}
}
