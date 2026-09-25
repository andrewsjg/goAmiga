package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchView(t *testing.T) {
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

	// Find graphics.library base via OpenLibrary
	// Or scan for copper list pointer $00002368 in structures
	cop1 := a.Copper.cop1lc
	// At $2244 we found a ref - dump surrounding as potential CopInit/View
	for _, base := range []uint32{0x2200, 0x2240, 0x2280, 0x22C0, 0x2300} {
		t.Logf("--- mem $%06X ---", base)
		for off := 0; off < 0x40; off += 4 {
			v, _ := a.CPU.M.Long(int(base) + off)
			t.Logf("  +%02X: $%08X", off, v)
		}
	}

	// GfxBase: OpenLibrary leaves base; search library node "graphics.library"
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	name := []byte("graphics.library")
	for i := 0; i+len(name) < len(mem); i++ {
		ok := true
		for j := 0; j < len(name); j++ {
			if mem[i+j] != name[j] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Library name is at ln_Name; Lib base is typically name_ptr - something
		// For ROM libs, name is in ROM. For disk... graphics is ROM.
		// GfxBase in chip: ActiView at GfxBase+34 ($22)
		t.Logf("graphics.library string at $%06X (likely ROM mirror or copy)", i)
	}

	// brute: find DWidth=640 ($0280) followed by plausible DHeight
	found := 0
	for i := 0; i+8 < len(mem); i += 2 {
		if mem[i] == 0x02 && mem[i+1] == 0x80 { // 640
			hh := uint16(mem[i+2])<<8 | uint16(mem[i+3])
			if hh > 50 && hh < 400 {
				found++
				if found <= 20 {
					pre := i - 16
					if pre < 0 {
						pre = 0
					}
					t.Logf("DWidth=640 DHeight=%d at $%06X; context:", hh, i)
					for o := pre; o < i+16 && o+4 <= len(mem); o += 4 {
						v := uint32(mem[o])<<24 | uint32(mem[o+1])<<16 | uint32(mem[o+2])<<8 | uint32(mem[o+3])
						t.Logf("  $%06X: $%08X", o, v)
					}
				}
			}
		}
	}
	t.Logf("640xh candidates=%d cop1=$%08X", found, cop1)

	// Sprite 0 data at SPR0PT
	spt := a.Sprites.sprites[0].pt
	t.Logf("SPR0 pt=$%08X pos=$%04X ctl=$%04X vstart=%d vstop=%d hstart=%d",
		spt, a.Sprites.sprites[0].pos, a.Sprites.sprites[0].ctl,
		a.Sprites.sprites[0].vstart(), a.Sprites.sprites[0].vstop(), a.Sprites.sprites[0].hstart())
}
