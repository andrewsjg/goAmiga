package amiga

import (
	"encoding/binary"
	"testing"

	m68k "github.com/cavaliercoder/go-m68k"
)

// createTestAmiga creates a minimal Amiga with a valid ROM for testing.
// The ROM has reset vectors at the start (SSP=$080000, PC=$FC0002)
// and a STOP instruction at the entry point.
func createTestAmiga(t *testing.T) *Amiga {
	t.Helper()

	rom := make([]byte, ROMSize)
	// Reset vectors: SSP = $00080000, PC = $00FC0002
	binary.BigEndian.PutUint32(rom[0:4], 0x00080000)
	binary.BigEndian.PutUint32(rom[4:8], 0x00FC0002)
	// STOP #$2700 at offset 2 (address $FC0002)
	binary.BigEndian.PutUint16(rom[2:4], 0x4E72)
	binary.BigEndian.PutUint16(rom[4:6], 0x2700)

	a, err := New(rom, nil)
	if err != nil {
		t.Fatalf("failed to create Amiga: %v", err)
	}
	if err := a.Reset(); err != nil {
		t.Fatalf("failed to reset Amiga: %v", err)
	}
	return a
}

func TestUpdateInterrupts_VERTB(t *testing.T) {
	a := createTestAmiga(t)

	// Enable master + VERTB (bit 5) in INTENA
	// Write INTENA = $C020 (set mode + master enable bit 14 + VERTB bit 5)
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0xC020)
	a.Custom.Write(0x09A, buf[:]) // INTENA

	// Set INTREQ VERTB bit
	binary.BigEndian.PutUint16(buf[:], 0x8020)
	a.Custom.Write(0x09C, buf[:]) // INTREQ

	// Verify VERTB maps to IPL 3
	intena := a.Custom.ReadReg16(0x09A)
	intreq := a.Custom.ReadReg16(0x09C)
	if intena&0x4020 != 0x4020 {
		t.Errorf("INTENA = $%04X, want master+VERTB ($4020)", intena)
	}
	if intreq&0x0020 == 0 {
		t.Errorf("INTREQ = $%04X, want VERTB bit set", intreq)
	}

	// CPU should have a pending interrupt
	if a.CPU.PendingIPL() == 0 {
		t.Error("expected pending interrupt, got 0")
	}
}

func TestUpdateInterrupts_MasterDisabled(t *testing.T) {
	a := createTestAmiga(t)

	// Enable only VERTB without master enable
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0x8020) // set VERTB but NOT master
	a.Custom.Write(0x09A, buf[:])

	// Set INTREQ
	binary.BigEndian.PutUint16(buf[:], 0x8020)
	a.Custom.Write(0x09C, buf[:])

	// Should NOT raise interrupt (master enable off)
	if a.CPU.PendingIPL() != 0 {
		t.Errorf("expected no pending interrupt with master disabled, got %d", a.CPU.PendingIPL())
	}
}

func TestCIATimerA_Underflow(t *testing.T) {
	a := createTestAmiga(t)

	// Enable master + PORTS interrupt (CIA-A maps to PORTS, bit 3)
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0xC008) // master + PORTS
	a.Custom.Write(0x09A, buf[:])

	// Set CIA-A Timer A latch to 5
	ciaBuf := [1]byte{5}
	a.CIAA.Write(0x0400, ciaBuf[:]) // TALO (reg 4, addr bits 8-11)
	ciaBuf[0] = 0
	a.CIAA.Write(0x0500, ciaBuf[:]) // TAHI (reg 5)

	// Enable Timer A ICR mask (bit 0)
	ciaBuf[0] = 0x81                // set mode + Timer A
	a.CIAA.Write(0x0D00, ciaBuf[:]) // ICR

	// Start Timer A
	ciaBuf[0] = 0x01
	a.CIAA.Write(0x0E00, ciaBuf[:]) // CRA = start

	// Tick 5 times — counter goes from 5 to 0
	for i := 0; i < 5; i++ {
		a.CIAA.Tick()
	}

	// Counter should be at 0 now, no underflow yet
	if a.CIAA.timerACounter != 0 {
		t.Errorf("after 5 ticks: counter = %d, want 0", a.CIAA.timerACounter)
	}

	// One more tick should trigger underflow
	a.CIAA.Tick()

	// Counter should have reloaded from latch (5)
	if a.CIAA.timerACounter != 5 {
		t.Errorf("after underflow: counter = %d, want 5 (reloaded from latch)", a.CIAA.timerACounter)
	}

	// ICR data should have Timer A bit set
	if a.CIAA.icrData&0x01 == 0 {
		t.Error("ICR data Timer A bit not set after underflow")
	}

	// INTREQ PORTS bit should be set (via OnInterrupt callback)
	intreq := a.Custom.ReadReg16(0x09C)
	if intreq&0x0008 == 0 {
		t.Errorf("INTREQ = $%04X, want PORTS bit (3) set", intreq)
	}
}

func TestCIATimerA_OneShot(t *testing.T) {
	a := createTestAmiga(t)

	// Set latch to 2
	ciaBuf := [1]byte{2}
	a.CIAA.Write(0x0400, ciaBuf[:]) // TALO
	ciaBuf[0] = 0
	a.CIAA.Write(0x0500, ciaBuf[:]) // TAHI

	// Start in one-shot mode (CRA bit 0 + bit 3)
	ciaBuf[0] = 0x09
	a.CIAA.Write(0x0E00, ciaBuf[:])

	// Tick 3 times to trigger underflow
	for i := 0; i < 3; i++ {
		a.CIAA.Tick()
	}

	// Timer should have stopped (CRA bit 0 cleared)
	if a.CIAA.regs[CIARegCRA]&0x01 != 0 {
		t.Error("one-shot timer did not stop after underflow")
	}
}

