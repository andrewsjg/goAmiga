package amiga

import "testing"

// bootKS32 boots Kickstart 3.2 (A500/A600/A2000, 47.96) with no disk and
// 512 KB slow RAM for frames frames.
func bootKS32(t *testing.T, frames int) *Amiga {
	t.Helper()
	rom, err := LoadROM("../Kick32.rom")
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
