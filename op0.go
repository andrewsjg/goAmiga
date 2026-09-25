package m68k

import (
	"encoding/binary"
	"fmt"
)

// opMovep implements MOVEP — byte-strided register/memory transfer (4-133)
// Encoding: 0000 Dn ooo 001 An
//
//	ooo=100: MOVEP.W (d16,An),Dn (mem→reg, word)
//	ooo=101: MOVEP.L (d16,An),Dn (mem→reg, long)
//	ooo=110: MOVEP.W Dn,(d16,An) (reg→mem, word)
//	ooo=111: MOVEP.L Dn,(d16,An) (reg→mem, long)
func opMovep(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "movep",
		n:    1,
	}

	dn := (c.op >> 9) & 7
	an := c.op & 7
	mode := (c.op >> 6) & 7 // 4=W mem→reg, 5=L mem→reg, 6=W reg→mem, 7=L reg→mem

	c.PC += 2

	// Read 16-bit signed displacement
	disp, err := c.M.Sword(int(c.PC))
	if err != nil {
		t.err = err
		return
	}
	c.PC += 2

	addr := int(int32(c.A[an]) + int32(disp))

	switch mode {
	case 4: // MOVEP.W (d16,An),Dn — read 2 bytes at addr, addr+2
		t.sz = SizeWord
		t.src = fmt.Sprintf("(%d,A%d)", disp, an)
		t.dst = fmt.Sprintf("D%d", dn)
		hi, err := c.M.Byte(addr)
		if err != nil {
			t.err = err
			return t
		}
		lo, err := c.M.Byte(addr + 2)
		if err != nil {
			t.err = err
			return t
		}
		c.D[dn] = (c.D[dn] & 0xFFFF0000) | uint32(hi)<<8 | uint32(lo)

	case 5: // MOVEP.L (d16,An),Dn — read 4 bytes at addr, addr+2, addr+4, addr+6
		t.sz = SizeLong
		t.src = fmt.Sprintf("(%d,A%d)", disp, an)
		t.dst = fmt.Sprintf("D%d", dn)
		var buf [4]byte
		for i := 0; i < 4; i++ {
			b, err := c.M.Byte(addr + i*2)
			if err != nil {
				t.err = err
				return t
			}
			buf[i] = b
		}
		c.D[dn] = binary.BigEndian.Uint32(buf[:])

	case 6: // MOVEP.W Dn,(d16,An) — write 2 bytes to addr, addr+2
		t.sz = SizeWord
		t.src = fmt.Sprintf("D%d", dn)
		t.dst = fmt.Sprintf("(%d,A%d)", disp, an)
		val := uint16(c.D[dn])
		if _, err := c.M.Write(addr, []byte{byte(val >> 8)}); err != nil {
			t.err = err
			return t
		}
		if _, err := c.M.Write(addr+2, []byte{byte(val)}); err != nil {
			t.err = err
			return t
		}

	case 7: // MOVEP.L Dn,(d16,An) — write 4 bytes to addr, addr+2, addr+4, addr+6
		t.sz = SizeLong
		t.src = fmt.Sprintf("D%d", dn)
		t.dst = fmt.Sprintf("(%d,A%d)", disp, an)
		val := c.D[dn]
		bytes := [4]byte{byte(val >> 24), byte(val >> 16), byte(val >> 8), byte(val)}
		for i := 0; i < 4; i++ {
			if _, err := c.M.Write(addr+i*2, []byte{bytes[i]}); err != nil {
				t.err = err
				return t
			}
		}
	}

	return
}

