package m68k

import (
	"encoding/binary"
	"fmt"
)

// The 68030's memory management unit.
//
// Model68030 is the 68020 instruction set plus the on-chip MMU (and the
// 68030's CACR). The MMU translates each access's logical address, with
// its function code (user or supervisor, program or data), to a physical
// one:
//
//   - TC enables translation and divides the logical address: an initial
//     shift (IS), up to four table indexes (TIA-TID) and the page offset
//     (PS, 256 bytes to 32 KB). With FCL the function code indexes a first
//     table level; with SRE supervisor accesses start from SRP rather than
//     CRP.
//   - The tables hold short (4-byte) or long (8-byte) descriptors: table
//     descriptors pointing to the next level, page descriptors (which may
//     end the walk early), indirect descriptors at the last level, and
//     invalid ones. Long descriptors add a limit on the next index and a
//     supervisor-only bit. A write-protect bit anywhere on the way
//     protects the page.
//   - The walk sets each descriptor's U (used) bit, and the page
//     descriptor's M (modified) bit on a write: an operating system pages
//     with them.
//   - TT0 and TT1 map ranges of 16 MB blocks transparently (logical =
//     physical) for chosen function codes and reads or writes.
//   - The address translation cache (ATC) is a cache of finished walks,
//     flushed by PFLUSH and by loading TC, CRP or SRP.
//
// A fault (an invalid descriptor, a limit, a write to a protected page,
// a user access to a supervisor page) aborts the instruction: its
// register changes are undone and a bus error (vector 2) is taken with a
// format $B frame describing the access. RTE runs the instruction again
// from the start, which is how this emulation continues it: the 68030
// itself resumes mid-instruction from the frame's internal state, but an
// operating system that fixes the page and returns sees the same result.
//
// The instructions are PMOVE (TC, CRP, SRP, TT0, TT1, MMUSR), PFLUSH,
// PFLUSHA, PLOAD and PTEST. The caches are not emulated (CACR is stored).

// Model68030 is the 68030: Model68020 plus the MMU.
const Model68030 Model = 2

// is030 reports a 68030 (the 040 has its own MMU and CACR).
func (c *Processor) is030() bool { return c.Model == Model68030 }

// mmu030 is the MMU's registers and its translation cache.
type mmu030 struct {
	tc       uint32
	crp, srp uint64
	tt       [2]uint32
	mmusr    uint16

	// From TC.
	enabled, sre, fcl bool
	is, ps            uint
	ti                [4]uint

	atc [atcSize]atcEntry

	// fcOverride, when set (MOVES), is the function code of data
	// accesses, rather than the one SR gives.
	fcOverride int
}

const atcSize = 1024

// atcEntry is a finished walk: logical page (with its function code) to
// physical page.
type atcEntry struct {
	valid    bool
	fc       uint8
	logical  uint32 // logical address >> ps
	physical uint32 // physical page base
	wp, s, m bool
}

// busFault is an access the MMU refused. It unwinds the instruction (see
// Step).
type busFault struct {
	addr  uint32
	write bool
	fc    uint8
	size  int
	fetch bool
	bus   bool // a bus error from the memory, not the MMU
}

// onBusError is the Decoder's OnBusError hook: it aborts the instruction
// with a bus error for the access.
func (c *Processor) onBusError(addr, n int, write bool) {
	panic(&busFault{addr: uint32(addr), write: write, fc: c.functionCode(), size: n, fetch: c.M.Fetch, bus: true})
}

// MMU registers, for diagnostics and save states.
func (c *Processor) MMUState() (tc uint32, crp, srp uint64, tt0, tt1 uint32, mmusr uint16) {
	m := &c.mmu
	return m.tc, m.crp, m.srp, m.tt[0], m.tt[1], m.mmusr
}

// SetMMUState restores the MMU registers (the ATC starts empty).
func (c *Processor) SetMMUState(tc uint32, crp, srp uint64, tt0, tt1 uint32, mmusr uint16) {
	m := &c.mmu
	m.crp, m.srp, m.tt, m.mmusr = crp, srp, [2]uint32{tt0, tt1}, mmusr
	c.setTC(tc)
}

