package amiga

import (
	"os"
	"path/filepath"
	"testing"
)

// Keys typed on the emulated keyboard reach AmigaDOS: a Startup-Sequence
// asks a question with Ask, and typing Y and Return answers it, under
// Kickstart 3.2 and 1.3.
func TestKeyboardReachesAmigaDOS(t *testing.T) {
	for _, rom := range []string{"../ROMS/kick32a1200.rom", "../ROMS/Kick13.rom"} {
		dir := filepath.Join(t.TempDir(), "Boot")
		os.MkdirAll(dir, 0o755)
		if rom == "../ROMS/Kick13.rom" {
			adfExtract(t, readDisk(t, "../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf"), dir)
		} else {
			adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), dir)
		}
		os.WriteFile(filepath.Join(dir, "S", "Startup-Sequence"), []byte("Ask \"Say Y\"\nIf WARN\nEcho >SYS:answer yes\nElse\nEcho >SYS:answer no\nEndIf\n"), 0o644)
		r, err := LoadROM(rom)
		if err != nil {
			t.Skip(err)
		}
		a, _ := New(r, nil)
		if err := a.AttachDirectory(dir, "", false); err != nil {
			t.Fatal(err)
		}
		a.Reset()
		for f := 0; f < 1500; f++ {
			if f == 1000 {
				a.Keyboard.EnqueueKey(0x15, true) // y
				a.Keyboard.EnqueueKey(0x15, false)
			}
			if f == 1010 {
				a.Keyboard.EnqueueKey(0x44, true) // Return
				a.Keyboard.EnqueueKey(0x44, false)
			}
			a.RunFrame()
		}
		if b, err := os.ReadFile(filepath.Join(dir, "answer")); string(b) != "yes\n" {
			t.Errorf("%s: answer %q (%v), want yes", rom, b, err)
		}
	}
}
