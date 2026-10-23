package m68k

import (
	"fmt"
	"math/bits"
)

// 68020 bit field instructions: BFTST, BFEXTU, BFCHG, BFEXTS, BFCLR, BFFFO,
// BFSET, BFINS ($E8C0-$EFC0 + <ea>). The extension word gives the offset
// (bits 10-6, or a data register if bit 11 is set) and width (bits 4-0,
// 0 meaning 32, or a data register if bit 5 is set). Offsets count from
// the most significant bit. In a data register the field wraps around the
// register; in memory the offset is signed and the field may span five
// bytes. N and Z reflect the field (BFINS: the inserted value) before
// the operation; V and C are cleared.

func opBitField(c *Processor) (t stepTrace) {
	kind := c.op >> 8 & 7
	t = stepTrace{addr: c.PC, n: 1, sz: SizeLong,
		op: [...]string{"bftst", "bfextu", "bfchg", "bfexts", "bfclr", "bfffo", "bfset", "bfins"}[kind]}
	c.PC += 2
	ext, _, err := c.readImmWord()
	if err != nil {
		t.err = err
		return
	}
	reg := ext >> 12 & 7
	var offset int32
	if ext&0x0800 != 0 {
		offset = int32(c.D[ext>>6&7])
	} else {
		offset = int32(ext >> 6 & 0x1F)
	}
	width := uint32(ext & 0x1F)
	if ext&0x0020 != 0 {
		width = c.D[ext&7] & 0x1F
	}
	if width == 0 {
		width = 32
	}
	mask := uint32(0xFFFFFFFF) >> (32 - width) // right-aligned

	// get and put read and write the field, right-aligned.
	var get func() uint32
	var put func(uint32) error
	mode := c.op >> 3 & 7
	if mode == 0 { // Dn: the field wraps around the register
		dn := &c.D[c.op&7]
		off := uint32(offset) & 31
		get = func() uint32 { return bits.RotateLeft32(*dn, int(off)) >> (32 - width) & mask }
		put = func(v uint32) error {
			m := bits.RotateLeft32(mask<<(32-width), -int(off))
			*dn = *dn&^m | bits.RotateLeft32((v&mask)<<(32-width), -int(off))
			return nil
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("D%d{%d:%d}", c.op&7, offset, width)
		}
	} else {
		base, opr, err := c.controlAddr(decodeEA(c.op))
		if err != nil {
			t.err = err
			return
		}
		addr := uint32(int32(base) + offset>>3)
		bit := uint32(offset & 7)
		n := int((bit + width + 7) / 8) // bytes the field touches
		var buf [5]byte
		read := func() (uint64, error) {
			if _, err := c.M.Read(int(addr), buf[:n]); err != nil {
				return 0, err
			}
			var x uint64
			for i := 0; i < 5; i++ {
				x = x<<8 | uint64(buf[i]&byte(boolMask(i < n)))
			}
			return x, nil // 40 bits, first byte highest
		}
		shift := 40 - bit - width
		get = func() uint32 {
			x, err := read()
			if err != nil {
				t.err = err
			}
			return uint32(x>>shift) & mask
		}
		put = func(v uint32) error {
			x, err := read()
			if err != nil {
				return err
			}
			x = x&^(uint64(mask)<<shift) | uint64(v&mask)<<shift
			for i := 4; i >= 0; i-- {
				buf[i] = byte(x)
				x >>= 8
			}
			_, err = c.M.Write(int(addr), buf[:n])
			return err
		}
		if c.TraceWriter != nil {
			t.dst = fmt.Sprintf("%s{%d:%d}", opr, offset, width)
		}
	}

	field := get()
	if t.err != nil {
		return
	}
	flags := field
	if kind == 7 { // BFINS: the flags show the value inserted
		flags = c.D[reg] & mask
	}
	c.SR &^= StatusNegative | StatusZero | StatusOverflow | StatusCarry
	if flags>>(width-1)&1 != 0 {
		c.SR |= StatusNegative
	}
	if flags == 0 {
		c.SR |= StatusZero
	}

	switch kind {
	case 0: // BFTST
	case 1: // BFEXTU
		c.D[reg] = field
	case 2: // BFCHG
		t.err = put(^field)
	case 3: // BFEXTS
		c.D[reg] = uint32(int32(field<<(32-width)) >> (32 - width))
	case 4: // BFCLR
		t.err = put(0)
	case 5: // BFFFO: offset of the first set bit from the field's MSB
		n := uint32(bits.LeadingZeros32(field << (32 - width)))
		if field == 0 {
			n = width
		}
		c.D[reg] = uint32(offset) + n
	case 6: // BFSET
		t.err = put(mask)
	case 7: // BFINS
		t.err = put(c.D[reg])
	}
	return
}

func boolMask(b bool) uint8 {
	if b {
		return 0xFF
	}
	return 0
}
