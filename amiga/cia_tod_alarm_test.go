package amiga

import (
	"testing"
)

// The Amiga wires CIA-B's TOD input to the horizontal sync signal, so TOD
// counts scanlines. graphics.library uses that plus the TOD alarm to start
// beam-synchronised blits; without it QBSBlit blits never run.
func TestCIATODAlarmFiresOnMatch(t *testing.T) {
	c := NewCIA("CIA-B", nil)
	_ = c.Reset()

	fired := 0
	c.OnInterrupt = func() { fired++ }
	c.icrMask = 0x04 // enable ALARM (ICR bit 2)

	// Load the counter (CRB bit 7 clear selects the counter).
	c.regs[CIARegCRB] = 0x00
	c.Write(CIARegTODHI<<8, []byte{0x00}) // halts counting
	c.Write(CIARegTODMID<<8, []byte{0x00})
	c.Write(CIARegTODLO<<8, []byte{0x01}) // resumes counting, TOD = 1

	// Arm the alarm at "scanline" 5 (CRB bit 7 set selects the alarm).
	c.regs[CIARegCRB] = 0x80
	c.Write(CIARegTODHI<<8, []byte{0x00})
	c.Write(CIARegTODMID<<8, []byte{0x00})
	c.Write(CIARegTODLO<<8, []byte{0x05})

	if c.todCounter != 1 {
		t.Fatalf("TOD counter = %d, want 1 (alarm writes must not touch the counter)", c.todCounter)
	}
	if c.todAlarm != 5 {
		t.Fatalf("TOD alarm = %d, want 5", c.todAlarm)
	}

	for i := 0; i < 3; i++ { // TOD 1 → 4
		c.TickTOD()
	}
	if fired != 0 {
		t.Fatalf("alarm fired early at TOD=%d", c.todCounter)
	}
	c.TickTOD() // TOD → 5, matches the alarm
	if fired != 1 {
		t.Fatalf("alarm did not fire at TOD=%d (fired=%d)", c.todCounter, fired)
	}
	if c.icrData&0x04 == 0 {
		t.Errorf("ICR ALARM bit not set, icrData=$%02X", c.icrData)
	}
}

func TestCIATODHaltedByTODHIWrite(t *testing.T) {
	c := NewCIA("CIA-B", nil)
	_ = c.Reset()
	c.regs[CIARegCRB] = 0x00

	c.Write(CIARegTODHI<<8, []byte{0x00}) // halt
	c.TickTOD()
	c.TickTOD()
	if c.todCounter != 0 {
		t.Errorf("TOD advanced while halted: %d", c.todCounter)
	}
	c.Write(CIARegTODLO<<8, []byte{0x01}) // resume
	c.TickTOD()
	if c.todCounter != 2 {
		t.Errorf("TOD = %d after resume+tick, want 2", c.todCounter)
	}
}

// CIA-B TOD must advance once per scanline and CIA-A once per frame.
func TestCIATODClockedByBeam(t *testing.T) {
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
	b0, a0 := a.CIAB.todCounter, a.CIAA.todCounter
	if err := a.RunFrame(); err != nil {
		t.Fatal(err)
	}
	bd := int(a.CIAB.todCounter - b0)
	ad := int(a.CIAA.todCounter - a0)
	// One frame is LinesPerFrame scanlines; allow a small margin because
	// RunFrame synchronises on VBLANK rather than an exact line count.
	if bd < LinesPerFrame-4 || bd > LinesPerFrame+4 {
		t.Errorf("CIA-B TOD advanced %d ticks in one frame, want ~%d (horizontal sync)",
			bd, LinesPerFrame)
	}
	if ad != 1 {
		t.Errorf("CIA-A TOD advanced %d ticks in one frame, want 1 (vertical sync)", ad)
	}
}
