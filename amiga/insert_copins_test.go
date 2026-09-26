package amiga

import (
	"encoding/binary"
	"testing"
)

func TestInsertCopInsStream(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
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

	// Dump instruction streams at DspIns+12, +16
	for _, base := range []uint32{0x10360, 0x103EA, 0x10450} {
		t.Logf("=== @$%06X ===", base)
		buf := make([]byte, 4)
		for n := 0; n < 35; n++ {
			a.ChipRAM.Read(int(base)+n*4, buf)
			ir1 := binary.BigEndian.Uint16(buf[:2])
			ir2 := binary.BigEndian.Uint16(buf[2:])
			if ir1 == 0xFFFF && ir2 == 0xFFFE {
				t.Logf("  END")
				break
			}
			if ir1&1 == 0 {
				reg := int(ir1 & 0x1FE)
				name := customRegNames[reg]
				if name == "" {
					name = ""
				}
				t.Logf("  MOVE %s($%03X),#$%04X", name, reg, ir2)
			} else {
				t.Logf("  WAIT v=%d #$%04X", (ir1>>8)&0xFF, ir2)
			}
		}
	}
}
