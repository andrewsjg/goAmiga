package amiga

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// The A2065 Ethernet card: a Zorro II board with an AMD Am7990 LANCE and
// 32 KB of buffer RAM. Amiga Unix drives it (aen.c), as its aen0
// interface; its frames go to a user-mode network (usernet.go) that
// connects the Amiga to the host's network without privileges.
//
// The board, as AMIX's driver uses it:
//
//	$0000  the Autoconfig data, still readable: the driver makes the MAC
//	       address 00:80:10 + the low 24 bits of the serial number from
//	       the inverted nibbles at $1C-$26
//	$4000  the LANCE's register data port (RDP)
//	$4002  its register address port (RAP)
//	$8000  32 KB of RAM, which the LANCE addresses as $8000-$FFFF
//
// The LANCE runs in byte-swap mode (CSR3 BSWP), so the 68000 writes its
// init block and descriptors as ordinary big-endian words and frames sit
// in the buffers in order. The init block gives the station address, the
// receive and transmit rings (8-byte descriptors: buffer address, flags
// OWN ERR ... STP ENP and the high address, the buffer length negated,
// and on receive the message length). The chip takes descriptors it owns:
// a frame to send spans STP to ENP; a frame received is spread over as
// many buffers as it needs, the last carrying ENP and the length (with
// the 4-byte FCS, zero here). It interrupts on INT2 for TINT, RINT, IDON
// and errors while INEA is set.

const (
	a2065Size         = 64 << 10
	a2065Manufacturer = 0x0202
	a2065Product      = 0x70
	a2065Serial       = 0x00A2065 // MAC 00:80:10:0A:20:65
)

type a2065 struct {
	autoconfigState
	amiga *Amiga
	ram   [32 << 10]byte
	net   ethernet

	rap  int
	csr  [4]uint16
	mac  [6]byte
	mode uint16
	rx   ring
	tx   ring

	lastIO uint64
	cycles int // since the last tick

	// Trace, if set, logs frames and register accesses.
	Trace io.Writer
}

// ring is a descriptor ring: its base (a LANCE address), length and the
// next descriptor the chip looks at.
type ring struct {
	base uint32
	n    int
	next int
}

// ethernet is where the card's frames go and come from.
type ethernet interface {
	send(frame []byte)
	receive() []byte // a frame waiting for the Amiga, or nil
	tick()
	close()
}

// CSR0 bits.
const (
	csrInit = 1 << 0
	csrStrt = 1 << 1
	csrStop = 1 << 2
	csrTdmd = 1 << 3
	csrTxon = 1 << 4
	csrRxon = 1 << 5
	csrInea = 1 << 6
	csrIntr = 1 << 7
	csrIdon = 1 << 8
	csrTint = 1 << 9
	csrRint = 1 << 10
	csrMerr = 1 << 11
	csrMiss = 1 << 12
	csrCerr = 1 << 13
	csrBabl = 1 << 14
	csrErr  = 1 << 15
)

// EnableEthernet fits an A2065 connected to the user-mode network, with
// host ports forwarded to the Amiga as given (host port -> Amiga port).
func (a *Amiga) EnableEthernet(forwards map[int]int) error {
	if a.Ethernet != nil {
		return nil
	}
	n, err := newUserNet(forwards)
	if err != nil {
		return err
	}
	a.Ethernet = &a2065{amiga: a, net: n}
	a.Ethernet.reset()
	a.wireMemoryMap(a.overlay)
	return nil
}

func (c *a2065) tracef(format string, args ...any) {
	if c.Trace != nil {
		fmt.Fprintf(c.Trace, "a2065: "+format+"\n", args...)
	}
}

func (c *a2065) configByte(reg int) byte {
	switch reg {
	case 0x00:
		return 0xC0 | sizeCode(a2065Size)
	case 0x04:
		return a2065Product
	case 0x10:
		return a2065Manufacturer >> 8
	case 0x14:
		return a2065Manufacturer & 0xFF
	case 0x18, 0x1C, 0x20, 0x24: // er_SerialNumber, most significant byte first
		return byte(uint32(a2065Serial) >> (8 * (3 - (reg-0x18)/4)))
	}
	return 0
}

func (c *a2065) space() m68kmem.Memory { return c }
func (c *a2065) size() int             { return a2065Size }
func (c *a2065) configured(uint32)     {}
func (c *a2065) Reset() error          { return nil }

func (c *a2065) reset() {
	c.rap = 0
	c.csr = [4]uint16{csrStop, 0, 0, 0}
}

// Activity reports recent traffic, for the status bar.
func (c *a2065) Activity() bool {
	return c.lastIO != 0 && c.amiga.cycles-c.lastIO < hdLEDHold
}

