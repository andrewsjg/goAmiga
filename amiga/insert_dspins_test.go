package amiga

import (
	"encoding/binary"
	"testing"
)

func TestInsertDspIns(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 3; i++ {
		a.RunFrame()
	}

	vp := uint32(0xA498)
	dsp, _ := a.CPU.M.Long(int(vp) + 8)
	t.Logf("VP DspIns=$%08X", dsp)
	// CopList structure: Next, _CopList, _CopIns, Count, MaxCount, DyOffset?
	// Intermediate copper instructions
	if dsp > 0x1000 && dsp < 0x80000 {
		for o := 0; o < 32; o += 4 {
			v, _ := a.CPU.M.Long(int(dsp) + o)
			t.Logf("  DspIns+%d: $%08X", o, v)
		}
		// _CopIns often at +8
		ins, _ := a.CPU.M.Long(int(dsp) + 8)
		t.Logf("CopIns=$%08X", ins)
		if ins > 0x1000 && ins < 0x80000 {
			buf := make([]byte, 4)
			for n := 0; n < 40; n++ {
				a.ChipRAM.Read(int(ins)+n*4, buf)
				ir1 := binary.BigEndian.Uint16(buf[:2])
				ir2 := binary.BigEndian.Uint16(buf[2:])
				// CopIns format may differ - may be CopInstruction structs
				t.Logf("  ins+%02X: $%04X $%04X", n*4, ir1, ir2)
				if ir1 == 0 && ir2 == 0 && n > 5 {
					break
				}
			}
		}
	}

	// Also dump final COP2 DIW instructions
	cop2 := a.Copper.cop2lc
	t.Logf("Final COP2@$%08X looking for DIW:", cop2)
	buf := make([]byte, 4)
	for n := 0; n < 25; n++ {
		a.ChipRAM.Read(int(cop2)+n*4, buf)
		ir1 := binary.BigEndian.Uint16(buf[:2])
		ir2 := binary.BigEndian.Uint16(buf[2:])
		if ir1&1 == 0 {
			reg := ir1 & 0x1FE
			if reg == 0x08E || reg == 0x090 || reg == 0x100 || reg == 0x0E0 || reg == 0x0E2 {
				t.Logf("  MOVE $%03X, #$%04X", reg, ir2)
			}
		}
	}
}
