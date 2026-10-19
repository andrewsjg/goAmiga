package m68k

import (
	"encoding/binary"
	"fmt"
)

// The 68040. Model68040 is the 68030's user instruction set with:
//
//   - The 040's own MMU (below), in place of the 030's: TC (enable, 4 or
//     8 KB pages), URP and SRP, the transparent translation registers
//     ITT0/ITT1 (instruction fetches) and DTT0/DTT1 (data), and MMUSR,
//     all reached with MOVEC. The 030's MMU instructions (PMOVE, PLOAD,
//     and its PFLUSH and PTEST) take the F-line exception, which is how
//     software tells the two apart; the 040 has PFLUSH (An), PFLUSHN (An),
//     PFLUSHA, PFLUSHAN, PTESTR (An) and PTESTW (An) instead.
//   - CACR with only the data and instruction cache enables (bits 31, 15).
//     The caches aren't emulated, so CINV and CPUSH do nothing, and memory
//     is always coherent.
//   - MOVE16, which copies a 16-byte line.
//   - Exception frames: format $0, $1, $2 (with address errors among
//     them), $3 (floating-point post-instruction) and $7 (access error).
//     CAAR is gone, and so are the 68020's coprocessor interface and
//     format $9/$A/$B frames.
//   - A floating-point unit on the chip (FPU68040, fpu.go). The real 040
//     traps the transcendental functions and some data types to software
//     (68040.library); here every instruction is done in hardware, so
//     programs run whether or not the library is loaded. FSAVE gives the
//     040's frames.
//
// The table walk is fixed: from URP (user) or SRP (supervisor), a root
// table indexed by address bits 31-25, a pointer table by bits 24-18, and
// a page table by bits 17-12 (4 KB pages) or 17-13 (8 KB), all with
// four-byte descriptors. Page descriptors may be indirect. The walk sets
// U bits on the way, and the page's M bit before a write; a W bit at any
// level protects the page and S makes it supervisor-only.
//
// A refused access is an access error (vector 2) with a format $7 frame.
// As for the 030 (mmu030.go), the instruction is undone and restarted by
// RTE, so the frame never holds pending write-backs: a handler that
// fixes the page and returns sees the instruction run again.

// Model68040 is the 68040.
const Model68040 Model = 3

func (c *Processor) is040() bool { return c.Model >= Model68040 }

// mmu040 is the 68040's MMU registers and its translation caches.
type mmu040 struct {
	tc            uint32 // E (bit 15), P (bit 14)
	urp, srp      uint32
	itt, dtt      [2]uint32
	mmusr         uint32
	enabled       bool
	ps            uint // page shift: 12 or 13
	iatc, datc    [atc040Size]atc040Entry
	pageMask      uint32
	ttWriteProt   bool // a TT register write-protects its block
	anyTT         bool // a TT register is enabled
	translationOn bool // the Decoder hook is installed
}

const atc040Size = 256

type atc040Entry struct {
	valid    bool
	super    bool
	logical  uint32 // logical address >> ps
	physical uint32 // physical page base
	wp, s, m bool
	g        bool
}

// MMU040State is the 68040's MMU registers, for diagnostics and save
// states.
type MMU040State struct {
	TC, URP, SRP, ITT0, ITT1, DTT0, DTT1, MMUSR uint32
}

func (c *Processor) MMU040State() MMU040State {
	m := &c.mmu4
	return MMU040State{m.tc, m.urp, m.srp, m.itt[0], m.itt[1], m.dtt[0], m.dtt[1], m.mmusr}
}

// SetMMU040State restores the registers (the ATCs start empty).
func (c *Processor) SetMMU040State(s MMU040State) {
	m := &c.mmu4
	m.tc, m.urp, m.srp, m.mmusr = s.TC, s.URP, s.SRP, s.MMUSR
	m.itt, m.dtt = [2]uint32{s.ITT0, s.ITT1}, [2]uint32{s.DTT0, s.DTT1}
	c.flushATC040(func(*atc040Entry) bool { return true })
	c.mmu040Changed()
}

// mmu040Changed recomputes what follows from the registers. Unlike the
// 030, loading TC, URP or SRP doesn't flush the ATCs: software issues
// PFLUSHA itself.
func (c *Processor) mmu040Changed() {
	m := &c.mmu4
	m.enabled = m.tc&0x8000 != 0
	m.ps = 12
	if m.tc&0x4000 != 0 {
		m.ps = 13
	}
	m.pageMask = 1<<m.ps - 1
	m.anyTT, m.ttWriteProt = false, false
	for _, tt := range append(m.itt[:], m.dtt[:]...) {
		if tt&0x8000 != 0 {
			m.anyTT = true
			if tt&4 != 0 {
				m.ttWriteProt = true
			}
		}
	}
	c.updateTranslation()
}

