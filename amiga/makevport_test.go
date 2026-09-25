package amiga

import (
	"testing"
)

func TestMakeVPortDIW(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	// Run to script where A6=GfxBase
	for i := 0; i < 2_200_000; i++ {
		if a.CPU.PC == 0xFE88AC {
			gb := a.CPU.A[6]
			t.Logf("GfxBase=$%08X", gb)
			for _, o := range []struct {
				n   string
				off int
			}{
				{"MakeVPort", -0xD8},
				{"MrgCop", -0xD2},
				{"LoadView", -0xDE},
				{"InitVPort", -0xCC},
				{"FreeVPortCopLists", -0x1A4},
			} {
				op, _ := a.CPU.M.Word(int(gb) + o.off)
				tgt, _ := a.CPU.M.Long(int(gb) + o.off + 2)
				t.Logf("  %s LVO %d: $%04X → $%08X", o.n, o.off, op, tgt)
			}
			return
		}
		a.Step()
	}
	t.Fatal("no script")
}
