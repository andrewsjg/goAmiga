package amiga

import (
	"testing"
)

// Track COP2LC over insert-disk boot — should never be ExecBase ($676).
func TestInsertCop2Valid(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	var events []string
	prev := uint32(0xFFFFFFFF)
	for i := 0; i < 4_000_000; i++ {
		c2 := a.Copper.cop2lc
		if c2 != prev {
			diw := a.Custom.ReadReg16(0x08E)
			diw2 := a.Custom.ReadReg16(0x090)
			msg := ""
			if c2 == 0x676 || c2 < 0x1000 {
				msg = " *** INVALID/EXECBASE ***"
			}
			events = append(events, "")
			t.Logf("step=%d COP2=$%08X DIW=$%04X/$%04X PC=$%08X%s",
				i, c2, diw, diw2, a.CPU.PC, msg)
			prev = c2
		}
		a.Step()
	}
	// final
	t.Logf("FINAL COP1=$%08X COP2=$%08X DIW=$%04X/$%04X BPL1PT=$%08X",
		a.Copper.cop1lc, a.Copper.cop2lc,
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		uint32(a.Custom.ReadReg16(0x0E0))<<16|uint32(a.Custom.ReadReg16(0x0E2)))

	// RasInfo / BitMap check
	av, _ := a.CPU.M.Long(0x221E + 0x22)
	vp, _ := a.CPU.M.Long(int(av))
	ri, _ := a.CPU.M.Long(int(vp) + 0x24)
	t.Logf("VP=$%08X RasInfo@$+24=$%08X", vp, ri)
	if ri > 0x1000 && ri < 0x80000 {
		bm, _ := a.CPU.M.Long(int(ri) + 4)
		bpr, _ := a.CPU.M.Word(int(bm))
		rows, _ := a.CPU.M.Word(int(bm) + 2)
		p0, _ := a.CPU.M.Long(int(bm) + 8)
		t.Logf("  BitMap@$%08X BPR=%d Rows=%d P0=$%08X", bm, bpr, rows, p0)
	}
}
