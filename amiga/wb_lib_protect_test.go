package amiga

import (
	"os"
	"testing"
)

func TestWBLibraryLVOProtectionLive(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Blitter.GuardWrites = true
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	for i := 0; i < 25_000_000; i++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(np), 24) == "workbench.task" {
				break
			}
		}
		_ = a.Step()
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		_ = a.Step()
	}
	for i := 0; i < 40; i++ {
		_ = a.RunFrame()
	}

	rd32 := func(addr int) uint32 {
		b := make([]byte, 4)
		a.ChipRAM.Read(addr, b)
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	rd16 := func(addr int) uint16 {
		b := make([]byte, 2)
		a.ChipRAM.Read(addr, b)
		return uint16(b[0])<<8 | uint16(b[1])
	}
	eb := int(rd32(4))
	head := rd32(eb + 0x17A)
	var gfx uint32
	for n, node := 0, head; n < 32 && node != 0; n++ {
		succ := rd32(int(node))
		if succ == 0 {
			break
		}
		name := readCStrMem(a, rd32(int(node)+10), 28)
		if name == "graphics.library" {
			gfx = node
			break
		}
		node = succ
	}
	if gfx == 0 {
		t.Fatal("no graphics.library")
	}
	neg := int(rd16(int(gfx) + 0x10))
	lvoBase := int(gfx) - neg
	t.Logf("gfx=$%06X neg=$%04X lvo=[$%06X,$%06X)", gfx, neg, lvoBase, gfx)

	a.Blitter.lastSize = 1<<6 | 40
	a.Blitter.dmod = 0
	a.Blitter.buildDeny()

	// Find deny covering lvoBase
	found := false
	for i := range a.Blitter.denyLo {
		lo, hi := a.Blitter.denyLo[i], a.Blitter.denyHi[i]
		if lo <= lvoBase && lvoBase < hi {
			t.Logf("LVO covered by deny [$%06X,$%06X)", lo, hi)
			found = true
		}
		if lo <= int(gfx) && int(gfx) < hi {
			t.Logf("GfxBase covered by deny [$%06X,$%06X)", lo, hi)
		}
	}
	if !found {
		t.Logf("LVO base NOT in any deny range! dumping lib-ish denies:")
		for i := range a.Blitter.denyLo {
			lo, hi := a.Blitter.denyLo[i], a.Blitter.denyHi[i]
			if lo < 0x10000 {
				t.Logf("  [$%06X,$%06X)", lo, hi)
			}
		}
	}

	// canWriteD with heap start (same as malicious mid-chip DPT)
	if a.Blitter.canWriteD(lvoBase, uint32(lvoBase)) {
		t.Errorf("canWriteD allows LVO base with start=LVO")
	}
	if a.Blitter.canWriteD(lvoBase+0x100, uint32(lvoBase)) {
		t.Errorf("canWriteD allows mid-LVO with start=LVO")
	}
	if a.Blitter.canWriteD(int(gfx)+0x20, uint32(lvoBase)) {
		t.Errorf("canWriteD allows GfxBase+20")
	}

	// Actual blit
	snap := make([]byte, neg)
	a.ChipRAM.Read(lvoBase, snap)
	a.Blitter.con0 = 0x01FF
	a.Blitter.con1 = 0
	a.Blitter.adat = 0xFFFF
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.dptH = 0
	a.Blitter.dptL = uint16(lvoBase)
	a.Blitter.dmod = 0
	words := neg / 2
	if words > 63 {
		// multi-row
		rows := (words + 62) / 63
		a.Blitter.Execute(uint16(rows<<6 | 63))
	} else {
		a.Blitter.Execute(uint16(1<<6 | words))
	}
	after := make([]byte, neg)
	a.ChipRAM.Read(lvoBase, after)
	diff := 0
	for i := range snap {
		if snap[i] != after[i] {
			diff++
		}
	}
	t.Logf("blitDiffBytes=%d", diff)
	if diff > 0 {
		t.Errorf("LVO table modified (%d bytes)", diff)
	}
}
