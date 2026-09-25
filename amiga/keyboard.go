package amiga

// Keyboard emulates the Amiga keyboard controller protocol.
// Keys are sent via CIA-A SDR with a handshake protocol.
type Keyboard struct {
	amiga            *Amiga
	queue            []byte // pending encoded key bytes
	handshakePending bool
	started          bool // delay sending until Kickstart is ready
	frameCount       int
}

// StartupDelay is the number of VBLANK frames to wait before sending
// power-up key codes. Kickstart needs time to set up CIA-A interrupt handler.
const StartupDelay = 50

// NewKeyboard creates a new Keyboard attached to the given Amiga.
func NewKeyboard(a *Amiga) *Keyboard {
	return &Keyboard{amiga: a}
}

// Reset queues the power-up key sequence ($FD=initPowerUp, $FE=termPowerUp).
func (k *Keyboard) Reset() {
	k.queue = nil
	k.handshakePending = false
	k.started = false
	k.frameCount = 0
	// Queue power-up codes (will be sent after startup delay)
	k.queue = append(k.queue, encodeKey(0xFD), encodeKey(0xFE))
}

// EnqueueKey adds a key event to the queue.
// rawCode is the Amiga raw key code (0-127).
// pressed=true for key down, false for key up.
func (k *Keyboard) EnqueueKey(rawCode byte, pressed bool) {
	code := rawCode
	if !pressed {
		code |= 0x80 // bit 7 = release flag
	}
	k.queue = append(k.queue, encodeKey(code))
}

// encodeKey encodes a raw key code into the SDR format.
// The keyboard controller rotates the code left by 1 bit and inverts.
// SDR = ^((code << 1) | (code >> 7))
func encodeKey(code byte) byte {
	return ^((code << 1) | (code >> 7))
}

// Tick is called once per frame at VBLANK.
// It sends the next queued key if no handshake is pending.
func (k *Keyboard) Tick() {
	if !k.started {
		k.frameCount++
		if k.frameCount >= StartupDelay {
			k.started = true
		}
		return
	}

	if k.handshakePending || len(k.queue) == 0 {
		return
	}

	// Send next key via CIA-A SDR + SP interrupt
	sdr := k.queue[0]
	k.queue = k.queue[1:]
	k.handshakePending = true
	k.amiga.CIAA.SetSDR(sdr)
}

// HandshakeComplete is called when CIA-A CRA bit 6 transitions from 1 to 0,
// indicating the CPU has acknowledged the key code.
func (k *Keyboard) HandshakeComplete() {
	k.handshakePending = false
}
