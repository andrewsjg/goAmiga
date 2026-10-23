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
	"os"
	"slices"

	m68k "github.com/andrewsjg/goamiga/m68k"
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
	stateVersion = 20 // 20: the RTG pointer, the clipboard helper
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
	chipset, video := a.Chipset(), a.VideoStandard()
	num(s, &chipKB)
	num(s, &slowKB)
	num(s, &fastKB)
	s.str(&chipset)
	s.str(&video)
	hasEth := a.Ethernet != nil
	s.bool(&hasEth)
	if s.load && hasEth != (a.Ethernet != nil) {
		s.err = cmpErr(s.err, fmt.Errorf("state was saved with the A2065 %v; it is %v now (--a2065)", onOff(hasEth), onOff(a.Ethernet != nil)))
		return
	}
	// The A3000/A4000 parts must match too.
	board := a.boardDescription()
	saved := board
	s.str(&saved)
	if s.load && saved != board {
		s.err = cmpErr(s.err, fmt.Errorf("state was saved on %q; this machine is %q", saved, board))
		return
	}
	hasNet := a.Net != nil
	s.bool(&hasNet)
	if s.load && hasNet != (a.Net != nil) {
		s.err = cmpErr(s.err, fmt.Errorf("state was saved with bsdsocket.library %v; it is %v now (--bsdsocket)", onOff(hasNet), onOff(a.Net != nil)))
		return
	}
	var hdfs []string
	if a.HardDisk != nil {
		for _, f := range a.HardDisk.Files {
			hdfs = append(hdfs, f.Path)
		}
		for _, d := range a.HardDisk.Dirs {
			hdfs = append(hdfs, "dir:"+d.Path)
		}
	}
	if a.SCSI != nil {
		for _, f := range a.SCSI.files {
			hdfs = append(hdfs, "scsi:"+f.Path)
		}
		for _, t := range a.SCSI.tapes {
			hdfs = append(hdfs, "tape:"+t.dir)
		}
	}
	// The IDE drives count as hardfiles too (empty positions as "").
	hasGayle := a.Gayle != nil
	s.bool(&hasGayle)
	if s.load && hasGayle != (a.Gayle != nil) {
		s.err = cmpErr(s.err, fmt.Errorf("state was saved with Gayle %v; it is %v now (--gayle)", onOff(hasGayle), onOff(a.Gayle != nil)))
		return
	}
	if a.Gayle != nil {
		for _, h := range a.Gayle.IDEDrives() {
			p := ""
			if h != nil {
				p = h.Path
			}
			hdfs = append(hdfs, "ide:"+p)
		}
	}
	savedHDFs := slices.Clone(hdfs)
	n := len(savedHDFs)
	num(s, &n)
	if s.load {
		if s.err != nil || n > 16 {
			s.err = cmpErr(s.err, errors.New("state: bad hardfile count"))
			return
		}
		savedHDFs = make([]string, n)
	}
	for i := range savedHDFs {
		s.str(&savedHDFs[i])
	}
	if s.err != nil {
		return
	}
	if s.load {
		if !slices.Equal(savedHDFs, hdfs) {
			s.err = fmt.Errorf("state was saved with hardfiles %q; attached now: %q", savedHDFs, hdfs)
			return
		}
		if err := a.SetChipset(chipset); err != nil {
			s.err = err
			return
		}
		if err := a.SetVideoStandard(video); err != nil {
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
	num(s, &a.lineCycles)
	num(s, &a.cycles)
	s.bool(&a.lof)
	num(s, &a.eClockCounter)
	s.bool(&a.overlay)
	s.bool(&a.cpuStopped)
	num(s, &a.busErrorCount)
	num(s, &a.CPUSpeed)
	num(s, &a.cpuCycleFrac)
	s.fixed(a.pots.count[:])
	s.bool(&a.pots.running)
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
	if m := a.Motherboard; m != nil {
		s.fixed(m.gary[:])
		num(s, &m.ramseyCtl)
		if s.load {
			m.sync()
		}
	}
	if a.MotherboardRAM != nil {
		s.fixed(a.MotherboardRAM.b)
	}
	if a.CPUSlotRAM != nil {
		s.fixed(a.CPUSlotRAM.b)
	}
	if a.Z3RAM != nil {
		a.Z3RAM.autoconfigState.state(s)
		s.fixed(a.Z3RAM.mem.b)
	}
	if a.RTG != nil {
		a.RTG.state(s)
	}
	if a.HardDisk != nil {
		a.HardDisk.state(s)
	}
	if a.SCSI != nil {
		a.SCSI.state(s)
	}
	if a.Ethernet != nil {
		a.Ethernet.state(s) // its connections aren't kept
	}
	if a.Net != nil {
		a.Net.state(s)
	}
	if a.Gayle != nil {
		a.Gayle.state(s)
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
	a.Serial.state(s)
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
	num(s, &c.Model)
	for _, r := range []*uint32{&c.VBR, &c.CACR, &c.CAAR, &c.SFC, &c.DFC, &c.MSP} {
		num(s, r)
	}
	num(s, &c.FPU)
	for i := range c.FP {
		s.f64(&c.FP[i])
	}
	num(s, &c.FPCR)
	num(s, &c.FPSR)
	num(s, &c.FPIAR)
	s.bool(&c.FPUUsed)
	for _, r := range []*uint32{&c.TC, &c.TT0, &c.TT1} {
		num(s, r)
	}
	num(s, &c.CRP)
	num(s, &c.SRP)
	num(s, &c.MMUSR)
	m := &c.MMU040
	for _, r := range []*uint32{&m.TC, &m.URP, &m.SRP, &m.ITT0, &m.ITT1, &m.DTT0, &m.DTT1, &m.MMUSR} {
		num(s, r)
	}
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
	ports := sortedKeys(h.newPorts)
	count(s, &ports, 64)
	for _, p := range ports {
		pool := h.newPorts[p]
		num(s, &pool)
		if s.load {
			if h.newPorts == nil {
				h.newPorts = map[uint32]uint32{}
			}
			h.newPorts[p] = pool
		}
	}
	for _, d := range h.Dirs {
		d.state(s)
	}
	h.clip.state(s)
}

// state saves or loads the clipboard helper's progress (the Amiga side
// is in the middle of its loop, in memory the state also holds).
func (c *clipState) state(s *stateCodec) {
	for _, p := range []*int{&c.step, &c.procStep, &c.retry, &c.hostCount, &c.dataPos} {
		num(s, p)
	}
	num(s, &c.wakeAt)
	for _, p := range []*uint32{&c.dosBase, &c.port, &c.io, &c.buf, &c.lastID} {
		num(s, p)
	}
	s.str(&c.lastText)
	s.bytes(&c.data)
}

// count saves or loads a slice of numbers.
func count[T ~uint32 | ~int](s *stateCodec, p *[]T, max int) {
	n := len(*p)
	num(s, &n)
	if s.load {
		if s.err != nil || n < 0 || n > max {
			s.err = cmpErr(s.err, errors.New("state: bad list length"))
			return
		}
		*p = make([]T, n)
	}
	for i := range *p {
		num(s, &(*p)[i])
	}
}

// state saves a folder's handler: its locks, and its open files by path
// and position, reopened on load.
func (d *HostDir) state(s *stateCodec) {
	if s.load {
		d.reset()
	}
	for _, v := range []*uint32{&d.devNode, &d.port, &d.volNode, &d.pool, &d.poolEnd, &d.poolTop, &d.nextKey} {
		num(s, v)
	}
	count(s, &d.free, hostPoolSize/hostLockSize)
	locks := sortedKeys(d.locks)
	count(s, &locks, hostPoolSize/hostLockSize)
	for _, addr := range locks {
		l := d.locks[addr]
		if s.load {
			l = &hostLock{}
			d.locks[addr] = l
		}
		s.str(&l.path)
		s.bool(&l.exclusive)
		num(s, &l.key)
	}
	files := sortedKeys(d.files)
	count(s, &files, 1<<16)
	for _, id := range files {
		f := d.files[id]
		var pos int64
		if s.load {
			f = &hostFile{}
		} else {
			pos, _ = f.f.Seek(0, io.SeekCurrent)
		}
		s.str(&f.path)
		s.bool(&f.write)
		s.bool(&f.exclusive)
		num(s, &pos)
		if s.load && s.err == nil {
			flag := os.O_RDONLY
			if f.write {
				flag = os.O_RDWR
			}
			var err error
			if f.f, err = os.OpenFile(d.host(f.path), flag, 0); err != nil {
				continue // gone from the host: the handle is invalid now
			}
			f.f.Seek(pos, io.SeekStart)
			d.files[id] = f
		}
	}
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
	drawing := b.lrun != nil
	s.bool(&drawing)
	if drawing {
		if s.load {
			b.lrun = &lineRun{}
		}
		l := b.lrun
		num(s, &l.remaining)
		num(s, &l.minterm)
		num(s, &l.octant)
		s.bool(&l.single)
		s.bool(&l.onedot)
		s.bool(&l.sign)
		num(s, &l.pixelBit)
		num(s, &l.errAcc)
		num(s, &l.cpt)
		num(s, &l.lineModulo)
		num(s, &l.incMajor)
		num(s, &l.incMinor)
		num(s, &l.texture)
		num(s, &l.textureShift)
		num(s, &l.credit)
	} else {
		b.lrun = nil
	}
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
	for i := range v.agaPal {
		num(s, &v.agaPal[i])
	}
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
			for i := range sp.dataX {
				num(s, &sp.dataX[i])
				num(s, &sp.datbX[i])
			}
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
	s.bool(&d.dmaWaiting)
	busy := d.xfer != nil
	s.bool(&busy)
	if busy {
		if s.load {
			d.xfer = &diskXfer{}
		}
		x := d.xfer
		s.bool(&x.write)
		n := len(x.words)
		num(s, &n)
		if s.load {
			if n < 0 || n > 0x4000 {
				s.err = cmpErr(s.err, errors.New("state: bad disk transfer"))
				return
			}
			x.words = make([]uint16, n)
		}
		for i := range x.words {
			num(s, &x.words[i])
		}
		for _, p := range []*int{&x.count, &x.done, &x.wait, &x.elapsed} {
			num(s, p)
		}
		num(s, &x.dst)
	} else {
		d.xfer = nil
	}
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
	// An IPF disk is saved as its file and parsed again on load (weak
	// cells' current values aren't kept: they're random anyway).
	var ipfRaw []byte
	if dr.ipf != nil {
		ipfRaw = dr.ipf.raw
	}
	s.bytes(&ipfRaw)
	if s.load {
		dr.ipf = nil
		if ipfRaw != nil && s.err == nil {
			dr.ipf, s.err = ParseIPF(ipfRaw)
		}
	}
	s.str(&dr.path)
	s.bool(&dr.WriteProtected)
	pending := dr.pending != nil
	s.bool(&pending)
	if pending {
		if s.load {
			dr.pending = &pendingDisk{}
		}
		s.bytes(&dr.pending.data)
		var pRaw []byte
		if dr.pending.ipf != nil {
			pRaw = dr.pending.ipf.raw
		}
		s.bytes(&pRaw)
		if s.load && pRaw != nil && s.err == nil {
			dr.pending.ipf, s.err = ParseIPF(pRaw)
		}
		s.str(&dr.pending.path)
	} else {
		dr.pending = nil
	}
	num(s, &dr.pendingWait)
	num(s, &dr.pendingElapsed)
	s.bool(&dr.motor)
	num(s, &dr.motorOnAt)
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

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
