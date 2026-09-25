package amiga

// Native Flood (graphics.library) implementation.
//
// Kickstart 1.3's ROM Flood path fills unbounded regions here (full-plane
// blits via TmpRas helpers). We intercept the library entry at $FC5AD0 and
// perform a correct seed fill against chip-RAM planar bitmaps.
//
//	Flood(rp, mode, x, y)
//	  mode 0 = outline: fill until a pixel of FgPen color is found
//	  mode 1 = color:   fill all connected pixels of the seed pixel's color
//
// ABI: A1 = *RastPort, D0 = x, D1 = y, D2 = mode

const floodEntryPC = uint32(0xFC5AD0)

// tryNativeFlood runs if PC is at the Flood entry. On success it fills the
// region, simulates RTS, and sets LastCycles. Returns true if handled.
func (a *Amiga) tryNativeFlood() bool {
	if a.CPU.PC != floodEntryPC {
		return false
	}

	rp := a.CPU.A[1]
	x := int(int32(a.CPU.D[0]))
	y := int(int32(a.CPU.D[1]))
	mode := a.CPU.D[2] & 0xFFFF

	if rp == 0 || rp >= ChipRAMSize {
		a.rtsNative(100)
		return true
	}

	// RastPort: +4 BitMap*, +25 FgPen
	bmPtr := a.readLongChip(rp + 4)
	if bmPtr == 0 || bmPtr >= ChipRAMSize-40 {
		a.rtsNative(100)
		return true
	}

	// BitMap: +0 BytesPerRow.w, +2 Rows.w, +5 Depth.b, +8 Planes[8]
	bpr := int(a.readWordChip(bmPtr))
	rows := int(a.readWordChip(bmPtr + 2))
	depth := int(a.readByteChip(bmPtr + 5))
	if depth <= 0 {
		depth = 1
	}
	if depth > 6 {
		depth = 6
	}
	if bpr <= 0 || rows <= 0 {
		a.rtsNative(100)
		return true
	}
	// Guard against corrupt BitMap values (OOM / multi-second freezes).
	const maxBPR, maxRows = 512, 1024
	if bpr > maxBPR || rows > maxRows {
		a.rtsNative(100)
		return true
	}
	width := bpr * 8

	planes := make([]uint32, depth)
	for p := 0; p < depth; p++ {
		planes[p] = a.readLongChip(bmPtr + 8 + uint32(p*4))
	}
	fgPen := int(a.readByteChip(rp + 25))

	if x < 0 || y < 0 || x >= width || y >= rows {
		a.rtsNative(50)
		return true
	}

	readPen := func(px, py int) int {
		pen := 0
		byteOff := py*bpr + px/8
		bit := uint(7 - (px % 8))
		for p := 0; p < depth; p++ {
			base := planes[p]
			if base == 0 || int(base)+byteOff >= ChipRAMSize {
				continue
			}
			b := a.readByteChip(base + uint32(byteOff))
			if b&(1<<bit) != 0 {
				pen |= 1 << uint(p)
			}
		}
		return pen
	}

	writePen := func(px, py, pen int) {
		byteOff := py*bpr + px/8
		bit := uint(7 - (px % 8))
		mask := byte(1 << bit)
		for p := 0; p < depth; p++ {
			base := planes[p]
			if base == 0 || int(base)+byteOff >= ChipRAMSize {
				continue
			}
			addr := base + uint32(byteOff)
			// Hard bans only (Exec + input.device slice). Full deny maps break
			// insert-disk Flood fills and prevent ADF boot.
			off := int(addr & 0x7FFFF)
			if off < 0x100 || (off >= 0x600 && off < 0xA00) || (off >= 0x2C00 && off < 0x5200) {
				continue
			}
			b := a.readByteChip(addr)
			if pen&(1<<uint(p)) != 0 {
				b |= mask
			} else {
				b &^= mask
			}
			a.writeByteChip(addr, b)
		}
	}

	seedColor := readPen(x, y)

	var match func(c int) bool
	if mode == 0 {
		// Outline mode: fill while pixel is not FgPen.
		if seedColor == fgPen {
			a.rtsNative(50)
			return true
		}
		match = func(c int) bool { return c != fgPen }
	} else {
		// Color mode: fill connected pixels of seed color.
		if seedColor == fgPen {
			a.rtsNative(50)
			return true
		}
		match = func(c int) bool { return c == seedColor }
	}

	type pt struct{ x, y int }
	stack := []pt{{x, y}}
	visited := make([]byte, (width*rows+7)/8)
	filled := 0
	maxFill := width * rows

	for len(stack) > 0 && filled < maxFill {
		n := len(stack) - 1
		p := stack[n]
		stack = stack[:n]
		px, py := p.x, p.y
		if px < 0 || py < 0 || px >= width || py >= rows {
			continue
		}
		i := py*width + px
		if visited[i/8]&(1<<uint(i%8)) != 0 {
			continue
		}
		visited[i/8] |= 1 << uint(i%8)
		if !match(readPen(px, py)) {
			continue
		}
		writePen(px, py, fgPen)
		filled++
		stack = append(stack,
			pt{px + 1, py}, pt{px - 1, py},
			pt{px, py + 1}, pt{px, py - 1},
		)
	}

	cycles := filled*8 + 200
	if cycles > CyclesPerFrame*2 {
		cycles = CyclesPerFrame * 2
	}
	a.rtsNative(cycles)
	return true
}

func (a *Amiga) rtsNative(cycles int) {
	sp := a.CPU.A[7]
	ret := a.readLongChip(sp)
	a.CPU.A[7] = sp + 4
	a.CPU.PC = ret
	a.CPU.LastCycles = cycles
}

func (a *Amiga) readByteChip(addr uint32) byte {
	buf := [1]byte{}
	a.ChipRAM.Read(int(addr&0x7FFFF), buf[:])
	return buf[0]
}

func (a *Amiga) writeByteChip(addr uint32, v byte) {
	buf := [1]byte{v}
	a.ChipRAM.Write(int(addr&0x7FFFF), buf[:])
}

func (a *Amiga) readWordChip(addr uint32) uint16 {
	buf := [2]byte{}
	a.ChipRAM.Read(int(addr&0x7FFFF), buf[:])
	return uint16(buf[0])<<8 | uint16(buf[1])
}

func (a *Amiga) readLongChip(addr uint32) uint32 {
	buf := [4]byte{}
	a.ChipRAM.Read(int(addr&0x7FFFF), buf[:])
	return uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
}
