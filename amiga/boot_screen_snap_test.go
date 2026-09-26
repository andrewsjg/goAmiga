package amiga

import (
	"testing"
)

func TestBootScreenSnap(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	a.Video.DisplayActive = true
	for i := 0; i < 8_000_000; i++ {
		a.Step()
	}
	// ensure copper runs a few frames
	for i := 0; i < 5; i++ {
		a.RunFrame()
	}
	// force palette in case copper not yet
	// use actual copper palette if set
	t.Logf("COLOR00-03: %04X %04X %04X %04X",
		a.Custom.ReadReg16(0x180), a.Custom.ReadReg16(0x182),
		a.Custom.ReadReg16(0x184), a.Custom.ReadReg16(0x186))
	if err := saveBPL12PNG(a, "/tmp/boot_insert_disk.png", 0xA572, 0xC4B2); err != nil {
		t.Fatal(err)
	}
	// also video framebuffer
	if a.Video != nil && len(a.Video.Pixels) > 0 {
		// save via image from pixels
		t.Logf("Video %dx%d", a.Video.Width, a.Video.Height)
	}
	t.Log("saved /tmp/boot_insert_disk.png")
}