func (c *Processor) setTC(tc uint32) {
	m := &c.mmu
	m.tc = tc
	m.enabled = tc&0x80000000 != 0
	m.sre = tc&0x02000000 != 0
	m.fcl = tc&0x01000000 != 0
	m.ps = uint(tc >> 20 & 0xF)
	m.is = uint(tc >> 16 & 0xF)
	for i := range m.ti {
		m.ti[i] = uint(tc >> (12 - 4*i) & 0xF)
	}
	c.flushATC()
	c.updateTranslation()
}

// tcValid reports whether TC describes a usable translation: a page of at
// least 256 bytes and the fields adding up to 32 bits.
func (m *mmu030) tcValid() bool {
	if m.ps < 8 {
		return false
	}
	sum := m.is + m.ps
	for _, n := range m.ti {
		if n == 0 {
			break
		}
		sum += n
	}
	return sum == 32
}

func (c *Processor) flushATC() { c.mmu.atc = [atcSize]atcEntry{} }

// updateTranslation installs the Decoder's translation hook while the
// MMU is on.
func (c *Processor) updateTranslation() {
	c.M.Translate = c.translateOrNil()
}

// functionCode is the function code of an access: 1 user data, 2 user
// program, 5 supervisor data, 6 supervisor program.
func (c *Processor) functionCode() uint8 {
	if c.mmu.fcOverride != 0 && !c.M.Fetch {
		return uint8(c.mmu.fcOverride & 7)
	}
	fc := uint8(1)
	if c.M.Fetch {
		fc = 2
	}
	if c.SR&StatusSupervisor != 0 {
		fc |= 4
	}
	return fc
}

// ttMatch reports whether TT0 or TT1 maps the access transparently.
func (m *mmu030) ttMatch(addr uint32, fc uint8, write bool) bool {
	for _, tt := range m.tt {
		if tt&0x8000 == 0 {
			continue
		}
		base, mask := tt>>24, tt>>16&0xFF
		if (addr>>24^base)&^mask&0xFF != 0 {
			continue
		}
		fcBase, fcMask := uint8(tt>>4&7), uint8(tt&7)
		if (fc^fcBase)&^fcMask&7 != 0 {
			continue
		}
		if tt&0x100 == 0 && (tt&0x200 != 0) == write { // R/W must match
			continue
		}
		return true
	}
	return false
}

// translate is the Decoder's hook: the physical address of an access of n
// bytes at addr. It panics with a busFault if the MMU refuses it.
func (c *Processor) translate(addr, n int, write bool) int {
	m := &c.mmu
	la := uint32(addr)
	fc := c.functionCode()
	if fc == 7 || m.ttMatch(la, fc, write) {
		return addr
	}
	page := la >> m.ps
	e := &m.atc[(page^uint32(fc)<<7)%atcSize]
	if !e.valid || e.logical != page || e.fc != fc || write && !e.m && !e.wp {
		w := c.walk(la, fc, write, true, 7)
		if w.fault() {
			panic(&busFault{addr: la, write: write, fc: fc, size: n, fetch: c.M.Fetch})
		}
		*e = atcEntry{valid: true, fc: fc, logical: page, physical: w.phys &^ (1<<m.ps - 1), wp: w.wp, s: w.s, m: w.m}
	}
	if write && e.wp || e.s && fc&4 == 0 {
		panic(&busFault{addr: la, write: write, fc: fc, size: n, fetch: c.M.Fetch})
	}
	return int(e.physical | la&(1<<m.ps-1))
}

// walkResult is the outcome of a table walk.
type walkResult struct {
	phys                    uint32
	wp, s, m                bool
	invalid, limit, buserr  bool
	levels                  int
	descAddr                uint32 // the last descriptor read
	supervisorViolation     bool
	writeProtectedViolation bool
}

func (w *walkResult) fault() bool { return w.invalid || w.limit || w.buserr }

