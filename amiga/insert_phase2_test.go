package amiga

import (
	"testing"
)

func TestInsertDiskPhase2(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Resolve gfx LVOs at first hit of script area
	resolved := false
	phase2 := 0
	bltCalls := map[uint32]int{}

	for i := 0; i < 5_000_000; i++ {
		pc := a.CPU.PC
		if !resolved && pc >= 0xFE88AC && pc < 0xFE8A00 {
			a6 := a.CPU.A[6]
			if a6 > 0x100 {
				for _, o := range []int{-0x1E, -0x24, -0x2A, -0x30, -0x36, -0xF6, -0x156, -0x14A} {
					op, _ := a.CPU.M.Word(int(a6) + o)
					tgt, _ := a.CPU.M.Long(int(a6) + o + 2)
					t.Logf("LVO %d ($%X): op=$%04X → $%08X", o, uint16(o), op, tgt)
				}
				resolved = true
			}
		}
		// Phase2 entry
		if pc == 0xFE891E {
			phase2++
			t.Logf("step=%d PHASE2 start (template blits) BPL1bits pre", i)
		}
		if pc == 0xFE8966 {
			// the blit call
			a6 := a.CPU.A[6]
			// which LVO is -0x24?
			tgt, _ := a.CPU.M.Long(int(a6) - 0x24 + 2)
			bltCalls[uint32(tgt)]++
			if bltCalls[uint32(tgt)] <= 5 {
				t.Logf("step=%d blit@FE8966 → $%08X D0=%d D1=%d D2=%d D3=%d D4=%d D5=%d A0=$%08X A1=$%08X",
					i, tgt, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.D[4], a.CPU.D[5],
					a.CPU.A[0], a.CPU.A[1])
			}
		}
		// Count entry to various blit funcs
		for _, addr := range []uint32{0xFC74AA, 0xFC55A4, 0xFC7640} {
			if pc == addr {
				bltCalls[addr]++
			}
		}
		a.Step()
	}
	t.Logf("phase2 entries=%d blit targets=%v", phase2, bltCalls)
}
