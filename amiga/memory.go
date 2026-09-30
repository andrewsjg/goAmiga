package amiga

import (
	"fmt"

	m68k "github.com/cavaliercoder/go-m68k"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

// Expansion memory: slow RAM and a Zorro II fast RAM board.
//
// Slow RAM ("trapdoor" RAM, the A501 and friends) sits at $C00000 on the
// chip bus: the CPU contends with DMA for it like chip RAM, but no DMA
// channel can reach it. Kickstart finds it by probing at boot.
//
// Fast RAM lives on a Zorro II board configured by Autoconfig (see
// autoconfig.go): expansion.library gives it a base address (normally
// $200000) and adds the RAM to the free list.

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
	autoconfigState
	mem *ram
}

// SetChipRAM fits kb of chip RAM: 512, or 1024 as on an A500 with the
// 8372A "Fat Agnus", which drives one more address line so DMA reaches
// the whole megabyte. Call it before Reset.
//
// Agnus still identifies as OCS in VPOSR: an ECS ID makes Kickstart 2.0+
// use ECS-only registers (BLTSIZV/BLTSIZH, BEAMCON0) that goAmiga does not
// have. Kickstart sizes chip RAM by probing, not from the ID.
func (a *Amiga) SetChipRAM(kb int) error {
	if kb != 512 && kb != 1024 {
		return fmt.Errorf("chip RAM must be 512 or 1024 KB, not %d", kb)
	}
	a.ChipRAM = newChipRAM(kb * 1024)
	a.chipSize = kb * 1024
	a.wireMemoryMap(a.overlay)
	return nil
}

// ChipRAMSize is the chip RAM fitted, in bytes. DMA pointers wrap at it.
func (a *Amiga) ChipRAMSize() int { return a.chipSize }

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
		a.FastRAM = &zorroRAM{mem: &ram{b: make([]byte, fastKB*1024)}}
	}
	a.wireMemoryMap(a.overlay)
	return nil
}

// configByte returns the logical Autoconfig register at offset reg (a
// multiple of 4 below $40).
func (z *zorroRAM) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro II, link into the memory list, size
		return 0xC0 | 0x20 | sizeCode(len(z.mem.b))
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

func (z *zorroRAM) space() m68kmem.Memory { return z.mem }
func (z *zorroRAM) size() int             { return len(z.mem.b) }
func (z *zorroRAM) configured(uint32)     {}

// SetChipset chooses the custom chips: "ocs" (8371 Agnus, 8362 Denise:
// the original A500), "ecs-agnus" (8372A Fat Agnus with the original
// Denise, as in A500s with 1 MB of chip RAM), or "ecs" (8372A and the
// 8373 ECS Denise, as in the A500+ and A600).
//
// ECS Agnus reports its ID in VPOSR and adds big blits (BLTSIZV/BLTSIZH),
// BLTCON0L, BEAMCON0 and DIWHIGH; Kickstart 2.0+ uses them once it sees
// the ID. ECS Denise answers DENISEID ($FC) and adds super-hires,
// KILLEHB and border blanking (BPLCON3).
func (a *Amiga) SetChipset(name string) error {
	switch name {
	case "ocs":
		a.ECSAgnus, a.ECSDenise = false, false
	case "ecs-agnus":
		a.ECSAgnus, a.ECSDenise = true, false
	case "ecs":
		a.ECSAgnus, a.ECSDenise = true, true
	default:
		return fmt.Errorf("chipset must be ocs, ecs-agnus or ecs, not %q", name)
	}
	return nil
}

// Chipset names the chips fitted (see SetChipset).
func (a *Amiga) Chipset() string {
	switch {
	case a.ECSDenise:
		return "ecs"
	case a.ECSAgnus:
		return "ecs-agnus"
	}
	return "ocs"
}

// SetCPU chooses the processor: "68000" (the A500's, 7 MHz) or "68020"
// (a 68EC020 accelerator: 24-bit addressing, no MMU or FPU). speed is the
// CPU's speed in percent of the 7 MHz 68000, 0 meaning the model's usual:
// 100 for the 68000, 250 for the 68020 (about an A1200's 14 MHz 68EC020).
// Call it before Reset.
func (a *Amiga) SetCPU(model string, speed int) error {
	switch model {
	case "68000":
		a.CPU.Model = m68k.Model68000
		if speed == 0 {
			speed = 100
		}
	case "68020":
		a.CPU.Model = m68k.Model68020
		if speed == 0 {
			speed = 250
		}
	default:
		return fmt.Errorf("CPU must be 68000 or 68020, not %q", model)
	}
	if speed < 100 || speed > 2000 {
		return fmt.Errorf("CPU speed must be 100-2000%%, not %d", speed)
	}
	a.CPUSpeed = speed
	return nil
}
