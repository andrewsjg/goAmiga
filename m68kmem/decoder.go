package m68kmem

import "unsafe"

// A Decoder provides native type decoding for a Memory interface.
type Decoder struct {
	M Memory

	// OnOddAccess, when set, is called before any word or long access at an
	// odd address (the 68000 raises an address error for those). The CPU
	// installs it while executing an instruction.
	OnOddAccess func(addr int, write bool)

	// Fetch is set while the CPU reads instruction words, so memory can
	// tell code fetches from data (to model an instruction cache).
	Fetch bool

	// OnWrite, when set, is called before every write with its address and
	// length (the CPU's prefetch emulation keeps what it overwrites).
	OnWrite func(addr, n int)

	// Accesses counts bus cycles (one per byte or word, two per long)
	// completed through this Decoder; the CPU uses it to time exceptions.
	Accesses int

	buf [4]byte
}

func (m *Decoder) checkOdd(addr int, size int, write bool) {
	if size > 1 && addr&1 != 0 && m.OnOddAccess != nil {
		m.OnOddAccess(addr, write)
	}
	m.Accesses += (size + 1) / 2
}

// NewDecoder returns a new Decoder for the given Memory interface.
func NewDecoder(m Memory) *Decoder {
	return &Decoder{M: m}
}

// Write writes to the underlying Memory interface.
func (m *Decoder) Write(addr int, p []byte) (n int, err error) {
	m.checkOdd(addr, len(p), true)
	if m.OnWrite != nil {
		m.OnWrite(addr, len(p))
	}
	return m.M.Write(addr, p)
}

// Read reads from the underlying Memory interface.
func (m *Decoder) Read(addr int, p []byte) (n int, err error) {
	m.checkOdd(addr, len(p), false)
	return m.M.Read(addr, p)
}

// Reset resets the underlying Memory interface.
func (m *Decoder) Reset() (err error) {
	return m.M.Reset()
}

// Byte reads a single byte from the underlying Memory interface.
func (m *Decoder) Byte(addr int) (b byte, err error) {
	m.Accesses++
	_, err = m.M.Read(addr, m.buf[:1])
	b = m.buf[0]
	return
}

// Word reads a single 16-bit unsigned word from the underlying Memory
// interface.
func (m *Decoder) Word(addr int) (n uint16, err error) {
	m.checkOdd(addr, 2, false)
	_, err = m.M.Read(addr, m.buf[:2])
	n = uint16(m.buf[0])<<8 | uint16(m.buf[1])
	return
}

// Sword reads a single 16-bit signed word from the underlying Memory interface.
func (m *Decoder) Sword(addr int) (n int16, err error) {
	m.checkOdd(addr, 2, false)
	_, err = m.M.Read(addr, m.buf[:2])
	u := uint16(m.buf[0])<<8 | uint16(m.buf[1])
	n = *(*int16)(unsafe.Pointer(&u))
	return
}

// Long reads a single 32-bit unsigned long from the underlying Memory
// interface.
func (m *Decoder) Long(addr int) (n uint32, err error) {
	m.checkOdd(addr, 4, false)
	_, err = m.M.Read(addr, m.buf[:4])
	n = uint32(m.buf[0])<<24 | uint32(m.buf[1])<<16 | uint32(m.buf[2])<<8 | uint32(m.buf[3])
	return
}

// Slong reads a single 32-bit signed long from the underlying Memory interface.
func (m *Decoder) Slong(addr int) (n int32, err error) {
	m.checkOdd(addr, 4, false)
	_, err = m.M.Read(addr, m.buf[:4])
	u := uint32(m.buf[0])<<24 | uint32(m.buf[1])<<16 | uint32(m.buf[2])<<8 | uint32(m.buf[3])
	n = *(*int32)(unsafe.Pointer(&u))
	return
}