func (c *a2065) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = c.readByte(addr + i)
	}
	return len(p), nil
}

func (c *a2065) readByte(off int) byte {
	switch {
	case off < 0x40: // the Autoconfig nibbles, as at $E80000
		if off&1 != 0 {
			return 0
		}
		b := c.configByte(off &^ 3)
		n := b >> 4
		if off&2 != 0 {
			n = b & 0xF
		}
		if off&^3 != 0 {
			n = ^n & 0xF
		}
		return n << 4
	case off == 0x4000 || off == 0x4001:
		v := c.csrRead()
		if off == 0x4000 {
			return byte(v >> 8)
		}
		return byte(v)
	case off == 0x4002 || off == 0x4003:
		if off == 0x4002 {
			return 0
		}
		return byte(c.rap)
	case off >= 0x8000:
		return c.ram[off-0x8000]
	}
	return 0xFF
}

func (c *a2065) Write(addr int, p []byte) (int, error) {
	if len(p) == 2 && (addr == 0x4000 || addr == 0x4002) {
		v := binary.BigEndian.Uint16(p)
		if addr == 0x4000 {
			c.csrWrite(v)
		} else {
			c.rap = int(v & 3)
		}
		return 2, nil
	}
	for i, v := range p {
		off := addr + i
		switch {
		case off >= 0x8000:
			c.ram[off-0x8000] = v
		case off == 0x4003:
			c.rap = int(v & 3)
		}
	}
	return len(p), nil
}

func (c *a2065) csrRead() uint16 {
	if c.rap == 0 {
		return c.csr0()
	}
	return c.csr[c.rap]
}

// csr0 is CSR0 with its summary bits.
func (c *a2065) csr0() uint16 {
	v := c.csr[0] &^ (csrIntr | csrErr)
	if v&(csrBabl|csrMiss|csrMerr|csrRint|csrTint|csrIdon) != 0 {
		v |= csrIntr
	}
	if v&(csrBabl|csrCerr|csrMiss|csrMerr) != 0 {
		v |= csrErr
	}
	return v
}

func (c *a2065) csrWrite(v uint16) {
	switch c.rap {
	case 0:
		cur := c.csr[0]
		cur &^= v & (csrBabl | csrCerr | csrMiss | csrMerr | csrRint | csrTint | csrIdon) // write 1 to clear
		cur = cur&^csrInea | v&csrInea
		c.csr[0] = cur
		switch {
		case v&csrStop != 0:
			c.csr[0] = csrStop
		case v&csrInit != 0:
			c.initialize()
			if v&csrStrt != 0 {
				c.start()
			}
		case v&csrStrt != 0:
			c.start()
		}
		if v&csrTdmd != 0 {
			c.transmit()
		}
		c.update()
	case 1, 2:
		if c.csr[0]&csrStop != 0 {
			c.csr[c.rap] = v
		}
	case 3:
		if c.csr[0]&csrStop != 0 {
			c.csr[3] = v & 7
		}
	}
}

// word reads a big-endian word of the board's RAM at a LANCE address.
func (c *a2065) word(addr uint32) uint16 {
	a := int(addr & 0x7FFF)
	return uint16(c.ram[a])<<8 | uint16(c.ram[(a+1)&0x7FFF])
}

func (c *a2065) setWord(addr uint32, v uint16) {
	a := int(addr & 0x7FFF)
	c.ram[a], c.ram[(a+1)&0x7FFF] = byte(v>>8), byte(v)
}

// initialize reads the init block.
func (c *a2065) initialize() {
	ib := uint32(c.csr[2]&0xFF)<<16 | uint32(c.csr[1]&^1)
	c.mode = c.word(ib)
	for i := 0; i < 3; i++ {
		w := c.word(ib + 2 + uint32(2*i))
		c.mac[2*i], c.mac[2*i+1] = byte(w), byte(w>>8) // the low byte first
	}
	rd := func(off uint32) ring {
		lo, hi := c.word(ib+off), c.word(ib+off+2)
		return ring{base: uint32(hi&0xFF)<<16 | uint32(lo&^7), n: 1 << (hi >> 13)}
	}
	c.rx, c.tx = rd(0x10), rd(0x14)
	c.csr[0] = c.csr[0]&^(csrStop|csrInit) | csrIdon
	c.tracef("init: MAC % X, mode $%04X, %d receive and %d transmit descriptors", c.mac, c.mode, c.rx.n, c.tx.n)
}

func (c *a2065) start() {
	c.csr[0] = c.csr[0]&^csrStop | csrStrt | csrTxon | csrRxon
}

// descriptor returns the address of ring entry i.
func (r *ring) desc(i int) uint32 { return r.base + uint32(8*i) }

