package amiga

import (
	"os"
	"testing"
)

// Find Intuition Screen structures (Width/Height) and GfxBase display geometry.
func TestWorkbenchScreen(t *testing.T) {
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

	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	rd16 := func(i int) uint16 { return uint16(mem[i])<<8 | uint16(mem[i+1]) }
	rd32 := func(i int) uint32 {
		return uint32(mem[i])<<24 | uint32(mem[i+1])<<16 | uint32(mem[i+2])<<8 | uint32(mem[i+3])
	}

	// Intuition Screen (V33) approximate:
	// +12 Width, +14 Height, +16 MouseX, +18 MouseY, +20 ViewPort...
	// Actually struct Screen:
	// NextScreen@0, Window@4, ... Title@26?
	// From intuition/screens.h V34:
	// 0 NextScreen, 4 FirstWindow, 8 LeftEdge, 10 TopEdge, 12 Width, 14 Height,
	// 16 MouseX, 18 MouseY, 20 Flags, 22 Title, 26 DefaultTitle, ...
	// 44 ViewPort (embedded or offset)
	t.Log("Possible Screens (Width=640):")
	for i := 12; i+50 < len(mem); i += 2 {
		if rd16(i) != 640 {
			continue
		}
		h := rd16(i + 2)
		if h < 20 || h > 400 {
			continue
		}
		base := i - 12
		// LeftEdge/TopEdge should be small
		le := int16(rd16(base + 8))
		te := int16(rd16(base + 10))
		if le < -100 || le > 100 || te < -50 || te > 100 {
			continue
		}
		mx := int16(rd16(base + 16))
		my := int16(rd16(base + 18))
		flags := rd16(base + 20)
		title := rd32(base + 22)
		titleStr := ""
		if title > 0 && title < 0x80000 {
			for k := 0; k < 40; k++ {
				c := mem[int(title)+k]
				if c == 0 {
					break
				}
				if c >= 32 && c < 127 {
					titleStr += string(c)
				}
			}
		}
		t.Logf("Screen@$%06X %dx%d LE=%d TE=%d Mouse=%d,%d Flags=$%04X Title=%q (@$%08X)",
			base, 640, h, le, te, mx, my, flags, titleStr, title)
		// FirstWindow
		fw := rd32(base + 4)
		t.Logf("  Next=$%08X FirstWindow=$%08X", rd32(base), fw)
		if fw > 0x1000 && fw < 0x80000 {
			// Window: +8 Left, +10 Top, +12 Width, +14 Height
			t.Logf("  Window@$%08X %d,%d %dx%d", fw,
				int16(rd16(int(fw)+8)), int16(rd16(int(fw)+10)),
				rd16(int(fw)+12), rd16(int(fw)+14))
		}
	}

	// GfxBase: find via OpenLibrary result stored - scan for NormalDisplayRows 200/256/512
	// GfxBase offsets V33: ActiView@34, copinit@50, cia@... NormalDisplayRows around 212?
	// From gfxbase.h V34:
	// 212 DisplayFlags, 214 NormalDisplayRows, etc. - version dependent
	// Search chip for sequence: DisplayFlags and rows
	for i := 0; i+20 < len(mem); i += 2 {
		rows := rd16(i)
		cols := rd16(i + 2)
		if (rows == 256 || rows == 200 || rows == 512) && (cols == 640 || cols == 320 || cols == 1280) {
			// plausible GfxBase field pair
			t.Logf("geom@$%06X rows=%d cols=%d prev=%04X next=%04X", i, rows, cols, rd16(i-2), rd16(i+4))
		}
	}
}
