package m68kmem

type rom struct {
	b []byte
}

// NewROM returns Read-Only Memory initialized with the given byte data. This
// memory can be used to load programs and data for a 68000 processor. Memory
// access is protected by an AddressBus.
func NewROM(b []byte) Memory {
	// TODO: check that size is addressable
	return AttachBus(&rom{b})
}

func (m *rom) Read(addr int, p []byte) (n int, err error) {
	if addr < 0 {
		return 0, AccessViolationError(uint32(addr))
	}
	// Open bus: reads at/past the end of ROM (e.g. long at $FFFFFF when
	// freelist next is $FFFFFFFF) return $FF, matching unconnected data
	// lines — not a host access violation that kills the emulator.
	if addr >= len(m.b) {
		for i := range p {
			p[i] = 0xFF
		}
		return len(p), nil
	}
	n = copy(p, m.b[addr:])
	for i := n; i < len(p); i++ {
		p[i] = 0xFF
	}
	return len(p), nil
}

// Write silently ignores writes (ROM is read-only).
func (m *rom) Write(addr int, p []byte) (n int, err error) {
	n = len(p)
	return
}

// Reset does nothing, but is required to implement the Memory interface.
func (m *rom) Reset() (err error) {
	// no-op
	return
}
