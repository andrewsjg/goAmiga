package amiga

import (
	"testing"
	"time"
)

// TestRunFrameNoHangWhenStoppedAtVBlank ensures STOP idle at vpos==0 cannot
// wedge RunFrame (the leave-line-0 infinite loop).
func TestRunFrameNoHangWhenStoppedAtVBlank(t *testing.T) {
	rom := make([]byte, ROMSize)
	// Minimal reset vectors
	rom[0] = 0x00
	rom[1] = 0x04
	rom[2] = 0x00
	rom[3] = 0x00 // SSP
	rom[4] = 0x00
	rom[5] = 0xFC
	rom[6] = 0x00
	rom[7] = 0x08 // PC in ROM

	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}

	// Force STOP-at-VBlank condition: beam at 0, CPU stopped, no VERTB wake.
	a.vpos = 0
	a.hpos = 0
	a.cpuStopped = true
	// Disable all interrupts so VERTB cannot wake the CPU.
	a.Custom.WriteReg16(0x09A, 0x7FFF) // INTENA clear all (bit 15 = 0 → clear)

	done := make(chan struct{})
	go func() {
		_ = a.RunFrame()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("RunFrame hung for 2s with CPU STOP'd at vpos==0 (leave-line-0 loop)")
	}

	if a.cycles == 0 {
		t.Error("expected cycle progress")
	}
}
