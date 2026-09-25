package amiga

import (
	"fmt"
	"os"
	"testing"
)

func TestDiskBootError2(t *testing.T) {
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

	// Watch FEA22A (DSKLEN double write), FEA362 (return), FEA280 (CHNG fail), FEA36E (PROT fail)
	// FEA2A2 bit4 fail, decode entries
	sites := map[uint32]string{
		0xFEA22A: "dsklen_write",
		0xFEA362: "td_return",
		0xFEA280: "chng_fail",
		0xFEA36E: "prot_path",
		0xFEA282: "err_1d",
		0xFEA2AA: "err_23",
		0xFEAC00: "decode?",
		0xFEA9C0: "gap",
	}
	counts := map[string]int{}
	// Also dump IO std error from trackdisk unit structure if possible

	for i := 0; i < 8_000_000; i++ {
		pc := a.CPU.PC
		if name, ok := sites[pc]; ok {
			counts[name]++
			if counts[name] <= 5 {
				t.Logf("step=%d %s D0=$%08X D2=$%08X cyl=%d side=%d motor=%v chg=%v PRA=$%02X",
					i, name, a.CPU.D[0], a.CPU.D[2], a.Disk.cylinder, a.Disk.side, a.Disk.motor, a.Disk.diskChange,
					func() byte {
						// read CIA-A PRA through status
						return a.Disk.ReadStatus(0xFF)
					}())
			}
		}
		// Capture when FEA234 is entered (main read)
		if pc == 0xFEA234 {
			counts["td_read"]++
			if counts["td_read"] <= 5 {
				t.Logf("step=%d td_read A0=$%08X D0=$%08X A3=$%08X", i, a.CPU.A[0], a.CPU.D[0], a.CPU.A[3])
			}
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("counts=%v", counts)
	_ = fmt.Sprintf
}
