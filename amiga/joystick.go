package amiga

// Joystick emulates a digital joystick in game port 2 (the port games read;
// the mouse stays in port 1).
//
// The switches drive the port's quadrature inputs directly, so JOY1DAT
// reads a static pattern: right sets X1 (bit 1), left sets Y1 (bit 9), and
// down and up appear as X1 XOR X0 and Y1 XOR Y0. The fire button pulls
// /FIR1 (CIA-A PRA bit 7) low; a second button pulls the port's POTY pin
// low, read back as POTGOR DATRY (bit 14).
type Joystick struct {
	Up, Down, Left, Right bool
	Fire, Fire2           bool
}

// JOYDAT returns the value of the port's JOYxDAT register.
func (j *Joystick) JOYDAT() uint16 {
	var v uint16
	if j.Right {
		v |= 1 << 1
	}
	if j.Left {
		v |= 1 << 9
	}
	if j.Right != j.Down {
		v |= 1 << 0
	}
	if j.Left != j.Up {
		v |= 1 << 8
	}
	return v
}

// potgor computes POTGOR ($DFF016) from POTGO and the buttons on the pot
// pins. Each of the four pins (RY, RX, LY, LX) is an input pulled high
// unless its OUTxx bit in POTGO makes it an output driving DATxx. A pressed
// button shorts the pin to ground. Only the four data bits read back.
func (a *Amiga) potgor() uint16 {
	potgo := a.Custom.ReadReg16(0x034)
	v := uint16(0x5500)                        // inputs float high
	v &^= (potgo >> 1) & 0x5500                // output pins ...
	v |= (potgo >> 1) & potgo & 0x5500         // ... read their data bit
	if a.Mouse != nil && a.Mouse.RightButton { // port 1 POTY
		v &^= 1 << 10
	}
	if a.Mouse != nil && a.Mouse.MiddleButton { // port 1 POTX
		v &^= 1 << 8
	}
	if a.Joystick != nil && a.Joystick.Fire2 { // port 2 POTY
		v &^= 1 << 14
	}
	return v
}

// joyTest handles a JOYTEST ($DFF036) write: it loads bits 7-2 of every
// counter, X from bits 7-2 and Y from bits 15-10, in both ports. Only the
// mouse has counters here.
func (a *Amiga) joyTest(val uint16) {
	if m := a.Mouse; m != nil {
		m.CounterX = m.CounterX&3 | byte(val)&0xFC
		m.CounterY = m.CounterY&3 | byte(val>>8)&0xFC
	}
}
