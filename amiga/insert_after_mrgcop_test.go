package amiga

import (
	"encoding/binary"
	"testing"
)

func TestInsertAfterMrgCop(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// MrgCop is FC582C
	for i := 0; i < 2_500_000; i++ {
		if a.CPU.PC == 0xFE8884 { // jsr MrgCop
			t.Logf("step=%d before MrgCop", i)
		}
		if a.CPU.PC == 0xFE8888 { // after MrgCop returns
			t.Logf("step=%d after MrgCop COP1=$%08X COP2=$%08X", i, a.Copper.cop1lc, a.Copper.cop2lc)
		}
		if a.CPU.PC == 0xFE888A { // after LoadView
			t.Logf("step=%d after LoadView DIW=$%04X/$%04X BPLCON0=$%04X",
				i, a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090), a.Custom.ReadReg16(0x100))
			cop2 := a.Copper.cop2lc
			t.Logf("COP2@$%08X:", cop2)
			buf := make([]byte, 4)
			for n := 0; n < 30; n++ {
				a.ChipRAM.Read(int(cop2)+n*4, buf)
				ir1 := binary.BigEndian.Uint16(buf[:2])
				ir2 := binary.BigEndian.Uint16(buf[2:])
				if ir1 == 0xFFFF && ir2 == 0xFFFE {
					t.Log("  END")
					break
				}
				if ir1&1 == 0 {
					reg := int(ir1 & 0x1FE)
					t.Logf("  MOVE $%03X,#$%04X", reg, ir2)
				} else {
					t.Logf("  WAIT $%04X,$%04X", ir1, ir2)
				}
			}
			return
		}
		a.Step()
	}
	t.Fatal("timeout")
}
