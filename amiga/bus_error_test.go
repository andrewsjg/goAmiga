package amiga

import (
	"testing"
)

// A long read at $FFFFFF (e.g. a smashed freelist next=$FFFFFFFF) is an odd
// address: the 68000 takes an address error (exception 3) rather than
// completing the read, and the host must keep running.
func TestOpenBusReadFFFFFF(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}
	a.wireMemoryMap(false)

	// MOVE.L (A0),D0 ; NOP ; STOP
	a.ChipRAM.Write(0x1000, []byte{0x20, 0x10, 0x4E, 0x71, 0x4E, 0x72, 0x27, 0x00})
	a.ChipRAM.Write(0x0C, []byte{0x00, 0x00, 0x20, 0x00}) // address error vector
	a.CPU.A[7] = 0x4000
	a.CPU.SSP = 0x4000
	a.CPU.PC = 0x1000
	a.CPU.A[0] = 0x00FFFFFF
	a.CPU.D[0] = 0
	a.cpuStopped = false

	if err := a.Step(); err != nil {
		t.Fatalf("Step must not return host error, got: %v", err)
	}
	if a.CPU.D[0] != 0 {
		t.Errorf("D0=$%08X: the faulting read must not complete", a.CPU.D[0])
	}
	if a.CPU.PC != 0x2000 || a.CPU.A[7] != 0x4000-14 {
		t.Fatalf("PC=$%06X SP=$%06X; want the address error handler $2000 and a 14-byte frame", a.CPU.PC, a.CPU.A[7])
	}
	b := make([]byte, 6)
	a.ChipRAM.Read(int(a.CPU.A[7]), b)
	if addr := uint32(b[2])<<24 | uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5]); addr != 0x00FFFFFF || b[1]&0x10 == 0 {
		t.Errorf("frame: SSW $%02X%02X address $%08X; want a read of $00FFFFFF", b[0], b[1], addr)
	}
	for i := 0; i < 3; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatalf("RunFrame: %v", err)
		}
	}
}
