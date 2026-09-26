package amiga

import (
	"os"
	"testing"
)

// Longer run after WB + mouse jiggle; count bitmap fill and key strings.
func TestWorkbenchDraw(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	a.Video.DisplayActive = false // speed

	wb := 0
	for i := 0; i < 40_000_000; i++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			b := make([]byte, 24)
			for j := 0; j < 24; j++ {
				v, e := a.CPU.M.Byte(int(np) + j)
				if e != nil || v == 0 {
					b = b[:j]
					break
				}
				b[j] = byte(v)
			}
			if string(b) == "workbench.task" && wb == 0 {
				wb = i
				t.Logf("step=%d WB start", i)
			}
		}
		// after WB, wiggle mouse slowly
		if wb > 0 && i%10000 == 0 {
			a.Mouse.Move(1, 0)
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if i > 0 && i%10_000_000 == 0 {
			nz := countPlaneNZ(a, 0xD510, 80, 256)
			t.Logf("--- %dM planeNZ=%d dmas done cyl=%d ---", i/1_000_000, nz, a.Disk.cylinder)
		}
	}

	nz := countPlaneNZ(a, 0xD510, 80, 256)
	t.Logf("FINAL planeNZ lines=%d", nz)

	// strings of interest
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	for _, s := range []string{
		"Workbench1.3", "Workbench", "RAM DISK", "RAM", "Trash", "Empty",
		".info", "disk", "Drawer", "Shell", "System",
	} {
		c := 0
		b := []byte(s)
		for i := 0; i+len(b) <= len(mem); i++ {
			match := true
			for j := 0; j < len(b); j++ {
				if mem[i+j] != b[j] {
					match = false
					break
				}
			}
			if match {
				c++
			}
		}
		t.Logf("str %q count=%d", s, c)
	}

	// try left click
	a.Mouse.LeftButton = true
	for i := 0; i < 500_000; i++ {
		a.Step()
	}
	a.Mouse.LeftButton = false
	for i := 0; i < 2_000_000; i++ {
		a.Step()
	}
	nz2 := countPlaneNZ(a, 0xD510, 80, 256)
	t.Logf("after click planeNZ=%d", nz2)
}

func countPlaneNZ(a *Amiga, base, bpr, rows int) int {
	mem := make([]byte, bpr*rows)
	a.ChipRAM.Read(base, mem)
	nz := 0
	for line := 0; line < rows; line++ {
		row := mem[line*bpr : (line+1)*bpr]
		for _, b := range row {
			if b != 0 {
				nz++
				break
			}
		}
	}
	return nz
}
