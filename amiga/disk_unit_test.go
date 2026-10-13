package amiga

import (
	"os"
	"testing"
)

func TestDiskUnitBuffer(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	// At FEAC62: a3 is unit, $4e(a3) is buffer
	n := 0
	for i := 0; i < 5_000_000 && n < 5; i++ {
		if a.CPU.PC == 0xFEAC62 {
			a3 := a.CPU.A[3]
			buf := make([]byte, 4)
			a.ChipRAM.Read(int(a3+0x4e), buf)
			p4e := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
			t.Logf("FEAC62 A3(unit)=$%08X $4e=$%08X dskpt=$%08X diff=$%x",
				a3, p4e, a.Disk.dskpt, int32(a.Disk.dskpt)-int32(p4e))
			// dump unit+0x40..0x60
			u := make([]byte, 0x40)
			a.ChipRAM.Read(int(a3+0x40), u)
			t.Logf("  unit+40: %02X", u)
			// After search would be at p4e+0x680
			search := p4e + 0x680
			sbuf := make([]byte, 16)
			if search < ChipRAMSize {
				a.ChipRAM.Read(int(search), sbuf)
				t.Logf("  mem@$%06X (+680): %02X", search, sbuf)
			}
			// dskpt content
			a.ChipRAM.Read(int(a.Disk.dskpt), sbuf)
			t.Logf("  dskpt@$%06X: %02X", a.Disk.dskpt, sbuf)
			n++
		}
		// also FEA1A4 / FEA234 with A3
		if a.CPU.PC == 0xFEA1A4 || a.CPU.PC == 0xFEA234 {
			if n == 0 {
				t.Logf("step=%d read_entry PC=$%08X A0=$%08X A3=$%08X D0=$%08X",
					i, a.CPU.PC, a.CPU.A[0], a.CPU.A[3], a.CPU.D[0])
			}
		}
		a.Step()
	}
}
