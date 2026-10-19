package amiga

// Paula's proportional (pot) counters: POT0DAT ($DFF012) for game port 1
// and POT1DAT ($DFF014) for port 2, each with an 8-bit count for the
// port's X pin (low byte) and Y pin (high byte). Writing POTGO with START
// (bit 0) dumps the pins' capacitors and clears the counters; each then
// counts once a line until its capacitor has charged past the threshold.
// Analogue joysticks and paddles charge it through their potentiometer,
// so the count is the stick's position.
//
// What each pin's count settles at (potTarget):
//   - a pin that POTGO makes an output (OUTxx) holds its capacitor: no
//     count;
//   - the mouse in port 1: its pull-up charges the pin at once (0), but a
//     pressed right or middle button grounds it (255);
//   - an analogue joystick in port 2 (Joystick.Analog): its axes, 0 to
//     255;
//   - anything else is unconnected and counts to 255, where the counter
//     stops (the real one wraps; 255 reads as "far").
type potCounters struct {
	count   [4]uint8 // X0, Y0, X1, Y1
	running bool
}

// potStart handles POTGO's START bit.
func (a *Amiga) potStart() {
	a.pots.count = [4]uint8{}
	a.pots.running = true
}

// potLine counts the pot counters for one line.
func (a *Amiga) potLine() {
	p := &a.pots
	if !p.running {
		return
	}
	done := true
	for i := range p.count {
		if t := a.potTarget(i); int(p.count[i]) < t {
			p.count[i]++
			done = false
		}
	}
	p.running = !done
}

// potPins are POTGO's OUTxx bits for X0, Y0, X1, Y1 (LX, LY, RX, RY).
var potPins = [4]uint16{1 << 9, 1 << 11, 1 << 13, 1 << 15}

// potTarget is the count pin i (X0, Y0, X1, Y1) settles at.
func (a *Amiga) potTarget(i int) int {
	if a.Custom.ReadReg16(0x034)&potPins[i] != 0 {
		return 0 // an output: the capacitor is held
	}
	switch i {
	case 0, 1:
		if m := a.Mouse; m != nil {
			if i == 0 && m.MiddleButton || i == 1 && m.RightButton {
				return 255
			}
			return 0
		}
	case 2, 3:
		if j := a.Joystick; j != nil && j.Analog {
			v := j.AnalogX
			if i == 3 {
				v = j.AnalogY
			}
			return int((min(max(v, -1), 1) + 1) / 2 * 255)
		}
	}
	return 255
}

// potDat is POT0DAT (port 0) or POT1DAT (port 1): Y count high, X low.
func (a *Amiga) potDat(port int) uint16 {
	return uint16(a.pots.count[2*port+1])<<8 | uint16(a.pots.count[2*port])
}
