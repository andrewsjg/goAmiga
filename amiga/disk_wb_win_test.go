package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchWindow(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
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

	// Screen at B7E0
	scr := 0xB7E0
	t.Logf("Screen@$%06X %dx%d FirstWindow=$%08X Title ptr...",
		scr, rd16(scr+12), rd16(scr+14), rd32(scr+4))
	title := rd32(scr + 22)
	if title < 0x80000 {
		s := ""
		for k := 0; k < 60; k++ {
			c := mem[int(title)+k]
			if c == 0 {
				break
			}
			s += string(c)
		}
		t.Logf("  Title=%q", s)
	}

	// Walk windows
	w := rd32(scr + 4)
	for n := 0; n < 10 && w >= 0x1000 && w < 0x80000; n++ {
		// Window V33: Next@0, Left@4, Top@6, Width@8, Height@10, MouseX@12, MouseY@14
		// Flags@24?, Title@32?
		le := int16(rd16(int(w) + 4))
		te := int16(rd16(int(w) + 6))
		ww := rd16(int(w) + 8)
		wh := rd16(int(w) + 10)
		t.Logf("Window@$%08X Next=$%08X geom=%d,%d %dx%d",
			w, rd32(int(w)), le, te, ww, wh)
		// dump more fields
		t.Logf("  +12..+40: %04X %04X %04X %04X %08X %08X %08X %08X %08X",
			rd16(int(w)+12), rd16(int(w)+14), rd16(int(w)+16), rd16(int(w)+18),
			rd32(int(w)+20), rd32(int(w)+24), rd32(int(w)+28), rd32(int(w)+32), rd32(int(w)+36))
		// RPort at +50? varies - try find RastPort with BitMap
		// Window.RPort is APTR at offset 50 ($32) in V33
		for _, off := range []int{0x32, 0x30, 0x34, 0x28, 0x2C, 0x38} {
			rp := rd32(int(w) + off)
			if rp < 0x1000 || rp >= 0x80000 {
				continue
			}
			// RastPort.BitMap at +4
			bm := rd32(int(rp) + 4)
			if bm >= 0x1000 && bm < 0x80000 {
				bpr := rd16(int(bm))
				rows := rd16(int(bm) + 2)
				depth := mem[int(bm)+5]
				if bpr >= 40 && bpr <= 80 && rows >= 50 && depth > 0 && depth < 7 {
					t.Logf("  RPort@$%08X (win+%d) BM@$%08X bpr=%d rows=%d depth=%d P0=$%08X",
						rp, off, bm, bpr, rows, depth, rd32(int(bm)+8))
				}
			}
		}
		w = rd32(int(w))
	}

	// DIW vs expected from screen height
	t.Logf("DIW=$%04X/$%04X copper; Screen 640x%d",
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090), rd16(scr+14))
}
