package amiga

import (
	"fmt"
	"os"
	"testing"
)

func TestDiskBootFast(t *testing.T) {
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
	// DisplayActive=false for speed

	hits := map[string]int{}
	pcs := map[uint32]string{
		0xFE8444: "strap",
		0xFC237E: "diskboot",
		0xFC3090: "coldprobe",
		0xFE97BE: "td_init",
		0xFF3E94: "dos_init",
		0xFEA11A: "motor_area",
	}
	var lastDSKLEN uint16
	dskDMA := 0
	cylHist := []int{}

	for i := 0; i < 40_000_000; i++ {
		pc := a.CPU.PC
		if name, ok := pcs[pc]; ok {
			hits[name]++
			if hits[name] <= 2 {
				t.Logf("step=%d %s motor=%v cyl=%d side=%d dsklen=$%04X dskpt=$%08X",
					i, name, a.Disk.motor, a.Disk.cylinder, a.Disk.side,
					a.Disk.dsklen, a.Disk.dskpt)
			}
		}
		// track DSKLEN
		if a.Disk.dsklen != lastDSKLEN {
			if a.Disk.dsklen&0x8000 != 0 {
				dskDMA++
				if dskDMA <= 10 {
					t.Logf("step=%d DSKDMA start len=$%04X pt=$%08X cyl=%d side=%d sync=$%04X PC=$%08X",
						i, a.Disk.dsklen, a.Disk.dskpt, a.Disk.cylinder, a.Disk.side, a.Disk.dsksync, pc)
				}
			}
			lastDSKLEN = a.Disk.dsklen
		}
		if i > 0 && i%10_000_000 == 0 {
			t.Logf("--- %dM PC=$%08X motor=%v cyl=%d hits=%v dskDMA=%d ---",
				i/1_000_000, pc, a.Disk.motor, a.Disk.cylinder, hits, dskDMA)
			cylHist = append(cylHist, a.Disk.cylinder)
		}
		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	t.Logf("FINAL PC=$%08X motor=%v cyl=%d hits=%v dskDMA=%d",
		a.CPU.PC, a.Disk.motor, a.Disk.cylinder, hits, dskDMA)
	t.Logf("SelectCount=%d DSKLEN writes=%d", a.Disk.SelectCount, a.Disk.dsklenWrites)
	_ = fmt.Sprintf
}
