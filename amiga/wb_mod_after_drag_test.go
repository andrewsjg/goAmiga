package amiga

import (
	"os"
	"testing"
)

// After many button-down moves, check whether display regs / bitplane structure
// stay coherent (BPLMOD, BPR, plane pointers).
func TestWBModsStableDuringDrag(t *testing.T) {
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
	for i := 0; i < 30; i++ {
		a.RunFrame()
	}

	// Snapshot plane 0 checksum
	sumPlane := func(addr, nbytes int) uint32 {
		b := make([]byte, nbytes)
		a.ChipRAM.Read(addr, b)
		var s uint32
		for _, v := range b {
			s = s*131 + uint32(v)
		}
		return s
	}
	// BitMap from earlier: P0=$D510 bpr=80 rows=256
	p0 := 0xD510
	bpr := 80
	rows := 256
	sum0 := sumPlane(p0, bpr*rows)
	mod0 := a.Custom.ReadReg16(0x108)
	mod1 := a.Custom.ReadReg16(0x10A)
	ddf0 := a.Custom.ReadReg16(0x092)
	ddf1 := a.Custom.ReadReg16(0x094)
	t.Logf("initial BPLMOD=$%04X/$%04X DDF=$%04X/$%04X planeSum=$%08X", mod0, mod1, ddf0, ddf1, sum0)

	// Count shift blits (con0 high nibble non-zero) and DESC
	shiftN, descN, xorN := 0, 0, 0
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		if con0&0xF000 != 0 {
			shiftN++
		}
		if con1&0x0002 != 0 {
			descN++
		}
		mt := con0 & 0xFF
		if mt == 0x5A || mt == 0xA5 || mt == 0x66 {
			xorN++
		}
	}

	// Aggressive drag-like input
	for session := 0; session < 12; session++ {
		for i := 0; i < 15; i++ {
			a.Mouse.Move(-30, -20)
			a.RunFrame()
		}
		a.Mouse.Move(80+session*10, 12)
		for i := 0; i < 5; i++ {
			a.RunFrame()
		}
		// double-click
		for c := 0; c < 2; c++ {
			a.Mouse.LeftButton = true
			a.RunFrame()
			a.RunFrame()
			a.RunFrame()
			a.Mouse.LeftButton = false
			a.RunFrame()
			a.RunFrame()
		}
		for i := 0; i < 15; i++ {
			a.RunFrame()
		}
		// drag
		a.Mouse.LeftButton = true
		for i := 0; i < 100; i++ {
			a.Mouse.Move(2-((i/10)%3), (i%5)-2)
			a.RunFrame()
		}
		a.Mouse.LeftButton = false
		for i := 0; i < 10; i++ {
			a.RunFrame()
		}

		sum1 := sumPlane(p0, bpr*rows)
		mod0b := a.Custom.ReadReg16(0x108)
		mod1b := a.Custom.ReadReg16(0x10A)
		// Sample mid-frame BPLMOD by running to line 100
		// Just log end-of-frame regs
		t.Logf("session %d sum=$%08X delta=%v BPLMOD=$%04X/$%04X shiftBlits=%d desc=%d xor=%d",
			session, sum1, sum1 != sum0, mod0b, mod1b, shiftN, descN, xorN)

		// Check for shear signature: if plane data has strong period-2 vertical correlation
		// compute simple entropy of first 20 rows
		b := make([]byte, bpr*20)
		a.ChipRAM.Read(p0, b)
		nonzero := 0
		for _, v := range b {
			if v != 0 {
				nonzero++
			}
		}
		t.Logf("  first20rows nonzeroBytes=%d/%d", nonzero, len(b))
		if mod0b != 0 || mod1b != 0 {
			t.Logf("  WARNING nonzero BPLMOD")
		}
	}
}
