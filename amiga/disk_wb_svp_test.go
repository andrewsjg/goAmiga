package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchScreenVP(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	for i := 0; i < 14_000_000; i++ {
		a.Step()
	}
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	rd16 := func(i int) uint16 { return uint16(mem[i])<<8 | uint16(mem[i+1]) }
	rd32 := func(i int) uint32 {
		return uint32(mem[i])<<24 | uint32(mem[i+1])<<16 | uint32(mem[i+2])<<8 | uint32(mem[i+3])
	}

	scr := 0xB7E0
	// Try ViewPort at various offsets into Screen
	for _, off := range []int{40, 44, 48, 52, 56, 60, 64, 68, 72} {
		vp := scr + off
		// Look for DWidth=640 at vp+28
		if rd16(vp+28) == 640 {
			t.Logf("VP at Screen+%d ($%06X): Next=$%08X CM=$%08X DspIns=$%08X Modes=$%04X Dx=%d Dy=%d %dx%d RasInfo=$%08X",
				off, vp, rd32(vp), rd32(vp+4), rd32(vp+8), rd16(vp+20),
				int16(rd16(vp+24)), int16(rd16(vp+26)), rd16(vp+28), rd16(vp+30), rd32(vp+32))
			dsp := rd32(vp + 8)
			ri := rd32(vp + 32)
			if ri > 0x1000 && ri < 0x80000 {
				bm := rd32(int(ri) + 4)
				t.Logf("  RasInfo BM=$%08X", bm)
			}
			if dsp > 0x1000 && dsp < 0x80000 {
				t.Logf("  DspIns=$%08X data %08X %08X %08X", dsp, rd32(int(dsp)), rd32(int(dsp)+4), rd32(int(dsp)+8))
				// follow CopList to copper instructions
			}
		}
		// Also try DWidth at +32 (alt layout)
		if rd16(vp+32) == 640 {
			t.Logf("alt DWidth@+32 VP Screen+%d: Dy?=%d %dx%d",
				off, int16(rd16(vp+30)), 640, rd16(vp+34))
		}
	}

	// Dump raw Screen+40..+120
	t.Log("Screen raw +40..:")
	for o := 40; o < 120; o += 4 {
		t.Logf("  +%d: $%08X", o, rd32(scr+o))
	}
}
