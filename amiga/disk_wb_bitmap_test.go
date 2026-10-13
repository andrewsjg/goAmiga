package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchBitMap(t *testing.T) {
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
	for i := 0; i < 14_000_000; i++ {
		a.Step()
	}

	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	rd16 := func(i int) uint16 { return uint16(mem[i])<<8 | uint16(mem[i+1]) }
	rd32 := func(i int) uint32 {
		return uint32(mem[i])<<24 | uint32(mem[i+1])<<16 | uint32(mem[i+2])<<8 | uint32(mem[i+3])
	}

	// Find BitMaps that reference plane $D510 or $12510
	targets := []uint32{0xD510, 0x12510, 0x0000D510, 0x00012510}
	for i := 0; i+40 < len(mem); i += 2 {
		for p := 0; p < 8; p++ {
			pl := rd32(i + 8 + p*4)
			for _, tgt := range targets {
				if pl != tgt {
					continue
				}
				bpr := rd16(i)
				rows := rd16(i + 2)
				flags := mem[i+4]
				depth := mem[i+5]
				if bpr < 20 || bpr > 200 || rows < 10 || rows > 400 || depth == 0 || depth > 6 {
					continue
				}
				t.Logf("BitMap@$%06X BPR=%d Rows=%d Flags=%02X Depth=%d", i, bpr, rows, flags, depth)
				for q := 0; q < int(depth); q++ {
					t.Logf("  P[%d]=$%08X", q, rd32(i+8+q*4))
				}
				// size check
				t.Logf("  plane bytes=%d gap P1-P0=%d", int(bpr)*int(rows), int(rd32(i+12))-int(rd32(i+8)))
			}
		}
	}

	// Also scan for BPR=80 or 40 (hires/lores) with rows 200/256/59
	t.Log("BitMaps BPR=80 (hires 640):")
	n := 0
	for i := 0; i+40 < len(mem); i += 2 {
		if rd16(i) != 80 {
			continue
		}
		rows := rd16(i + 2)
		depth := mem[i+5]
		if rows < 50 || rows > 400 || depth == 0 || depth > 6 {
			continue
		}
		// plane0 should be in chip
		p0 := rd32(i + 8)
		if p0 < 0x1000 || p0 >= 0x80000 {
			continue
		}
		n++
		if n <= 15 {
			t.Logf("  BM@$%06X Rows=%d Depth=%d P0=$%08X P1=$%08X", i, rows, depth, p0, rd32(i+12))
		}
	}
	t.Logf("total BPR80=%d", n)

	// Dump first non-zero content lines of plane at D510
	base := 0xD510
	bpr := 80
	for line := 0; line < 80; line++ {
		off := base + line*bpr
		row := mem[off : off+bpr]
		nz := 0
		for _, b := range row {
			if b != 0 {
				nz++
			}
		}
		if nz > 0 && line < 30 {
			// show hex preview
			t.Logf("P0 line %d nz=%d head=%02X", line, nz, row[:16])
		}
	}
	// count non-zero lines
	nzLines := 0
	for line := 0; line < 300; line++ {
		off := base + line*bpr
		if off+bpr > len(mem) {
			break
		}
		for _, b := range mem[off : off+bpr] {
			if b != 0 {
				nzLines++
				break
			}
		}
	}
	t.Logf("P0@$D510 non-zero lines (bpr80)=%d", nzLines)
}
