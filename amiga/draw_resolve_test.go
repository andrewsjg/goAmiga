package amiga

import (
	"testing"
)

func TestResolveDrawAndDisasm(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Run until graphics is open, then read Draw vector
	for i := 0; i < 2_000_000; i++ {
		a.Step()
		if i > 1_300_000 && i%10000 == 0 {
			// OpenLibrary graphics if we can find it
		}
	}

	// Find graphics.library on lib list
	// ExecBase+$15A LibList? KS1.3: SoftInts etc.
	// Use OpenLibrary LVO
	// Simpler: scan chip for jump table near known gfx
	// At boot after script, A6 for gfx calls is GfxBase

	// Intercept PolyDraw/Draw call from script at FE88xx
	// Script interpreter at FE88AC area calls Draw via jsr
	// From earlier disasm of script runner: 4EAE Fxxx LVOs

	// Just run and when PC is in FE88xx-FE89xx dump A6-$F6
	for i := 0; i < 1_000_000; i++ {
		pc := a.CPU.PC
		if pc >= 0xFE88AC && pc <= 0xFE8A00 {
			// try A6 as gfx
			a6 := a.CPU.A[6]
			if a6 > 0x100 && a6 < 0x80000 {
				// read Draw LVO -0xF6
				vec, err := a.CPU.M.Long(int(a6) - 0xF6 + 2) // skip JMP opcode
				if err == nil && vec > 0xFC0000 {
					t.Logf("step=%d PC=$%08X A6=$%08X Draw@$%08X", i, pc, a6, vec)
					// also -0xF0 Move, -0x150 PolyDraw
					for _, o := range []struct {
						name string
						off  int
					}{
						{"Move", -0xF0}, {"Draw", -0xF6}, {"PolyDraw", -0x150}, {"BltTemplate", -0x1E},
					} {
						op, _ := a.CPU.M.Word(int(a6) + o.off)
						tgt, _ := a.CPU.M.Long(int(a6) + o.off + 2)
						t.Logf("  %s LVO: op=$%04X tgt=$%08X", o.name, op, tgt)
					}
					return
				}
			}
		}
		a.Step()
	}
	t.Fatal("could not resolve Draw")
}