// physRead and physWrite access the tables, which are in physical memory.
func (c *Processor) physLong(addr uint32) uint32 {
	var b [4]byte
	c.M.M.Read(int(addr), b[:])
	return binary.BigEndian.Uint32(b[:])
}

func (c *Processor) physSetLong(addr, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	c.M.M.Write(int(addr), b[:])
}

// walk translates la through the tables, setting U and M bits if update.
// maxLevels limits the levels searched (PTEST).
func (c *Processor) walk(la uint32, fc uint8, write, update bool, maxLevels int) (w walkResult) {
	m := &c.mmu
	root := m.crp
	if m.sre && fc&4 != 0 {
		root = m.srp
	}
	dt := uint32(root>>32) & 3
	table := uint32(root) &^ 0xF
	lu, limit, hasLimit := root>>63&1 != 0, uint32(root>>48&0x7FFF), true
	bit := 32 - m.is // address bits left above this point

	// The indexes: the function code first with FCL, then TIA-TID up to
	// the first zero.
	type level struct {
		idx  uint32
		bits uint
	}
	var lv [5]level
	nl := 0
	if m.fcl {
		lv[nl] = level{uint32(fc), 0}
		nl++
	}
	for _, n := range m.ti {
		if n == 0 {
			break
		}
		bit -= n
		lv[nl] = level{la >> bit & (1<<n - 1), n}
		nl++
	}
	offsetBits := func(i int) uint { // logical bits below level i's index
		b := 32 - m.is
		for j := 0; j <= i; j++ {
			b -= lv[j].bits
		}
		return b
	}
	page := func(addr uint32, below uint) uint32 {
		return addr&^(1<<below-1) | la&(1<<below-1)
	}

	if dt == 0 {
		w.invalid = true
		return
	}
	if dt == 1 { // the root is a page descriptor: no tables at all
		w.phys = page(table, 32-m.is)
		return
	}
	for i := 0; i < nl; i++ {
		if w.levels >= maxLevels {
			return
		}
		idx := lv[i].idx
		if hasLimit && (lu && idx < limit || !lu && idx > limit) {
			w.limit = true
			return
		}
		long := dt == 3
		size := uint32(4)
		if long {
			size = 8
		}
		da := table + idx*size
		w.descAddr = da
		w.levels++
		hi := c.physLong(da)
		lo := hi
		if long {
			lo = c.physLong(da + 4)
		}
		ndt := hi & 3
		if ndt == 0 {
			w.invalid = true
			return
		}
		w.wp = w.wp || hi&4 != 0
		if long {
			w.s = w.s || hi&0x100 != 0
		}
		last := i == nl-1
		if ndt != 1 && last {
			// An indirect descriptor: the page descriptor is elsewhere.
			if update && hi&8 == 0 {
				c.physSetLong(da, hi|8)
			}
			ia := lo &^ 3
			if !long {
				ia = hi &^ 3
			}
			long = ndt == 3
			da = ia
			w.descAddr = da
			w.levels++
			hi = c.physLong(da)
			lo = hi
			if long {
				lo = c.physLong(da + 4)
			}
			if hi&3 != 1 {
				w.invalid = true
				return
			}
			w.wp = w.wp || hi&4 != 0
			if long {
				w.s = w.s || hi&0x100 != 0
			}
			ndt = 1
		}
		if ndt == 1 { // a page descriptor, at the last level or early
			nhi := hi | 8
			if write && !w.wp {
				nhi |= 0x10
			}
			if update && nhi != hi {
				c.physSetLong(da, nhi)
				hi = nhi
			}
			w.m = hi&0x10 != 0
			addr := hi &^ 0xFF
			if long {
				addr = lo &^ 0xFF
			}
			w.phys = page(addr, offsetBits(i))
			return
		}
		// A table descriptor.
		if update && hi&8 == 0 {
			c.physSetLong(da, hi|8)
		}
		if long {
			lu, limit, hasLimit = hi>>31 != 0, hi>>16&0x7FFF, true
			table = lo &^ 0xF
		} else {
			hasLimit = false
			table = hi &^ 0xF
		}
		dt = ndt
	}
	w.invalid = true // ran out of levels without a page descriptor
	return
}

