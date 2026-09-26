package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

func TestWorkbenchCopper(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	// headless for speed until WB, then enable display
	const max = 20_000_000
	wb := false
	for i := 0; i < max; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			b := make([]byte, 20)
			for j := 0; j < 20; j++ {
				v, e := a.CPU.M.Byte(int(namePtr) + j)
				if e != nil || v == 0 {
					b = b[:j]
					break
				}
				b[j] = byte(v)
			}
			if string(b) == "workbench.task" {
				wb = true
				t.Logf("step=%d workbench.task", i)
			}
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if wb && i > 12_000_000 {
			break
		}
		if !wb && i > 15_000_000 {
			t.Fatal("no WB")
		}
	}
	// run a bit more with display
	a.Video.DisplayActive = true
	for i := 0; i < 2_000_000; i++ {
		a.Step()
	}

	// Dump copper list
	cop := a.Copper.cop1lc
	t.Logf("COP1LC=$%08X COP2LC=$%08X copPC=$%08X", cop, a.Copper.cop2lc, a.Copper.pc)
	buf := make([]byte, 4)
	for n := 0; n < 40; n++ {
		addr := int(cop) + n*4
		if addr < 0 || addr+4 > ChipRAMSize {
			break
		}
		a.ChipRAM.Read(addr, buf)
		ir1 := binary.BigEndian.Uint16(buf[0:2])
		ir2 := binary.BigEndian.Uint16(buf[2:4])
		if ir1 == 0xFFFF && ir2 == 0xFFFE {
			t.Logf("  $%06X: WAIT END", addr)
			break
		}
		if ir1&1 == 0 {
			reg := int(ir1 & 0x1FE)
			name := customRegNames[reg]
			if name == "" {
				name = ""
			}
			t.Logf("  $%06X: MOVE %s($%03X),$%04X", addr, name, reg, ir2)
		} else {
			t.Logf("  $%06X: WAIT/SKIP $%04X $%04X", addr, ir1, ir2)
		}
	}

	// Video regs
	t.Logf("BPLCON0=$%04X DIWSTRT=$%04X DIWSTOP=$%04X DDFSTRT=$%04X DDFSTOP=$%04X",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094))
	for i := 0; i < 2; i++ {
		hi := a.Custom.ReadReg16(0x0E0 + i*4)
		lo := a.Custom.ReadReg16(0x0E2 + i*4)
		t.Logf("BPL%dPT=$%08X (working bplpt=$%08X)", i+1, uint32(hi)<<16|uint32(lo), a.Video.bplpt[i])
	}
	t.Logf("BPL1MOD=$%04X BPL2MOD=$%04X COLOR00-03=$%04X $%04X $%04X $%04X",
		a.Custom.ReadReg16(0x108), a.Custom.ReadReg16(0x10A),
		a.Custom.ReadReg16(0x180), a.Custom.ReadReg16(0x182),
		a.Custom.ReadReg16(0x184), a.Custom.ReadReg16(0x186))
	t.Logf("DMACON=$%04X INTENA=$%04X COPCON=$%04X",
		a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x09A), a.Custom.ReadReg16(0x02E))

	// Peek bitplane data at BPL1PT if non-zero, else at copper-set value
	// Also dump View/GfxBase if we can find it
	// GfxBase often at various places - search for known copper ptr $2368 in chip as long
	target := make([]byte, 4)
	binary.BigEndian.PutUint32(target, cop)
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	hits := 0
	for i := 0; i+4 <= len(mem); i += 2 {
		if mem[i] == target[0] && mem[i+1] == target[1] && mem[i+2] == target[2] && mem[i+3] == target[3] {
			hits++
			if hits <= 8 {
				t.Logf("copper ptr ref at $%06X", i)
			}
		}
	}
	t.Logf("copper ptr refs=%d", hits)
}
