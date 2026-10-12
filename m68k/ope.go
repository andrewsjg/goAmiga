package m68k

import "fmt"

// opShift dispatches all shift/rotate instructions in group 0xE
func opShift(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	if sz == 3 {
		// memory shift/rotate (single bit, word size)
		return opShiftMem(c)
	}
	return opShiftReg(c)
}

// opShiftReg handles register shift/rotate: ASd, LSd, ROXd, ROd
func opShiftReg(c *Processor) (t stepTrace) {
	sz := (c.op & 0xC0) >> 6
	dr := (c.op & 0x0100) >> 8  // 0=right, 1=left
	ir := (c.op & 0x0020) >> 5  // 0=immediate count, 1=register count
	typ := (c.op & 0x0018) >> 3 // 00=AS, 01=LS, 10=ROX, 11=RO
	reg := c.op & 0x07

	names := []string{"as", "ls", "rox", "ro"}
	dirChar := []string{"r", "l"}

	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   fmt.Sprintf("%s%s", names[typ], dirChar[dr]),
		sz:   sz,
		dst:  fmt.Sprintf("D%d", reg),
	}
	c.PC += 2

	var count uint32
	cr := (c.op & 0x0E00) >> 9
	if ir == 0 {
		// immediate: 0 means 8
		count = uint32(cr)
		if count == 0 {
			count = 8
		}
		t.src = fmt.Sprintf("#%d", count)
	} else {
		count = c.D[cr] % 64
		t.src = fmt.Sprintf("D%d", cr)
	}

	val := c.D[reg] & sizeFilter[sz]
	c.tim.count = int(count)

	var result uint32
	switch typ {
	case 0: // AS (arithmetic shift)
		result = doAShift(c, val, count, int(sz), dr)
	case 1: // LS (logical shift)
		result = doLShift(c, val, count, int(sz), dr)
	case 2: // ROX (rotate through extend)
		result = doROX(c, val, count, int(sz), dr)
	case 3: // RO (rotate)
		result = doRO(c, val, count, int(sz), dr)
	}

	result &= sizeFilter[sz]
	// write back preserving upper bits
	c.D[reg] = c.D[reg]&sizeMask[sz] | result
	return
}

// opShiftMem handles memory shift/rotate (single bit, word operand)
func opShiftMem(c *Processor) (t stepTrace) {
	dr := (c.op & 0x0100) >> 8
	typ := (c.op & 0x0600) >> 9

	names := []string{"as", "ls", "rox", "ro"}
	dirChar := []string{"r", "l"}

	t = stepTrace{
		addr: c.PC,
		n:    1,
		op:   fmt.Sprintf("%s%s", names[typ], dirChar[dr]),
		sz:   SizeWord,
	}
	c.PC += 2

	ea := decodeEA(c.op)
	s := c.saveRMW(ea)
	var val uint16
	val, t.dst, t.err = c.readWord(ea)
	if t.err != nil {
		return
	}

	var result uint32
	switch typ {
	case 0:
		result = doAShift(c, uint32(val), 1, SizeWord, dr)
	case 1:
		result = doLShift(c, uint32(val), 1, SizeWord, dr)
	case 2:
		result = doROX(c, uint32(val), 1, SizeWord, dr)
	case 3:
		result = doRO(c, uint32(val), 1, SizeWord, dr)
	}

	c.restoreRMW(s)
	t.dst, t.err = c.writeWord(ea, uint16(result&0xFFFF))
	return
}

// doAShift performs arithmetic shift and sets flags
func doAShift(c *Processor, val, count uint32, sz int, dr uint16) uint32 {
	bits := []uint32{8, 16, 32}[sz]
	msb := signMask[sz]

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if count == 0 {
		// X not affected when count=0
		// just set N and Z
		if val&sizeFilter[sz] == 0 {
			c.SR |= StatusZero
		}
		if val&msb != 0 {
			c.SR |= StatusNegative
		}
		return val
	}

	var result uint32
	sign := val & msb
	overflow := false

	if dr == 1 { // left
		result = val
		for i := uint32(0); i < count; i++ {
			lastBit := result & msb
			result = (result << 1) & sizeFilter[sz]
			if lastBit != 0 {
				c.SR |= StatusCarry | StatusExtend
			} else {
				c.SR &= ^(StatusCarry | StatusExtend)
			}
			if result&msb != sign {
				overflow = true
			}
		}
	} else { // right
		result = val
		for i := uint32(0); i < count; i++ {
			lastBit := result & 1
			if sign != 0 {
				result = (result >> 1) | msb
			} else {
				result = result >> 1
			}
			result &= sizeFilter[sz]
			if lastBit != 0 {
				c.SR |= StatusCarry | StatusExtend
			} else {
				c.SR &= ^(StatusCarry | StatusExtend)
			}
		}
		// overflow is always 0 for ASR
	}

	if overflow {
		c.SR |= StatusOverflow
	}
	if result&sizeFilter[sz] == 0 {
		c.SR |= StatusZero
	}
	if result&msb != 0 {
		c.SR |= StatusNegative
	}

	_ = bits
	return result
}

