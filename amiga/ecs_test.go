package amiga

import (
	"image/png"
	"os"
	"testing"
)

// gfxChipRevBits returns GfxBase->ChipRevBits0: bit 0 ECS Agnus (big
// blits), bit 1 ECS Denise.
func gfxChipRevBits(a *Amiga) byte {
	gfx := findLibrary(a, "graphics.library")
	b, _ := a.CPU.M.Byte(int(gfx + 0xEC))
	return b
}

func bootChipset(t *testing.T, rom, disk, chipset string) *Amiga {
	t.Helper()
	romData, err := LoadROM(rom)
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(romData, nil)
	if err := a.SetChipset(chipset); err != nil {
		t.Fatal(err)
	}
	a.SetChipRAM(1024)
	a.ConfigureMemory(0, 2048)
	if err := a.AttachHardfile(hardfileFromADF(t, disk), false); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatalf("%s: Workbench did not start", chipset)
	}
	return a
}

// Kickstart 3.2 recognises each chipset, and boots with it.
func TestChipsetDetectedKS32(t *testing.T) {
	for _, c := range []struct {
		chipset string
		want    byte
	}{{"ocs", 0}, {"ecs-agnus", 1}, {"ecs", 3}} {
		a := bootChipset(t, "../ROMS/Kick32.rom", "../disks/Install3.2.adf", c.chipset)
		for i := 0; i < 400; i++ {
			a.RunFrame()
		}
		if got := gfxChipRevBits(a) & 3; got != c.want {
			t.Errorf("%s: GfxBase ChipRevBits0 = %d, want %d", c.chipset, got, c.want)
		}
		if os.Getenv("GOAMIGA_DIAG_PNG") != "" {
			a.Video.DisplayActive = true
			a.RunFrame()
			a.RunFrame()
			savePNG(t, a, os.Getenv("GOAMIGA_DIAG_PNG")+"_"+c.chipset+".png")
		}
	}
}

// Kickstart 1.3 predates ECS and must still boot with it fitted.
func TestChipsetECSKS13(t *testing.T) {
	bootChipset(t, "../ROMS/Kick13.rom", "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf", "ecs")
}

// ECS big blits: BLTSIZV then BLTSIZH starts a blit taller than BLTSIZE
// can express (2000 lines).
func TestECSBigBlit(t *testing.T) {
	a := createTestAmiga(t)
	a.SetChipset("ecs-agnus")
	a.wireMemoryMap(false)
	a.Custom.Write(0x096, []byte{0x82, 0x40})
	a.Custom.Write(0x040, []byte{0x01, 0xFF}) // D only, all ones
	a.Custom.Write(0x066, []byte{0x00, 0x00}) // BLTDMOD 0
	a.Custom.Write(0x054, []byte{0x00, 0x01}) // BLTDPT $10000
	a.Custom.Write(0x056, []byte{0x00, 0x00})
	a.Custom.Write(0x05C, []byte{0x07, 0xD0}) // BLTSIZV 2000
	a.Custom.Write(0x05E, []byte{0x00, 0x01}) // BLTSIZH 1 word: starts
	a.Blitter.completeRun()
	if got := a.peek16(0x10000 + 2*1999); got != 0xFFFF {
		t.Errorf("line 1999 = $%04X, want $FFFF", got)
	}
	if got := a.peek16(0x10000 + 2*2000); got != 0 {
		t.Errorf("line 2000 = $%04X, want untouched", got)
	}
}

// On OCS the ECS registers do not exist.
func TestOCSIgnoresBigBlit(t *testing.T) {
	a := createTestAmiga(t)
	a.Custom.Write(0x096, []byte{0x82, 0x40})
	a.Custom.Write(0x05E, []byte{0x00, 0x01})
	if a.Blitter.busy {
		t.Error("BLTSIZH started a blit on OCS")
	}
	var b [2]byte
	a.Custom.Read(0x004, b[:])
	if b[0]&0x7F != 0 {
		t.Errorf("OCS VPOSR Agnus ID = $%02X, want 0", b[0]&0x7F)
	}
}

// DIWHIGH supplies the window's high bits until DIWSTRT/DIWSTOP is written.
func TestDIWHIGH(t *testing.T) {
	a := newTestAmigaVideo()
	a.SetChipset("ecs-agnus")
	a.Custom.Write(0x08E, []byte{0x2C, 0x81})
	a.Custom.Write(0x090, []byte{0x2C, 0xC1})
	a.Custom.Write(0x1E4, []byte{0x21, 0x00}) // stop H8, stop V8; start V8=0
	w := a.Video.window()
	if w.vstop != 0x12C || w.hstop != 0x1C1 || w.vstart != 0x2C {
		t.Errorf("window with DIWHIGH: %+v", w)
	}
	a.Custom.Write(0x1E4, []byte{0x00, 0x01}) // start V8
	if w := a.Video.window(); w.vstart != 0x12C || w.hstop != 0xC1 {
		t.Errorf("DIWHIGH start V8, stop H8 clear: %+v", w)
	}
	a.Custom.Write(0x08E, []byte{0x2C, 0x81}) // back to OCS rules
	if w := a.Video.window(); w.vstart != 0x2C || w.hstop != 0x1C1 {
		t.Errorf("after DIWSTRT write: %+v", w)
	}
}

