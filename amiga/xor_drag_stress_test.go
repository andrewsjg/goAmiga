package amiga

import (
	"testing"
)

// graphics.library-style complement line (as used for window drag outlines).
func xorLine(a *Amiga, plane, bpr int, x0, y0, x1, y1 int, sing bool) {
	dx := x1 - x0
	dy := y1 - y0
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	adx, ady := abs(dx), abs(dy)
	var d7 uint16
	maj, min := adx, ady
	if dx >= 0 {
		if dy > 0 {
			if ady < adx {
				d7 = 0x11
			} else {
				d7 = 0x01
				maj, min = ady, adx
			}
		} else if dy < 0 {
			if ady < adx {
				d7 = 0x19
			} else {
				d7 = 0x05
				maj, min = ady, adx
			}
		} else {
			d7 = 0x11 // pure H
		}
	} else {
		if dy > 0 {
			if ady < adx {
				d7 = 0x15
			} else {
				d7 = 0x09
				maj, min = ady, adx
			}
		} else if dy < 0 {
			if ady < adx {
				d7 = 0x1d
			} else {
				d7 = 0x0d
				maj, min = ady, adx
			}
		} else {
			d7 = 0x15 // pure H left
		}
	}
	if sing {
		d7 |= 0x0002
	}
	d7 |= 0x0001 // LINE
	twoMin := min * 2
	err := twoMin - maj
	if err < 0 {
		d7 |= 0x40
	}
	amod := int16(twoMin - 2*maj)
	bmod := int16(twoMin)
	start := uint32(plane + y0*bpr + (x0/16)*2)
	startBit := uint16(x0 & 15)
	a.Blitter.con0 = (startBit << 12) | 0x0B5A
	a.Blitter.con1 = d7
	a.Blitter.aptL = uint16(int16(err))
	a.Blitter.aptH = 0
	a.Blitter.amod = amod
	a.Blitter.bmod = bmod
	a.Blitter.cmod = int16(bpr)
	a.Blitter.dmod = int16(bpr)
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.adat = 0x8000
	a.Blitter.bdat = 0xFFFF
	a.Blitter.cptH = uint16(start >> 16)
	a.Blitter.cptL = uint16(start)
	a.Blitter.dptH = uint16(start >> 16)
	a.Blitter.dptL = uint16(start)
	length := maj + 1
	if length < 1 {
		length = 1
	}
	a.Blitter.Execute(uint16(length<<6 | 2))
}

func xorRect(a *Amiga, plane, bpr int, x, y, w, h int, sing bool) {
	xorLine(a, plane, bpr, x, y, x+w-1, y, sing)
	xorLine(a, plane, bpr, x+w-1, y, x+w-1, y+h-1, sing)
	xorLine(a, plane, bpr, x+w-1, y+h-1, x, y+h-1, sing)
	xorLine(a, plane, bpr, x, y+h-1, x, y, sing)
}

func planeChecksum(a *Amiga, plane, n int) (sum uint32, nonzero int) {
	b := make([]byte, n)
	a.ChipRAM.Read(plane, b)
	for _, v := range b {
		sum = sum*131 + uint32(v)
		if v != 0 {
			nonzero++
		}
	}
	return
}

func TestXORDragOutlineStress(t *testing.T) {
	for _, sing := range []bool{true, false} {
		a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
		a.Custom = NewCustomChips(nil, a)
		a.Blitter = NewBlitter(a)
		plane, bpr, rows := 0x10000, 80, 200
		for r := 0; r < rows; r++ {
			buf := make([]byte, bpr)
			for i := range buf {
				buf[i] = byte((r*13 + i*7) & 0xFF)
			}
			a.ChipRAM.Write(plane+r*bpr, buf)
		}
		before := make([]byte, bpr*rows)
		a.ChipRAM.Read(plane, before)

		x, y, w, h := 50, 30, 120, 80
		// initial outline
		xorRect(a, plane, bpr, x, y, w, h, sing)
		for step := 0; step < 150; step++ {
			nx := x + (step % 7) - 3
			ny := y + (step % 5) - 2
			if nx < 1 {
				nx = 1
			}
			if ny < 1 {
				ny = 1
			}
			if nx+w >= 640 {
				nx = 640 - w - 1
			}
			if ny+h >= rows {
				ny = rows - h - 1
			}
			// erase old, draw new
			xorRect(a, plane, bpr, x, y, w, h, sing)
			x, y = nx, ny
			xorRect(a, plane, bpr, x, y, w, h, sing)
		}
		// final erase
		xorRect(a, plane, bpr, x, y, w, h, sing)

		after := make([]byte, bpr*rows)
		a.ChipRAM.Read(plane, after)
		mismatch := 0
		for i := range before {
			if before[i] != after[i] {
				mismatch++
				if mismatch <= 8 {
					t.Logf("  SING=%v +%d $%02X->$%02X", sing, i, before[i], after[i])
				}
			}
		}
		t.Logf("SING=%v: mismatches=%d after drag", sing, mismatch)
		if mismatch > 0 {
			t.Errorf("XOR drag residue with SING=%v: %d bytes", sing, mismatch)
		}
	}
}

func TestXORRectIdentity(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	plane, bpr, rows := 0x10000, 80, 100
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		for i := range buf {
			buf[i] = byte(0xA5)
		}
		a.ChipRAM.Write(plane+r*bpr, buf)
	}
	before := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, before)
	for i := 0; i < 50; i++ {
		xorRect(a, plane, bpr, 10+i%20, 10+i%15, 80, 50, true)
		xorRect(a, plane, bpr, 10+i%20, 10+i%15, 80, 50, true)
	}
	after := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, after)
	m := 0
	for i := range before {
		if before[i] != after[i] {
			m++
		}
	}
	if m > 0 {
		t.Fatalf("double-xor rect left %d residue bytes", m)
	}
}