// doLShift performs logical shift and sets flags
func doLShift(c *Processor, val, count uint32, sz int, dr uint16) uint32 {
	msb := signMask[sz]

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)
	if count == 0 {
		if val&sizeFilter[sz] == 0 {
			c.SR |= StatusZero
		}
		if val&msb != 0 {
			c.SR |= StatusNegative
		}
		return val
	}

	var result uint32
	if dr == 1 { // left
		result = val
		for i := uint32(0); i < count; i++ {
			lastBit := result & msb
			result = (result << 1) & sizeFilter[sz]
			if lastBit != 0 {
				c.SR |= StatusCarry | StatusExtend
			} else {
				c.SR &= ^(StatusCarry | StatusExtend)
			}
		}
	} else { // right
		result = val
		for i := uint32(0); i < count; i++ {
			lastBit := result & 1
			result = (result >> 1) & sizeFilter[sz]
			if lastBit != 0 {
				c.SR |= StatusCarry | StatusExtend
			} else {
				c.SR &= ^(StatusCarry | StatusExtend)
			}
		}
	}

	if result&sizeFilter[sz] == 0 {
		c.SR |= StatusZero
	}
	if result&msb != 0 {
		c.SR |= StatusNegative
	}
	return result
}

// doROX performs rotate through extend and sets flags
func doROX(c *Processor, val, count uint32, sz int, dr uint16) uint32 {
	bits := []uint32{8, 16, 32}[sz]
	msb := signMask[sz]

	c.SR &= ^uint32(StatusOverflow | StatusZero | StatusNegative)

	if count == 0 {
		// C = X
		if c.SR&StatusExtend != 0 {
			c.SR |= StatusCarry
		} else {
			c.SR &= ^StatusCarry
		}
		if val&sizeFilter[sz] == 0 {
			c.SR |= StatusZero
		}
		if val&msb != 0 {
			c.SR |= StatusNegative
		}
		return val
	}

	result := val
	x := uint32(0)
	if c.SR&StatusExtend != 0 {
		x = 1
	}

	for i := uint32(0); i < count; i++ {
		if dr == 1 { // left
			oldMsb := (result & msb) >> (bits - 1)
			result = ((result << 1) | x) & sizeFilter[sz]
			x = oldMsb
		} else { // right
			oldLsb := result & 1
			result = ((result >> 1) | (x << (bits - 1))) & sizeFilter[sz]
			x = oldLsb
		}
	}

	c.SR &= ^(StatusCarry | StatusExtend)
	if x != 0 {
		c.SR |= StatusCarry | StatusExtend
	}
	if result&sizeFilter[sz] == 0 {
		c.SR |= StatusZero
	}
	if result&msb != 0 {
		c.SR |= StatusNegative
	}
	return result
}

// doRO performs rotate (no extend) and sets flags
func doRO(c *Processor, val, count uint32, sz int, dr uint16) uint32 {
	bits := []uint32{8, 16, 32}[sz]
	msb := signMask[sz]

	c.SR &= ^uint32(StatusCarry | StatusOverflow | StatusZero | StatusNegative)

	if count == 0 {
		if val&sizeFilter[sz] == 0 {
			c.SR |= StatusZero
		}
		if val&msb != 0 {
			c.SR |= StatusNegative
		}
		return val
	}

	result := val
	for i := uint32(0); i < count; i++ {
		if dr == 1 { // left
			oldMsb := (result & msb) >> (bits - 1)
			result = ((result << 1) | oldMsb) & sizeFilter[sz]
		} else { // right
			oldLsb := result & 1
			result = ((result >> 1) | (oldLsb << (bits - 1))) & sizeFilter[sz]
		}
	}

	// C is set to last bit rotated
	if dr == 1 {
		if result&1 != 0 {
			c.SR |= StatusCarry
		}
	} else {
		if result&msb != 0 {
			c.SR |= StatusCarry
		}
	}

	if result&sizeFilter[sz] == 0 {
		c.SR |= StatusZero
	}
	if result&msb != 0 {
		c.SR |= StatusNegative
	}
	return result
}
