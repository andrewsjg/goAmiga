package amiga

import (
	"testing"
	"time"
)

func rtcReg(a *Amiga, n int) int { return int(a.peek8(uint32(rtcBase+4*n+3)) & 0xF) }

// The clock reads the host's time; setting it under HOLD moves it.
func TestClock(t *testing.T) {
	a := createTestAmiga(t)
	a.EnableClock()
	a.wireMemoryMap(false)
	if rtcReg(a, 15) != 4 {
		t.Fatalf("register F = %d, want 4 (24-hour mode: an Oki MSM6242)", rtcReg(a, 15))
	}
	now := time.Now()
	if m := rtcReg(a, 9)*10 + rtcReg(a, 8); m != int(now.Month()) {
		t.Errorf("month %d, want %d", m, now.Month())
	}
	// Set 1999-12-31 23:59:50.
	a.poke8(rtcBase+4*13+3, 1) // HOLD
	for n, v := range []byte{0, 5, 9, 5, 3, 2, 1, 3, 2, 1, 9, 9} {
		a.poke8(uint32(rtcBase+4*n+3), v)
	}
	a.poke8(rtcBase+4*13+3, 0)
	if y := rtcReg(a, 11)*10 + rtcReg(a, 10); y != 99 {
		t.Errorf("year %d after setting 99", y)
	}
	if h := rtcReg(a, 5)*10 + rtcReg(a, 4); h != 23 {
		t.Errorf("hour %d after setting 23", h)
	}
}

// With Y2K the clock shows a year 28 years back, with the same weekday.
func TestClockY2K(t *testing.T) {
	a := createTestAmiga(t)
	a.EnableClock()
	a.RTC.Y2K = true
	a.wireMemoryMap(false)
	now := time.Now()
	if y := rtcReg(a, 11)*10 + rtcReg(a, 10); y != (now.Year()-28)%100 {
		t.Errorf("year %02d, want %02d", y, (now.Year()-28)%100)
	}
	if wd := rtcReg(a, 12); wd != int(now.Weekday()) {
		t.Errorf("weekday %d, want %d (the same)", wd, now.Weekday())
	}
	// Setting 1998 sets 2026.
	a.poke8(rtcBase+4*13+3, 1)
	a.poke8(rtcBase+4*10+3, 8)
	a.poke8(rtcBase+4*11+3, 9)
	a.poke8(rtcBase+4*13+3, 0)
	if y := a.RTC.now().AddDate(28, 0, 0).Year(); y != 2026 {
		t.Errorf("setting 98 under Y2K gave %d, want 2026", y)
	}
}
