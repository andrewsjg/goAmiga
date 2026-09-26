package amiga

import (
	"encoding/binary"
	"testing"
)

// A 512KB ROM fills $F80000-$FFFFFF; a 256KB ROM appears at both $F80000
// and $FC0000. At reset the ROM overlays $000000.
func TestROMMapping(t *testing.T) {
	for _, size := range []int{ROMSize, 2 * ROMSize} {
		rom := make([]byte, size)
		for i := 0; i < size; i += 4 {
			binary.BigEndian.PutUint32(rom[i:], uint32(i)) // each long holds its offset
		}
		binary.BigEndian.PutUint32(rom[0:], 0x00080000) // SSP
		binary.BigEndian.PutUint32(rom[4:], 0x00F80008) // PC
		binary.BigEndian.PutUint16(rom[8:], 0x4E72)     // STOP #$2700
		binary.BigEndian.PutUint16(rom[10:], 0x2700)
		a, err := New(rom, nil)
		if err != nil {
			t.Fatal(err)
		}
		a.Reset()
		long := func(addr int) uint32 { v, _ := a.CPU.M.Long(addr); return v }
		want := map[int]uint32{
			0xF80100: 0x100,
			0xFC0100: uint32(0x40100 % size), // upper half, or the mirror
			0x000100: 0x100,                  // reset overlay
		}
		for addr, w := range want {
			if got := long(addr); got != w {
				t.Errorf("%dKB ROM: $%06X reads $%08X, want $%08X", size/1024, addr, got, w)
			}
		}
		if a.CPU.PC != 0xF80008 {
			t.Errorf("%dKB ROM: reset PC $%06X, want $F80008", size/1024, a.CPU.PC)
		}
	}
}

func TestROMChecksum(t *testing.T) {
	rom := make([]byte, 16)
	binary.BigEndian.PutUint32(rom[0:], 0xFFFFFFF0)
	binary.BigEndian.PutUint32(rom[4:], 0x00000010) // carry out: sum wraps to 0, +1
	binary.BigEndian.PutUint32(rom[8:], 0xFFFFFFFE)
	if got := ROMChecksum(rom); got != 0xFFFFFFFF {
		t.Errorf("checksum $%08X, want $FFFFFFFF", got)
	}
	if rom, err := LoadROM("../Kick13.rom"); err == nil {
		if got := ROMChecksum(rom); got != 0xFFFFFFFF {
			t.Errorf("Kick13.rom checksum $%08X", got)
		}
	}
}