// Shared corner pixels: four edges of a rect each include corners.
// Drawing adjacent rects that share an edge pixel can leave residue if
// endpoints are inclusive on both lines.
func TestXORAdjacentRects(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	plane, bpr, rows := 0x10000, 80, 100
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		a.ChipRAM.Write(plane+r*bpr, buf)
	}
	before := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, before)

	// Draw rect at (10,10), erase, draw at (11,10), erase — should clean
	xorRect(a, plane, bpr, 10, 10, 40, 30, true)
	xorRect(a, plane, bpr, 10, 10, 40, 30, true)
	xorRect(a, plane, bpr, 11, 10, 40, 30, true)
	xorRect(a, plane, bpr, 11, 10, 40, 30, true)

	after := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, after)
	m := 0
	for i := range before {
		if before[i] != after[i] {
			m++
			if m <= 10 {
				t.Logf("  +%d $%02X->$%02X", i, before[i], after[i])
			}
		}
	}
	if m > 0 {
		t.Errorf("adjacent rect xor left %d residue", m)
	}
}

// Zero-length and pure vertical/horizontal with SING
func TestXORAllOctantsTwice(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	plane, bpr, rows := 0x10000, 80, 100
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		for i := range buf {
			buf[i] = byte(i + r)
		}
		a.ChipRAM.Write(plane+r*bpr, buf)
	}
	before := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, before)

	pts := [][4]int{
		{20, 20, 80, 20}, // H+
		{80, 20, 20, 20}, // H-
		{40, 10, 40, 70}, // V+
		{40, 70, 40, 10}, // V-
		{10, 10, 90, 40}, // diag
		{90, 10, 10, 40},
		{10, 40, 90, 10},
		{90, 40, 10, 10},
		{30, 10, 50, 80}, // steep
		{50, 80, 30, 10},
	}
	for _, sing := range []bool{true, false} {
		a.ChipRAM.Write(plane, before)
		for _, p := range pts {
			xorLine(a, plane, bpr, p[0], p[1], p[2], p[3], sing)
			xorLine(a, plane, bpr, p[0], p[1], p[2], p[3], sing)
		}
		after := make([]byte, bpr*rows)
		a.ChipRAM.Read(plane, after)
		m := 0
		for i := range before {
			if before[i] != after[i] {
				m++
			}
		}
		t.Logf("octants SING=%v mismatches=%d", sing, m)
		if m > 0 {
			t.Errorf("octant XOR twice SING=%v left %d", sing, m)
		}
	}
}

// Minterm $4A is the HRM "exclusive-or" line config (AB~C + ~AC).
// With solid texture B=$FFFF it equals A xor C, but SING handling may differ.
func TestXOR4AMinterm(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	plane, bpr, rows := 0x10000, 80, 100
	for r := 0; r < rows; r++ {
		buf := make([]byte, bpr)
		for i := range buf {
			buf[i] = byte((r + i) * 3)
		}
		a.ChipRAM.Write(plane+r*bpr, buf)
	}
	before := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, before)

	draw4A := func(x0, y0, x1, y1 int) {
		dx, dy := x1-x0, y1-y0
		abs := func(v int) int {
			if v < 0 {
				return -v
			}
			return v
		}
		adx, ady := abs(dx), abs(dy)
		d7 := uint16(0x0003) // LINE|SING
		maj, min := adx, ady
		if ady < adx {
			d7 |= 0x10 // rough octant
		} else {
			maj, min = ady, adx
		}
		if dx < 0 {
			d7 |= 0x04
		}
		if dy < 0 {
			d7 |= 0x08
		}
		twoMin := min * 2
		err := twoMin - maj
		if err < 0 {
			d7 |= 0x40
		}
		start := uint32(plane + y0*bpr + (x0/16)*2)
		a.Blitter.con0 = uint16((x0&15)<<12) | 0x0B4A // minterm $4A
		a.Blitter.con1 = d7
		a.Blitter.aptL = uint16(int16(err))
		a.Blitter.amod = int16(twoMin - 2*maj)
		a.Blitter.bmod = int16(twoMin)
		a.Blitter.cmod = int16(bpr)
		a.Blitter.dmod = int16(bpr)
		a.Blitter.afwm = 0xFFFF
		a.Blitter.alwm = 0xFFFF
		a.Blitter.adat = 0x8000
		a.Blitter.bdat = 0xFFFF
		a.Blitter.cptH = uint16(start >> 16)
		a.Blitter.cptL = uint16(start)
		a.Blitter.dptH = uint16(start >> 16)
		a.Blitter.dptL = uint16(start)
		a.Blitter.Execute(uint16((maj+1)<<6 | 2))
	}

	// Many lines draw+erase
	for i := 0; i < 80; i++ {
		x0, y0 := 10+i%30, 10+i%20
		x1, y1 := 100+i%40, 50+i%30
		draw4A(x0, y0, x1, y1)
		draw4A(x0, y0, x1, y1)
	}
	after := make([]byte, bpr*rows)
	a.ChipRAM.Read(plane, after)
	m := 0
	for i := range before {
		if before[i] != after[i] {
			m++
		}
	}
	t.Logf("$4A SING line XOR twice mismatches=%d", m)
	if m > 0 {
		t.Errorf("$4A left residue %d", m)
	}
}