// opMove implements MOVE, MOVEA and MOVEP
func opMove(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "move",
		n:    1,

		// decode unusual size encoding for move instructions
		sz: []uint16{0xFFFF, SizeByte, SizeLong, SizeWord}[(c.op&0x3000)>>12],
	}
	c.PC += 2

	src, dst := decodeEA(c.op), decodeExEA(c.op)
	if src&0x38 == 0x08 && dst&0x40 == 0x40 {
		t.op = "movep"
	}
	if dst&0x38 == 0x08 {
		t.op = "movea"
		// TODO: sign extension and drop support for bytes
	}

	// MOVEA (destination is address register) does NOT affect condition codes
	// on the real 68000. Only regular MOVE sets N, Z and clears V, C.
	isMovea := dst&0x38 == 0x08

	if !isMovea {
		c.SR &= 0xFFFFFFF0
	}

	switch t.sz {
	case SizeByte:
		var b byte
		b, t.src, t.err = c.readByte(src)
		if t.err != nil {
			break
		}
		t.dst, t.err = c.writeByte(dst, b)
		if !isMovea {
			if b == 0 {
				c.SR |= StatusZero
			}
			if b&0x80 != 0 {
				c.SR |= StatusNegative
			}
		}

	case SizeWord:
		var v uint16
		v, t.src, t.err = c.readWord(src)
		if t.err != nil {
			break
		}
		t.dst, t.err = c.writeWord(dst, v)
		if !isMovea {
			if v == 0 {
				c.SR |= StatusZero
			}
			if v&0x8000 != 0 {
				c.SR |= StatusNegative
			}
		}

	case SizeLong:
		var v uint32
		v, t.src, t.err = c.readLong(src)
		if t.err != nil {
			break
		}
		t.dst, t.err = c.writeLong(dst, v)
		if !isMovea {
			if v == 0 {
				c.SR |= StatusZero
			}
			if v&0x80000000 != 0 {
				c.SR |= StatusNegative
			}
		}
	}

	return
}

// opOri implements ORI, ORI to CCR and ORI to SR.
func opOri(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "ori",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	switch t.sz {
	default:
		t.err = errBadOpSize
		return

	case SizeByte:
		// read immediate byte
		var src, dst byte
		src, t.src, t.err = c.readImmByte()
		if t.err != nil {
			break
		}
		if ea == 0x3C { // ori.b to CCR
			c.SR |= uint32(src) & StatusMask
			t.sz = noSize
			t.dst = "CCR"
			break
		}

		// ori.b
		s := c.saveRMW(ea)
		dst, _, t.err = c.readByte(ea)
		if t.err != nil {
			break
		}
		b := dst | src
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
		c.SR &= 0xFFFFFFF0
		c.setStatusForByte(b)

	case SizeWord:
		// read immediate word
		var src, dst uint16
		src, t.src, t.err = c.readImmWord()
		if t.err != nil {
			break
		}
		if ea == 0x3C { // ori.w to SR (privileged)
			var ok bool
			ok, t.err = c.checkPrivilege(t.addr)
			if !ok {
				return
			}
			c.setSR(c.SR | uint32(src)&StatusMask)
			t.sz = noSize
			t.dst = "SR"
			break
		}

		// ori.w
		s := c.saveRMW(ea)
		dst, _, t.err = c.readWord(ea)
		if t.err != nil {
			break
		}
		v := dst | src
		c.restoreRMW(s)
		t.dst, t.err = c.writeWord(ea, v)
		c.SR &= 0xFFFFFFF0
		c.setStatusForWord(v)

	case SizeLong:
		// read immediate long
		var src, dst uint32
		src, t.src, t.err = c.readImmLong()
		if t.err != nil {
			break
		}
		s := c.saveRMW(ea)
		dst, _, t.err = c.readLong(ea)
		if t.err != nil {
			break
		}
		v := dst | src
		c.restoreRMW(s)
		t.dst, t.err = c.writeLong(ea, v)
		c.SR &= 0xFFFFFFF0
		c.setStatusForLong(v)
	}
	return
}

// opAndi implements ANDI (4-18), ANDI to CCR and ANDI to SR
func opAndi(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "andi",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)
	sr := c.SR         // cache value in case destination is status register
	c.SR &= 0xFFFFFFF0 // reset last 4 bits only
	switch t.sz {
	default:
		t.err = errBadOpSize
		return

	case SizeByte:
		// read immediate byte
		var src, dst byte
		src, t.src, t.err = c.readImmByte()

		if ea == 0x3C { // andi.b to CCR (only affects bits 0-4)
			c.SR = (sr & ^uint32(0x1F)) | (sr & uint32(src) & 0x1F)
			t.sz = noSize
			t.dst = "CCR"
			break
		}

		// andi.b
		s := c.saveRMW(ea)
		dst, _, t.err = c.readByte(ea)
		if t.err != nil {
			break
		}
		b := dst & src
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
		c.setStatusForByte(b)

	case SizeWord:
		// read immediate word
		var src, dst uint16
		src, t.src, t.err = c.readImmWord()

		if ea == 0x3C { // andi.w to SR (privileged)
			var ok bool
			ok, t.err = c.checkPrivilege(t.addr)
			if !ok {
				return
			}
			newSR := sr & uint32(src)
			c.setSR(newSR)
			t.sz = noSize
			t.dst = "SR"
			break
		}

		// andi.w
		s := c.saveRMW(ea)
		dst, _, t.err = c.readWord(ea)
		if t.err != nil {
			break
		}
		v := dst & src
		c.restoreRMW(s)
		t.dst, t.err = c.writeWord(ea, v)
		c.setStatusForWord(v)

	case SizeLong:
		// read immediate long
		var src, dst uint32
		src, t.src, t.err = c.readImmLong()

		// andi.l
		s := c.saveRMW(ea)
		dst, _, t.err = c.readLong(ea)
		if t.err != nil {
			break
		}
		v := dst & src
		c.restoreRMW(s)
		t.dst, t.err = c.writeLong(ea, v)
		c.setStatusForLong(v)
	}

	return
}

