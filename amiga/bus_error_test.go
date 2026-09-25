package amiga

import (
	"testing"
)

// Long reads at $FFFFFF (smashed freelist next=$FFFFFFFF) must not kill the host.
// Mapper/ROM open-bus returns $FF; the instruction completes.
func TestOpenBusReadFFFFFF(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
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
	a.CPU.A[7] = 0x4000
	a.CPU.SSP = 0x4000
	a.CPU.PC = 0x1000
	a.CPU.A[0] = 0x00FFFFFF
	a.CPU.D[0] = 0
	a.cpuStopped = false

	if err := a.Step(); err != nil {
		t.Fatalf("Step must not return host error, got: %v", err)
	}
	// ROM pads past-end bytes with $FF; first byte is last ROM byte.
	if a.CPU.D[0]&0x00FFFFFF != 0x00FFFFFF {
		t.Fatalf("D0=$%08X want low 24 bits $FFFFFF (open-bus pad)", a.CPU.D[0])
	}
	if a.CPU.PC != 0x1002 {
		t.Fatalf("PC=$%08X want $1002 after MOVE.L", a.CPU.PC)
	}
	for i := 0; i < 3; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatalf("RunFrame: %v", err)
		}
	}
}
