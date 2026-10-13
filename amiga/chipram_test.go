package amiga

import "testing"

// chipFree returns the free bytes and size of exec's chip memory region.
func chipFree(a *Amiga) (free, upper uint32) {
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	eb := rd(4)
	for n := rd(eb + 0x142); n != 0 && rd(n) != 0; n = rd(n) {
		attr, _ := a.CPU.M.Word(int(n + 14))
		if attr&2 != 0 { // MEMF_CHIP
			return rd(n + 28), rd(n + 24)
		}
	}
	return 0, 0
}

// With the 8372A's 1MB, Kickstart finds and uses all of it, and the
// display (bitplane, Copper and blitter DMA above 512KB) still works well
// enough for Workbench to start.
func TestChipRAM1MBKS13(t *testing.T) {
	a := bootHardfileMem(t, "../ROMS/Kick13.rom", "../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf", 1024, 0, 0)
	free, upper := chipFree(a)
	t.Logf("chip RAM: upper $%06X, %d KB free", upper, free/1024)
	// 1.3's exec keeps its supervisor stack at the top of chip RAM.
	if upper < 0xF0000 || free < 700*1024 {
		t.Errorf("chip region ends at $%06X with %d KB free, want most of 1MB", upper, free/1024)
	}
}

func TestChipRAM1MBKS32(t *testing.T) {
	a := bootHardfileMem(t, "../ROMS/Kick32.rom", "../Workbench/Install3.2.adf", 1024, 0, 0)
	free, upper := chipFree(a)
	t.Logf("chip RAM: upper $%06X, %d KB free", upper, free/1024)
	if upper != 0x100000 || free < 500*1024 {
		t.Errorf("chip region ends at $%06X with %d KB free, want $100000", upper, free/1024)
	}
}

func TestSetChipRAMWraps(t *testing.T) {
	a := createTestAmiga(t)
	if err := a.SetChipRAM(1024); err != nil {
		t.Fatal(err)
	}
	if err := a.SetChipRAM(4096); err == nil {
		t.Error("4MB chip RAM should be refused (AGA has at most 2MB)")
	}
	a.ChipRAM.Write(0x80000, []byte{0xAB})
	var b [1]byte
	a.ChipRAM.Read(0x180000, b[:]) // DMA pointers wrap at 1MB
	if b[0] != 0xAB {
		t.Errorf("read $%02X through the 1MB wrap, want $AB", b[0])
	}
	a.ChipRAM.Read(0x00000, b[:])
	if b[0] == 0xAB {
		t.Error("$80000 aliases $00000 with 1MB fitted")
	}
}