// opSubi implements SUBI
func opSubi(c *Processor) (t stepTrace) {
	// TODO: eliminate illegal address schemes
	t = stepTrace{
		addr: c.PC,
		op:   "subi",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	var a, b Int
	a, t.src, t.err = c.readImmInt(t.sz)
	if t.err != nil {
		return
	}
	s := c.saveRMW(ea)
	b, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}
	b = b.Sub(a)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
	c.SR |= b.Status()
	if b.Status()&StatusCarry != 0 {
		c.SR |= StatusExtend
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, b)
	return
}

// opAddi implements ADDI
func opAddi(c *Processor) (t stepTrace) {
	// TODO: eliminate illegal address schemes
	t = stepTrace{
		addr: c.PC,
		op:   "addi",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	var a, b Int
	a, t.src, t.err = c.readImmInt(t.sz)
	if t.err != nil {
		return
	}
	s := c.saveRMW(ea)
	b, _, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}
	b = b.Add(a)

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative | StatusExtend)
	c.SR |= b.Status()
	if b.Status()&StatusCarry != 0 {
		c.SR |= StatusExtend
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeInt(ea, b)
	return
}

// opEori implements EORI, EORI to CCR and EORI to SR.
func opEori(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "eori",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)
	switch t.sz {
	default:
		t.err = errBadOpSize
		return

	case SizeByte:
		// read immediate byte
		var src, dst byte
		src, t.src, t.err = c.readImmByte()
		if t.err != nil {
			break
		}

		if ea == 0x3C { // eori.b to CCR (only affects bits 0-4)
			c.SR = (c.SR & ^uint32(0x1F)) | ((c.SR ^ uint32(src)) & 0x1F)
			t.sz = noSize
			t.dst = "CCR"
			break
		}

		// eori.b
		s := c.saveRMW(ea)
		dst, _, t.err = c.readByte(ea)
		if t.err != nil {
			break
		}
		b := dst ^ src
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
		c.SR &= 0xFFFFFFF0
		c.setStatusForByte(b)

	case SizeWord:
		// read immediate word
		var src, dst uint16
		src, t.src, t.err = c.readImmWord()
		if t.err != nil {
			break
		}

		if ea == 0x3C { // eori.w to SR (privileged)
			var ok bool
			ok, t.err = c.checkPrivilege(t.addr)
			if !ok {
				return
			}
			c.setSR(c.SR ^ uint32(src))
			t.sz = noSize
			t.dst = "SR"
			break
		}

		// eori.w
		s := c.saveRMW(ea)
		dst, _, t.err = c.readWord(ea)
		if t.err != nil {
			break
		}
		v := dst ^ src
		c.restoreRMW(s)
		t.dst, t.err = c.writeWord(ea, v)
		c.SR &= 0xFFFFFFF0
		c.setStatusForWord(v)

	case SizeLong:
		// read immediate long
		var src, dst uint32
		src, t.src, t.err = c.readImmLong()
		if t.err != nil {
			break
		}

		// eori.l
		s := c.saveRMW(ea)
		dst, _, t.err = c.readLong(ea)
		if t.err != nil {
			break
		}
		v := dst ^ src
		c.restoreRMW(s)
		t.dst, t.err = c.writeLong(ea, v)
		c.SR &= 0xFFFFFFF0
		c.setStatusForLong(v)
	}
	return
}

