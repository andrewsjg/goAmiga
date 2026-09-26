package amiga

import "fmt"

// Expansion memory: slow RAM and a Zorro II fast RAM board.
//
// Slow RAM ("trapdoor" RAM, the A501 and friends) sits at $C00000 on the
// chip bus: the CPU contends with DMA for it like chip RAM, but no DMA
// channel can reach it. Kickstart finds it by probing at boot.
//
// Fast RAM lives on a Zorro II board configured by Autoconfig: at boot,
// expansion.library reads the board's ID from $E80000, writes a base
// address (normally $200000) and adds the RAM to the free list. The board
// then leaves the config space and its RAM appears at that base.

const (
	SlowRAMBase    = 0xC00000
	AutoconfigBase = 0xE80000
	AutoconfigEnd  = 0xE8FFFF
)

// ram is plain memory with no address wrapping; the Mapper keeps accesses
// inside it.
type ram struct{ b []byte }

func (m *ram) Read(addr int, p []byte) (int, error)  { return copy(p, m.b[addr:]), nil }
func (m *ram) Write(addr int, p []byte) (int, error) { return copy(m.b[addr:], p), nil }
func (m *ram) Reset() error                          { return nil }

// zorroRAM is a Zorro II fast RAM board.
type zorroRAM struct {
	amiga *Amiga
	mem   *ram
	base  uint32 // 0 until configured
	shut  bool   // told to shut up: never appears
	low   byte   // A19-A16, written to $4A before $48
}

// ConfigureMemory fits slowKB of slow RAM (0, 512, 1024 or 1536) and a
// fastKB Zorro II fast RAM board (0 or a power of two from 64 to 8192).
// Call it before Reset.
func (a *Amiga) ConfigureMemory(slowKB, fastKB int) error {
	switch slowKB {
	case 0, 512, 1024, 1536:
	default:
		return fmt.Errorf("slow RAM must be 0, 512, 1024 or 1536 KB, not %d", slowKB)
	}
	if fastKB != 0 && (fastKB < 64 || fastKB > 8192 || fastKB&(fastKB-1) != 0) {
		return fmt.Errorf("fast RAM must be 0 or a power of two from 64 to 8192 KB, not %d", fastKB)
	}
	a.SlowRAM, a.FastRAM = nil, nil
	if slowKB > 0 {
		a.SlowRAM = &ram{b: make([]byte, slowKB*1024)}
	}
	if fastKB > 0 {
		a.FastRAM = &zorroRAM{amiga: a, mem: &ram{b: make([]byte, fastKB*1024)}}
	}
	a.wireMemoryMap(a.overlay)
	return nil
}

// sizeCode is the er_Type size field for the board's RAM.
func (z *zorroRAM) sizeCode() byte {
	switch len(z.mem.b) {
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

// configByte returns the logical Autoconfig register at offset reg (a
// multiple of 4 below $40).
func (z *zorroRAM) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro II, link into the memory list, size
		return 0xC0 | 0x20 | z.sizeCode()
	case 0x04: // er_Product
		return 0x01
	case 0x08: // er_Flags: prefers the 8 MB memory space
		return 0x80
	case 0x10, 0x14: // er_Manufacturer: $07DB (goAmiga, not assigned)
		if reg == 0x10 {
			return 0x07
		}
		return 0xDB
	}
	return 0x00 // reserved, serial number, no diagnostic ROM
}

// Read returns the Autoconfig nibbles: each register occupies two words,
// high nibble at reg, low nibble at reg+2, in bits 7-4 of the even byte.
// All registers but er_Type read inverted.
func (z *zorroRAM) Read(addr int, p []byte) (int, error) {
	for i := range p {
		off := (addr + i) & 0xFFFF
		p[i] = 0
		if off&1 != 0 || off >= 0x40 {
			continue
		}
		b := z.configByte(off &^ 3)
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
func (z *zorroRAM) Write(addr int, p []byte) (int, error) {
	for i, v := range p {
		switch (addr + i) & 0xFFFF {
		case 0x4A:
			z.low = v >> 4
		case 0x48:
			z.base = uint32(v&0xF0|z.low) << 16
			z.amiga.wireMemoryMap(z.amiga.overlay)
		case 0x4C:
			z.shut = true
			z.amiga.wireMemoryMap(z.amiga.overlay)
		}
	}
	return len(p), nil
}

func (z *zorroRAM) Reset() error { return nil }

// unconfigure returns the board to its power-on state (reset).
func (z *zorroRAM) unconfigure() {
	z.base, z.shut, z.low = 0, false, 0
}

// Configured reports the board's base address, or 0.
func (z *zorroRAM) Configured() uint32 { return z.base }
