package amiga

import (
	"fmt"
	"testing"
)

func TestGfxLVOsAndFlood(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Run until GfxBase exists at a known location - from earlier A6=$221E
	// Or find graphics.library
	for i := 0; i < 3_000_000; i++ {
		a.Step()
		// When we hit FE8904 (Flood call site), dump vectors
		if a.CPU.PC == 0xFE8904 || a.CPU.PC == 0xFE88F4 {
			gfx := a.CPU.A[6]
			t.Logf("step=%d PC=$%08X GfxBase/A6=$%08X", i, a.CPU.PC, gfx)
			// Read LVOs as absolute jumps from library base
			// Library vectors: base + LVO points to JMP instruction or direct
			// On Amiga, negative offsets from lib base are jump table entries:
			// each entry is 6 bytes: JMP $xx.L (4EF9 xxxx xxxx)
			for _, lvo := range []struct {
				off  int
				name string
			}{
				{-0xEA, "SetRast"},
				{-0x14A, "Flood"},
				{-0x156, "SetAPen"},
				{-0xF0, "Move"},
				{-0xF6, "Draw"},
				{-0x132, "RectFill"},
				{-0xC6, "InitRastPort"},
				{-0xC0, "LoadRGB4"},
			} {
				addr := int(gfx) + lvo.off
				buf := make([]byte, 6)
				// vectors may be in chip RAM (library copied) or ROM
				if addr >= 0 && addr+6 < ChipRAMSize {
					a.ChipRAM.Read(addr, buf)
				} else {
					// try via CPU mapper
					a.CPU.M.Read(addr, buf)
				}
				// parse JMP abs.L
				if buf[0] == 0x4E && buf[1] == 0xF9 {
					target := uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
					t.Logf("  LVO %s (%d): JMP $%08X", lvo.name, lvo.off, target)
				} else {
					t.Logf("  LVO %s (%d) @ $%08X: %02X", lvo.name, lvo.off, addr, buf)
				}
			}
			t.Logf("  D0=%d D1=%d D2=%d D3=%d D5=%d A1=$%08X A2=$%08X A3=$%08X",
				a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.D[5],
				a.CPU.A[1], a.CPU.A[2], a.CPU.A[3])
			if a.CPU.PC == 0xFE8904 {
				break
			}
		}
	}

	// Also count actual hits on resolved Flood target for another run
	a2, _ := New(rom, nil)
	a2.Reset()
	// First find flood target
	var floodTarget uint32
	var gfxBase uint32
	for i := 0; i < 3_000_000; i++ {
		a2.Step()
		if a2.CPU.PC == 0xFE88F4 {
			gfxBase = a2.CPU.A[6]
			buf := make([]byte, 6)
			addr := int(gfxBase) - 0x14A
			a2.CPU.M.Read(addr, buf)
			if buf[0] == 0x4E && buf[1] == 0xF9 {
				floodTarget = uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
			}
			t.Logf("Flood target resolved to $%08X (GfxBase=$%08X)", floodTarget, gfxBase)
			break
		}
	}

	// Continue counting flood entries and FE command executions
	if floodTarget != 0 {
		hits := 0
		feCmds := 0
		for i := 0; i < 2_000_000; i++ {
			if a2.CPU.PC == floodTarget {
				hits++
				if hits <= 12 {
					t.Logf("Flood#%d step=%d D0=%d D1=%d D2=%d A1=$%05X",
						hits, i, a2.CPU.D[0], a2.CPU.D[1], a2.CPU.D[2], a2.CPU.A[1])
				}
			}
			if a2.CPU.PC == 0xFE88F4 {
				feCmds++
				if feCmds <= 12 {
					t.Logf("FE-cmd#%d step=%d pen(D5)=%d x(D4)=%d y(D3)=%d floodXY will be (%d,%d)",
						feCmds, i, a2.CPU.D[5], a2.CPU.D[4], a2.CPU.D[3],
						int(a2.CPU.D[4])+70, int(a2.CPU.D[3])+40)
				}
			}
			a2.Step()
		}
		t.Logf("Total Flood hits=%d FE-cmds=%d", hits, feCmds)
	}
	_ = fmt.Sprintf
}
