package amiga

import (
	"testing"
)

// TestNoDiskBoot verifies the emulator survives a no-disk boot (55M steps).
// Without a disk, the ROM shows the "insert disk" screen via graphics.library
// blitter operations. This exercises the blitter timing, interrupt delivery,
// and 68000 CCR handling (especially MOVEA not affecting flags).
func TestNoDiskBoot(t *testing.T) {
	romData, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip("Kick13.rom not found:", err)
	}
	a, err := New(romData, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}

	const maxSteps = 55_000_000

	for i := 0; i < maxSteps; i++ {
		if err := a.Step(); err != nil {
			t.Fatalf("Boot failed at step %d: %v (PC=$%08X SR=$%04X)", i, err, a.CPU.PC, a.CPU.SR)
		}
	}
	t.Logf("Completed %d steps, final PC=$%08X", maxSteps, a.CPU.PC)
}
