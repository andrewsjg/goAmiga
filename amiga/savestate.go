package amiga

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"slices"

	m68k "github.com/cavaliercoder/go-m68k"
)

// Save states: the whole machine between two instructions, so a loaded
// state resumes exactly where it was saved.
//
// Each component has one state method that both saves and loads, through a
// stateCodec that either writes each field or reads it back into place, so
// the two directions cannot drift apart. The file is gzip-compressed:
// a magic string and version, the configuration it needs (ROM checksum,
// memory, chipset, drives, hardfile paths), then the components in a fixed
// order.
//
// Not saved: hard disk contents (too big; the state records which
// hardfiles were attached and loading one after the disk has changed can
// confuse its filesystem), host settings (window, warp, sound), and each
// line's rendering scratch, which is rebuilt as the frame is drawn.

const (
	stateMagic   = "goAmiga state\n"
	stateVersion = 1
)

// stateCodec writes or reads the fields of a state in order.
type stateCodec struct {
	load bool
	w    io.Writer
	r    io.Reader
	err  error
	buf  [8]byte
}

func (s *stateCodec) raw(p []byte) {
	if s.err != nil {
		return
	}
	if s.load {
		_, s.err = io.ReadFull(s.r, p)
	} else {
		_, s.err = s.w.Write(p)
	}
}

// num saves or loads any integer as 8 bytes.
func num[T ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](s *stateCodec, p *T) {
	if s.load {
		s.raw(s.buf[:])
		*p = T(binary.BigEndian.Uint64(s.buf[:]))
		return
	}
	binary.BigEndian.PutUint64(s.buf[:], uint64(*p))
	s.raw(s.buf[:])
}

func (s *stateCodec) bool(p *bool) {
	v := uint8(0)
	if *p {
		v = 1
	}
	num(s, &v)
	*p = v != 0
}

func (s *stateCodec) f64(p *float64) {
	v := math.Float64bits(*p)
	num(s, &v)
	*p = math.Float64frombits(v)
}

// bytes saves or loads a byte slice of any length (nil stays nil).
func (s *stateCodec) bytes(p *[]byte) {
	n := int64(-1)
	if *p != nil {
		n = int64(len(*p))
	}
	num(s, &n)
	if s.load {
		if n < 0 || s.err != nil {
			*p = nil
			return
		}
		if n > 64<<20 {
			s.err = fmt.Errorf("state: implausible %d-byte field", n)
			return
		}
		*p = make([]byte, n)
	}
	s.raw(*p)
}

// fixed saves or loads a buffer whose length both sides already agree on.
func (s *stateCodec) fixed(p []byte) {
	n := int64(len(p))
	num(s, &n)
	if s.load && s.err == nil && n != int64(len(p)) {
		s.err = fmt.Errorf("state: field is %d bytes, expected %d", n, len(p))
		return
	}
	s.raw(p)
}

func (s *stateCodec) str(p *string) {
	b := []byte(*p)
	s.bytes(&b)
	*p = string(b)
}

// check fails the codec on load if saved differs from current.
func (s *stateCodec) check(what string, current int64) {
	v := current
	num(s, &v)
	if s.load && s.err == nil && v != current {
		s.err = fmt.Errorf("state was saved with %s %d, this machine has %d", what, v, current)
	}
}

// ---- The machine ----

