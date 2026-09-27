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

// The CPU sees CIA-A on odd bytes (A12 low) and CIA-B on even bytes (A13
// low) anywhere in $BF0000-$BFFFFF.
func TestCIAAddressDecoding(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	a.CIAA.regs[CIARegDDRB] = 0xA5
	a.CIAB.regs[CIARegDDRB] = 0x5A
	rd := func(addr, n int) []byte {
		b := make([]byte, n)
		a.CPU.M.Read(addr, b)
		return b
	}
	for _, c := range []struct {
		addr int
		want []byte
	}{
		{0xBFE301, []byte{0xA5}},       // CIA-A DDRB
		{0xBFD300, []byte{0x5A}},       // CIA-B DDRB
		{0xBFD301, []byte{0x00}},       // A12 high: CIA-A not selected
		{0xBFE300, []byte{0x00}},       // A13 high: CIA-B not selected
		{0xBFE300, []byte{0x00, 0xA5}}, // word: CIA-A on the low byte only
		{0xBFC300, []byte{0x5A, 0xA5}}, // word hitting both chips
		{0xBF0301, []byte{0xA5}},       // partial decode mirror
	} {
		if got := rd(c.addr, len(c.want)); string(got) != string(c.want) {
			t.Errorf("read $%06X x%d = % X, want % X", c.addr, len(c.want), got, c.want)
		}
	}

	a.CPU.M.Write(0xBFE300, []byte{0x11}) // even byte, A13 high: nobody
	if a.CIAA.regs[CIARegDDRB] != 0xA5 || a.CIAB.regs[CIARegDDRB] != 0x5A {
		t.Error("write to an undriven byte lane reached a CIA")
	}
	a.CPU.M.Write(0xBFC300, []byte{0x12, 0x34})
	if a.CIAB.regs[CIARegDDRB] != 0x12 || a.CIAA.regs[CIARegDDRB] != 0x34 {
		t.Errorf("word write at $BFC300: CIA-B $%02X, CIA-A $%02X; want $12, $34",
			a.CIAB.regs[CIARegDDRB], a.CIAA.regs[CIARegDDRB])
	}
}

// A CIA's /IRQ is a level: while an enabled interrupt is pending (ICR not
// read), software clearing INTREQ PORTS/EXTER does not stick.
func TestCIAIRQIsLevel(t *testing.T) {
	a := createTestAmiga(t)
	intreq := func() uint16 { return a.Custom.ReadReg16(0x09C) }
	ciaWrite(a.CIAA, CIARegICR, 0x81) // enable timer A
	ciaWrite(a.CIAA, CIARegTALO, 1)
	ciaWrite(a.CIAA, CIARegTAHI, 0)
	ciaWrite(a.CIAA, CIARegCRA, 0x09) // one-shot
	for i := 0; i < 4; i++ {
		a.CIAA.Tick()
	}
	if intreq()&0x08 == 0 {
		t.Fatal("timer A underflow did not raise PORTS")
	}
	a.Custom.Write(0x09C, []byte{0x00, 0x08}) // clear PORTS before reading ICR
	if intreq()&0x08 == 0 {
		t.Error("PORTS cleared while CIA-A still asserts /IRQ")
	}
	b := []byte{0}
	a.CIAA.Read(CIARegICR<<8, b) // acknowledge
	a.Custom.Write(0x09C, []byte{0x00, 0x08})
	if intreq()&0x08 != 0 {
		t.Error("PORTS still set after the ICR was read and INTREQ cleared")
	}
}
