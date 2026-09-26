package amiga

import (
	"fmt"
	"testing"
)

func TestBltTemplateProbe(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// Run to FE8966 (BltTemplate call)
	hits := 0
	for i := 0; i < 5_000_000; i++ {
		pc := a.CPU.PC
		if pc == 0xFE8966 {
			gfx := a.CPU.A[6]
			buf := make([]byte, 6)
			a.CPU.M.Read(int(gfx)-0x24, buf)
			var target uint32
			if buf[0] == 0x4E && buf[1] == 0xF9 {
				target = uint32(buf[2])<<24 | uint32(buf[3])<<16 | uint32(buf[4])<<8 | uint32(buf[5])
			}
			code := make([]byte, 16)
			a.CPU.M.Read(int(target), code)
			t.Logf("BltTemplate LVO → $%08X head %02X", target, code)
			if code[0] == 0x42 && code[1] == 0x80 {
				t.Log("  STUB clr.l d0")
			}
			// Args for BltTemplate stack or regs?
			// Standard: BltTemplate(src, srcmod, srcx, dest, destx, desty, width, height)
			// Actually C ABI with A6=GfxBase - need to check
			t.Logf("  D0=%08X D1=%08X A0=$%08X A1=$%08X A2=$%08X A3=$%08X",
				a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3])
			// stack args
			sp := a.CPU.A[7]
			sb := make([]byte, 40)
			a.ChipRAM.Read(int(sp), sb)
			var stack string
			for j := 0; j < 40; j += 4 {
				v := uint32(sb[j])<<24 | uint32(sb[j+1])<<16 | uint32(sb[j+2])<<8 | uint32(sb[j+3])
				stack += fmt.Sprintf(" $%08X", v)
			}
			t.Logf("  stack:%s", stack)
			hits++
			if hits >= 3 {
				break
			}
		}
		// Also detect entry to BltTemplate target
		a.Step()
	}
	if hits == 0 {
		t.Log("FE8966 never reached")
	}
}