func savePNG(t *testing.T, a *Amiga, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, a.Video.ScreenImage())
}

// A byte write reaches the custom chips as a word write of the byte
// doubled: MOVE.B to $DFF05B sets BLTCON0L (Kickstart 3.x draws text so).
func TestCustomByteWriteIsDoubled(t *testing.T) {
	a := createTestAmiga(t)
	a.SetChipset("ecs-agnus")
	a.Custom.Write(0x040, []byte{0x09, 0xF0})
	a.Custom.Write(0x05B, []byte{0xCA})
	if got := a.Blitter.con0; got != 0x09CA {
		t.Errorf("BLTCON0 = $%04X after BLTCON0L byte write, want $09CA", got)
	}
	a.Custom.Write(0x180, []byte{0x0F}) // COLOR00 via its even address
	if got := a.Custom.ReadReg16(0x180); got != 0x0F0F {
		t.Errorf("COLOR00 = $%04X after a byte write of $0F, want $0F0F", got)
	}
}

// ECS Denise features, on a standard screen.
func ecsDenise(t *testing.T, bplcon0 uint16, planes ...[]byte) *Amiga {
	a := deniseAmiga(t, bplcon0, planes...)
	a.SetChipset("ecs")
	return a
}

// Super-hires: two pixels per output column, shown as their average.
func TestSuperHires(t *testing.T) {
	// Plane 1: alternate pixels set (1010...), so each column holds one
	// colour-1 and one colour-0 pixel.
	plane := make([]byte, 200)
	for i := range plane {
		plane[i] = 0xAA
	}
	a := ecsDenise(t, 0x1240, plane) // SHRES, 1 plane
	a.Custom.WriteReg16(0x092, 0x003C)
	a.Custom.WriteReg16(0x094, 0x00D4)
	setColors(a, 0x000, 0xEEE)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 100); got != 0x777 {
		t.Errorf("column 100 = $%03X, want the average $777", got)
	}
	// DDF $3C-$D4 is 40 hires words; super-hires fetches twice as many.
	if got := a.Video.bplpt[0]; got != 0x1000+2*80 {
		t.Errorf("plane pointer advanced to $%X, want $%X (80 words)", got, 0x1000+2*80)
	}
}

// KILLEHB: with it set, the sixth plane no longer halves the colour.
func TestKillEHB(t *testing.T) {
	ones := solidPlane()
	zero := make([]byte, 64)
	a := ecsDenise(t, 0x6200, ones, zero, zero, zero, zero, ones) // colour 33
	setColors(a, 0x000, 0xEEE)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 100); got != 0x777 {
		t.Fatalf("EHB colour 33 = $%03X, want half of $EEE", got)
	}
	a = ecsDenise(t, 0x6200, ones, zero, zero, zero, zero, ones)
	setColors(a, 0x000, 0xEEE)
	a.Custom.WriteReg16(0x104, 0x0200) // KILLEHB
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 100); got != 0xEEE {
		t.Errorf("KILLEHB colour 33 = $%03X, want COLOR01 $EEE", got)
	}
}

// BRDRBLNK (BPLCON3 bit 5, with BPLCON0 ECSENA): the border is black.
func TestBorderBlank(t *testing.T) {
	a := ecsDenise(t, 0x0201)
	setColors(a, 0x00F)
	a.Custom.WriteReg16(0x08E, 0x2C91) // window starts 16 pixels in
	a.Custom.WriteReg16(0x106, 0x0020)
	a.Video.RenderLine(DisplayTop)
	if got := rgbAt(a, 0); got != 0x000 {
		t.Errorf("border = $%03X, want black", got)
	}
	if got := rgbAt(a, 100); got != 0x00F {
		t.Errorf("inside the window = $%03X, want COLOR00", got)
	}
}

// A byte read of a register's odd address returns its low byte, read the
// same way as the word (INTREQR is a view of INTREQ, not stored at $01E).
func TestCustomByteReadOddAddress(t *testing.T) {
	a := createTestAmiga(t)
	a.Custom.SetIntreq(1 << 1) // DSKBLK
	b := []byte{0}
	a.Custom.Read(0x01F, b) // BTST #1,$DFF01F
	if b[0]&0x02 == 0 {
		t.Errorf("INTREQR low byte = $%02X, want DSKBLK (bit 1) set", b[0])
	}
}
