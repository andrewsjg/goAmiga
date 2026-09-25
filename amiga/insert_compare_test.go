package amiga

import (
	"image"
	"image/png"
	"os"
	"testing"
)

// Freeze insert-disk art after full script and export for visual comparison.
// Also count edge pixels and report BltTemplate activity.
func TestInsertDiskCompare(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	// NO disk — stay on insert screen
	a.Reset()
	a.Video.DisplayActive = true

	bltT := 0
	lineBlits := 0
	floods := 0
	for i := 0; i < 5_000_000; i++ {
		pc := a.CPU.PC
		switch pc {
		case 0xFC74AA: // BltTemplate
			bltT++
			if bltT <= 8 {
				t.Logf("step=%d BltTemplate A0=$%08X A1=$%08X D0=%d D1=%d",
					i, a.CPU.A[0], a.CPU.A[1], int16(a.CPU.D[0]), int16(a.CPU.D[1]))
			}
		case 0xFC5AD0:
			floods++
		case 0xFC5634:
			lineBlits++
		}
		a.Step()
	}
	// stabilize copper
	for i := 0; i < 20; i++ {
		a.RunFrame()
	}

	t.Logf("Draw entries=%d Floods=%d BltTemplate=%d", lineBlits, floods, bltT)
	t.Logf("BPLCON0=$%04X DIW=$%04X/$%04X DDF=$%04X/$%04X COLOR=%04X %04X %04X %04X",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094),
		a.Custom.ReadReg16(0x180), a.Custom.ReadReg16(0x182),
		a.Custom.ReadReg16(0x184), a.Custom.ReadReg16(0x186))

	// Edge density on BPL1 (black outlines mainly)
	edges := countEdgePixels(a, 0xA572, 40, 320, 200)
	t.Logf("BPL1 edge-ish pixels (4-neigh bare)=%d total nonzero=%d", edges[0], edges[1])

	// Save planar and FB
	if err := saveBPL12PNG(a, "/tmp/insert_planar.png", 0xA572, 0xC4B2); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, a.Video.Width, a.Video.Height))
	copy(img.Pix, a.Video.Pixels)
	f, _ := os.Create("/tmp/insert_fb.png")
	png.Encode(f, img)
	f.Close()
	// workspace copies
	for _, dst := range []string{"../insert_planar.png", "../insert_fb.png"} {
		if out, err := os.Create(dst); err == nil {
			png.Encode(out, img) // wrong for planar - fix
			out.Close()
		}
	}
	// proper planar copy
	saveBPL12PNG(a, "../insert_planar.png", 0xA572, 0xC4B2)
	f2, _ := os.Create("../insert_fb.png")
	png.Encode(f2, img)
	f2.Close()
	t.Log("saved insert_planar.png insert_fb.png")
}

func countEdgePixels(a *Amiga, base, bpr, w, h int) [2]int {
	get := func(x, y int) bool {
		if x < 0 || y < 0 || x >= w || y >= h {
			return false
		}
		buf := make([]byte, 1)
		a.ChipRAM.Read(base+y*bpr+x/8, buf)
		return buf[0]&(1<<uint(7-x%8)) != 0
	}
	nz, edge := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !get(x, y) {
				continue
			}
			nz++
			if !get(x-1, y) || !get(x+1, y) || !get(x, y-1) || !get(x, y+1) {
				edge++
			}
		}
	}
	return [2]int{edge, nz}
}
