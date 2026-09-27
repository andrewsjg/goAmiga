package amiga

import "testing"

func readCustom16(a *Amiga, offset int) uint16 {
	buf := make([]byte, 2)
	a.Custom.Read(offset, buf)
	return uint16(buf[0])<<8 | uint16(buf[1])
}

func writeCustom16(a *Amiga, offset int, val uint16) {
	a.Custom.Write(offset, []byte{byte(val >> 8), byte(val)})
}

// JOY1DAT encodes the switches as the HRM describes: right = X1 (bit 1),
// left = Y1 (bit 9), down = X1^X0, up = Y1^Y0.
func TestJoystickDirections(t *testing.T) {
	a := createTestAmiga(t)
	j := a.Joystick
	cases := []struct {
		name                  string
		up, down, left, right bool
		want                  uint16
	}{
		{"centre", false, false, false, false, 0x0000},
		{"right", false, false, false, true, 0x0003},
		{"left", false, false, true, false, 0x0300},
		{"down", false, true, false, false, 0x0001},
		{"up", true, false, false, false, 0x0100},
		{"down-right", false, true, false, true, 0x0002},
		{"up-left", true, false, true, false, 0x0200},
		{"up-right", true, false, false, true, 0x0103},
		{"down-left", false, true, true, false, 0x0301},
	}
	for _, c := range cases {
		*j = Joystick{Up: c.up, Down: c.down, Left: c.left, Right: c.right}
		if got := readCustom16(a, 0x00C); got != c.want {
			t.Errorf("%s: JOY1DAT=$%04X want $%04X", c.name, got, c.want)
		}
	}
	// Port 1 (the mouse) is unaffected.
	if got := readCustom16(a, 0x00A); got != 0 {
		t.Errorf("JOY0DAT=$%04X want 0", got)
	}
}

func TestJoystickFire(t *testing.T) {
	a := createTestAmiga(t)
	a.CIAA.Write(0x0000, []byte{0x03})
	buf := make([]byte, 1)

	a.Joystick.Fire = true
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x80 != 0 {
		t.Errorf("PRA bit 7 (/FIR1) should be low with fire pressed, got $%02X", buf[0])
	}
	if buf[0]&0x40 == 0 {
		t.Errorf("PRA bit 6 (/FIR0) should stay high, got $%02X", buf[0])
	}
	a.Joystick.Fire = false
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x80 == 0 {
		t.Errorf("PRA bit 7 should be high after release, got $%02X", buf[0])
	}

	// The second button pulls port 2's POTY pin (DATRY) low.
	a.Joystick.Fire2 = true
	if got := readCustom16(a, 0x016); got != 0x1500 {
		t.Errorf("POTGOR=$%04X want $1500 with fire 2 pressed", got)
	}
}

// Pins set as outputs by POTGO read back their data bit.
func TestPOTGOOutputs(t *testing.T) {
	a := createTestAmiga(t)
	// OUTRY with DATRY=0, OUTLX with DATLX=1.
	writeCustom16(a, 0x034, 0x8000|0x0200|0x0100)
	if got := readCustom16(a, 0x016); got != 0x1500 {
		t.Errorf("POTGOR=$%04X want $1500", got)
	}
	writeCustom16(a, 0x034, 0xC000)
	if got := readCustom16(a, 0x016); got != 0x5500 {
		t.Errorf("POTGOR=$%04X want $5500", got)
	}
}

func TestJOYTESTLoadsMouseCounters(t *testing.T) {
	a := createTestAmiga(t)
	a.Mouse.CounterX, a.Mouse.CounterY = 0x01, 0x02
	writeCustom16(a, 0x036, 0xA4F8)
	if got := readCustom16(a, 0x00A); got != 0xA6F9 {
		t.Errorf("JOY0DAT=$%04X want $A6F9", got)
	}
}
