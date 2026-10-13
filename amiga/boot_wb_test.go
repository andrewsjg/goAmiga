package amiga

import (
	"os"
	"testing"
)

// Regression: blitter write guards must not prevent Kickstart from booting
// Workbench from ADF (insert-disk art uses planes that cross $D000).
func TestBootReachesWorkbench(t *testing.T) {
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

	for i := 0; i < 25_000_000; i++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(np), 24) == "workbench.task" {
				t.Logf("workbench.task at step %d", i)
				return
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("step: %v pc=$%08X", err, a.CPU.PC)
		}
	}
	t.Fatalf("did not reach workbench.task; pc=$%08X COLOR00=$%04X",
		a.CPU.PC, a.Custom.ReadReg16(0x180))
}
