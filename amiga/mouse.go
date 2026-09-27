package amiga

// Mouse emulates the Amiga mouse (directly wired to CIA-A and custom chips).
// Quadrature counters wrap at 8 bits, matching JOY0DAT register format.
type Mouse struct {
	amiga        *Amiga
	CounterX     uint8 // quadrature X counter
	CounterY     uint8 // quadrature Y counter
	LeftButton   bool
	RightButton  bool
	MiddleButton bool
}

// NewMouse creates a new Mouse attached to the given Amiga.
func NewMouse(a *Amiga) *Mouse {
	return &Mouse{amiga: a}
}

// Move adds deltas to the quadrature counters (uint8 wrapping).
func (m *Mouse) Move(dx, dy int) {
	m.CounterX += uint8(dx)
	m.CounterY += uint8(dy)
}

// JOY0DAT returns the value for custom register JOY0DAT ($DFF00A).
// Format: Y7-Y0 in bits 15-8, X7-X0 in bits 7-0.
func (m *Mouse) JOY0DAT() uint16 {
	return uint16(m.CounterY)<<8 | uint16(m.CounterX)
}