func TestCopperMOVE(t *testing.T) {
	a := createTestAmiga(t)

	// Write a Copper program to chip RAM at address $1000:
	// MOVE $0ABC, COLOR00 ($180)
	// WAIT $FFFF,$FFFE (end of list)
	copperProg := []byte{
		0x01, 0x80, 0x0A, 0xBC, // MOVE COLOR00 ($180) = $0ABC
		0xFF, 0xFF, 0xFF, 0xFE, // WAIT end-of-list
	}
	a.ChipRAM.Write(0x1000, copperProg)

	// Set COP1LC to $1000
	a.Copper.cop1lc = 0x00001000
	a.Copper.Restart(1)

	// Enable Copper DMA (DMACON bits 7+9)
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0x8280) // set DMAEN + COPEN
	a.Custom.Write(0x096, buf[:])

	// Two bus cycles: the MOVE's two words
	a.Copper.Step(0, 0)
	a.Copper.Step(4, 0)

	// COLOR00 ($180) should now be $0ABC
	color00 := a.Custom.ReadReg16(0x180)
	if color00 != 0x0ABC {
		t.Errorf("COLOR00 = $%04X, want $0ABC", color00)
	}
}

func TestCopperWAIT(t *testing.T) {
	a := createTestAmiga(t)

	// Copper program: WAIT for vpos=100, then MOVE COLOR00
	copperProg := []byte{
		0x64, 0x01, 0xFF, 0xFE, // WAIT vpos=$64 (100), hpos=0
		0x01, 0x80, 0x0F, 0xFF, // MOVE COLOR00 = $0FFF
		0xFF, 0xFF, 0xFF, 0xFE, // end of list
	}
	a.ChipRAM.Write(0x2000, copperProg)

	a.Copper.cop1lc = 0x00002000
	a.Copper.Restart(1)

	// Fetch the WAIT (two words) and compare at vpos=50 — should be waiting
	a.Copper.Step(0, 50)
	a.Copper.Step(4, 50)
	a.Copper.Step(8, 50)
	if !a.Copper.wait {
		t.Error("Copper should be waiting at vpos=50")
	}

	// Step at vpos=100 — WAIT should be satisfied, then MOVE executes
	a.Copper.Step(0, 100)
	if a.Copper.wait {
		t.Error("Copper should have stopped waiting at vpos=100")
	}

	// The MOVE's two words
	a.Copper.Step(4, 100)
	a.Copper.Step(8, 100)
	color00 := a.Custom.ReadReg16(0x180)
	if color00 != 0x0FFF {
		t.Errorf("COLOR00 = $%04X, want $0FFF after WAIT+MOVE", color00)
	}
}

func TestDMACON_LongWrite(t *testing.T) {
	a := createTestAmiga(t)

	// First clear DMACON
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0x7FFF) // clear all
	a.Custom.Write(0x096, buf[:])

	dmacon := a.Custom.ReadReg16(0x096)
	if dmacon != 0 {
		t.Fatalf("DMACON after clear: got $%04X, want $0000", dmacon)
	}

	// Now do a 4-byte (long) write at $094 (DDFSTOP)
	// Bytes 0-1 go to $094 (DDFSTOP), bytes 2-3 go to $096 (DMACON)
	// DMACON enable = $83A0 (set mode + DMAEN + BPLEN + COPEN + BLTEN)
	longBuf := [4]byte{0x00, 0xD8, 0x83, 0xA0} // DDFSTOP=$00D8, DMACON=$83A0
	a.Custom.Write(0x094, longBuf[:])

	// DMACON should now have bits set via set/clear logic
	dmacon = a.Custom.ReadReg16(0x096)
	expected := uint16(0x03A0) // DMAEN + BPLEN + COPEN + BLTEN (without set bit)
	if dmacon != expected {
		t.Errorf("DMACON after long write: got $%04X, want $%04X", dmacon, expected)
	}
}

func TestVBLANKInterrupt(t *testing.T) {
	a := createTestAmiga(t)

	// Set up interrupt vector for level 3 (autovector 27) at address $6C
	// Point it to a known address in chip RAM
	handlerAddr := uint32(0x001000)
	vecBuf := [4]byte{}
	binary.BigEndian.PutUint32(vecBuf[:], handlerAddr)

	// Need to disable overlay first so we can write to chip RAM at low addresses
	a.wireMemoryMap(false)

	// Write level 3 autovector ($6C = vector 27 * 4)
	a.ChipRAM.Write(0x6C, vecBuf[:])

	// Write a STOP instruction at the handler
	stopBuf := [4]byte{0x4E, 0x72, 0x27, 0x00} // STOP #$2700
	a.ChipRAM.Write(int(handlerAddr), stopBuf[:])

	// Enable master + VERTB in INTENA
	buf := [2]byte{}
	binary.BigEndian.PutUint16(buf[:], 0xC020)
	a.Custom.Write(0x09A, buf[:])

	// Ensure CPU is in supervisor mode with VERTB vector set
	a.CPU.SR = m68k.StatusSupervisor | (3 << 8) // IPL mask = 3 initially
	a.CPU.SR = m68k.StatusSupervisor            // IPL mask = 0 (allow all interrupts)

	// Simulate VBLANK by calling SetIntreq directly
	a.Custom.SetIntreq(1 << 5)

	// Verify INTREQ has VERTB set
	intreq := a.Custom.ReadReg16(0x09C)
	if intreq&0x0020 == 0 {
		t.Errorf("INTREQ = $%04X, want VERTB bit set", intreq)
	}

	// CPU should have pending interrupt at level 3
	if a.CPU.PendingIPL() == 0 {
		t.Error("expected pending interrupt after VBLANK")
	}
}
