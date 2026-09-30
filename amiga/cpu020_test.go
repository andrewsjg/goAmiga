package amiga

import (
	"os"
	"testing"
)

// execAttnFlags returns ExecBase->AttnFlags: bit 0 68010, bit 1 68020.
func execAttnFlags(a *Amiga) uint16 {
	eb, _ := a.CPU.M.Long(4)
	f, _ := a.CPU.M.Word(int(eb) + 0x128)
	return f
}

func boot020(t *testing.T, rom, disk string) *Amiga {
	t.Helper()
	romData, err := LoadROM(rom)
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(romData, nil)
	if err := a.SetCPU("68020", 0); err != nil {
		t.Fatal(err)
	}
	a.SetChipset("ecs-agnus")
	a.SetChipRAM(1024)
	a.ConfigureMemory(0, 2048)
	if err := a.AttachHardfile(hardfileFromADF(t, disk), false); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start on the 68020")
	}
	return a
}

// Kickstart finds the 68020 by itself (it probes with MOVEC and CACR) and
// boots Workbench on it.
func TestBoot68020KS32(t *testing.T) {
	a := boot020(t, "../ROMS/Kick32.rom", "../disks/Install3.2.adf")
	for i := 0; i < 400; i++ {
		a.RunFrame()
	}
	if f := execAttnFlags(a); f&3 != 3 {
		t.Errorf("AttnFlags = $%04X, want 68010 and 68020 bits set", f)
	}
	if p := os.Getenv("GOAMIGA_DIAG_PNG"); p != "" {
		a.Video.DisplayActive = true
		a.RunFrame()
		a.RunFrame()
		savePNG(t, a, p)
	}
}

func TestBoot68020KS13(t *testing.T) {
	a := boot020(t, "../ROMS/Kick13.rom", "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if f := execAttnFlags(a); f&2 == 0 {
		t.Errorf("AttnFlags = $%04X, want the 68020 bit", f)
	}
}
