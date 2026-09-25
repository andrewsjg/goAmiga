package amiga

import (
	"testing"
)

func TestInsertVPRaw(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 3; i++ {
		a.RunFrame()
	}

	gb := uint32(0x221E)
	av, _ := a.CPU.M.Long(int(gb) + 0x22)
	vp, _ := a.CPU.M.Long(int(av))
	t.Logf("ActiView=$%08X VP=$%08X", av, vp)
	// dump 40 words of View
	t.Log("View raw:")
	for o := 0; o < 24; o += 2 {
		w, _ := a.CPU.M.Word(int(av) + o)
		t.Logf("  +%02X: $%04X (%d)", o, w, int16(w))
	}
	t.Log("ViewPort raw:")
	for o := 0; o < 48; o += 2 {
		w, _ := a.CPU.M.Word(int(vp) + o)
		t.Logf("  +%02X: $%04X (%d)", o, w, int16(w))
	}
	// RasInfo at various guesses
	for _, off := range []int{32, 36, 28, 24} {
		ri, _ := a.CPU.M.Long(int(vp) + off)
		t.Logf("VP+%d as long $%08X", off, ri)
	}
}
