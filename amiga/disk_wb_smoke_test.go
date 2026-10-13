package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchSmoke(t *testing.T) {
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

	wb := 0
	ram := 0
	illegals := 0
	dmas := 0
	prevBusy := false
	prevPC := uint32(0)
	lastPC := uint32(0)

	for i := 0; i < 50_000_000; i++ {
		pc := a.CPU.PC
		if a.Disk.completionCycles > 0 && !prevBusy {
			dmas++
		}
		prevBusy = a.Disk.completionCycles > 0

		if pc == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			name := ""
			for j := 0; j < 32; j++ {
				v, err := a.CPU.M.Byte(int(namePtr) + j)
				if err != nil || v == 0 {
					break
				}
				name += string(rune(v))
			}
			if name == "workbench.task" {
				wb++
				if wb == 1 {
					t.Logf("step=%d workbench.task initPC=$%08X", i, a.CPU.A[0])
				}
			}
			if name == "RAM" {
				ram++
			}
		}

		// detect illegal (vec 4)
		if i > 2_000_000 && pc != lastPC+2 && pc != lastPC+4 && pc != lastPC {
			buf := make([]byte, 4)
			a.ChipRAM.Read(16, buf)
			h := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
			if h != 0 && pc == h {
				illegals++
				if illegals <= 5 {
					t.Logf("step=%d ILLEGAL from=$%08X", i, prevPC)
				}
			}
		}
		prevPC = pc
		lastPC = pc
		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if i > 0 && i%10_000_000 == 0 {
			eb, _ := a.CPU.M.Long(4)
			la := uint32(0)
			if eb > 0 {
				v, _ := a.CPU.M.Long(int(eb) + 0x202)
				la = uint32(v)
			}
			t.Logf("--- %dM PC=$%08X dmas=%d wb=%d ram=%d ill=%d LastAlert=$%08X cyl=%d ---",
				i/1_000_000, pc, dmas, wb, ram, illegals, la, a.Disk.cylinder)
		}
	}
	eb, _ := a.CPU.M.Long(4)
	la, _ := a.CPU.M.Long(int(eb) + 0x202)
	t.Logf("FINAL PC=$%08X dmas=%d wb=%d ram=%d ill=%d LastAlert=$%08X",
		a.CPU.PC, dmas, wb, ram, illegals, la)
	if illegals > 0 {
		t.Errorf("saw %d illegal instructions", illegals)
	}
	if wb == 0 {
		t.Error("workbench.task never started")
	}
}
