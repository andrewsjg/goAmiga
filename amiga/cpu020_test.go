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
	a := boot020(t, "../ROMS/Kick32.rom", "../Workbench/Install3.2.adf")
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
	a := boot020(t, "../ROMS/Kick13.rom", "../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if f := execAttnFlags(a); f&2 == 0 {
		t.Errorf("AttnFlags = $%04X, want the 68020 bit", f)
	}
}

// Kickstart finds the FPU and its type (by the size of the FSAVE frame).
func TestBootFPU(t *testing.T) {
	for _, f := range []struct {
		model string
		flags uint16
	}{{"68881", 0x10}, {"68882", 0x30}} {
		romData, err := LoadROM("../ROMS/Kick32.rom")
		if err != nil {
			t.Skip(err)
		}
		a, _ := New(romData, nil)
		a.SetCPU("68020", 0)
		if err := a.SetFPU(f.model); err != nil {
			t.Fatal(err)
		}
		a.SetChipRAM(1024)
		a.ConfigureMemory(0, 2048)
		a.AttachHardfile(hardfileFromADF(t, "../Workbench/Install3.2.adf"), false)
		a.Reset()
		if !waitWorkbench(t, a) {
			t.Fatalf("%s: Workbench did not start", f.model)
		}
		if got := execAttnFlags(a) & 0x30; got != f.flags {
			t.Errorf("%s: AttnFlags FPU bits $%02X, want $%02X", f.model, got, f.flags)
		}
	}
}

// Kickstart 3.2 on a 68030 with a 68882: exec recognises both and
// Workbench starts.
func TestBootKS32On68030(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	if err := a.SetCPU("68030", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFPU("68882"); err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(0, 4096)
	if err := a.AttachHardfile(hardfileFromADF(t, "../Workbench/Install3.2.adf"), false); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start on the 68030")
	}
	eb := a.peek32(4)
	attn := a.peek16(eb + 0x128)
	t.Logf("AttnFlags $%04X", attn)
	if attn&0x04 == 0 || attn&0x20 == 0 {
		t.Errorf("AttnFlags $%04X: want AFF_68030 and AFF_68882", attn)
	}
}

// Kickstart 3.2 on a 68040, as an A1200 with a 68040 accelerator and as
// an A500 with one: exec finds the 040 (by its MOVEC registers) and its
// FPU (by the FSAVE frame), and Workbench starts.
func TestBootKS32On68040(t *testing.T) {
	for _, m := range []struct{ rom, chipset string }{
		{"../ROMS/kick32a1200.rom", "aga"},
		{"../ROMS/Kick32.rom", "ecs"},
	} {
		rom, err := LoadROM(m.rom)
		if err != nil {
			t.Skip(err)
		}
		a, _ := New(rom, nil)
		if err := a.SetCPU("68040", 0); err != nil {
			t.Fatal(err)
		}
		a.SetFPU("auto")
		a.SetChipset(m.chipset)
		if m.chipset == "aga" {
			a.SetChipRAM(2048)
		}
		a.ConfigureMemory(0, 8192)
		if err := a.AttachHardfile(hardfileFromADF(t, "../Workbench/Install3.2.adf"), false); err != nil {
			t.Fatal(err)
		}
		exc := map[int]int{}
		a.CPU.OnException = func(v int, pc, instr uint32) {
			if v < 24 || v >= 32 {
				exc[v]++
			}
		}
		a.Reset()
		if !waitWorkbench(t, a) {
			t.Fatalf("%s: Workbench did not start on the 68040", m.rom)
		}
		for i := 0; i < 300; i++ {
			a.RunFrame()
		}
		// Exec's Supervisor() takes privilege violations (vector 8) on
		// purpose; nothing should take a bus or address error or F-line.
		t.Logf("%s: exceptions other than interrupts: %v", m.rom, exc)
		for _, v := range []int{2, 3, 11} {
			if exc[v] != 0 {
				t.Errorf("%s: %d exceptions with vector %d", m.rom, exc[v], v)
			}
		}
		if p := os.Getenv("GOAMIGA_DIAG_PNG"); p != "" {
			a.Video.DisplayActive = true
			a.RunFrame()
			a.RunFrame()
			savePNG(t, a, p+"-"+m.chipset+".png")
		}
		eb := a.peek32(4)
		attn := a.peek16(eb + 0x128)
		t.Logf("%s: AttnFlags $%04X", m.rom, attn)
		// AFF_68010-68040, and the FPU: AFF_FPU40 (with the 68881/2 bits,
		// which exec sets for any FPU).
		if attn&0x0F != 0x0F || attn&0x40 == 0 {
			t.Errorf("%s: AttnFlags $%04X: want AFF_68010-68040 and AFF_FPU40", m.rom, attn)
		}
	}
}
