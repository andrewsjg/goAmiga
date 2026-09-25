package amiga

import (
	"encoding/binary"
	"testing"
)

func TestInsertDiskDIW(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 5; i++ {
		a.RunFrame()
	}

	t.Logf("regs DIWSTRT=$%04X DIWSTOP=$%04X BPLCON0=$%04X",
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090), a.Custom.ReadReg16(0x100))
	t.Logf("COP1=$%08X COP2=$%08X", a.Copper.cop1lc, a.Copper.cop2lc)

	dump := func(base uint32, n int) {
		buf := make([]byte, 4)
		for i := 0; i < n; i++ {
			a.ChipRAM.Read(int(base)+i*4, buf)
			ir1 := binary.BigEndian.Uint16(buf[0:2])
			ir2 := binary.BigEndian.Uint16(buf[2:4])
			if ir1 == 0xFFFF && ir2 == 0xFFFE {
				t.Logf("  END")
				return
			}
			if ir1&1 == 0 {
				reg := int(ir1 & 0x1FE)
				name := customRegNames[reg]
				t.Logf("  MOVE %s($%03X),$%04X", name, reg, ir2)
			} else {
				t.Logf("  WAIT v=%d h=%d $%04X", (ir1>>8)&0xFF, ir1&0xFE, ir2)
			}
		}
	}
	t.Log("COP1:")
	dump(a.Copper.cop1lc, 25)
	t.Log("COP2:")
	dump(a.Copper.cop2lc, 40)
}