// ---- Bus errors ----

// raiseBusError030 takes the bus error for f with a format $B (long bus
// cycle fault) frame. The registers have been restored to the start of
// the instruction, so the stacked PC is the instruction's own and RTE runs
// it again.
func (c *Processor) raiseBusError030(f *busFault) (err error) {
	if c.OnException != nil {
		c.OnException(VecBusError, c.instrAddr, c.instrAddr)
	}
	oldSR := uint16(c.SR)
	c.runState = RunStateRunning
	if c.SR&StatusSupervisor == 0 {
		c.USP = c.A[7]
		c.A[7] = c.SSP
		c.SR |= StatusSupervisor
	}
	c.SR &^= StatusTrace
	ssw := uint16(f.fc)
	stageB := uint32(0)
	if f.fetch {
		ssw |= 0x4000 | 0x1000 // FB, RB: the fault is in pipe stage B
		stageB = f.addr
	} else {
		ssw |= 0x0100 // DF
		if !f.write {
			ssw |= 0x0040 // RW: read
		}
		switch f.size {
		case 1:
			ssw |= 0x10
		case 2:
			ssw |= 0x20
		}
	}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(*busFault); !ok {
				panic(r)
			}
			c.runState = RunStateStopped // a fault building the frame: double fault
			err = nil
		}
	}()
	c.M.Translate = c.translateOrNil()
	// From the top of the frame down (92 bytes).
	for _, v := range []struct {
		v    uint32
		size int
	}{
		{0, 4}, {0, 4}, {0, 4}, {0, 4}, {0, 4}, {0, 4}, {0, 4}, {0, 4}, {0, 4}, // internal registers ($38-$5B)
		{0, 2},         // version and internal information ($36)
		{0, 2}, {0, 4}, // internal registers ($30-$35)
		{0, 4},         // data input buffer ($2C)
		{0, 4},         // internal registers ($28)
		{stageB, 4},    // stage B address ($24)
		{0, 4}, {0, 4}, // internal registers ($1C)
		{0, 4},            // data output buffer ($18)
		{0, 4},            // internal registers ($14)
		{f.addr, 4},       // data cycle fault address ($10)
		{0, 2},            // instruction pipe stage B ($0E)
		{uint32(c.op), 2}, // instruction pipe stage C ($0C)
		{uint32(ssw), 2},  // special status word ($0A)
		{0, 2},            // internal register ($08)
		{0xB000 | VecBusError*4, 2},
		{c.instrAddr, 4},
		{uint32(oldSR), 2},
	} {
		if v.size == 4 {
			err = c.push32(v.v)
		} else {
			err = c.push16(uint16(v.v))
		}
		if err != nil {
			return err
		}
	}
	pc, err := c.M.Long(int(c.VBR) + VecBusError*4)
	if err != nil {
		return err
	}
	c.PC = pc
	c.LastCycles = 50
	return nil
}

func (c *Processor) translateOrNil() func(int, int, bool) int {
	if c.is030() && c.mmu.enabled {
		return c.translate
	}
	if c.translating040() {
		return c.translate040
	}
	return nil
}

// cpuSnapshot is the register state at the start of an instruction, to
// put back if it faults.
type cpuSnapshot struct {
	d, a          [8]uint32
	sr, pc        uint32
	usp, ssp, msp uint32
}

func (c *Processor) snapshot() cpuSnapshot {
	return cpuSnapshot{c.D, c.A, c.SR, c.PC, c.USP, c.SSP, c.MSP}
}

func (c *Processor) restore(s *cpuSnapshot) {
	c.D, c.A, c.SR, c.PC, c.USP, c.SSP, c.MSP = s.d, s.a, s.sr, s.pc, s.usp, s.ssp, s.msp
}

// ---- Instructions ----

