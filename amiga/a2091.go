package amiga

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// The A2091 SCSI controller: a Zorro II board with Commodore's DMA
// controller (the DMAC) in front of a Western Digital WD33C93 SCSI bus
// controller. Amiga Unix drives it directly. Its boot ROM isn't emulated
// (Commodore's scsi.device isn't here), so Kickstart configures the board
// but has no driver for it. Instead each SCSI disk is also a unit of
// goamiga.device: AmigaOS mounts and boots it from there (Amiga Unix's
// boot partition included), and Unix then finds it on the A2091.
//
// The DMAC's registers, at offsets from the board's base:
//
//	$40  ISTR   interrupt status: INT_P (bit 4, an interrupt pending and
//	            enabled), INTS (bit 6, the WD33C93's), FE_FLG (bit 0, the
//	            FIFO empty: always, as DMA here is instant)
//	$42  CNTR   control: TCEN, PREST, PDMD (SCSI), INTEN (bit 4), DDIR
//	            (bit 3: memory to SCSI)
//	$80  WTC    word transfer count (unused by the WD33C93 path)
//	$84  ACR    DMA address
//	$91  SASR   the WD33C93's address register (reading it gives the
//	            auxiliary status)
//	$93  SCMD   the WD33C93's register SASR selects
//	$E0  ST_DMA, $E2 SP_DMA, $E4 CINT, $E8 FLUSH: strobes
//
// The WD33C93 runs "select and transfer" (commands $08/$09) as one step:
// the target, LUN, command descriptor block and transfer count are in its
// registers, and the whole SCSI exchange happens at once, with the data
// moved by DMA. It ends with status $16 (or $42 if no target answers),
// the target's status byte in the target LUN register, the command phase
// register at $60, and the interrupt.

const (
	a2091Size         = 64 << 10
	a2091Manufacturer = 0x0202 // Commodore West Chester
	a2091Product      = 3
)

type a2091 struct {
	autoconfigState
	amiga   *Amiga
	targets [8]scsiDevice
	files   []*Hardfile

	// DMAC
	cntr    uint16
	acr     uint32
	wtc     uint32
	dmaOn   bool
	lastIRQ bool

	// WD33C93
	sasr  byte
	regs  [32]byte
	intrq bool

	lastIO    uint64
	lastWrite bool

	// Trace, if set, logs commands.
	Trace io.Writer
}

func (c *a2091) tracef(format string, args ...any) {
	if c.Trace != nil {
		fmt.Fprintf(c.Trace, "a2091: "+format+"\n", args...)
	}
}

// AttachSCSI connects a hardfile as SCSI target id (0-6) on an A2091,
// fitting the board if needed, and as a unit of goamiga.device for
// AmigaOS (unless it is one already). Call it before Reset.
func (a *Amiga) AttachSCSI(id int, path string, readOnly bool) error {
	if id < 0 || id > 6 {
		return fmt.Errorf("SCSI ID %d: must be 0-6 (7 is the controller)", id)
	}
	if a.SCSI == nil {
		a.SCSI = &a2091{amiga: a}
	}
	c := a.SCSI
	if c.targets[id] != nil {
		return fmt.Errorf("SCSI ID %d is taken", id)
	}
	hf, err := OpenHardfile(path, readOnly, id, 0x444F5301)
	if err != nil {
		return err
	}
	d := newSCSIDisk(hf)
	d.onIO = func(write bool) { c.lastIO, c.lastWrite = a.cycles|1, write }
	c.targets[id] = d
	c.files = append(c.files, hf)
	if a.HardDisk != nil {
		for _, f := range a.HardDisk.Files {
			if sameFile(f.Path, path) {
				a.wireMemoryMap(a.overlay)
				return nil
			}
		}
	}
	return a.AttachHardfile(path, readOnly)
}

