package amiga

import (
	"fmt"
	"os"
	"testing"
)

func TestDiskBootError(t *testing.T) {
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

	// Capture first entry to error/display paths
	seen := map[uint32]bool{}
	ioErrors := 0
	for i := 0; i < 12_000_000; i++ {
		pc := a.CPU.PC
		// Alert-ish
		if pc == 0xFC3130 && !seen[pc] {
			seen[pc] = true
			t.Logf("step=%d FC3130 A6=$%08X D2=$%08X (alert data?)", i, a.CPU.A[6], a.CPU.D[2])
			// read string pointers if any
		}
		if pc == 0xFD423C && !seen[0xFD423C] {
			seen[0xFD423C] = true
			t.Logf("step=%d enter timeout loop FD423C D3=$%08X D4=%d D5=%d D6=$%08X A3=$%08X A5=$%08X",
				i, a.CPU.D[3], a.CPU.D[4], a.CPU.D[5], a.CPU.D[6], a.CPU.A[3], a.CPU.A[5])
		}
		// trackdisk returns with error in d0
		if pc == 0xFEA366 {
			ioErrors++
			if ioErrors <= 15 {
				t.Logf("step=%d trackread return d0/d2=$%08X cyl=%d side=%d",
					i, a.CPU.D[2], a.Disk.cylinder, a.Disk.side)
			}
		}
		// IORequest error field store patterns - watch when d0 is error code 20-40
		if pc == 0xFEA368 {
			if a.CPU.D[0] != 0 && ioErrors <= 15 {
				t.Logf("  -> returning error code %d ($%08X)", int32(a.CPU.D[0]), a.CPU.D[0])
			}
		}
		// Decode path
		if pc == 0xFEAC62 && !seen[0xFEAC62+uint32(i/1000000)] {
			// limit
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("trackread returns logged=%d", ioErrors)
	_ = fmt.Sprintf
}
