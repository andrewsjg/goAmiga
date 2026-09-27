package m68k

import (
	"encoding/binary"
	"fmt"
)

// opBcc implements:
// - BRA
// - BSR
// - Bcc (4-25)
func opBcc(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	cc := c.op & 0x0F00 >> 8
	t.op = []string{
		"bra", // bra
		"bsr", // bsr
		"bhi", // high
		"bls", // low or same
		"bcc", // (hi) carry clear
		"bcs", // (lo) carry set
		"bne", // not equal
		"beq", // equal
		"bvc", // overflow clear
		"bvs", // overflow set
		"bpl", // plus
		"bmi", // minus
		"bge", // greater or equal
		"blt", // less than
		"bgt", // greater than
		"ble", // less or equal
	}[cc]

	// compute displacement
	var disp int32
	// An 8-bit displacement, or 0 for a 16-bit one in the next word. (On
	// the 68020 $FF introduces a 32-bit displacement; on the 68000 it is
	// just -1, an odd target that raises an address error if taken.)
	switch c.op & 0xFF {
	default: // 8-bit displacement
		disp = byteToInt32(byte(c.op))

	case 0: // 16-bit displacement
		var n uint16
		n, _, t.err = c.readImmWord()
		disp = wordToInt32(n)
	}
	if t.err != nil {
		return
	}
	t.src = fmt.Sprintf("($%X,PC)", disp)

	// BSR: push return address, then always branch
	if cc == 1 {
		c.A[7] -= 4
		binary.BigEndian.PutUint32(c.buf[:4], c.PC)
		if _, t.err = c.M.Write(int(c.A[7]), c.buf[:4]); t.err != nil {
			return
		}
		c.PC = uint32(int32(t.addr+2) + disp)
		return
	}

	// branch
	if !testCond(c, cc) {
		return
	}
	c.tim.taken = true
	c.PC = uint32((int32(t.addr+2) + disp))
	if cc == 0 && c.PC == t.addr {
		// this is a branch to self - a common pattern to terminate a program by
		// placing the processor in an infinite loop. As a convenience to users,
		// let's stop the processor.
		t.err = c.Stop()
	}
	return
}
