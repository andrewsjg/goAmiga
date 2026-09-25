package amiga

import "testing"

func ciaWrite(c *CIA, reg int, val byte) { c.Write(reg<<8, []byte{val}) }

// In one-shot mode a write to TAHI/TBHI loads the counter and starts the
// timer, even while it is already counting.
func TestCIAOneShotHIWriteRestartsRunningTimer(t *testing.T) {
	c := NewCIA("CIA-A", nil)
	c.Reset()
	ciaWrite(c, CIARegTALO, 0x00)
	ciaWrite(c, CIARegTAHI, 0x10)
	ciaWrite(c, CIARegCRA, 0x09) // START + one-shot
	for i := 0; i < 0x800; i++ {
		c.Tick()
	}
	ciaWrite(c, CIARegTAHI, 0x20)
	if c.timerACounter != 0x2000 || c.regs[CIARegCRA]&0x01 == 0 {
		t.Errorf("after TAHI write: counter $%04X, CRA $%02X; want $2000 and running",
			c.timerACounter, c.regs[CIARegCRA])
	}

	// Continuous mode: a running timer only takes the new latch.
	ciaWrite(c, CIARegCRA, 0x01)
	for i := 0; i < 0x100; i++ {
		c.Tick()
	}
	before := c.timerACounter
	ciaWrite(c, CIARegTAHI, 0x30)
	if c.timerACounter != before || c.timerALatch != 0x3000 {
		t.Errorf("continuous TAHI write: counter $%04X (was $%04X), latch $%04X",
			c.timerACounter, before, c.timerALatch)
	}
}

// CRB INMODE (bits 6-5): 00 E clock, 01 CNT, 1x Timer A underflows.
func TestCIATimerBInputMode(t *testing.T) {
	for _, tc := range []struct {
		crb  byte
		want uint16 // Timer B after 100 E clocks from $1000, Timer A latch 9
	}{
		{0x01, 0x1000 - 100}, // E clock
		{0x21, 0x1000},       // CNT: no edges
		{0x41, 0x1000 - 10},  // Timer A underflows (every 10 ticks)
		{0x61, 0x1000 - 10},  // Timer A underflows while CNT high
	} {
		c := NewCIA("CIA-B", nil)
		c.Reset()
		ciaWrite(c, CIARegTALO, 9)
		ciaWrite(c, CIARegTAHI, 0)
		ciaWrite(c, CIARegTBLO, 0x00)
		ciaWrite(c, CIARegTBHI, 0x10)
		ciaWrite(c, CIARegCRA, 0x01)
		ciaWrite(c, CIARegCRB, tc.crb)
		for i := 0; i < 100; i++ {
			c.Tick()
		}
		if c.timerBCounter != tc.want {
			t.Errorf("CRB $%02X: Timer B = $%04X, want $%04X", tc.crb, c.timerBCounter, tc.want)
		}
	}
}

func TestCIAICRMaskBits(t *testing.T) {
	c := NewCIA("CIA-A", nil)
	c.Reset()
	ciaWrite(c, CIARegICR, 0xFF)
	if c.icrMask != 0x1F {
		t.Errorf("ICR mask = $%02X, want $1F", c.icrMask)
	}
}
