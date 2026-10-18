package amiga

import (
	"fmt"

	m68k "github.com/andrewsjg/goamiga/m68k"
	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// Zorro III, the A3000's and A4000's 32-bit expansion bus. Boards join
// the same Autoconfig chain as Zorro II ones, but an unconfigured Zorro
// III board answers in its own configuration space at $FF000000, which
// expansion.library reads first:
//
//   - Register k's high nibble is in bits 7-4 of $FF000000 + 4k and its
//     low nibble in bits 7-4 of $FF000100 + 4k. As on Zorro II,
//     everything but er_Type reads inverted.
//   - The base address is written as a word to $44 (A31-A16), after a
//     byte to $48; the word configures the board. $4C shuts it up.
//   - er_Type is $80 (Zorro III) with the memory-list and size bits. With
//     er_Flags' extended bit (5) the size codes run 0 = 16 MB to 6 =
//     1 GB; bit 4 marks a Zorro III board.
//
// expansion.library puts Zorro III boards from $40000000 up.

const (
	z3ConfigBase = 0xFF000000
	z3ConfigEnd  = 0xFF00FFFF
)

// z3Board is a board that answers in Zorro III configuration space.
type z3Board interface {
	zorro3() bool
}

// zorro3RAM is a Zorro III fast RAM board.
type zorro3RAM struct {
	autoconfigState
	mem *ram
}

func (z *zorro3RAM) zorro3() bool { return true }

func (z *zorro3RAM) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro III, link into the memory list, size
		return 0x80 | 0x20 | z3SizeCode(len(z.mem.b))
	case 0x04: // er_Product
		return 0x03
	case 0x08: // er_Flags: extended sizes, Zorro III
		return 0x30
	case 0x10, 0x14: // er_Manufacturer: $07DB (goAmiga, not assigned)
		if reg == 0x10 {
			return 0x07
		}
		return 0xDB
	}
	return 0x00
}

func (z *zorro3RAM) space() m68kmem.Memory { return z.mem }
func (z *zorro3RAM) size() int             { return len(z.mem.b) }
func (z *zorro3RAM) configured(uint32)     {}

// z3SizeCode is the extended size code for a Zorro III board of n bytes
// (16 MB to 1 GB).
func z3SizeCode(n int) byte {
	code := byte(0)
	for s := 16 << 20; s < n; s <<= 1 {
		code++
	}
	return code
}

// EnableZorro3RAM fits a Zorro III fast RAM board of mb megabytes (0 for
// none, or a power of two from 16 to 1024). It needs a 32-bit machine:
// the A3000 or A4000 motherboard (EnableMotherboard first). Call it
// before Reset.
func (a *Amiga) EnableZorro3RAM(mb int) error {
	if mb == 0 {
		a.Z3RAM = nil
		a.wireMemoryMap(a.overlay)
		return nil
	}
	if mb < 16 || mb > 1024 || mb&(mb-1) != 0 {
		return fmt.Errorf("Zorro III RAM must be a power of two from 16 to 1024 MB, not %d", mb)
	}
	if a.Motherboard == nil || a.CPU.Model < m68k.Model68030 {
		return fmt.Errorf("Zorro III needs the A3000 or A4000 (a 68030 or 68040 and its motherboard)")
	}
	a.Z3RAM = &zorro3RAM{mem: &ram{b: make([]byte, mb<<20)}}
	a.wireMemoryMap(a.overlay)
	return nil
}

// autoconfig3Space is $FF000000-$FF00FFFF: the first unconfigured
// board's registers, when it is a Zorro III board.
type autoconfig3Space struct {
	amiga *Amiga
	board zorroBoard
}

func (s *autoconfig3Space) Read(addr int, p []byte) (int, error) {
	for i := range p {
		off := (addr + i) & 0xFFFF
		p[i] = 0
		low := off&0x100 != 0
		off &^= 0x100
		if off&3 != 0 || off >= 0x40 {
			continue
		}
		b := s.board.configByte(off)
		n := b >> 4
		if low {
			n = b & 0xF
		}
		if off != 0 {
			n = ^n & 0xF
		}
		p[i] = n << 4
	}
	return len(p), nil
}

func (s *autoconfig3Space) Write(addr int, p []byte) (int, error) {
	st := s.board.autoconfig()
	switch off := addr & 0xFFFF; {
	case off == 0x44 && len(p) >= 2:
		st.base = uint32(p[0])<<24 | uint32(p[1])<<16
		s.board.configured(st.base)
		s.amiga.wireMemoryMap(s.amiga.overlay)
	case off == 0x4C:
		st.shut = true
		s.amiga.wireMemoryMap(s.amiga.overlay)
	}
	return len(p), nil
}

func (s *autoconfig3Space) Reset() error { return nil }
