package amiga

import (
	"testing"
)

func TestKeyEncoding(t *testing.T) {
	// Key code $45 (Escape) pressed: SDR = ^($45 << 1) = ^($8A) = $75
	sdr := encodeKey(0x45)
	if sdr != 0x75 {
		t.Errorf("expected SDR=$75 for key $45, got $%02X", sdr)
	}

	// Key code $45 released: raw = $45 | $80 = $C5
	// SDR = ^(($C5 << 1) | ($C5 >> 7)) = ^($8A | $01) = ^($8B) = $74
	sdr = encodeKey(0xC5)
	if sdr != 0x74 {
		t.Errorf("expected SDR=$74 for key $C5 (release), got $%02X", sdr)
	}
}

func TestKeyboardPowerUpSequence(t *testing.T) {
	a := createTestAmiga(t)
	kb := a.Keyboard

	// After Reset(), queue should have 2 power-up codes
	if len(kb.queue) != 2 {
		t.Fatalf("expected 2 power-up codes in queue, got %d", len(kb.queue))
	}

	// First should be $FD encoded, second $FE encoded
	expectedFD := encodeKey(0xFD)
	expectedFE := encodeKey(0xFE)
	if kb.queue[0] != expectedFD {
		t.Errorf("expected first power-up code $%02X, got $%02X", expectedFD, kb.queue[0])
	}
	if kb.queue[1] != expectedFE {
		t.Errorf("expected second power-up code $%02X, got $%02X", expectedFE, kb.queue[1])
	}
}

func TestKeyboardStartupDelay(t *testing.T) {
	a := createTestAmiga(t)
	kb := a.Keyboard

	// Tick should not send keys before StartupDelay frames
	for i := 0; i < StartupDelay-1; i++ {
		kb.Tick()
	}
	if kb.started {
		t.Error("keyboard should not be started before startup delay")
	}
	if len(kb.queue) != 2 {
		t.Errorf("queue should still have 2 items, got %d", len(kb.queue))
	}

	// One more tick reaches StartupDelay but returns early (sets started=true)
	kb.Tick()
	if !kb.started {
		t.Error("keyboard should be started after startup delay")
	}
	// Queue unchanged on the transition tick
	if len(kb.queue) != 2 {
		t.Errorf("queue should still have 2 items on transition tick, got %d", len(kb.queue))
	}

	// Next tick actually sends
	kb.Tick()
	if len(kb.queue) != 1 {
		t.Errorf("expected 1 item in queue after first send, got %d", len(kb.queue))
	}
	if !kb.handshakePending {
		t.Error("handshake should be pending after sending key")
	}
}

func TestKeyboardHandshake(t *testing.T) {
	a := createTestAmiga(t)
	kb := a.Keyboard

	// Skip startup delay
	kb.started = true

	// Tick sends first key
	kb.Tick()
	if !kb.handshakePending {
		t.Error("handshake should be pending")
	}

	// Another tick should NOT send while handshake is pending
	queueLen := len(kb.queue)
	kb.Tick()
	if len(kb.queue) != queueLen {
		t.Error("should not dequeue while handshake pending")
	}

	// Complete handshake
	kb.HandshakeComplete()
	if kb.handshakePending {
		t.Error("handshake should not be pending after HandshakeComplete")
	}

	// Now tick should send next key
	kb.Tick()
	if len(kb.queue) != queueLen-1 {
		t.Errorf("expected queue length %d, got %d", queueLen-1, len(kb.queue))
	}
}

func TestKeyboardEnqueueKey(t *testing.T) {
	a := createTestAmiga(t)
	kb := a.Keyboard
	kb.queue = nil // clear power-up codes

	// Enqueue press
	kb.EnqueueKey(0x45, true)
	if len(kb.queue) != 1 {
		t.Fatalf("expected 1 item in queue, got %d", len(kb.queue))
	}
	expected := encodeKey(0x45)
	if kb.queue[0] != expected {
		t.Errorf("expected $%02X, got $%02X", expected, kb.queue[0])
	}

	// Enqueue release
	kb.EnqueueKey(0x45, false)
	if len(kb.queue) != 2 {
		t.Fatalf("expected 2 items in queue, got %d", len(kb.queue))
	}
	expected = encodeKey(0x45 | 0x80)
	if kb.queue[1] != expected {
		t.Errorf("expected $%02X, got $%02X", expected, kb.queue[1])
	}
}

func TestKeyboardCIAHandshakeWiring(t *testing.T) {
	a := createTestAmiga(t)
	kb := a.Keyboard
	kb.started = true

	// Send a key
	kb.Tick()
	if !kb.handshakePending {
		t.Fatal("handshake should be pending")
	}

	// Simulate Kickstart handshake via CIA-A CRA write:
	// First set bit 6 high, then clear it
	cra := []byte{0x40} // bit 6 set
	a.CIAA.Write(0x0E00, cra)
	if !kb.handshakePending {
		t.Error("handshake should still be pending (bit 6 went 0→1)")
	}

	cra[0] = 0x00 // bit 6 clear
	a.CIAA.Write(0x0E00, cra)
	if kb.handshakePending {
		t.Error("handshake should be cleared (bit 6 went 1→0)")
	}
}
