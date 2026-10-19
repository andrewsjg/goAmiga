package amiga

import "testing"

// The pot counters: POTGO START clears them and they count a line at a
// time to where each pin's capacitor charges: an analogue joystick's
// position, 0 for the mouse's pins, 255 when nothing is connected or a
// mouse button grounds a pin, and nothing for a pin POTGO makes an output.
func TestPotCounters(t *testing.T) {
	a := createTestAmiga(t)
	a.Joystick.Analog = true
	a.Joystick.AnalogX, a.Joystick.AnalogY = -1, 0.5
	read := func(reg int) uint16 {
		var b [2]byte
		a.Custom.Read(reg, b[:])
		return uint16(b[0])<<8 | uint16(b[1])
	}
	a.Custom.Write(0x034, []byte{0x00, 0x01}) // POTGO START
	if v := read(0x014); v != 0 {
		t.Fatalf("POT1DAT $%04X right after START, want 0", v)
	}
	for i := 0; i < 300; i++ {
		a.nextLine()
	}
	if v := read(0x014); v != 191<<8|0 {
		t.Errorf("POT1DAT $%04X, want Y 191 X 0 (the stick at X -1, Y +0.5)", v)
	}
	if v := read(0x012); v != 0 {
		t.Errorf("POT0DAT $%04X, want 0: the mouse's pins charge at once", v)
	}
	// A pressed right mouse button grounds port 1's Y pin; an output pin
	// doesn't count.
	a.Mouse.RightButton = true
	a.Custom.Write(0x034, []byte{0x02, 0x01}) // OUTLX (port 1 X an output), START
	for i := 0; i < 300; i++ {
		a.nextLine()
	}
	if v := read(0x012); v != 255<<8 {
		t.Errorf("POT0DAT $%04X, want Y 255 (button down), X 0 (an output)", v)
	}
	// Midway the counts are still climbing.
	a.Joystick.AnalogX = 1
	a.Custom.Write(0x034, []byte{0x00, 0x01})
	for i := 0; i < 100; i++ {
		a.nextLine()
	}
	if v := read(0x014); v&0xFF != 100 {
		t.Errorf("POT1DAT X %d after 100 lines, want 100", v&0xFF)
	}
	// An analogue stick's buttons are on the left and right lines.
	a.Joystick.Fire = true
	if v := a.Joystick.JOYDAT(); v != 0x0300 {
		t.Errorf("JOY1DAT $%04X with the first button, want the left line ($0300)", v)
	}
}