// SaveState writes the machine's state.
func (a *Amiga) SaveState(w io.Writer) error {
	zw := gzip.NewWriter(w)
	bw := bufio.NewWriter(zw)
	s := &stateCodec{w: bw}
	s.raw([]byte(stateMagic))
	a.state(s)
	if s.err != nil {
		return s.err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// LoadState restores a state written by SaveState. The ROM must be the
// same; memory and chipset are set to match the state; the same hardfiles
// must be attached. On error the machine is left as it was only if the
// failure was in the header; a damaged state can leave it half restored,
// so reset it.
func (a *Amiga) LoadState(r io.Reader) error {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("not a goAmiga state: %w", err)
	}
	s := &stateCodec{load: true, r: bufio.NewReader(zr)}
	magic := make([]byte, len(stateMagic))
	s.raw(magic)
	if s.err != nil || string(magic) != stateMagic {
		return errors.New("not a goAmiga state")
	}
	a.state(s)
	if s.err != nil {
		return s.err
	}
	a.wireMemoryMap(a.overlay)
	if a.Paula != nil && a.Paula.Output != nil {
		a.Paula.Output.SetMuted(false)
	}
	return nil
}

func (a *Amiga) state(s *stateCodec) {
	version := int64(stateVersion)
	num(s, &version)
	if s.load && version != stateVersion {
		s.err = fmt.Errorf("state version %d, this goAmiga reads version %d", version, stateVersion)
		return
	}
	s.check("ROM checksum", int64(a.romChecksum()))

	// Configuration: memory and chipset follow the state.
	chipKB, slowKB, fastKB := a.chipSize/1024, 0, 0
	if a.SlowRAM != nil {
		slowKB = len(a.SlowRAM.b) / 1024
	}
	if a.FastRAM != nil {
		fastKB = len(a.FastRAM.mem.b) / 1024
	}
	chipset := a.Chipset()
	num(s, &chipKB)
	num(s, &slowKB)
	num(s, &fastKB)
	s.str(&chipset)
	var hdfs []string
	if a.HardDisk != nil {
		for _, f := range a.HardDisk.Files {
			hdfs = append(hdfs, f.Path)
		}
	}
	saved := slices.Clone(hdfs)
	n := len(saved)
	num(s, &n)
	if s.load {
		if s.err != nil || n > 16 {
			s.err = cmpErr(s.err, errors.New("state: bad hardfile count"))
			return
		}
		saved = make([]string, n)
	}
	for i := range saved {
		s.str(&saved[i])
	}
	if s.err != nil {
		return
	}
	if s.load {
		if !slices.Equal(saved, hdfs) {
			s.err = fmt.Errorf("state was saved with hardfiles %q; attached now: %q", saved, hdfs)
			return
		}
		if err := a.SetChipset(chipset); err != nil {
			s.err = err
			return
		}
		if err := a.SetChipRAM(chipKB); err != nil {
			s.err = err
			return
		}
		if err := a.ConfigureMemory(slowKB, fastKB); err != nil {
			s.err = err
			return
		}
	}

	// The beam and machine-level state.
	num(s, &a.hpos)
	num(s, &a.vpos)
	num(s, &a.cycles)
	s.bool(&a.lof)
	num(s, &a.eClockCounter)
	s.bool(&a.overlay)
	s.bool(&a.cpuStopped)
	num(s, &a.busErrorCount)
	num(s, &a.cpuChipAccesses)
	num(s, &a.cpuCIAAccesses)

	cpu := a.CPU.State()
	cpuState(s, &cpu)
	if s.load {
		a.CPU.SetState(cpu)
	}

	// Memory.
	chip := make([]byte, a.chipSize)
	if !s.load {
		a.ChipRAM.Read(0, chip)
	}
	s.fixed(chip)
	if s.load {
		a.ChipRAM.Write(0, chip)
	}
	if a.SlowRAM != nil {
		s.fixed(a.SlowRAM.b)
	}
	if a.FastRAM != nil {
		a.FastRAM.autoconfigState.state(s)
		s.fixed(a.FastRAM.mem.b)
	}
	if a.HardDisk != nil {
		a.HardDisk.state(s)
	}

	// Chips.
	s.fixed(a.Custom.regs[:])
	a.CIAA.state(s)
	a.CIAB.state(s)
	a.Copper.state(s)
	a.Blitter.state(s)
	a.Video.state(s)
	a.Sprites.state(s)
	a.Disk.state(s)
	a.Paula.state(s)
	a.Keyboard.state(s)
	num(s, &a.Mouse.CounterX)
	num(s, &a.Mouse.CounterY)
}

func cmpErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// romChecksum identifies the ROM: a CRC-32 of its image. (The Kickstart
// checksum can't: every good ROM sums to $FFFFFFFF.)
func (a *Amiga) romChecksum() uint32 {
	b := make([]byte, a.romSize)
	a.ROM.Read(0, b)
	return crc32.ChecksumIEEE(b)
}

func cpuState(s *stateCodec, c *m68k.ProcessorState) {
	for i := range c.D {
		num(s, &c.D[i])
		num(s, &c.A[i])
	}
	num(s, &c.SR)
	num(s, &c.PC)
	num(s, &c.SSP)
	num(s, &c.USP)
	num(s, &c.PendingIPL)
	num(s, &c.RunState)
}

func (st *autoconfigState) state(s *stateCodec) {
	num(s, &st.base)
	s.bool(&st.shut)
	num(s, &st.low)
}

func (h *hdBoard) state(s *stateCodec) {
	h.autoconfigState.state(s)
	if s.load && h.base != 0 {
		h.buildROM(h.base) // offsets and partition list; memory follows
	}
	s.fixed(h.mem[:])
	num(s, &h.configDev)
	num(s, &h.nextPart)
	num(s, &h.lastIO)
	s.bool(&h.lastWrite)
}

func (c *CIA) state(s *stateCodec) {
	s.fixed(c.regs[:])
	num(s, &c.timerALatch)
	num(s, &c.timerACounter)
	num(s, &c.timerBLatch)
	num(s, &c.timerBCounter)
	num(s, &c.todCounter)
	num(s, &c.todAlarm)
	num(s, &c.todLatch)
	s.bool(&c.todLatched)
	s.bool(&c.todStopped)
	num(s, &c.todClkDiv)
	num(s, &c.todClkRate)
	num(s, &c.icrMask)
	num(s, &c.icrData)
	s.bool(&c.irqAsserted)
}

func (c *Copper) state(s *stateCodec) {
	num(s, &c.cop1lc)
	num(s, &c.cop2lc)
	num(s, &c.pc)
	s.bool(&c.halted)
	s.bool(&c.wait)
	s.bool(&c.compare)
	s.bool(&c.second)
	num(s, &c.ir1)
	num(s, &c.waitVP)
	num(s, &c.waitHP)
	num(s, &c.waitVM)
	num(s, &c.waitHM)
}

func (b *Blitter) state(s *stateCodec) {
	for _, p := range []*uint16{&b.con0, &b.con1, &b.afwm, &b.alwm,
		&b.aptH, &b.aptL, &b.bptH, &b.bptL, &b.cptH, &b.cptL, &b.dptH, &b.dptL,
		&b.adat, &b.bdat, &b.cdat, &b.lastSize} {
		num(s, p)
	}
	for _, p := range []*int16{&b.amod, &b.bmod, &b.cmod, &b.dmod} {
		num(s, p)
	}
	s.bool(&b.busy)
	s.bool(&b.zero)
	num(s, &b.completionCycles)
	num(s, &b.pendingIRQs)
	running := b.run != nil
	s.bool(&running)
	if !running {
		b.run = nil
		return
	}
	if s.load {
		b.run = &blitRun{}
	}
	r := b.run
	for _, p := range []*int{&r.width, &r.height, &r.x, &r.y, &r.credit, &r.wordCost} {
		num(s, p)
	}
	for _, p := range []*uint32{&r.apt, &r.bpt, &r.cpt, &r.dpt} {
		num(s, p)
	}
	for _, p := range []*uint16{&r.prevA, &r.prevB, &r.fillCarry, &r.con0, &r.con1, &r.afwm, &r.alwm} {
		num(s, p)
	}
	for _, p := range []*int16{&r.amod, &r.bmod, &r.cmod, &r.dmod} {
		num(s, p)
	}
}

func (v *Video) state(s *stateCodec) {
	for i := range v.bplpt {
		num(s, &v.bplpt[i])
	}
	s.bool(&v.laced)
	s.bool(&v.diwHigh)
	num(s, &v.clxdat)
	if s.load {
		v.resize() // rows follow the interlace state
		v.changes = v.changes[:0]
	}
}

func (se *SpriteEngine) state(s *stateCodec) {
	for _, set := range []*[8]Sprite{&se.sprites, &se.start} {
		for i := range set {
			sp := &set[i]
			num(s, &sp.pt)
			num(s, &sp.pos)
			num(s, &sp.ctl)
			num(s, &sp.data)
			num(s, &sp.datb)
			num(s, &sp.state)
		}
	}
	n := len(se.events)
	num(s, &n)
	if s.load {
		if n < 0 || n > 4096 {
			s.err = cmpErr(s.err, errors.New("state: bad sprite event count"))
			return
		}
		se.events = make([]sprEvent, n)
	}
	for i := range se.events {
		e := &se.events[i]
		num(s, &e.col)
		num(s, &e.sprite)
		num(s, &e.reg)
		num(s, &e.val)
	}
}

func (d *DiskDrive) state(s *stateCodec) {
	num(s, &d.side)
	num(s, &d.dskpt)
	num(s, &d.dsklen)
	num(s, &d.dsklenWrites)
	num(s, &d.dsksync)
	num(s, &d.adkcon)
	s.bool(&d.wordequal)
	num(s, &d.syncCycles)
	num(s, &d.completionCycles)
	for n := range d.drives {
		connected := d.drives[n] != nil
		s.bool(&connected)
		if !connected {
			if s.load && n > 0 {
				d.drives[n] = nil
			}
			continue
		}
		dr := d.drives[n]
		if dr == nil {
			dr = d.ConnectDrive(n)
		}
		dr.state(s)
	}
}

func (dr *Drive) state(s *stateCodec) {
	s.bytes(&dr.adf)
	s.str(&dr.path)
	s.bool(&dr.WriteProtected)
	pending := dr.pending != nil
	s.bool(&pending)
	if pending {
		if s.load {
			dr.pending = &pendingDisk{}
		}
		s.bytes(&dr.pending.data)
		s.str(&dr.pending.path)
	} else {
		dr.pending = nil
	}
	num(s, &dr.pendingWait)
	num(s, &dr.pendingElapsed)
	s.bool(&dr.motor)
	num(s, &dr.cylinder)
	s.bool(&dr.selected)
	s.bool(&dr.diskChange)
	num(s, &dr.idBit)
}

func (p *Paula) state(s *stateCodec) {
	for i := range p.ch {
		c := &p.ch[i]
		num(s, &c.lc)
		num(s, &c.len)
		num(s, &c.per)
		num(s, &c.vol)
		num(s, &c.dat)
		s.bool(&c.datFull)
		s.bool(&c.dma)
		s.bool(&c.manual)
		num(s, &c.ptr)
		num(s, &c.count)
		num(s, &c.word)
		s.bool(&c.lowByte)
		num(s, &c.periodLeft)
		num(s, &c.sample)
		num(s, &c.modWord)
	}
	num(s, &p.cpuRem)
	num(s, &p.hostAcc)
	s.f64(&p.accL)
	s.f64(&p.accR)
	num(s, &p.accCCK)
	for _, f := range []*onePole{&p.fixedL, &p.fixedR} {
		s.f64(&f.a)
		s.f64(&f.y)
	}
	for _, f := range []*biquad{&p.ledL, &p.ledR} {
		for _, v := range []*float64{&f.b0, &f.b1, &f.b2, &f.a1, &f.a2, &f.x1, &f.x2, &f.y1, &f.y2} {
			s.f64(v)
		}
	}
}

func (k *Keyboard) state(s *stateCodec) {
	s.bytes(&k.queue)
	s.bool(&k.handshakePending)
	s.bool(&k.started)
	num(s, &k.frameCount)
}
