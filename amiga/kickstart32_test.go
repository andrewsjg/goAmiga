package amiga

import (
	"bytes"
	"os"
	"testing"
)

// bootKS32 boots Kickstart 3.2 (A500/A600/A2000, 47.96) with no disk and
// 512 KB slow RAM for frames frames.
func bootKS32(t *testing.T, frames int) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	if ROMChecksum(rom) != 0xFFFFFFFF {
		t.Skip("Kick32.rom fails its checksum")
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(512, 0)
	a.Reset()
	a.Video.DisplayActive = true
	for f := 0; f < frames; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// Kickstart 3.2 reaches its insert-disk screen (Boing ball, version text,
// disk and drive) with no alert. Reference counts are from a render
// checked by eye; the ball animates, so its colours get more slack.
func TestKS32InsertScreen(t *testing.T) {
	a := bootKS32(t, 600)
	if eb, _ := a.CPU.M.Long(4); eb > 0x100 {
		if alert, _ := a.CPU.M.Long(int(eb) + 0x202); alert != 0xFFFFFFFF {
			t.Errorf("exec LastAlert = $%08X", alert)
		}
	}
	hist := map[uint16]int{}
	p := a.Video.Pixels
	for i := 0; i < len(p); i += 4 {
		hist[uint16(p[i]/17)<<8|uint16(p[i+1]/17)<<4|uint16(p[i+2]/17)]++
	}
	for _, c := range []struct {
		rgb   uint16
		n     int
		slack int // percent
	}{
		{0x525, 135607, 5}, // background
		{0xF00, 7744, 20},  // Boing ball red
		{0xFFF, 10983, 20}, // ball white, disk label
		{0xEA8, 4325, 10},  // version text, drive
	} {
		if got := hist[c.rgb]; got < c.n*(100-c.slack)/100 || got > c.n*(100+c.slack)/100 {
			t.Errorf("colour $%03X: %d pixels, want %d ±%d%%", c.rgb, got, c.n, c.slack)
		}
	}
}

// Kickstart 3.2 boots the Install 3.2 disk to Workbench with a correct
// display: graphics.library detects OCS (a zero DENISEID read made SetPatch
// detect AGA and shear every screen), and the screen's bitplanes stay on
// down to line 299 (the Copper used to run the instruction after a line-255
// WAIT on line 255 itself, cutting the screen off 40 lines early).
func TestKS32InstallDiskWorkbench(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(512, 0)
	if err := a.LoadDisk("../Workbench/Install3.2.adf"); err != nil {
		t.Skip(err)
	}
	a.Disk.WriteProtected = true
	a.Reset()
	for f := 0; f < 3000; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	if alert := rd(rd(4) + 0x202); alert != 0xFFFFFFFF {
		t.Errorf("exec LastAlert = $%08X", alert)
	}
	gfx := findLibrary(a, "graphics.library")
	if crb, _ := a.CPU.M.Byte(int(gfx + 0xEC)); crb != 0 {
		t.Errorf("GfxBase->ChipRevBits0 = $%02X, want 0 (OCS)", crb)
	}
	// Workbench's window spans the screen.
	ib := findLibrary(a, "intuition.library")
	scr := rd(ib + 60)
	if w := rd(scr + 4); w == 0 || readCStrMem(a, rd(w+32), 20) != "Workbench" {
		t.Fatal("no Workbench window")
	}
	for a.vpos != 299 {
		a.advanceBeamPerCycle(1)
	}
	a.advanceBeamPerCycle(200)
	if planes := a.Custom.ReadReg16(0x100) >> 12 & 7; planes != 2 {
		t.Errorf("line 299: %d bitplanes, want 2 (screen cut short)", planes)
	}
}

// patchLine replaces a startup-sequence line in an in-memory image with a
// command of at most the same length (FFS data blocks have no checksum).
func patchLine(t *testing.T, adf []byte, old, repl string) {
	t.Helper()
	i := bytes.Index(adf, []byte(old))
	if i < 0 || len(repl) > len(old) {
		t.Fatalf("cannot patch %q", old)
	}
	copy(adf[i:], append([]byte(repl), bytes.Repeat([]byte(" "), len(old)-len(repl))...))
}

// windows lists the titles of all Intuition windows.
func windows(a *Amiga) []string {
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	var out []string
	if ib := findLibrary(a, "intuition.library"); ib != 0 {
		for scr := rd(ib + 60); scr != 0; scr = rd(scr) {
			for w := rd(scr + 4); w != 0; w = rd(w) {
				out = append(out, readCStrMem(a, rd(w+32), 40))
			}
		}
	}
	return out
}

// Install 3.2 in DF0: and Workbench 3.2 in DF1: — the Hyperion floppy set
// used with two drives. Both volumes mount, and with Workbench 3.2's Libs
// and Classes in LIBS: (as its own startup-sequence would set up), Clock
// runs. Booted from Install alone, Clock crashes: amigaguide.library is
// missing and Clock calls through the NULL base (error #8000000B).
func TestKS32TwoDrivesClock(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	install, err1 := os.ReadFile("../Workbench/Install3.2.adf")
	wb, err2 := os.ReadFile("../Workbench/Workbench3.2.adf")
	if err1 != nil || err2 != nil {
		t.Skip("needs Workbench/Install3.2.adf and Workbench/Workbench3.2.adf")
	}
	patchLine(t, install, "Copy ENVARC: RAM:ENV QUIET ALL NOREQ", "Assign LIBS: DF1:Libs DF1:Classes")
	patchLine(t, install, "Assign >NIL: T:   RAM:T", "Run DF1:Utilities/Clock")

	a, _ := New(rom, nil)
	a.ConfigureMemory(512, 0)
	a.Disk.InsertDisk(install)
	a.Disk.ConnectDrive(1).InsertDisk(wb)
	a.Disk.SetWriteProtected(true)
	a.Reset()
	for f := 0; f < 5000; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	vols := dosVolumes(a)
	if !vols["Install3.2"] || !vols["Workbench3.2"] {
		t.Errorf("volumes %v: want Install3.2 and Workbench3.2 both in drives", vols)
	}
	ws := windows(a)
	t.Logf("windows: %q", ws)
	hasClock := false
	for _, w := range ws {
		if w == "Software Failure" || w == "System Request" {
			t.Errorf("unexpected %q window", w)
		}
		hasClock = hasClock || w == "Clock"
	}
	if !hasClock {
		t.Error("no Clock window")
	}
}