// opMMU030 implements the MMU instructions ($F000-$F03F and an extension
// word): PMOVE, PFLUSH, PFLUSHA, PLOAD and PTEST.
func opMMU030(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "pmmu", sz: noSize, n: 1}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	ea := c.op & 0x3F
	m := &c.mmu
	switch ext >> 13 {
	case 0, 2, 3: // PMOVE
		return c.pmove(t, ext, ea)
	case 1:
		if ext>>10&7 == 0 { // PLOAD
			fc, ok := c.mmuFC(ext)
			if !ok {
				return c.mmuIllegal(t)
			}
			addr, opr, err := c.eaAddr(ea, 1)
			if err != nil {
				t.err = err
				return
			}
			t.op, t.src = "pload", opr
			c.walk(addr, fc, ext&0x200 == 0, true, 7)
			c.flushATC()
			return
		}
		t.op = "pflush"
		switch ext >> 10 & 7 {
		case 1: // PFLUSHA
			t.op = "pflusha"
			c.flushATC()
		case 4, 6: // PFLUSH fc,#mask[,<ea>]
			fc, ok := c.mmuFC(ext)
			if !ok {
				return c.mmuIllegal(t)
			}
			mask := uint8(ext >> 5 & 0xF)
			var la uint32
			byEA := ext>>10&7 == 6
			if byEA {
				if la, t.src, t.err = c.eaAddr(ea, 1); t.err != nil {
					return
				}
			}
			for i := range m.atc {
				e := &m.atc[i]
				if e.valid && (e.fc^fc)&mask&7 == 0 && (!byEA || e.logical == la>>m.ps) {
					e.valid = false
				}
			}
		default:
			return c.mmuIllegal(t)
		}
		return
	case 4: // PTEST
		return c.ptest(t, ext, ea)
	}
	return c.mmuIllegal(t)
}

func (c *Processor) mmuIllegal(t stepTrace) stepTrace {
	c.PC = t.addr
	t.err = c.RaiseException(VecLine1111)
	return t
}

// mmuFC decodes the function code field of PFLUSH, PLOAD and PTEST.
func (c *Processor) mmuFC(ext uint16) (uint8, bool) {
	f := ext & 0x1F
	switch {
	case f == 0:
		return uint8(c.SFC & 7), true
	case f == 1:
		return uint8(c.DFC & 7), true
	case f&0x18 == 0x08:
		return uint8(c.D[f&7] & 7), true
	case f&0x10 != 0:
		return uint8(f & 7), true
	}
	return 0, false
}