// transmit sends every frame the Amiga has handed the chip.
func (c *a2065) transmit() {
	if c.csr[0]&csrTxon == 0 {
		return
	}
	sent := false
	for {
		d := c.tx.desc(c.tx.next)
		flags := c.word(d + 2)
		if flags&0x8000 == 0 { // not ours
			break
		}
		// Gather STP..ENP.
		var frame []byte
		i := c.tx.next
		for n := 0; n < c.tx.n; n++ {
			d := c.tx.desc(i)
			f := c.word(d + 2)
			if f&0x8000 == 0 {
				break
			}
			buf := uint32(f&0xFF)<<16 | uint32(c.word(d))
			length := int(0x1000-c.word(d+4)&0xFFF) & 0xFFF
			for k := 0; k < length; k++ {
				frame = append(frame, c.ram[(buf+uint32(k))&0x7FFF])
			}
			c.setWord(d+2, f&^0x8000) // done, no errors
			c.setWord(d+6, 0)
			i = (i + 1) % c.tx.n
			if f&0x0100 != 0 { // ENP
				break
			}
		}
		c.tx.next = i
		if len(frame) > 0 {
			c.tracef("send %d bytes", len(frame))
			c.net.send(frame)
			c.lastIO = c.amiga.cycles | 1
			sent = true
		}
	}
	if sent {
		c.csr[0] |= csrTint
	}
}

// receive puts a frame in the receive ring, reporting false if the
// buffers the chip owns can't hold it.
func (c *a2065) deliver(frame []byte) bool {
	data := append(append([]byte(nil), frame...), 0, 0, 0, 0) // and the FCS
	// Count the buffers it needs and check they're ours.
	need, left, i := 0, len(data), c.rx.next
	for left > 0 {
		if need >= c.rx.n {
			return false
		}
		d := c.rx.desc(i)
		if c.word(d+2)&0x8000 == 0 {
			return false
		}
		left -= int(0x1000-c.word(d+4)&0xFFF) & 0xFFF
		need++
		i = (i + 1) % c.rx.n
	}
	pos := 0
	for k := 0; k < need; k++ {
		d := c.rx.desc(c.rx.next)
		f := c.word(d + 2)
		buf := uint32(f&0xFF)<<16 | uint32(c.word(d))
		length := int(0x1000-c.word(d+4)&0xFFF) & 0xFFF
		n := min(length, len(data)-pos)
		for j := 0; j < n; j++ {
			c.ram[(buf+uint32(j))&0x7FFF] = data[pos+j]
		}
		pos += n
		nf := f & 0xFF // high address bits; OWN, errors cleared
		if k == 0 {
			nf |= 0x0200 // STP
		}
		if k == need-1 {
			nf |= 0x0100 // ENP
			c.setWord(d+6, uint16(len(data)))
		}
		c.setWord(d+2, nf)
		c.rx.next = (c.rx.next + 1) % c.rx.n
	}
	c.tracef("received %d bytes into %d buffers", len(frame), need)
	c.lastIO = c.amiga.cycles | 1
	c.csr[0] |= csrRint
	return true
}

// tick moves frames: the Amiga's out (the LANCE polls its ring), the
// network's in. It does the work every 2000 CPU cycles or so.
func (c *a2065) tick(cycles int) {
	c.cycles += cycles
	if c.cycles < 2000 {
		if c.csr[0]&csrInea != 0 && c.csr0()&csrIntr != 0 {
			c.amiga.Custom.SetIntreq(1 << 3)
		}
		return
	}
	c.cycles = 0
	c.net.tick()
	if c.csr[0]&csrTxon != 0 {
		c.transmit()
	}
	if c.csr[0]&csrRxon != 0 {
		for i := 0; i < 8; i++ {
			f := c.net.receive()
			if f == nil {
				break
			}
			if !c.deliver(f) {
				c.csr[0] |= csrMiss
				c.tracef("missed a %d-byte frame: no free receive buffers", len(f))
			}
		}
	}
	c.update()
}

// update raises INT2 while an enabled interrupt is pending.
func (c *a2065) update() {
	if c.csr[0]&csrInea != 0 && c.csr0()&csrIntr != 0 {
		c.amiga.Custom.SetIntreq(1 << 3)
	}
}

func (c *a2065) close() { c.net.close() }

func (c *a2065) state(s *stateCodec) {
	c.autoconfigState.state(s)
	s.fixed(c.ram[:])
	num(s, &c.rap)
	for i := range c.csr {
		num(s, &c.csr[i])
	}
	s.fixed(c.mac[:])
	num(s, &c.mode)
	for _, r := range []*ring{&c.rx, &c.tx} {
		num(s, &r.base)
		num(s, &r.n)
		num(s, &r.next)
	}
}
