package m68kmem

import (
	"errors"
)

// A Mapper allows a Motorola 68000 processor to communicate with external
// devices using a memory address, mapped into its addressable range.
type Mapper struct {
	mappings *mapping
	last     *mapping // the mapping find found last: most accesses fall in it again

	// OpenBus models a real bus with nothing driving unmapped addresses:
	// reads of unmapped (or faulting) addresses return zero and writes to
	// them are ignored, instead of returning an access violation. Machines
	// such as the Amiga need this — software routinely probes or strays
	// into unmapped space and the hardware never faults.
	OpenBus bool

	// Wide gives the Mapper a 32-bit address bus (a 68020 or later with
	// all its address lines, as in the A3000 and A4000) instead of the
	// 68000's 24 bits. Without it addresses wrap at 16 MB.
	Wide bool

	// OnOpenBus, when set, is called for each access to unmapped
	// addresses in OpenBus mode (a diagnostic: what software probes).
	OnOpenBus func(addr, n int, write bool)

	// Fault, when set, decides whether an access to unmapped addresses in
	// OpenBus mode ends in a bus error (a timeout, as the A3000's and
	// A4000's Gary can raise) rather than floating. The Mapper then
	// returns an error IsBusError recognises.
	Fault func(addr int, write bool) bool
}

// mask is the bus's address mask.
func (mm *Mapper) mask() uint32 {
	if mm.Wide {
		return 0xFFFFFFFF
	}
	return MaxAddress
}

// maskAddr reduces addr to the bus's width.
func (mm *Mapper) maskAddr(addr int) (int, error) {
	if addr < 0 {
		return 0, AccessViolationError(uint32(addr))
	}
	return int(uint32(addr) & mm.mask()), nil
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
	if start < 0 || uint32(start) < MinAddress || uint64(start) > uint64(mm.mask()) {
		return errors.New("start address out of range")
	}
	if end < start || uint32(end) < MinAddress || uint64(end) > uint64(mm.mask()) {
		return errors.New("end address out of range")
	}
	n := &mapping{
		m:     m,
		start: start,
		end:   end,
	}
	mm.mappings = insertMapping(mm.mappings, n)
	mm.last = nil
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
	addr, err = mm.maskAddr(addr)
	if err != nil {
		return
	}
	// A read may span several devices (and, in OpenBus mode, gaps between
	// them), so walk the range, handing each device the part it covers.
	for n < len(p) {
		i := (addr + n) & int(mm.mask()) // the bus wraps
		m := mm.find(i)
		if m == nil {
			if !mm.OpenBus {
				return n, accessViolationError(i)
			}
			if mm.OnOpenBus != nil {
				mm.OnOpenBus(i, min(len(p)-n, mm.gap(i)), false)
			}
			if mm.Fault != nil && mm.Fault(i, false) {
				return n, busError(i)
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
	addr, err = mm.maskAddr(addr)
	if err != nil {
		return
	}
	for n < len(p) {
		i := (addr + n) & int(mm.mask()) // the bus wraps
		m := mm.find(i)
		if m == nil {
			if !mm.OpenBus {
				return n, accessViolationError(i)
			}
			if mm.OnOpenBus != nil {
				mm.OnOpenBus(i, min(len(p)-n, mm.gap(i)), true)
			}
			if mm.Fault != nil && mm.Fault(i, true) {
				return n, busError(i)
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
	if m := mm.last; m != nil && m.start <= addr && m.end >= addr {
		return m
	}
	for m := mm.mappings; m != nil; m = m.next {
		if m.start <= addr && m.end >= addr {
			mm.last = m
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
	return int(mm.mask()) + 1 - addr
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
