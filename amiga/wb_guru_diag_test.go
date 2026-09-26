package amiga

import (
	"os"
	"testing"
)

// Watch for Alert / illegal-instruction style crashes during drag-like input.
func TestWBGuruDuringDrag(t *testing.T) {
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
	for i := 0; i < 20_000_000; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(namePtr), 24) == "workbench.task" {
				break
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot step: %v pc=$%08X", err, a.CPU.PC)
		}
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		if err := a.Step(); err != nil {
			t.Fatalf("wb step: %v pc=$%08X", err, a.CPU.PC)
		}
	}
	for i := 0; i < 40; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatalf("frame: %v pc=$%08X", err, a.CPU.PC)
		}
	}

	lowWrites := 0
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		if dpt&0x7FFFE < 0x5000 {
			lowWrites++
			if lowWrites <= 12 {
				t.Logf("blit dpt=$%08X size=$%04X con0=$%04X con1=$%04X line=%v",
					dpt, size, con0, con1, con1&1 != 0)
			}
		}
	}

	// Aggressive mouse + clicks to provoke drag/OS activity
	for session := 0; session < 20; session++ {
		for i := 0; i < 10; i++ {
			a.Mouse.Move(-40, -30)
			if err := a.RunFrame(); err != nil {
				t.Fatalf("session %d home: %v pc=$%08X", session, err, a.CPU.PC)
			}
		}
		a.Mouse.Move(100+session*15, 20)
		for i := 0; i < 5; i++ {
			if err := a.RunFrame(); err != nil {
				t.Fatalf("move: %v", err)
			}
		}
		for c := 0; c < 2; c++ {
			a.Mouse.LeftButton = true
			for i := 0; i < 4; i++ {
				if err := a.RunFrame(); err != nil {
					t.Fatalf("click down: %v pc=$%08X", err, a.CPU.PC)
				}
			}
			a.Mouse.LeftButton = false
			for i := 0; i < 4; i++ {
				if err := a.RunFrame(); err != nil {
					t.Fatalf("click up: %v pc=$%08X", err, a.CPU.PC)
				}
			}
		}
		a.Mouse.LeftButton = true
		for i := 0; i < 80; i++ {
			a.Mouse.Move(2-(i%5), (i%3)-1)
			if err := a.RunFrame(); err != nil {
				t.Fatalf("drag session %d i=%d: %v pc=$%08X", session, i, err, a.CPU.PC)
			}
			// Detect guru / alert screen: copper/BPL often still run but PC in Alert
			if a.CPU.PC >= 0xFC3000 && a.CPU.PC < 0xFC3400 {
				t.Fatalf("in Alert ROM pc=$%08X session=%d", a.CPU.PC, session)
			}
		}
		a.Mouse.LeftButton = false
		for i := 0; i < 10; i++ {
			if err := a.RunFrame(); err != nil {
				t.Fatalf("release: %v pc=$%08X", err, a.CPU.PC)
			}
		}
	}

	// Check Exec LastAlert
	// AbsExecBase at 4
	ebuf := make([]byte, 4)
	a.ChipRAM.Read(4, ebuf)
	eb := uint32(ebuf[0])<<24 | uint32(ebuf[1])<<16 | uint32(ebuf[2])<<8 | uint32(ebuf[3])
	if eb > 0x1000 && eb < 0x80000 {
		a.ChipRAM.Read(int(eb)+0x20, ebuf) // LastAlert often at ExecBase+something
		// dump a few known offsets
		mem := make([]byte, 0x100)
		a.ChipRAM.Read(int(eb), mem)
		t.Logf("ExecBase=$%08X first bytes %02X%02X%02X%02X", eb, mem[0], mem[1], mem[2], mem[3])
	}
	t.Logf("low-dpt blits=%d final pc=$%08X", lowWrites, a.CPU.PC)
}