// opBtst implements BTST (4-61) — dynamic bit number from register
func opBtst(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "btst",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	// read bit number from data register
	src := (c.op >> 9) & 0x7
	t.src = fmt.Sprintf("D%d", src)
	bit := c.D[src]

	// read destination value
	mod := c.op & 0x38 >> 3
	var v, width uint32
	if mod == 0 { // data register
		v, t.dst, t.err = c.readLong(c.op)
		if t.err != nil {
			return
		}
		width = 32
	} else {
		var b byte
		b, t.dst, t.err = c.readByte(c.op)
		v = uint32(b)
		width = 8
	}

	// set status bit
	bit %= width
	if v&(1<<bit) == 0 {
		c.SR |= StatusZero
	} else {
		c.SR &= ^StatusZero
	}
	return
}

// opBtstImm implements BTST #imm (4-63) — static bit number from immediate
func opBtstImm(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "btst",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	var bitNum byte
	bitNum, t.src, t.err = c.readImmByte()
	if t.err != nil {
		return
	}

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	var v uint32
	var width uint32
	if mod == 0 {
		v, t.dst, t.err = c.readLong(ea)
		width = 32
	} else {
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		v = uint32(b)
		width = 8
	}
	if t.err != nil {
		return
	}

	bit := uint32(bitNum) % width
	if v&(1<<bit) == 0 {
		c.SR |= StatusZero
	} else {
		c.SR &= ^StatusZero
	}
	return
}

// opBchgImm implements BCHG #imm (4-57)
func opBchgImm(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bchg",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	var bitNum byte
	bitNum, t.src, t.err = c.readImmByte()
	if t.err != nil {
		return
	}

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v ^= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b ^= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opBclrImm implements BCLR #imm (4-59)
func opBclrImm(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bclr",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	var bitNum byte
	bitNum, t.src, t.err = c.readImmByte()
	if t.err != nil {
		return
	}

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v &^= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b &^= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opBsetImm implements BSET #imm (4-61)
func opBsetImm(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bset",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	var bitNum byte
	bitNum, t.src, t.err = c.readImmByte()
	if t.err != nil {
		return
	}

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v |= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := uint32(bitNum) % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b |= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opBchg implements BCHG Dn (dynamic)
func opBchg(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bchg",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	src := (c.op >> 9) & 0x7
	t.src = fmt.Sprintf("D%d", src)
	bitNum := c.D[src]

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v ^= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b ^= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opBclr implements BCLR Dn (dynamic)
func opBclr(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bclr",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	src := (c.op >> 9) & 0x7
	t.src = fmt.Sprintf("D%d", src)
	bitNum := c.D[src]

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v &^= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b &^= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opBset implements BSET Dn (dynamic)
func opBset(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "bset",
		sz:   noSize,
		n:    1,
	}
	c.PC += 2

	src := (c.op >> 9) & 0x7
	t.src = fmt.Sprintf("D%d", src)
	bitNum := c.D[src]

	ea := decodeEA(c.op)
	mod := ea & 0x38 >> 3
	if mod == 0 {
		var v uint32
		v, t.dst, t.err = c.readLong(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 32
		if v&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		v |= 1 << bit
		t.dst, t.err = c.writeLong(ea, v)
	} else {
		s := c.saveRMW(ea)
		var b byte
		b, t.dst, t.err = c.readByte(ea)
		if t.err != nil {
			return
		}
		bit := bitNum % 8
		if uint32(b)&(1<<bit) == 0 {
			c.SR |= StatusZero
		} else {
			c.SR &= ^StatusZero
		}
		b |= byte(1 << bit)
		c.restoreRMW(s)
		t.dst, t.err = c.writeByte(ea, b)
	}
	return
}

// opCmpi implements CMPI (4-79)
func opCmpi(c *Processor) (t stepTrace) {
	t = stepTrace{
		addr: c.PC,
		op:   "cmpi",
		n:    1,
		sz:   (c.op & 0xC0) >> 6,
	}
	c.PC += 2
	ea := decodeEA(c.op)

	var a, b Int
	a, t.src, t.err = c.readImmInt(t.sz)
	if t.err != nil {
		return
	}
	b, t.dst, t.err = c.readInt(ea, t.sz)
	if t.err != nil {
		return
	}
	result := b.Sub(a)
	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	c.SR |= result.Status()
	if c.TraceWriter != nil && t.sz == SizeLong {
		fmt.Fprintf(c.TraceWriter, "  CMPI.L debug: imm=$%08X ea_val=$%08X result_status=$%04X Z=%v\n",
			a.Long(), b.Long(), result.Status(), result.Status()&StatusZero != 0)
	}
	return
}