// translating040 reports whether accesses need the MMU: translation is
// on, or a transparent block is write-protected.
func (c *Processor) translating040() bool {
	return c.is040() && (c.mmu4.enabled || c.mmu4.ttWriteProt)
}

// ttMatch040 reports whether one of a pair of TT registers maps the
// access transparently, and whether that block is write-protected.
func ttMatch040(tts *[2]uint32, addr uint32, super bool) (hit, wp bool) {
	for _, tt := range tts {
		if tt&0x8000 == 0 {
			continue
		}
		base, mask := tt>>24, tt>>16&0xFF
		if (addr>>24^base)&^mask&0xFF != 0 {
			continue
		}
		switch tt >> 13 & 3 {
		case 0: // user accesses only
			if super {
				continue
			}
		case 1: // supervisor only
			if !super {
				continue
			}
		}
		return true, tt&4 != 0
	}
	return false, false
}

func (m *mmu040) atcFor(fetch bool) *[atc040Size]atc040Entry {
	if fetch {
		return &m.iatc
	}
	return &m.datc
}

// translate040 is the Decoder's hook on the 040.
func (c *Processor) translate040(addr, n int, write bool) int {
	m := &c.mmu4
	la := uint32(addr)
	fc := c.functionCode()
	if fc == 7 { // CPU space
		return addr
	}
	super := fc&4 != 0
	fetch := fc&3 == 2
	tts := &m.dtt
	if fetch {
		tts = &m.itt
	}
	if hit, wp := ttMatch040(tts, la, super); hit {
		if write && wp {
			panic(&busFault{addr: la, write: write, fc: fc, size: n, fetch: fetch})
		}
		return addr
	}
	if !m.enabled {
		return addr
	}
	page := la >> m.ps
	atc := m.atcFor(fetch)
	idx := page % atc040Size
	if super {
		idx = (page ^ 0x80) % atc040Size
	}
	e := &atc[idx]
	if !e.valid || e.logical != page || e.super != super || write && !e.m && !e.wp {
		w := c.walk040(la, super, write, true)
		if w.invalid {
			panic(&busFault{addr: la, write: write, fc: fc, size: n, fetch: fetch})
		}
		*e = atc040Entry{valid: true, super: super, logical: page, physical: w.phys &^ m.pageMask,
			wp: w.wp, s: w.s, m: w.m, g: w.g}
	}
	if write && e.wp || e.s && !super {
		panic(&busFault{addr: la, write: write, fc: fc, size: n, fetch: fetch})
	}
	return int(e.physical | la&m.pageMask)
}

// walk040Result is the outcome of a 68040 table walk.
type walk040Result struct {
	phys          uint32
	wp, s, m, g   bool
	u0, u1        bool
	cm            uint32
	invalid       bool
	pageDescFound bool
}

// walk040 searches the tables for la, setting U bits (and the page's M
// bit for a write) if update.
func (c *Processor) walk040(la uint32, super, write, update bool) (w walk040Result) {
	m := &c.mmu4
	root := m.urp
	if super {
		root = m.srp
	}
	// The root and pointer levels: table descriptors.
	table := root &^ 0x1FF
	for level, idx := range []uint32{la >> 25, la >> 18 & 0x7F} {
		da := table | idx<<2
		d := c.physLong(da)
		if d&2 == 0 { // UDT 00 or 01: invalid
			w.invalid = true
			return
		}
		w.wp = w.wp || d&4 != 0
		if update && d&8 == 0 {
			c.physSetLong(da, d|8)
		}
		if level == 0 {
			table = d &^ 0x1FF
		} else if m.ps == 12 {
			table = d &^ 0xFF
		} else {
			table = d &^ 0x7F
		}
	}
	// The page level.
	pgi := la >> 12 & 0x3F
	if m.ps == 13 {
		pgi = la >> 13 & 0x1F
	}
	da := table | pgi<<2
	d := c.physLong(da)
	switch d & 3 {
	case 0:
		w.invalid = true
		return
	case 2: // indirect: the page descriptor is elsewhere
		da = d &^ 3
		d = c.physLong(da)
		if d&1 == 0 { // invalid, or another indirect
			w.invalid = true
			return
		}
	}
	w.pageDescFound = true
	w.wp = w.wp || d&4 != 0
	w.s = d&0x80 != 0
	if update {
		nd := d | 8
		if write && !w.wp && !(w.s && !super) {
			nd |= 0x10
		}
		if nd != d {
			c.physSetLong(da, nd)
			d = nd
		}
	}
	w.m = d&0x10 != 0
	w.g = d&0x400 != 0
	w.u0, w.u1 = d&0x100 != 0, d&0x200 != 0
	w.cm = d >> 5 & 3
	w.phys = d&^m.pageMask | la&m.pageMask
	return
}