// sameFile reports whether two paths name the same file.
func sameFile(p, q string) bool {
	a, err1 := os.Stat(p)
	b, err2 := os.Stat(q)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// Activity reports recent transfers, for the status bar.
func (c *a2091) Activity() (active, write bool) {
	if c.lastIO == 0 {
		return false, false
	}
	return c.amiga.cycles-c.lastIO < hdLEDHold, c.lastWrite
}

func (c *a2091) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro II, no boot ROM, 64 KB
		return 0xC0 | sizeCode(a2091Size)
	case 0x04:
		return a2091Product
	case 0x10:
		return a2091Manufacturer >> 8
	case 0x14:
		return a2091Manufacturer & 0xFF
	}
	return 0
}

func (c *a2091) space() m68kmem.Memory { return c }
func (c *a2091) size() int             { return a2091Size }
func (c *a2091) configured(uint32)     {}
func (c *a2091) Reset() error          { return nil }

func (c *a2091) reset() {
	c.cntr, c.acr, c.wtc, c.dmaOn, c.lastIRQ = 0, 0, 0, false, false
	c.sasr, c.regs, c.intrq = 0, [32]byte{}, false
}

func (c *a2091) close() {
	for _, f := range c.files {
		f.Close()
	}
}

// istr is the DMAC's interrupt status.
func (c *a2091) istr() uint16 {
	v := uint16(0x01) // FE_FLG: the FIFO is empty
	if c.intrq {
		v |= 0x40 // INTS
		if c.cntr&0x10 != 0 {
			v |= 0x10 // INT_P
		}
	}
	return v
}

// update raises INT2 while the board's interrupt is pending (it is
// level-triggered, held until the WD33C93's status is read).
func (c *a2091) update() {
	if c.istr()&0x10 != 0 {
		c.amiga.Custom.SetIntreq(1 << 3)
	}
}

func (c *a2091) tick() {
	if c.intrq {
		c.update()
	}
}

func (c *a2091) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = c.readByte(addr + i)
	}
	return len(p), nil
}

func (c *a2091) readByte(off int) byte {
	switch off {
	case 0x40:
		return byte(c.istr() >> 8)
	case 0x41:
		return byte(c.istr())
	case 0x42:
		return byte(c.cntr >> 8)
	case 0x43:
		return byte(c.cntr)
	case 0x91:
		return c.aux()
	case 0x93:
		return c.readReg()
	}
	return 0xFF
}

func (c *a2091) Write(addr int, p []byte) (int, error) {
	if len(p) == 4 && addr == 0x84 {
		c.acr = binary.BigEndian.Uint32(p) & 0xFFFFFE
		return len(p), nil
	}
	if len(p) == 4 && addr == 0x80 {
		c.wtc = binary.BigEndian.Uint32(p)
		return len(p), nil
	}
	for i, v := range p {
		c.writeByte(addr+i, v)
	}
	return len(p), nil
}

func (c *a2091) writeByte(off int, v byte) {
	switch off {
	case 0x42:
		c.cntr = c.cntr&0xFF | uint16(v)<<8
	case 0x43:
		c.cntr = c.cntr&0xFF00 | uint16(v)
		c.update()
	case 0x84, 0x85, 0x86, 0x87: // ACR a byte at a time
		sh := uint(8 * (3 - (off - 0x84)))
		c.acr = (c.acr&^(0xFF<<sh) | uint32(v)<<sh) & 0xFFFFFE
	case 0x91:
		c.sasr = v & 0x1F
	case 0x93:
		c.writeReg(v)
	case 0xE0, 0xE1:
		c.dmaOn = true
	case 0xE2, 0xE3:
		c.dmaOn = false
	case 0xE4, 0xE5: // CINT: the DMAC's own interrupts (none are kept)
	case 0xE8, 0xE9: // FLUSH: nothing waits in the FIFO
	}
}

// ---- The WD33C93 ----

// WD33C93 register numbers.
const (
	wdOwnID     = 0x00
	wdControl   = 0x01
	wdCDB1      = 0x03
	wdTargetLUN = 0x0F
	wdPhase     = 0x10
	wdCount     = 0x12 // to $14
	wdDestID    = 0x15
	wdSourceID  = 0x16
	wdStatus    = 0x17
	wdCommand   = 0x18
	wdData      = 0x19
	wdAux       = 0x1F
)

