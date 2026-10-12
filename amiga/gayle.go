package amiga

import (
	"errors"
	"fmt"
	"io"
)

// Gayle, the A600 and A1200 gate array: its IDE interface (the part
// emulated here; ide.go has the drives) and the registers around it. The
// PCMCIA slot is not emulated: no card is ever present.
//
//	$DA0000-$DA3FFF  the IDE task file: register n at $DA0000 + 4n
//	                 (any byte of the four), the device control and
//	                 alternate status register at $DA1018; $DA2000 mirrors
//	$DA8000          status: bit 7 is the IDE interrupt line
//	$DA9000          interrupt requests: bit 7 latches the IDE interrupt;
//	                 writing a 0 bit clears it
//	$DAA000          interrupt enables: bit 7 lets IDE interrupts through,
//	                 on INT2 (PORTS)
//	$DAB000          configuration (PCMCIA speed)
//	$DE1000          Gayle's ID, read a bit at a time in bit 7 after a
//	                 write resets it: $D0
//
// Kickstart 3.x for the A600 and A1200 checks the ID, and its scsi.device
// then looks for drives and boots from the partitions in their Rigid Disk
// Blocks (an IDE disk needs an RDB; plain hardfiles go on goamiga.device).

const (
	gayleIDEBase = 0xDA0000
	gayleIDEEnd  = 0xDAFFFF
	gayleIDBase  = 0xDE1000
	gayleIDEnd   = 0xDE1FFF
	gayleID      = 0xD0
)

type gayle struct {
	amiga  *Amiga
	drives [2]*ideDrive // master, slave

	devCtrl             byte // device control: bit 1 nIEN, bit 2 SRST
	cs, irq, intEn, cfg byte
	idCount             int
	lastLine            bool

	lastIO    uint64 // Amiga.cycles at the last transfer, for the light
	lastWrite bool

	// Trace, if set, logs register accesses (not the data port).
	Trace io.Writer
}

func (g *gayle) tracef(format string, args ...any) {
	if g.Trace != nil {
		fmt.Fprintf(g.Trace, "gayle: "+format+"\n", args...)
	}
}

// EnableGayle fits Gayle's IDE interface (the A600 and A1200 have it).
// Call it before Reset.
func (a *Amiga) EnableGayle() {
	if a.Gayle == nil {
		a.Gayle = &gayle{amiga: a}
	}
}

// AttachIDE connects a hardfile as the next IDE drive: the master, then
// the slave. It fits Gayle if it isn't already.
func (a *Amiga) AttachIDE(path string, readOnly bool) error {
	a.EnableGayle()
	g := a.Gayle
	n := 0
	for n < 2 && g.drives[n] != nil {
		n++
	}
	if n == 2 {
		return errors.New("Gayle has two IDE drives (master and slave) already")
	}
	hf, err := OpenHardfile(path, readOnly, n, 0x444F5300)
	if err != nil {
		return err
	}
	d := newIDEDrive(hf)
	d.onIO = func(write bool) { g.lastIO, g.lastWrite = a.cycles, write }
	g.drives[n] = d
	return nil
}

// Activity reports whether an IDE drive transferred data recently, and if
// so whether the last transfer was a write.
func (g *gayle) Activity() (active, write bool) {
	if g.lastIO == 0 {
		return false, false
	}
	return g.amiga.cycles-g.lastIO < hdLEDHold, g.lastWrite
}

// IDEDrives lists the hardfiles on the IDE interface (nil for an empty
// position).
func (g *gayle) IDEDrives() [2]*Hardfile {
	var hs [2]*Hardfile
	for i, d := range g.drives {
		if d != nil {
			hs[i] = d.hf
		}
	}
	return hs
}

func (g *gayle) reset() {
	for _, d := range g.drives {
		if d != nil {
			d.reset()
		}
	}
	g.devCtrl, g.cs, g.irq, g.intEn, g.cfg, g.idCount, g.lastLine = 0, 0, 0, 0, 0, 0, false
}

// selected is the drive the select register names, or nil.
func (g *gayle) selected() *ideDrive {
	for _, d := range g.drives {
		if d != nil {
			return g.drives[d.selectReg>>4&1]
		}
	}
	return nil
}

// ideReg decodes a task file address: 0-7, or $406 (device control and
// alternate status) and $407 (drive address).
func ideReg(addr int) int { return (addr & 0xFFFF &^ 0x2020) >> 2 }

// gayleView maps one of Gayle's address ranges: the Mapper passes
// offsets from the start of the range, and Gayle wants addresses.
type gayleView struct {
	g    *gayle
	base int
}

func (v gayleView) Read(addr int, p []byte) (int, error)  { return v.g.Read(v.base+addr, p) }
func (v gayleView) Write(addr int, p []byte) (int, error) { return v.g.Write(v.base+addr, p) }
func (v gayleView) Reset() error                          { return nil }

