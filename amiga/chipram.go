package amiga

import "github.com/andrewsjg/goamiga/m68k/m68kmem"

// chipRAM is Amiga chip memory. Agnus only drives the address lines needed
// for the fitted RAM, so any address wraps modulo the RAM size — a DMA
// pointer that runs off the end of 512KB lands back at $000000 rather than
// faulting. The CPU sees the same RAM through the Mapper at $000000.
type chipRAM struct {
	b []byte
}

func newChipRAM(size int) m68kmem.Memory {
	return &chipRAM{b: make([]byte, size)}
}

func (m *chipRAM) Read(addr int, p []byte) (int, error) {
	mask := len(m.b) - 1
	for i := range p {
		p[i] = m.b[(addr+i)&mask]
	}
	return len(p), nil
}

func (m *chipRAM) Write(addr int, p []byte) (int, error) {
	mask := len(m.b) - 1
	for i, v := range p {
		m.b[(addr+i)&mask] = v
	}
	return len(p), nil
}

func (m *chipRAM) Reset() error {
	clear(m.b)
	return nil
}
