package amiga

import (
	"encoding/binary"
	"testing"
)

func TestMouseCounterWrapping(t *testing.T) {
	a := createTestAmiga(t)
	m := a.Mouse

	// Move right and down
	m.Move(100, 50)
	if m.CounterX != 100 || m.CounterY != 50 {
		t.Errorf("expected counters (100,50), got (%d,%d)", m.CounterX, m.CounterY)
	}

	// Wrap around (uint8)
	m.Move(200, 0)
	expected := uint8(44) // (100+200) mod 256 = 44
	if m.CounterX != expected {
		t.Errorf("expected wrapped X=%d, got %d", expected, m.CounterX)
	}
}

func TestMouseJOY0DAT(t *testing.T) {
	a := createTestAmiga(t)
	m := a.Mouse

	m.Move(0x42, 0xAB)
	val := m.JOY0DAT()
	if val != 0xAB42 {
		t.Errorf("expected JOY0DAT=$AB42, got $%04X", val)
	}

	// Read through custom chip register
	buf := make([]byte, 2)
	a.Custom.Read(0x00A, buf) // JOY0DAT at offset $00A
	got := binary.BigEndian.Uint16(buf)
	if got != 0xAB42 {
		t.Errorf("expected custom read JOY0DAT=$AB42, got $%04X", got)
	}
}

func TestMouseButtonPRA(t *testing.T) {
	a := createTestAmiga(t)

	// Even with PRA latch bit 6 clear (hardware default), released left
	// button must read high (active-low open input).
	a.CIAA.Write(0x0000, []byte{0x03}) // typical latch: OVL+LED only

	buf := make([]byte, 1)
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x40 == 0 {
		t.Errorf("PRA bit 6 should be high when left button released, got $%02X", buf[0])
	}
	if buf[0]&0x80 == 0 {
		t.Errorf("PRA bit 7 (/FIR1) should be high when idle, got $%02X", buf[0])
	}

	a.Mouse.LeftButton = true
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x40 != 0 {
		t.Errorf("PRA bit 6 should be low when left button pressed, got $%02X", buf[0])
	}

	a.Mouse.LeftButton = false
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x40 == 0 {
		t.Errorf("PRA bit 6 should return high after release, got $%02X", buf[0])
	}
}

func TestMouseRightButtonPOTGOR(t *testing.T) {
	a := createTestAmiga(t)

	// Right button not pressed: all four pot pins read high
	if val := readCustom16(a, 0x016); val != 0x5500 {
		t.Errorf("expected POTGOR=$5500, got $%04X", val)
	}

	// Press right button: DATLY (bit 10) goes low
	a.Mouse.RightButton = true
	if val := readCustom16(a, 0x016); val != 0x5100 {
		t.Errorf("expected POTGOR=$5100, got $%04X", val)
	}
}
