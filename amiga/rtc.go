package amiga

import "time"

// The battery-backed clock: an Oki MSM6242B at $DC0000, as on the A500's
// A501 expansion and the A2000. Kickstart's battclock.resource sets the
// system time from it at boot, and Amiga Unix reads it through /dev/clock.
//
// Register n (4 bits) is at $DC0000 + 4n + 3: seconds, tens of seconds,
// minutes, tens, hours, tens, day, tens, month, tens, year, tens, weekday,
// then control registers D, E and F. The clock runs on the host's local
// time; setting it keeps the difference.
//
// Register F reads 4 (24-hour mode, running), which is how both
// battclock.resource and AMIX recognise an Oki chip.
//
// Software from before 2000 can't take a later year: Amiga Unix's date
// refuses two-digit years below 70. With Y2K set, the clock shows a year
// 28 years earlier (2026 as 1998), which has the same weekdays and leap
// years, so the day, month and time stay right.

const (
	rtcBase = 0xDC0000
	rtcEnd  = 0xDCFFFF
)

type rtc struct {
	offset time.Duration // set time minus host time
	Y2K    bool          // show the year 28 years earlier
	ctrl   [3]byte       // D, E, F
	held   [13]byte      // the time registers while being set
	hold   bool
	dirty  bool
}

// EnableClock fits the battery-backed clock.
func (a *Amiga) EnableClock() {
	if a.RTC == nil {
		a.RTC = &rtc{ctrl: [3]byte{0, 0, 4}}
	}
}

// y2kYears is the shift Y2K makes: the Gregorian calendar repeats
// every 28 years between 1901 and 2099.
const y2kYears = 28

func (r *rtc) now() time.Time {
	t := time.Now().Add(r.offset)
	if r.Y2K && t.Year() >= 2000 {
		t = t.AddDate(-y2kYears, 0, 0)
	}
	return t
}

// regs is the time as the 13 time registers.
func (r *rtc) regs() [13]byte {
	t := r.now()
	y := t.Year() % 100
	return [13]byte{
		byte(t.Second() % 10), byte(t.Second() / 10),
		byte(t.Minute() % 10), byte(t.Minute() / 10),
		byte(t.Hour() % 10), byte(t.Hour() / 10),
		byte(t.Day() % 10), byte(t.Day() / 10),
		byte(int(t.Month()) % 10), byte(int(t.Month()) / 10),
		byte(y % 10), byte(y / 10),
		byte(t.Weekday()),
	}
}

func (r *rtc) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = 0xFF
		off := (addr + i) & 0xFFFF
		if off&3 != 3 {
			continue
		}
		n := off >> 2 & 0xF
		switch {
		case n >= 13:
			p[i] = 0xF0 | r.ctrl[n-13]
		case r.hold || r.dirty:
			p[i] = 0xF0 | r.held[n]
		default:
			p[i] = 0xF0 | r.regs()[n]
		}
	}
	return len(p), nil
}

func (r *rtc) Write(addr int, p []byte) (int, error) {
	for i, v := range p {
		off := (addr + i) & 0xFFFF
		if off&3 != 3 {
			continue
		}
		n := off >> 2 & 0xF
		v &= 0xF
		switch n {
		case 13: // D: HOLD (bit 0) freezes the registers for setting
			if v&1 != 0 && !r.hold {
				r.held = r.regs()
			}
			r.hold = v&1 != 0
			r.ctrl[0] = v & 0x9 // HOLD and 30-second adjust; BUSY never set
			if !r.hold {
				r.commit()
			}
		case 14:
			r.ctrl[1] = v
		case 15: // F: always 24-hour mode
			r.ctrl[2] = v | 4
		default:
			if !r.hold && !r.dirty {
				r.held = r.regs()
			}
			r.held[n] = v
			r.dirty = true
			if !r.hold {
				r.commit()
			}
		}
	}
	return len(p), nil
}

// commit makes the time written the clock's time.
func (r *rtc) commit() {
	if !r.dirty {
		return
	}
	r.dirty = false
	h := r.held
	y := int(h[11])*10 + int(h[10])
	if y < 78 {
		y += 2000
	} else {
		y += 1900
	}
	if r.Y2K && y < 2000 && y >= 2000-y2kYears {
		y += y2kYears
	}
	t := time.Date(y, time.Month(int(h[9])*10+int(h[8])), int(h[7])*10+int(h[6]),
		int(h[5]&3)*10+int(h[4]), int(h[3])*10+int(h[2]), int(h[1])*10+int(h[0]), 0, time.Local)
	r.offset = time.Until(t)
}

func (r *rtc) Reset() error { return nil }
