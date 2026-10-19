package amiga

import (
	"os"
	"testing"
)

func TestIdentifyROM(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"../ROMS/Kick13.rom", "Kickstart 1.3 (34.5)"},
		{"../ROMS/Kick32.rom", "Kickstart 3.2 (47.96)"},
		{"../ROMS/kick32a1200.rom", "Kickstart 3.2 (47.96)"},
		{"../ROMS/kicka4000.rom", "Kickstart 3.2 (47.96) for the A4000"},
	} {
		d, err := os.ReadFile(c.file)
		if err != nil {
			t.Skip(err)
		}
		r := IdentifyROM(d)
		if got := r.String(); got != c.want {
			t.Errorf("%s: %q, want %q", c.file, got, c.want)
		}
		if r.Machine == "a4000" {
			if r.Mismatch("68000", "none") == "" {
				t.Errorf("the A4000 Kickstart on an A500 should be a mismatch")
			}
			if m := r.Mismatch("68040", "a4000"); m != "" {
				t.Errorf("the A4000 Kickstart on the A4000: %q", m)
			}
		}
	}
}
