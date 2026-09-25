package m68kmem

import (
	"errors"
)

// A Mapper allows a Motorola 68000 processor to communicate with external
// devices using a memory address, mapped into its addressable range.
type Mapper struct {
	mappings *mapping
}

type mapping struct {
	m          Memory
	start, end int
	next       *mapping
}

// NewMapper returns a new MemoryMapper.
func NewMapper() *Mapper {
	return &Mapper{}
}

// Map maps the given Memory addressable device into the given address range.
func (mm *Mapper) Map(m Memory, start, end int) (err error) {
	if uint32(start) < MinAddress || uint32(start) > MaxAddress {
		return errors.New("start address out of range")
	}
	if end < start || uint32(end) < MinAddress || uint32(end) > MaxAddress {
		return errors.New("end address out of range")
	}
	n := &mapping{
		m:     m,
		start: start,
		end:   end,
	}
	mm.mappings = insertMapping(mm.mappings, n)
	return
}

func insertMapping(root, m *mapping) *mapping {
	for n := root; n != nil; n = n.next {
		if n.next == nil || n.next.start >= m.start {
			m.next = n.next
			n.next = m
			if m.next != nil && m.start == m.next.start {
				// deref any mappings with duplicate start address
				m.next = m.next.next
			}
			return root
		}
	}
	return m
}

func (mm *Mapper) Read(addr int, p []byte) (n int, err error) {
	addr, err = maskAddr(addr)
	if err != nil {
		return
	}
	// Block reads for device correctness (custom regs need word access).
	// Unmapped / device errors → open bus (0), never host access violation.
	// (Smashed freelist next=$FFFFFFFF used to kill the UI with
	// "access violation: 0xFFFFFF" at AllocMem.)
	m := mm.mappings
	for i := addr; i < addr+len(p); {
		if m == nil {
			for j := i - addr; j < len(p); j++ {
				p[j] = 0
			}
			return len(p), nil
		}
		found := false
		for m != nil {
			if m.start <= i && m.end >= i {
				var nn int
				nn, err = m.m.Read(i-m.start, p[i-addr:])
				if err != nil {
					for j := i - addr; j < len(p); j++ {
						p[j] = 0
					}
					return len(p), nil
				}
				if nn <= 0 {
					p[i-addr] = 0
					nn = 1
				}
				n += nn
				i += nn
				m = m.next
				found = true
				break
			}
			m = m.next
		}
		if !found {
			p[i-addr] = 0
			n++
			i++
			m = mm.mappings
		}
	}
	return n, nil
}

func (mm *Mapper) Write(addr int, p []byte) (n int, err error) {
	addr, err = maskAddr(addr)
	if err != nil {
		return
	}
	for m := mm.mappings; m != nil; m = m.next {
		if m.start <= addr && m.end >= addr {
			addr -= m.start
			n, err = m.m.Write(addr, p)
			return
		}
	}
	// Unmapped write: ignore (do not fault the host).
	return len(p), nil
}

// Reset calls Reset on all mapped Memory devices. Mappings are preserved.
func (mm *Mapper) Reset() (err error) {
	for m := mm.mappings; m != nil; m = m.next {
		err = m.m.Reset()
		if err != nil {
			return
		}
	}
	return
}
