package amiga

import (
	"os"
	"testing"
)

// Smoke: after boot + idle, plane0 must not spontaneously fill with noise.
// (Full interactive drag is covered by manual testing; headless mouse rarely
// opens windows.)
func TestWBPlaneStableIdle(t *testing.T) {
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
	for i := 0; i < 20_000_000; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(namePtr), 24) == "workbench.task" {
				break
			}
		}
		a.Step()
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 40; i++ {
		a.RunFrame()
	}

	p0, bpr, rows := 0xD510, 80, 200
	sum := func() (s uint32, nz int) {
		b := make([]byte, bpr*rows)
		a.ChipRAM.Read(p0, b)
		for _, v := range b {
			s = s*131 + uint32(v)
			if v != 0 {
				nz++
			}
		}
		return
	}
	s0, n0 := sum()
	// Idle with some mouse motion (no reliable window open headless)
	for i := 0; i < 60; i++ {
		a.Mouse.Move(2-(i%5), (i%3)-1)
		a.RunFrame()
	}
	s1, n1 := sum()
	t.Logf("plane0 nonzero %d→%d sum $%08X→$%08X", n0, n1, s0, s1)
	// Should stay sparse (icons only). Full-screen salt is ~20% = ~3200+ nonzero bytes/20rows *10
	if n1 > n0+500 {
		t.Errorf("plane0 gained too many nonzero bytes: %d → %d (possible corruption)", n0, n1)
	}
}