func (c *Processor) flushATC040(match func(*atc040Entry) bool) {
	m := &c.mmu4
	for _, atc := range []*[atc040Size]atc040Entry{&m.iatc, &m.datc} {
		for i := range atc {
			if atc[i].valid && match(&atc[i]) {
				atc[i].valid = false
			}
		}
	}
}

// ---- Control registers ----

// controlReg040 is MOVEC's register n on the 040: a pointer to it, the
// bits that exist, and whether it exists at all. CAAR doesn't.
func (c *Processor) controlReg040(n uint16) (*uint32, uint32, bool) {
	m := &c.mmu4
	switch n {
	case 0x002:
		return &c.CACR, 0x80008000, true
	case 0x003:
		return &m.tc, 0xC000, true
	case 0x004:
		return &m.itt[0], 0xFFFFE364, true
	case 0x005:
		return &m.itt[1], 0xFFFFE364, true
	case 0x006:
		return &m.dtt[0], 0xFFFFE364, true
	case 0x007:
		return &m.dtt[1], 0xFFFFE364, true
	case 0x802:
		return nil, 0, false
	case 0x805:
		return &m.mmusr, 0xFFFFFFF7, true
	case 0x806:
		return &m.urp, 0xFFFFFE00, true
	case 0x807:
		return &m.srp, 0xFFFFFE00, true
	}
	return c.controlReg020(n)
}

// ---- Instructions ----

// opcodes040 are the 68040's $F4xx-$F6xx instructions.
func dispatch040(op uint16) stepFunc {
	switch {
	case op&0xFF00 == 0xF400 && op&0x18 != 0:
		return opCache040
	case op&0xFFE0 == 0xF500:
		return opPflush040
	case op&0xFFD8 == 0xF548:
		return opPtest040
	case op&0xFFF8 == 0xF620, op&0xFFE0 == 0xF600:
		return opMove16
	}
	return nil
}

// opCache040 implements CINV and CPUSH ($F400-$F4FF): with no caches
// emulated there is nothing to invalidate or push.
func opCache040(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "cinv", sz: noSize, n: 1}
	if c.op&0x20 != 0 {
		t.op = "cpush"
	}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	t.src = [4]string{"nc", "dc", "ic", "bc"}[c.op>>6&3]
	return
}

// opPflush040 implements PFLUSHN (An), PFLUSH (An), PFLUSHAN and PFLUSHA
// ($F500-$F51F). The (An) forms flush the page at An for the function
// code in DFC; the N forms leave global entries.
func opPflush040(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "pflush", sz: noSize, n: 1}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	m := &c.mmu4
	opmode := c.op >> 3 & 3
	nonGlobal := opmode&1 == 0
	if opmode >= 2 {
		t.op = [2]string{"pflushan", "pflusha"}[opmode&1]
		c.flushATC040(func(e *atc040Entry) bool { return !nonGlobal || !e.g })
		return
	}
	if nonGlobal {
		t.op = "pflushn"
	}
	reg := c.op & 7
	t.src = fmt.Sprintf("(A%d)", reg)
	page := c.A[reg] >> m.ps
	super := c.DFC&4 != 0
	c.flushATC040(func(e *atc040Entry) bool {
		return e.logical == page && e.super == super && (!nonGlobal || !e.g)
	})
	return
}

// opPtest040 implements PTESTW (An) ($F548) and PTESTR (An) ($F568): it
// searches the tables for An with DFC's function code, puts what it
// finds in MMUSR, and loads the data ATC.
func opPtest040(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "ptestw", sz: noSize, n: 1}
	write := c.op&0x20 == 0
	if !write {
		t.op = "ptestr"
	}
	if ok, err := c.checkPrivilege(t.addr); !ok {
		t.err = err
		return
	}
	c.PC += 2
	m := &c.mmu4
	reg := c.op & 7
	t.src = fmt.Sprintf("(A%d)", reg)
	la := c.A[reg]
	fc := uint8(c.DFC & 7)
	super := fc&4 != 0
	tts := &m.dtt
	if fc&3 == 2 {
		tts = &m.itt
	}
	if hit, _ := ttMatch040(tts, la, super); hit {
		m.mmusr = 3 // T, R
		return
	}
	w := c.walk040(la, super, write, true)
	if w.invalid {
		m.mmusr = 0
		return
	}
	s := w.phys&0xFFFFF000 | 1 // R
	for _, b := range []struct {
		set bool
		bit uint32
	}{{w.g, 0x400}, {w.u1, 0x200}, {w.u0, 0x100}, {w.s, 0x80}, {w.m, 0x10}, {w.wp, 4}} {
		if b.set {
			s |= b.bit
		}
	}
	m.mmusr = s | w.cm<<5
	page := la >> m.ps
	idx := page % atc040Size
	if super {
		idx = (page ^ 0x80) % atc040Size
	}
	m.datc[idx] = atc040Entry{valid: true, super: super, logical: page, physical: w.phys &^ m.pageMask,
		wp: w.wp, s: w.s, m: w.m, g: w.g}
	return
}

