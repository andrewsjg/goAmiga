package m68k

import "fmt"

// byteToInt32 sign extends the given byte to an Int32.
func byteToInt32(b byte) int32 {
	return NewInt(uint32(b), SizeByte).SignedLong()
}

// wordToInt32 sign extends the given word to an Int32.
func wordToInt32(n uint16) int32 {
	return NewInt(uint32(n), SizeWord).SignedLong()
}

// longToInt32 decodes the given long as a signed Int32.
func longToInt32(n uint32) int32 {
	return int32(n)
}

// rmwState holds saved processor state for read-modify-write cycles.
// For memory EAs with extension words, the read phase advances PC past
// the extensions. The write phase would then re-read extensions from
// the wrong PC. Saving and restoring state between read and write fixes this.
type rmwState struct {
	pc uint32
	a  uint32
	ea uint16
}

// saveRMW saves state before the read phase of a read-modify-write cycle.
func (c *Processor) saveRMW(ea uint16) rmwState {
	reg := ea & 0x07
	return rmwState{pc: c.PC, a: c.A[reg], ea: ea}
}

// restoreRMW restores state before the write phase of a read-modify-write cycle.
func (c *Processor) restoreRMW(s rmwState) {
	reg := s.ea & 0x07
	c.PC = s.pc
	c.A[reg] = s.a
}

// decodeEA returns the effective address segment (bits 0 - 5) of an opcode.
func decodeEA(op uint16) uint16 {
	return op & 0x003F
}

// decodeExEA returns the effective destination address segment of an opcode,
// for instructions that have an effective address in bit 6 - 11. The result is
// flipped so that the mode and register portions of the ea match the encoding
// used by typical ea fields.
func decodeExEA(op uint16) uint16 {
	return ((op & 0x01C0) >> 3) | ((op & 0x0E00) >> 9)
}

// decodeBriefExt decodes a brief extension word for indexed addressing modes.
// Returns the displacement (sign-extended from 8 bits), the index register value,
// and a formatted operand string.
func (c *Processor) decodeBriefExt(baseAddr uint32, baseName string) (addr uint32, opr string, err error) {
	var ext uint16
	ext, err = c.fetchWord(c.PC)
	if err != nil {
		return
	}
	c.PC += 2
	if c.is020() && ext&0x0100 != 0 {
		return c.decodeFullExt(ext, baseAddr, baseName)
	}

	// displacement is low 8 bits, sign extended
	disp := byteToInt32(byte(ext))

	// index register: bit 15 = D/A, bits 14-12 = register number
	idxReg := (ext >> 12) & 0x07
	var idx int32
	if ext&0x8000 != 0 {
		idx = int32(c.A[idxReg])
	} else {
		idx = int32(c.D[idxReg])
	}

	// index size: bit 11 = 0 for word (sign-extended), 1 for long
	if ext&0x0800 == 0 {
		idx = int32(int16(idx))
	}
	// The 68020 scales the index by 1, 2, 4 or 8 (bits 10-9); the 68000
	// ignores those bits.
	if c.is020() {
		idx <<= ext >> 9 & 3
	}

	addr = uint32(int32(baseAddr) + disp + idx)

	// format operand string
	regType := "D"
	if ext&0x8000 != 0 {
		regType = "A"
	}
	szChar := "W"
	if ext&0x0800 != 0 {
		szChar = "L"
	}
	opr = fmt.Sprintf("($%X,%s,%s%d.%s)", disp, baseName, regType, idxReg, szChar)
	return
}

// decodeFullExt decodes the 68020's full extension word format (bit 8 of
// the extension word set): an optional base register (BS suppresses it)
// and index register (IS suppresses it, scaled by bits 10-9), a null,
// word or long base displacement, and optionally memory indirection,
// pre-indexed ([bd,An,Xn],od) or post-indexed ([bd,An],Xn,od), with a null,
// word or long outer displacement.
func (c *Processor) decodeFullExt(ext uint16, baseAddr uint32, baseName string) (addr uint32, opr string, err error) {
	readDisp := func(size uint16) (int32, error) {
		switch size {
		case 2:
			n, _, err := c.readImmWord()
			return int32(int16(n)), err
		case 3:
			n, _, err := c.readImmLong()
			return int32(n), err
		}
		return 0, nil
	}
	base := baseAddr
	if ext&0x80 != 0 { // BS
		base, baseName = 0, "Z"+baseName
	}
	var idx int32
	idxName := ""
	if ext&0x40 == 0 { // index not suppressed
		r := ext >> 12 & 7
		idxName = fmt.Sprintf("D%d", r)
		if ext&0x8000 != 0 {
			idx, idxName = int32(c.A[r]), fmt.Sprintf("A%d", r)
		} else {
			idx = int32(c.D[r])
		}
		if ext&0x0800 == 0 {
			idx = int32(int16(idx))
		}
		idx <<= ext >> 9 & 3
		idxName += fmt.Sprintf("*%d", 1<<(ext>>9&3))
	}
	bdSize := ext >> 4 & 3
	if bdSize == 0 {
		return 0, "", errBadAddress
	}
	bd, err := readDisp(bdSize)
	if err != nil {
		return
	}
	iis := ext & 7
	if iis == 0 { // no memory indirection
		addr = uint32(int32(base) + bd + idx)
		opr = fmt.Sprintf("($%X,%s,%s)", bd, baseName, idxName)
		return
	}
	if ext&0x40 != 0 && iis > 3 || iis == 4 {
		return 0, "", errBadAddress
	}
	post := iis >= 5
	od, err := readDisp(iis & 3)
	if err != nil {
		return
	}
	ptr := uint32(int32(base) + bd)
	if !post {
		ptr = uint32(int32(ptr) + idx)
	}
	inter, err := c.M.Long(int(ptr))
	if err != nil {
		return
	}
	if post {
		addr = uint32(int32(inter) + idx + od)
		opr = fmt.Sprintf("([$%X,%s],%s,$%X)", bd, baseName, idxName, od)
	} else {
		addr = uint32(int32(inter) + od)
		opr = fmt.Sprintf("([$%X,%s,%s],$%X)", bd, baseName, idxName, od)
	}
	return
}
