package amiga

import "github.com/andrewsjg/goamiga/m68k/m68kmem"

// Zorro II Autoconfig. Boards sit in a chain: at boot only the first board
// not yet configured answers at $E80000. expansion.library reads its ID
// nibbles, writes a base address (which configures it and passes /CFGOUT to
// the next board) or tells it to shut up, and moves on to the next.

// zorroBoard is one board in the Autoconfig chain.
type zorroBoard interface {
	autoconfig() *autoconfigState
	// configByte returns the logical Autoconfig register at reg (a
	// multiple of 4 below $40).
	configByte(reg int) byte
	// space is the board's memory once configured, mapped from its base.
	space() m68kmem.Memory
	size() int
	// configured is called when the board gets its base address.
	configured(base uint32)
}

type autoconfigState struct {
	base uint32 // 0 until configured
	shut bool   // told to shut up: never appears
	low  byte   // A19-A16, written to $4A before $48
}

// Configured reports the board's base address, or 0.
func (s *autoconfigState) Configured() uint32 { return s.base }

// unconfigure returns the board to its power-on state (reset).
func (s *autoconfigState) unconfigure() { *s = autoconfigState{} }

func (s *autoconfigState) autoconfig() *autoconfigState { return s }

// zorroBoards lists the boards in chain order.
func (a *Amiga) zorroBoards() []zorroBoard {
	var bs []zorroBoard
	if a.Z3RAM != nil {
		bs = append(bs, a.Z3RAM)
	}
	if a.FastRAM != nil {
		bs = append(bs, a.FastRAM)
	}
	if a.HardDisk != nil {
		bs = append(bs, a.HardDisk)
	}
	if a.SCSI != nil {
		bs = append(bs, a.SCSI)
	}
	if a.Ethernet != nil {
		bs = append(bs, a.Ethernet)
	}
	if a.Net != nil {
		bs = append(bs, a.Net)
	}
	return bs
}

// autoconfigSpace is $E80000-$E8FFFF: the first unconfigured board's
// registers.
type autoconfigSpace struct {
	amiga *Amiga
	board zorroBoard
}

// Read returns the Autoconfig nibbles: each register occupies two words,
// high nibble at reg, low nibble at reg+2, in bits 7-4 of the even byte.
// All registers but er_Type read inverted.
func (s *autoconfigSpace) Read(addr int, p []byte) (int, error) {
	for i := range p {
		off := (addr + i) & 0xFFFF
		p[i] = 0
		if off&1 != 0 || off >= 0x40 {
			continue
		}
		b := s.board.configByte(off &^ 3)
		n := b >> 4
		if off&2 != 0 {
			n = b & 0xF
		}
		if off&^3 != 0 {
			n = ^n & 0xF
		}
		p[i] = n << 4
	}
	return len(p), nil
}

// Write takes the base address (A19-A16 at $4A, then A23-A20 at $48,
// which configures the board) or a shut-up request ($4C).
func (s *autoconfigSpace) Write(addr int, p []byte) (int, error) {
	st := s.board.autoconfig()
	for i, v := range p {
		switch (addr + i) & 0xFFFF {
		case 0x4A:
			st.low = v >> 4
		case 0x48:
			st.base = uint32(v&0xF0|st.low) << 16
			s.board.configured(st.base)
			s.amiga.wireMemoryMap(s.amiga.overlay)
			return len(p), nil
		case 0x4C:
			st.shut = true
			s.amiga.wireMemoryMap(s.amiga.overlay)
			return len(p), nil
		}
	}
	return len(p), nil
}

func (s *autoconfigSpace) Reset() error { return nil }

// mapZorro maps configured boards at their bases and the first
// unconfigured one at $E80000 (Zorro II) or $FF000000 (Zorro III).
func (a *Amiga) mapZorro() {
	answering := false
	for _, b := range a.zorroBoards() {
		st := b.autoconfig()
		switch {
		case st.shut:
		case st.base != 0:
			a.Mapper.Map(b.space(), int(st.base), int(st.base)+b.size()-1)
		case !answering:
			if z, ok := b.(z3Board); ok && z.zorro3() {
				a.Mapper.Map(&autoconfig3Space{a, b}, z3ConfigBase, z3ConfigEnd)
			} else {
				a.Mapper.Map(&autoconfigSpace{a, b}, AutoconfigBase, AutoconfigEnd)
			}
			answering = true
		}
	}
}

// sizeCode is the er_Type size field for a board of n bytes.
func sizeCode(n int) byte {
	switch n {
	case 8 << 20:
		return 0
	case 64 << 10:
		return 1
	case 128 << 10:
		return 2
	case 256 << 10:
		return 3
	case 512 << 10:
		return 4
	case 1 << 20:
		return 5
	case 2 << 20:
		return 6
	default: // 4 MB
		return 7
	}
}
