package m68kmem

type ram struct {
	b []byte
}

// NewRAM returns Random Access Memory initialized to the given size. This
// memory can used to load programs and data for a 68000 processor. Memory
// access is protected by an AddressBus.
func NewRAM(size uint32) Memory {
	// TODO: check size is addressable
	return AttachBus(&ram{
		b: make([]byte, size),
	})
}

func (m *ram) Read(addr int, p []byte) (n int, err error) {
	// Completely out of range: open bus (zeros), never a host access violation.
	// Smashed SP / freelist walks used to kill exception setup mid-frame and
	// leave Guru #00000000.00000000.
	if addr < 0 || addr >= len(m.b) {
		for i := range p {
			p[i] = 0
		}
		return len(p), nil
	}
	// Partial at end of RAM: return only bytes present so Mapper can continue
	// into the next mapped device for the remainder.
	n = copy(p, m.b[addr:])
	return n, nil
}

func (m *ram) Write(addr int, p []byte) (n int, err error) {
	// Completely out of range: ignore (same as unmapped).
	if addr < 0 || addr >= len(m.b) {
		return len(p), nil
	}
	// Partial at end: write what fits, report full length so exception stack
	// pushes never abort RaiseException mid-way with ShortWrite.
	copy(m.b[addr:], p)
	return len(p), nil
}

// Reset resets all memory pages to zero.
func (m *ram) Reset() (err error) {
	for i := 0; i < len(m.b); i++ {
		m.b[i] = 0
	}
	return
}
