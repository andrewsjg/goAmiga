package amiga

import (
	"fmt"

	m68k "github.com/andrewsjg/goamiga/m68k"
)

// The A3000 and A4000 motherboard resources at $DE0000, and the
// motherboard's 32-bit fast RAM.
//
// Gary (the A3000's Fat Gary, and the same registers on the A4000) has
// three one-bit registers, in bit 7 of bytes $DE0000-$DE0002:
//
//   - $DE0000: bus timeout mode (Kickstart sets it).
//   - $DE0001: bus timeout enable.
//   - $DE0002: cold boot. Set at power-on; Kickstart clears it, and on a
//     68020+ seeing it set forces a cold start (it clears the ExecBase
//     pointer at 4).
//
// Ramsey, the RAM controller, has a control register at $DE0003 (page
// mode, burst, wrap, RAM type, refresh) and its version at $DE0043:
// $0D in the A3000, $0F in the A4000. Exec writes the control register
// and waits until it reads back (so a missing Ramsey hangs it); a version
// of $7F means no Ramsey. The control bits change only timing, which
// isn't modelled, so they are simply kept.
//
// Motherboard RAM ends at $07FFFFFF and grows down: Kickstart sizes it
// by testing a megabyte at a time downwards from $08000000.

const (
	motherboardBase = 0xDE0000
	motherboardEnd  = 0xDE0FFF
	mbRAMTop        = 0x08000000
)

type motherboard struct {
	model     string
	gary      [3]byte // bit 7 of $DE0000-$DE0002
	ramseyCtl byte
	ramseyVer byte
}

func (m *motherboard) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = m.readByte(addr + i)
	}
	return len(p), nil
}

func (m *motherboard) readByte(off int) byte {
	switch off {
	case 0, 1, 2:
		return m.gary[off]
	case 3:
		return m.ramseyCtl
	case 0x43:
		return m.ramseyVer
	}
	return 0
}

func (m *motherboard) Write(addr int, p []byte) (int, error) {
	for i, b := range p {
		switch off := addr + i; off {
		case 0, 1, 2:
			m.gary[off] = b & 0x80
		case 3:
			m.ramseyCtl = b
		}
	}
	return len(p), nil
}

func (m *motherboard) Reset() error { return nil }

func (r *ram) bytes() []byte {
	if r == nil {
		return nil
	}
	return r.b
}

// reset is a hardware reset (not power-on: the cold boot bit stays).
func (m *motherboard) reset() {
	m.gary[0], m.gary[1] = 0, 0
	m.ramseyCtl = 0
}

// EnableMotherboard makes the machine an A3000 ("a3000") or A4000
// ("a4000"): Gary's and Ramsey's registers at $DE0000, and mbRAM KB of
// motherboard fast RAM (0, or 1-16 MB in whole megabytes) below
// $08000000. It needs a 68030 or 68040 (SetCPU first), whose 32-bit
// address bus reaches that RAM. Call it before Reset.
func (a *Amiga) EnableMotherboard(model string, mbRAM int) error {
	ver := byte(0x0D)
	switch model {
	case "a3000":
	case "a4000":
		ver = 0x0F
	default:
		return fmt.Errorf("motherboard must be a3000 or a4000, not %q", model)
	}
	if a.CPU.Model < m68k.Model68030 {
		return fmt.Errorf("the %s motherboard needs a 68030 or 68040", model)
	}
	if mbRAM < 0 || mbRAM > 16384 || mbRAM%1024 != 0 {
		return fmt.Errorf("motherboard RAM must be 0-16384 KB in whole megabytes, not %d", mbRAM)
	}
	a.Motherboard = &motherboard{model: model, ramseyVer: ver}
	a.Motherboard.gary[2] = 0x80 // power-on: a cold boot
	a.MotherboardRAM = nil
	if mbRAM > 0 {
		a.MotherboardRAM = &ram{b: make([]byte, mbRAM*1024)}
	}
	a.wireMemoryMap(a.overlay)
	return nil
}

// boardDescription names the A3000/A4000 parts fitted, for save states:
// "" for neither.
func (a *Amiga) boardDescription() string {
	d := ""
	if m := a.Motherboard; m != nil {
		d = fmt.Sprintf("%s, %d KB on the motherboard", m.model, len(a.MotherboardRAM.bytes())/1024)
	}
	if a.Gayle != nil && a.Gayle.a4000 {
		d += ", A4000 IDE"
	}
	if a.Z3RAM != nil {
		d += fmt.Sprintf(", %d MB Zorro III RAM", len(a.Z3RAM.mem.b)>>20)
	}
	return d
}

// mapMotherboard maps the motherboard resources and RAM, if fitted.
func (a *Amiga) mapMotherboard() {
	if a.Motherboard == nil {
		return
	}
	a.Mapper.Map(a.Motherboard, motherboardBase, motherboardEnd)
	if r := a.MotherboardRAM; r != nil {
		a.Mapper.Map(r, mbRAMTop-len(r.b), mbRAMTop-1)
	}
}
