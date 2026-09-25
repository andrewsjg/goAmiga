package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestWorkbenchCop2(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	for i := 0; i < 14_000_000; i++ {
		a.Step()
	}

	dumpList := func(label string, base uint32, max int) {
		t.Logf("=== %s @$%08X ===", label, base)
		buf := make([]byte, 4)
		for n := 0; n < max; n++ {
			addr := int(base) + n*4
			if addr < 0 || addr+4 > ChipRAMSize {
				break
			}
			a.ChipRAM.Read(addr, buf)
			ir1 := binary.BigEndian.Uint16(buf[0:2])
			ir2 := binary.BigEndian.Uint16(buf[2:4])
			if ir1 == 0xFFFF && ir2 == 0xFFFE {
				t.Logf("  $%06X: END", addr)
				break
			}
			if ir1&1 == 0 {
				reg := int(ir1 & 0x1FE)
				name := customRegNames[reg]
				t.Logf("  $%06X: MOVE %s($%03X),$%04X", addr, name, reg, ir2)
			} else {
				vp := (ir1 >> 8) & 0xFF
				hp := ir1 & 0xFE
				t.Logf("  $%06X: WAIT v=%d h=%d mask=$%04X", addr, vp, hp, ir2)
			}
		}
	}

	t.Logf("COP1LC=$%08X COP2LC=$%08X", a.Copper.cop1lc, a.Copper.cop2lc)
	dumpList("COP1", a.Copper.cop1lc, 30)
	dumpList("COP2", a.Copper.cop2lc, 60)

	// Also follow if COP2 has nested structure - dump common MrgCop areas
	// Search for BPL1PTH writes (MOVE $0E0) in chip RAM copper-like sequences
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	// look for word 0x00E0 followed by non-zero (BPL1PTH move)
	found := 0
	for i := 0; i+4 < len(mem); i += 2 {
		if mem[i] == 0x00 && mem[i+1] == 0xE0 {
			val := binary.BigEndian.Uint16(mem[i+2 : i+4])
			// previous word even alignment for copper MOVE
			if i%4 == 0 {
				found++
				if found <= 15 {
					t.Logf("BPL1PTH MOVE at $%06X value=$%04X (next long ir2 context)", i, val)
					// dump surrounding 8 copper insns
					base := uint32(i) &^ 3
					if base >= 16 {
						base -= 16
					}
					dumpList("near BPL1PTH", base, 12)
				}
			}
		}
	}
	t.Logf("BPL1PTH MOVE count=%d", found)

	t.Logf("regs: BPLCON0=$%04X DIW=$%04X/$%04X DDF=$%04X/$%04X BPL1PT=$%08X BPL2PT=$%08X",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094),
		uint32(a.Custom.ReadReg16(0x0E0))<<16|uint32(a.Custom.ReadReg16(0x0E2)),
		uint32(a.Custom.ReadReg16(0x0E4))<<16|uint32(a.Custom.ReadReg16(0x0E6)))
}
