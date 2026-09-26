package amiga

import (
	"fmt"
	"os"
	"testing"
)

// TestKS31Diag boots Kickstart 3.1 (A500 build, 40.63) with no disk and
// saves the screen every 2 s to /tmp/ks31_<frame>.png:
//
//	GOAMIGA_DIAG=1 go test -run TestKS31Diag -v ./amiga/
func TestKS31Diag(t *testing.T) {
	requireDiag(t)
	path := "../Kick31.rom"
	if p := os.Getenv("GOAMIGA_KS31"); p != "" {
		path = p // e.g. a copy with the checksum fixed, for diagnosis
	}
	rom, err := LoadROM(path)
	if err != nil {
		t.Skip(err)
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(512, 0)
	a.Reset()
	a.Video.DisplayActive = true
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	// Catch illegal-instruction exceptions: execution arriving at the
	// vector 4 handler right after running another instruction.
	illegals := 0
	step := func() error {
		prev := a.CPU.PC
		if err := a.Step(); err != nil {
			return err
		}
		if h := rd(0x10); a.CPU.PC == h && prev != h && illegals < 5 && rd(4) > 0x100 {
			illegals++
			op, _ := a.CPU.M.Word(int(prev))
			eb := rd(4)
			task := rd(eb + 276)
			name := readCStrMem(a, rd(task+10), 32)
			t.Logf("ILLEGAL at $%06X opcode $%04X (ROM offset $%05X) task $%06X %q",
				prev, op, prev-0xF80000, task, name)
		}
		return nil
	}
	for f := 0; f <= 600; f++ {
		start := a.cycles
		for a.cycles-start < CyclesPerFrame {
			if err := step(); err != nil {
				t.Fatalf("frame %d: %v", f, err)
			}
		}
		if f%100 == 0 {
			eb := rd(4)
			t.Logf("frame %d: PC=$%06X SR=$%04X stopped=%v ExecBase=$%06X BPLCON0=$%04X DMACON=$%04X COLOR00=$%03X busErrors=%d",
				f, a.CPU.PC, a.CPU.SR, a.cpuStopped, eb, a.Custom.ReadReg16(0x100),
				a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x180), a.busErrorCount)
			saveScreenshot(a.Video, fmt.Sprintf("/tmp/ks31_%03d.png", f))
			if eb >= 0x100 && eb < 0x1000000 {
				t.Logf("  LastAlert=$%08X $%08X  [$100]=$%08X [$104]=$%08X  D7=$%08X A5=$%08X",
					rd(eb+0x202), rd(eb+0x206), rd(0x100), rd(0x104), a.CPU.D[7], a.CPU.A[5])
			}
		}
	}
}
