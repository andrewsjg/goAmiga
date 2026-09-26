package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

// TestBootQuality runs no-disk boot long enough for the insert-disk art,
// saves PNGs from planar RAM and from the video framebuffer, and dumps
// display state for remaining issues.
func TestBootQuality(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true

	// Run until script done + stable (floods complete around 2.2-2.6M, copper later)
	for i := 0; i < 6_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 10; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}

	// Display regs
	bplcon0 := a.Custom.ReadReg16(0x100)
	dmacon := a.Custom.ReadReg16(0x096)
	diwstrt := a.Custom.ReadReg16(0x08E)
	diwstop := a.Custom.ReadReg16(0x090)
	ddfstrt := a.Custom.ReadReg16(0x092)
	ddfstop := a.Custom.ReadReg16(0x094)
	t.Logf("BPLCON0=$%04X planes=%d DMACON=$%04X", bplcon0, (bplcon0>>12)&7, dmacon)
	t.Logf("DIWSTRT=$%04X (V=%d H=%d) DIWSTOP=$%04X (V=%d H=%d)",
		diwstrt, diwstrt>>8, diwstrt&0xFF, diwstop, diwstop>>8, diwstop&0xFF)
	t.Logf("DDFSTRT=$%04X DDFSTOP=$%04X", ddfstrt, ddfstop)
	t.Logf("COLOR %04X %04X %04X %04X",
		a.Custom.ReadReg16(0x180), a.Custom.ReadReg16(0x182),
		a.Custom.ReadReg16(0x184), a.Custom.ReadReg16(0x186))
	t.Logf("BPL1PT=$%04X%04X BPL2PT=$%04X%04X",
		a.Custom.ReadReg16(0x0E0), a.Custom.ReadReg16(0x0E2),
		a.Custom.ReadReg16(0x0E4), a.Custom.ReadReg16(0x0E6))
	t.Logf("COP1=$%08X COP2=$%08X", a.Copper.GetCop1LC(), a.Copper.GetCop2LC())
	t.Logf("Video %dx%d DisplayTop=%d", a.Video.Width, a.Video.Height, DisplayTop)

	// Planar snapshot
	if err := saveBPL12PNG(a, "/tmp/boot_planar.png", 0xA572, 0xC4B2); err != nil {
		t.Fatal(err)
	}
	// Framebuffer snapshot
	img := image.NewRGBA(image.Rect(0, 0, a.Video.Width, a.Video.Height))
	copy(img.Pix, a.Video.Pixels)
	f, _ := os.Create("/tmp/boot_framebuffer.png")
	png.Encode(f, img)
	f.Close()
	t.Log("saved /tmp/boot_planar.png and /tmp/boot_framebuffer.png")

	// Pixel diversity
	same, total := 0, a.Video.Width*a.Video.Height
	fr, fg, fb := a.Video.Pixels[0], a.Video.Pixels[1], a.Video.Pixels[2]
	nonWhite := 0
	for y := 0; y < a.Video.Height; y++ {
		for x := 0; x < a.Video.Width; x++ {
			o := (y*a.Video.Width + x) * 4
			if a.Video.Pixels[o] == fr && a.Video.Pixels[o+1] == fg && a.Video.Pixels[o+2] == fb {
				same++
			}
			if a.Video.Pixels[o] != 255 || a.Video.Pixels[o+1] != 255 || a.Video.Pixels[o+2] != 255 {
				nonWhite++
			}
		}
	}
	t.Logf("framebuffer: %d/%d solid (%d,%d,%d), nonWhite=%d", same, total, fr, fg, fb, nonWhite)

	// Bit counts
	buf := make([]byte, 2)
	b1, b2 := 0, 0
	for off := 0; off < 8000; off += 2 {
		a.ChipRAM.Read(0xA572+off, buf)
		v := uint16(buf[0])<<8 | uint16(buf[1])
		for i := 0; i < 16; i++ {
			if v&(1<<uint(i)) != 0 {
				b1++
			}
		}
		a.ChipRAM.Read(0xC4B2+off, buf)
		v = uint16(buf[0])<<8 | uint16(buf[1])
		for i := 0; i < 16; i++ {
			if v&(1<<uint(i)) != 0 {
				b2++
			}
		}
	}
	t.Logf("planar bits BPL1=%d BPL2=%d", b1, b2)
	_ = fmt.Sprintf
}
