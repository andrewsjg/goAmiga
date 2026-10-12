package m68k

// The 68000 fetches instruction words ahead of the instruction it is
// running: as an instruction starts, its first two words are already in
// the prefetch queue, and it fetches the words after it as it goes, so
// that by its end the next instruction's first two words are in the queue.
// Most instructions make that final prefetch before writing their result
// (the bus sequence of EOR Dn,(An) is read, prefetch, write), so an
// instruction that modifies the code just ahead of it is not seen to: the
// next instruction runs from the queue. Copy protection uses that. SDI's
// decryption loop
//
//	loop: MOVE.W (A0)+,D0
//	      EOR.W  D0,(A1)+  ; first pass: decrypts the DBRA's displacement
//	      DBRA   D1,loop   ; branches with the encrypted displacement
//
// branches elsewhere on a real 68000.
//
// The emulation records what an instruction overwrites in the five words
// after its first (the longest instruction is five words, and the queue
// holds two more) and, if it continued in sequence, gives the next
// instruction the old contents of its first two words. A word that was
// already in the queue (fetched by an earlier instruction) carries over.
// MOVE writes before its final prefetch (except to -(An)), so the second
// word comes from memory after a MOVE.

type prefetchState struct {
	in, out prefetchQueue // queue for this instruction; for the next one

	// Bytes this instruction overwrote at base..base+11, before writing.
	base uint32
	old  [12]byte
	have uint16 // which bytes of old are set
}

type prefetchQueue struct {
	addr  uint32 // address of w[0]
	w     [2]uint16
	valid [2]bool
}

// prefetchBegin starts an instruction at PC.
func (c *Processor) prefetchBegin() {
	if !c.Prefetch || c.is020() {
		c.M.OnWrite = nil
		return
	}
	p := &c.pf
	p.in, p.out = p.out, prefetchQueue{}
	p.base, p.have = c.PC+2, 0
	c.M.OnWrite = c.prefetchWrite
}

// prefetchWrite keeps the bytes a write is about to replace in the words
// after the instruction.
func (c *Processor) prefetchWrite(addr, n int) {
	p := &c.pf
	for i := 0; i < n; i++ {
		off := addr + i - int(p.base)
		if off < 0 || off >= len(p.old) || p.have&(1<<off) != 0 {
			continue
		}
		var b [1]byte
		if _, err := c.M.M.Read(addr+i, b[:]); err == nil {
			p.old[off] = b[0]
			p.have |= 1 << off
		}
	}
}

// prefetchEnd fills the queue for the next instruction, at PC.
func (c *Processor) prefetchEnd() {
	if !c.Prefetch || c.is020() {
		return
	}
	p := &c.pf
	next := c.PC
	q := prefetchQueue{addr: next}
	if c.tim.trapped {
		p.out = q
		return
	}
	isMove := c.op>>12 >= 1 && c.op>>12 <= 3 && c.op>>6&7 != 4
	for k := 0; k < 2; k++ {
		a := next + uint32(2*k)
		if k == 0 && p.in.valid[1] && p.in.addr+2 == a {
			q.w[0], q.valid[0] = p.in.w[1], true // fetched before this instruction
			continue
		}
		if k == 1 && isMove {
			break
		}
		off := int(a) - int(p.base)
		if off < 0 || off+1 >= len(p.old) || p.have>>off&3 == 0 {
			continue
		}
		var cur [2]byte
		c.M.M.Read(int(a), cur[:])
		for j := 0; j < 2; j++ {
			if p.have>>(off+j)&1 != 0 {
				cur[j] = p.old[off+j]
			}
		}
		q.w[k], q.valid[k] = uint16(cur[0])<<8|uint16(cur[1]), true
	}
	p.out = q
}

// fetchWord reads the instruction word at addr: from the prefetch queue
// when it holds an older copy, otherwise from memory.
func (c *Processor) fetchWord(addr uint32) (uint16, error) {
	if q := &c.pf.in; c.Prefetch {
		for k := 0; k < 2; k++ {
			if q.valid[k] && addr == q.addr+uint32(2*k) {
				c.M.Accesses++
				return q.w[k], nil
			}
		}
	}
	c.M.Fetch = true
	w, err := c.M.Word(int(addr))
	c.M.Fetch = false
	return w, err
}

// fetchLong reads two instruction words at addr.
func (c *Processor) fetchLong(addr uint32) (uint32, error) {
	if !c.Prefetch || !c.pf.in.valid[0] && !c.pf.in.valid[1] {
		c.M.Fetch = true
		l, err := c.M.Long(int(addr))
		c.M.Fetch = false
		return l, err
	}
	hi, err := c.fetchWord(addr)
	if err != nil {
		return 0, err
	}
	lo, err := c.fetchWord(addr + 2)
	return uint32(hi)<<16 | uint32(lo), err
}