// aux is the auxiliary status: INT (bit 7) while the interrupt is
// pending. Nothing is ever busy.
func (c *a2091) aux() byte {
	if c.intrq {
		return 0x80
	}
	return 0
}

// readReg reads the register SASR selects; SASR then moves to the next,
// except for the auxiliary status, command and data registers.
func (c *a2091) readReg() byte {
	r := c.sasr
	var v byte
	switch r {
	case wdAux:
		v = c.aux()
	case wdStatus: // reading it acknowledges the interrupt
		v = c.regs[wdStatus]
		c.intrq = false
	default:
		v = c.regs[r]
	}
	c.advance()
	return v
}

func (c *a2091) writeReg(v byte) {
	r := c.sasr
	switch r {
	case wdAux, wdStatus:
	default:
		c.regs[r] = v
	}
	c.advance()
	if r == wdCommand {
		c.command(v)
	}
}

func (c *a2091) advance() {
	switch c.sasr {
	case wdAux, wdCommand, wdData:
	default:
		c.sasr = (c.sasr + 1) & 0x1F
	}
}

func (c *a2091) interrupt(status byte) {
	c.regs[wdStatus] = status
	c.intrq = true
	c.update()
}

func (c *a2091) command(cmd byte) {
	switch cmd & 0x7F {
	case 0x00: // RESET: status $01 with advanced features (own ID EAF), else $00
		own := c.regs[wdOwnID]
		c.regs = [32]byte{}
		c.regs[wdOwnID] = own
		st := byte(0)
		if own&0x08 != 0 {
			st = 1
		}
		c.tracef("reset (own ID $%02X)", own)
		c.interrupt(st)
	case 0x01: // ABORT
		c.interrupt(0x22)
	case 0x08, 0x09: // SELECT-AND-TRANSFER, with or without ATN
		c.selectAndTransfer()
	case 0x04: // DISCONNECT
	default:
		c.tracef("command $%02X not emulated", cmd)
		c.interrupt(0x40) // invalid command
	}
}

func (c *a2091) count() int {
	return int(c.regs[wdCount])<<16 | int(c.regs[wdCount+1])<<8 | int(c.regs[wdCount+2])
}

func (c *a2091) setCount(n int) {
	c.regs[wdCount], c.regs[wdCount+1], c.regs[wdCount+2] = byte(n>>16), byte(n>>8), byte(n)
}

// selectAndTransfer runs the SCSI command in the registers.
func (c *a2091) selectAndTransfer() {
	id := int(c.regs[wdDestID] & 7)
	lun := int(c.regs[wdTargetLUN] & 7)
	dev := c.targets[id]
	if dev == nil || id == int(c.regs[wdOwnID]&7) {
		c.regs[wdPhase] = 0
		c.interrupt(0x42) // timeout during selection
		return
	}
	cdb := make([]byte, 12)
	copy(cdb, c.regs[wdCDB1:wdCDB1+12])
	cdb = cdb[:cdbLength(cdb[0])]
	tc := c.count()
	var out []byte
	if n := dev.dataOut(cdb); n > 0 {
		out = make([]byte, n)
		got := min(n, tc)
		c.amiga.peekBytes(c.acr, out[:got])
		c.acr += uint32(got)
		tc -= got
	}
	in, status := dev.command(lun, cdb, out)
	if len(in) > 0 {
		n := min(len(in), tc)
		c.amiga.pokeBytes(c.acr, in[:n])
		c.acr += uint32(n)
		tc -= n
	}
	c.setCount(tc)
	c.regs[wdTargetLUN] = status
	c.regs[wdPhase] = 0x60
	c.tracef("target %d LUN %d: % X -> status $%02X, %d bytes in, %d out, %d left", id, lun, cdb, status, len(in), len(out), tc)
	c.interrupt(0x16) // select-and-transfer completed
}

func (c *a2091) state(s *stateCodec) {
	c.autoconfigState.state(s)
	num(s, &c.cntr)
	num(s, &c.acr)
	num(s, &c.wtc)
	s.bool(&c.dmaOn)
	num(s, &c.sasr)
	s.fixed(c.regs[:])
	s.bool(&c.intrq)
}