// pmove moves an MMU register to or from memory (or, for the 32-bit
// registers, a data or address register).
func (c *Processor) pmove(t stepTrace, ext, ea uint16) stepTrace {
	t.op = "pmove"
	m := &c.mmu
	toMem := ext&0x200 != 0
	var reg string
	var size int
	switch {
	case ext>>13 == 2 && ext>>10&7 == 0:
		reg, size = "TC", 4
	case ext>>13 == 2 && ext>>10&7 == 2:
		reg, size = "SRP", 8
	case ext>>13 == 2 && ext>>10&7 == 3:
		reg, size = "CRP", 8
	case ext>>13 == 0 && ext>>10&7 == 2:
		reg, size = "TT0", 4
	case ext>>13 == 0 && ext>>10&7 == 3:
		reg, size = "TT1", 4
	case ext>>13 == 3 && ext>>10&7 == 0:
		reg, size = "MMUSR", 2
	default:
		return c.mmuIllegal(t)
	}
	var get func() uint64
	var set func(uint64)
	switch reg {
	case "TC":
		get, set = func() uint64 { return uint64(m.tc) }, func(v uint64) { c.setTC(uint32(v)) }
	case "SRP":
		get, set = func() uint64 { return m.srp }, func(v uint64) { m.srp = v }
	case "CRP":
		get, set = func() uint64 { return m.crp }, func(v uint64) { m.crp = v }
	case "TT0":
		get, set = func() uint64 { return uint64(m.tt[0]) }, func(v uint64) { m.tt[0] = uint32(v) }
	case "TT1":
		get, set = func() uint64 { return uint64(m.tt[1]) }, func(v uint64) { m.tt[1] = uint32(v) }
	case "MMUSR":
		get, set = func() uint64 { return uint64(m.mmusr) }, func(v uint64) { m.mmusr = uint16(v) }
	}
	// Register direct is allowed for the 32-bit and 16-bit registers.
	var addr uint32
	var opr string
	mode := ea >> 3 & 7
	if mode <= 1 {
		if size == 8 {
			return c.mmuIllegal(t)
		}
		r := &c.D[ea&7]
		if c.TraceWriter != nil {
			opr = fmt.Sprintf("D%d", ea&7)
		}
		if mode == 1 {
			r, opr = &c.A[ea&7], c.sprintf("A%d", ea&7)
		}
		if toMem {
			*r = uint32(get())
		} else {
			set(uint64(*r))
		}
	} else {
		var err error
		if addr, opr, err = c.eaAddr(ea, size); err != nil {
			t.err = err
			return t
		}
		if toMem {
			v := get()
			switch size {
			case 8:
				binary.BigEndian.PutUint64(c.buf[:8], v)
			case 4:
				binary.BigEndian.PutUint32(c.buf[:4], uint32(v))
			case 2:
				binary.BigEndian.PutUint16(c.buf[:2], uint16(v))
			}
			if _, err := c.M.Write(int(addr), c.buf[:size]); err != nil {
				t.err = err
				return t
			}
		} else {
			if _, err := c.M.Read(int(addr), c.buf[:size]); err != nil {
				t.err = err
				return t
			}
			var v uint64
			switch size {
			case 8:
				v = binary.BigEndian.Uint64(c.buf[:8])
			case 4:
				v = uint64(binary.BigEndian.Uint32(c.buf[:4]))
			case 2:
				v = uint64(binary.BigEndian.Uint16(c.buf[:2]))
			}
			// A root pointer with descriptor type 0, or a TC that enables a
			// translation that doesn't add up, is a configuration error.
			if (reg == "SRP" || reg == "CRP") && v>>32&3 == 0 {
				c.PC = t.addr
				t.err = c.RaiseException(VecMMUConfig)
				return t
			}
			set(v)
			if reg == "TC" && m.enabled && !m.tcValid() {
				c.setTC(m.tc &^ 0x80000000)
				c.PC = t.addr
				t.err = c.RaiseException(VecMMUConfig)
				return t
			}
		}
	}
	if !toMem && (reg == "SRP" || reg == "CRP") && ext&0x100 == 0 {
		c.flushATC()
	}
	if toMem {
		t.src, t.dst = reg, opr
	} else {
		t.src, t.dst = opr, reg
	}
	return t
}

// VecMMUConfig is the MMU configuration error exception.
const VecMMUConfig = 56

// ptest searches the translation of <ea> and sets MMUSR.
func (c *Processor) ptest(t stepTrace, ext, ea uint16) stepTrace {
	t.op = "ptest"
	fc, ok := c.mmuFC(ext)
	if !ok {
		return c.mmuIllegal(t)
	}
	la, opr, err := c.eaAddr(ea, 1)
	if err != nil {
		t.err = err
		return t
	}
	t.src = opr
	m := &c.mmu
	write := ext&0x200 == 0
	level := int(ext >> 10 & 7)
	var sr uint16
	if m.ttMatch(la, fc, write) {
		sr = 0x0040 // T
	} else {
		maxLevels := level
		if level == 0 {
			maxLevels = 7 // the ATC: as the walk that would fill it
		}
		w := c.walk(la, fc, write, false, maxLevels)
		if w.buserr {
			sr |= 0x8000
		}
		if w.limit {
			sr |= 0x4000
		}
		if w.s && fc&4 == 0 {
			sr |= 0x2000
		}
		if w.wp {
			sr |= 0x0800
		}
		if w.invalid {
			sr |= 0x0400
		}
		if w.m {
			sr |= 0x0200
		}
		if level != 0 {
			sr |= uint16(w.levels & 7)
		}
		if ext&0x100 != 0 { // A: the last descriptor's address to An
			c.A[ext>>5&7] = w.descAddr
		}
	}
	m.mmusr = sr
	return t
}