// opMove16 implements MOVE16, the 16-byte line copy: (Ax)+,(Ay)+
// ($F620 and an extension word) and the forms with an absolute address
// ($F600-$F61F). The addresses' low four bits are ignored.
func opMove16(c *Processor) (t stepTrace) {
	t = stepTrace{addr: c.PC, op: "move16", sz: noSize, n: 1}
	c.PC += 2
	var src, dst uint32
	if c.op&0xFFF8 == 0xF620 {
		ext, _, err := c.readImmWord()
		if err != nil {
			t.err = err
			return
		}
		if ext&0x8FFF != 0x8000 {
			c.PC = t.addr
			t.err = c.lineF()
			return
		}
		ax, ay := c.op&7, ext>>12&7
		src, dst = c.A[ax], c.A[ay]
		c.A[ax] += 16
		if ay != ax {
			c.A[ay] += 16
		}
		t.src, t.dst = fmt.Sprintf("(A%d)+", ax), fmt.Sprintf("(A%d)+", ay)
	} else {
		abs, _, err := c.readImmLong()
		if err != nil {
			t.err = err
			return
		}
		ay := c.op & 7
		an := c.A[ay]
		switch c.op >> 3 & 3 {
		case 0: // (Ay)+,abs
			src, dst = an, abs
			c.A[ay] += 16
			t.src, t.dst = fmt.Sprintf("(A%d)+", ay), fmt.Sprintf("$%X", abs)
		case 1: // abs,(Ay)+
			src, dst = abs, an
			c.A[ay] += 16
			t.src, t.dst = fmt.Sprintf("$%X", abs), fmt.Sprintf("(A%d)+", ay)
		case 2: // (Ay),abs
			src, dst = an, abs
			t.src, t.dst = fmt.Sprintf("(A%d)", ay), fmt.Sprintf("$%X", abs)
		case 3: // abs,(Ay)
			src, dst = abs, an
			t.src, t.dst = fmt.Sprintf("$%X", abs), fmt.Sprintf("(A%d)", ay)
		}
	}
	var line [16]byte
	if _, t.err = c.M.Read(int(src&^15), line[:]); t.err != nil {
		return
	}
	_, t.err = c.M.Write(int(dst&^15), line[:])
	return
}

// ---- Exceptions ----

// raiseAccessError040 takes the access error for f with a format $7
// frame. The registers have been restored to the start of the
// instruction, which RTE runs again; no write-backs are pending.
func (c *Processor) raiseAccessError040(f *busFault) (err error) {
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
	ssw := uint16(f.fc) // TT normal; TM the function code
	if !f.bus {
		ssw |= 0x0400 // ATC: the MMU refused it (else a bus error)
	}
	if !f.write {
		ssw |= 0x0100 // RW: read
	}
	switch f.size {
	case 1:
		ssw |= 0x20
	case 2:
		ssw |= 0x40
	case 16:
		ssw |= 0x60
	}
	if f.addr&^c.mmu4.pageMask != (f.addr+uint32(max(f.size, 1))-1)&^c.mmu4.pageMask {
		ssw |= 0x0800 // MA: the second page of a misaligned access
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
	var frame [60]byte
	be := binary.BigEndian
	be.PutUint16(frame[0x00:], oldSR)
	be.PutUint32(frame[0x02:], c.instrAddr)
	be.PutUint16(frame[0x06:], 0x7000|VecBusError*4)
	be.PutUint16(frame[0x0C:], ssw)
	be.PutUint32(frame[0x14:], f.addr)
	c.A[7] -= uint32(len(frame))
	if _, err = c.M.Write(int(c.A[7]), frame[:]); err != nil {
		return err
	}
	pc, err := c.M.Long(int(c.VBR) + VecBusError*4)
	if err != nil {
		return err
	}
	c.PC = pc
	c.LastCycles = 50
	return nil
}

// raiseAddressError040 takes an address error (a jump to an odd address)
// with a format $2 frame: the instruction's address, and the address it
// referenced with bit 0 cleared.
func (c *Processor) raiseAddressError040(ae *addressError) error {
	if err := c.push32(ae.addr &^ 1); err != nil {
		return err
	}
	if err := c.push16(0x2000 | VecAddressError*4); err != nil {
		return err
	}
	if err := c.push32(c.instrAddr); err != nil {
		return err
	}
	if err := c.push16(uint16(ae.sr)); err != nil {
		return err
	}
	newPC, err := c.M.Long(int(c.VBR) + VecAddressError*4)
	if err != nil {
		return err
	}
	c.PC = newPC
	c.LastCycles = 50
	return nil
}
