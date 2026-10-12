package m68kmem

import (
	"errors"
)

// A Mapper allows a Motorola 68000 processor to communicate with external
// devices using a memory address, mapped into its addressable range.
type Mapper struct {
	mappings *mapping

	// OpenBus models a real bus with nothing driving unmapped addresses:
	// reads of unmapped (or faulting) addresses return zero and writes to
	// them are ignored, instead of returning an access violation. Machines
	// such as the Amiga need this — software routinely probes or strays
	// into unmapped space and the hardware never faults.
	OpenBus bool
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
	// A read may span several devices (and, in OpenBus mode, gaps between
	// them), so walk the range, handing each device the part it covers.
	for n < len(p) {
		i := (addr + n) & int(MaxAddress) // 24-bit bus wraps
		m := mm.find(i)
		if m == nil {
			if !mm.OpenBus {
				return n, accessViolationError(i)
			}
			n += mm.float(p[n:], mm.gap(i))
			continue
		}
		span := min(len(p)-n, m.end-i+1)
		nn, derr := m.m.Read(i-m.start, p[n:n+span])
		if derr != nil || nn <= 0 {
			if !mm.OpenBus {
				if derr == nil {
					derr = accessViolationError(i)
				}
				return n, derr
			}
			nn = mm.float(p[n:n+span], span)
		}
		n += nn
	}
	return n, nil
}

func (mm *Mapper) Write(addr int, p []byte) (n int, err error) {
	addr, err = maskAddr(addr)
	if err != nil {
		return
	}
	for n < len(p) {
		i := (addr + n) & int(MaxAddress) // 24-bit bus wraps
		m := mm.find(i)
		if m == nil {
			if !mm.OpenBus {
				return n, accessViolationError(i)
			}
			n += min(len(p)-n, mm.gap(i)) // unmapped: write is lost
			continue
		}
		span := min(len(p)-n, m.end-i+1)
		nn, derr := m.m.Write(i-m.start, p[n:n+span])
		if derr != nil || nn <= 0 {
			if !mm.OpenBus {
				if derr == nil {
					derr = accessViolationError(i)
				}
				return n, derr
			}
			nn = span // e.g. write to ROM: ignored
		}
		n += nn
	}
	return n, nil
}

// find returns the mapping containing addr, or nil.
func (mm *Mapper) find(addr int) *mapping {
	for m := mm.mappings; m != nil; m = m.next {
		if m.start <= addr && m.end >= addr {
			return m
		}
	}
	return nil
}

// gap returns the number of unmapped bytes from addr up to the next mapping.
func (mm *Mapper) gap(addr int) int {
	for m := mm.mappings; m != nil; m = m.next {
		if m.start > addr {
			return m.start - addr
		}
	}
	return int(MaxAddress) + 1 - addr
}

// float fills up to max bytes of p with the open-bus value and returns the
// number of bytes filled.
func (mm *Mapper) float(p []byte, max int) int {
	if max > len(p) {
		max = len(p)
	}
	for j := 0; j < max; j++ {
		p[j] = 0
	}
	return max
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