func (g *gayle) Read(addr int, p []byte) (int, error) {
	off := addr & 0xFFFF
	if addr >= gayleIDBase && addr <= gayleIDEnd {
		g.tracef("read ID $%06X (bit %d)", addr, g.idCount)
		for i := range p {
			p[i] = byte(gayleID<<g.idCount) & 0x80
			g.idCount = (g.idCount + 1) & 7
		}
		return len(p), nil
	}
	if off >= 0x8000 {
		v := byte(0)
		switch off >> 12 {
		case 0x8:
			v = g.cs & 0x03
			if g.line() {
				v |= 0x80
			}
		case 0x9:
			v = g.irq
		case 0xA:
			v = g.intEn
		case 0xB:
			v = g.cfg
		}
		for i := range p {
			p[i] = v
		}
		g.tracef("read  $%06X = $%02X", addr, v)
		return len(p), nil
	}
	reg := ideReg(addr)
	d := g.selected()
	if reg == 0 { // data: words
		for i := 0; i+1 < len(p); i += 2 {
			w := uint16(0xFFFF)
			if d != nil {
				w = d.readData()
			}
			p[i], p[i+1] = byte(w>>8), byte(w)
		}
		if len(p)%2 == 1 {
			p[len(p)-1] = 0xFF
		}
		g.update()
		return len(p), nil
	}
	v := g.readReg(reg, d)
	for i := range p {
		p[i] = v
	}
	g.tracef("read  $%06X (reg $%X) = $%02X", addr, reg, v)
	g.update()
	return len(p), nil
}

func (g *gayle) readReg(reg int, d *ideDrive) byte {
	if d == nil {
		// With a master but no slave, the master answers for it with
		// zeros (ATA); with no drive at all the bus floats high.
		if g.drives[0] != nil || g.drives[1] != nil {
			return 0
		}
		return 0xFF
	}
	switch reg {
	case 1:
		return d.errReg
	case 2:
		return d.nsector
	case 3:
		return d.sector
	case 4:
		return d.lcyl
	case 5:
		return d.hcyl
	case 6:
		return d.selectReg | 0xA0
	case 7: // status: reading it acknowledges the interrupt
		d.irq = false
		return d.status
	case 0x406: // alternate status
		return d.status
	case 0x407:
		return 0xFF
	}
	return 0
}

func (g *gayle) Write(addr int, p []byte) (int, error) {
	off := addr & 0xFFFF
	if addr >= gayleIDBase && addr <= gayleIDEnd {
		g.tracef("write ID $%06X: reset", addr)
		g.idCount = 0
		return len(p), nil
	}
	if off >= 0x8000 {
		v := p[0]
		g.tracef("write $%06X = % X", addr, p)
		switch off >> 12 {
		case 0x8:
			g.cs = g.cs&^0x03 | v&0x03
		case 0x9:
			g.irq &= v
		case 0xA:
			g.intEn = v
		case 0xB:
			g.cfg = v
		}
		g.update()
		return len(p), nil
	}
	reg := ideReg(addr)
	if reg == 0 {
		if d := g.selected(); d != nil {
			for i := 0; i+1 < len(p); i += 2 {
				d.writeData(uint16(p[i])<<8 | uint16(p[i+1]))
			}
		}
		g.update()
		return len(p), nil
	}
	// The byte on the even (high) lane: what a MOVE.W or a MOVE.B to the
	// register's address carries.
	g.tracef("write $%06X (reg $%X) = % X", addr, reg, p)
	g.writeReg(reg, p[0])
	g.update()
	return len(p), nil
}

func (g *gayle) writeReg(reg int, v byte) {
	if reg == 0x406 { // device control
		if v&0x04 != 0 && g.devCtrl&0x04 == 0 { // SRST
			for _, d := range g.drives {
				if d != nil {
					d.reset()
				}
			}
		}
		g.devCtrl = v
		return
	}
	// Both drives latch the task file; the selected one acts on commands.
	for _, d := range g.drives {
		if d == nil {
			continue
		}
		switch reg {
		case 1:
			d.features = v
		case 2:
			d.nsector = v
		case 3:
			d.sector = v
		case 4:
			d.lcyl = v
		case 5:
			d.hcyl = v
		case 6:
			d.selectReg = v
		}
	}
	if reg == 7 {
		if d := g.selected(); d != nil && d.status&ataBSY == 0 {
			d.command(v)
		}
	}
}

// line is the drive's interrupt request as Gayle sees it.
func (g *gayle) line() bool {
	d := g.selected()
	return d != nil && d.irq && g.devCtrl&0x02 == 0
}

// update latches a rising interrupt line and raises INT2 while an enabled
// request is pending (it is level-triggered: the PORTS request comes back
// until the driver clears Gayle's latch).
func (g *gayle) update() {
	line := g.line()
	if line && !g.lastLine {
		g.irq |= 0x80
	}
	g.lastLine = line
	if g.irq&g.intEn&0x80 != 0 {
		g.amiga.Custom.SetIntreq(1 << 3)
	}
}

// tick advances the drives' busy time.
func (g *gayle) tick(cycles int) {
	busy := false
	for _, d := range g.drives {
		if d != nil && d.busy > 0 {
			d.tick(cycles)
			busy = true
		}
	}
	if busy || g.irq&g.intEn&0x80 != 0 {
		g.update()
	}
}

// cyclesToNextEvent is the CPU cycles until a drive finishes being busy,
// or -1.
func (g *gayle) cyclesToNextEvent() int {
	next := -1
	for _, d := range g.drives {
		if d != nil && d.busy > 0 && (next < 0 || d.busy < next) {
			next = d.busy
		}
	}
	return next
}

func (g *gayle) Reset() error { return nil }

func (g *gayle) close() {
	for _, d := range g.drives {
		if d != nil {
			d.hf.Close()
		}
	}
}

func (g *gayle) state(s *stateCodec) {
	num(s, &g.devCtrl)
	num(s, &g.cs)
	num(s, &g.irq)
	num(s, &g.intEn)
	num(s, &g.cfg)
	num(s, &g.idCount)
	s.bool(&g.lastLine)
	for _, d := range g.drives {
		if d != nil {
			d.state(s)
		}
	}
}
