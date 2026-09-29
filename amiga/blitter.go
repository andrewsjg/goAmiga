package amiga

import (
	"fmt"
	"io"
)

// Blitter implements the Amiga blitter (block image transferrer).
// It performs boolean logic operations on up to 3 source bitplanes (A, B, C)
// and writes the result to a destination (D) in chip RAM.
type Blitter struct {
	amiga  *Amiga
	logger io.Writer

	// Control registers
	con0 uint16 // BLTCON0: minterm (7-0), shift A (15-12), use flags
	con1 uint16 // BLTCON1: shift B (15-12), fill mode, direction

	// Word masks
	afwm uint16 // BLTAFWM: first word mask for channel A
	alwm uint16 // BLTALWM: last word mask for channel A

	// Pointers (byte addresses in chip RAM)
	aptH, aptL uint16 // Channel A pointer
	bptH, bptL uint16 // Channel B pointer
	cptH, cptL uint16 // Channel C pointer
	dptH, dptL uint16 // Channel D pointer

	// Modulos (signed, added after each line)
	amod int16
	bmod int16
	cmod int16
	dmod int16

	// Data registers (last fetched word)
	adat uint16
	bdat uint16
	cdat uint16

	// State
	busy             bool
	run              *blitRun // area blit in progress (nil: none, or line mode)
	zero             bool     // BZERO: every word the last blit produced was 0
	completionCycles int      // cycles until BBUSY clears
	pendingIRQs      int      // BLIT completions not yet signaled via INTREQ
	lastSize         uint16
	// Pointers at the start of the last blit (before write-back), for
	// diagnostics.
	preApt, preBpt, preCpt, preDpt uint32
	// Debug: if non-nil, called on each D-channel write with (addr, value, aWord, bWord, cWord, minterm)
	DebugWriteHook func(addr int, val, aW, bW, cW uint16, mt byte)
	// OnExecute is called at the start of each blit (after size decode).
	OnExecute func(size uint16, con0, con1 uint16, dpt uint32)
}

// NewBlitter creates a new Blitter.
func NewBlitter(a *Amiga) *Blitter {
	return &Blitter{
		amiga:  a,
		logger: a.logger,
		afwm:   0xFFFF,
		alwm:   0xFFFF,
	}
}

func (b *Blitter) ptr(hi, lo uint16) uint32 {
	return uint32(hi)<<16 | uint32(lo)
}

// Execute starts the blitter operation triggered by writing BLTSIZE. An
// area blit then moves its words as it gets bus slots (TickCycles), and
// BBUSY stays set for the blit's real duration (blitDuration); the BLIT
// interrupt is raised when it ends.
//
// BLIT INTREQ is never raised from inside Execute. Raising it here (or from a
// supersede of a still-busy blit) re-enters the QBlit softint while the CPU is
// still in the BLTSIZE write / mid OwnBlitter setup. That corrupts GfxBase and
// the Exec mem lists → Guru #A1000001 (bad FreeMem) or #00000004 on input.device.
// Completions are counted in pendingIRQs and delivered from TickCycles only
// when the blitter is idle, one IRQ per Step.
func (b *Blitter) Execute(size uint16) {
	// Previous blit already finished its data. Free BBUSY and remember that
	// QBlit still needs a completion IRQ — but do not SetIntreq here.
	if b.busy {
		b.completeRun()
		b.busy = false
		b.completionCycles = 0
		b.pendingIRQs++
	}

	b.lastSize = size
	b.preApt = b.ptr(b.aptH, b.aptL)
	b.preBpt = b.ptr(b.bptH, b.bptL)
	b.preCpt = b.ptr(b.cptH, b.cptL)
	b.preDpt = b.ptr(b.dptH, b.dptL)
	height := int(size >> 6)
	width := int(size & 0x3F)
	if height == 0 {
		height = 1024
	}
	if width == 0 {
		width = 64
	}

	b.busy = true
	if b.OnExecute != nil {
		b.OnExecute(size, b.con0, b.con1, b.ptr(b.dptH, b.dptL))
	}

	// Line mode is done at once (its data still lands at the start); area
	// blits proceed word by word as the blitter gets bus slots (step).
	if b.con1&0x0001 != 0 {
		b.zero = true
		b.executeLine(size)
	} else {
		b.beginBlit(size)
	}

	b.completionCycles = blitDuration(b.con0, b.con1, width, height)
}

// blitCyclesPerWord is the blitter's colour clocks per word for each
// BLTCON0 USEx combination (index = USEA<<3|USEB<<2|USEC<<1|USED), from the
// HRM blitter cycle sequence table.
var blitCyclesPerWord = [16]int{2, 2, 2, 3, 3, 3, 3, 4, 2, 2, 2, 3, 3, 3, 3, 4}

const (
	blitLineCyclesPerPixel = 4 // colour clocks per pixel in line mode
	blitStartupCycles      = 2 // colour clocks of pipeline startup
)

// hogsBus reports whether the running blit uses every bus slot (its cycle
// pattern has no idle cycles), so the CPU only gets in by the blitter
// yielding. Line mode leaves idle cycles.
func (b *Blitter) hogsBus() bool {
	if b.con1&0x0001 != 0 {
		return false
	}
	use := (b.con0 >> 8) & 0xF
	channels := int(use&1 + use>>1&1 + use>>2&1 + use>>3&1)
	return channels == blitCyclesPerWord[use]
}

// blitDuration is how many CPU cycles of free bus slots a blit needs. It
// takes longer in real time when bitplane or fixed DMA holds slots: the
// Amiga only counts slots the blitter actually gets (Amiga.blitterSlots).
func blitDuration(con0, con1 uint16, width, height int) int {
	cck := height * width * blitCyclesPerWord[(con0>>8)&0xF]
	if con1&0x0001 != 0 {
		cck = height * blitLineCyclesPerPixel
	}
	return (cck + blitStartupCycles) * CPUCyclesPerColorClock
}

// finish marks one blit complete: BBUSY clear + queue a BLIT INTREQ for
// TickCycles to deliver when idle.
func (b *Blitter) finish() {
	b.completeRun()
	b.busy = false
	b.completionCycles = 0
	b.pendingIRQs++
}

// TickCycles advances the busy timer and delivers deferred BLIT IRQs.
// Called from Amiga.Step() after each CPU instruction (never re-enters the
// instruction that wrote BLTSIZE).
func (b *Blitter) TickCycles(cpuCycles int) {
	if b.busy {
		b.step(cpuCycles)
		if b.completionCycles <= 0 {
			b.finish()
		} else {
			b.completionCycles -= cpuCycles
			if b.completionCycles <= 0 {
				b.finish()
			}
		}
	}
	// Only signal completion when the blitter is free so QBlit never runs
	// against an in-progress (busy) blit. One IRQ per Step keeps softint
	// work bounded if several completions were queued by supersede.
	if !b.busy && b.pendingIRQs > 0 {
		b.pendingIRQs--
		if b.amiga != nil && b.amiga.Custom != nil {
			b.amiga.Custom.SetIntreq(1 << 6)
		}
	}
}

// blitRun is an area blit in progress. The blitter works through it a
// word at a time as it gets bus slots (step), so the CPU sees the
// destination fill in, and source words are read when the blitter
// reaches them, as on the real machine.
type blitRun struct {
	width, height      int
	x, y               int
	apt, bpt, cpt, dpt uint32
	prevA, prevB       uint16
	fillCarry          uint16
	credit             int // CPU cycles of bus slots not yet spent on words
	wordCost           int // CPU cycles per word
	// Latched at the BLTSIZE write.
	con0, con1             uint16
	afwm, alwm             uint16
	amod, bmod, cmod, dmod int16
}

// beginBlit starts an area blit; the words are done by step.
func (b *Blitter) beginBlit(size uint16) {
	height := int(size >> 6)
	width := int(size & 0x3F)
	if height == 0 {
		height = 1024
	}
	if width == 0 {
		width = 64
	}
	r := &blitRun{
		width: width, height: height,
		apt: b.ptr(b.aptH, b.aptL), bpt: b.ptr(b.bptH, b.bptL),
		cpt: b.ptr(b.cptH, b.cptL), dpt: b.ptr(b.dptH, b.dptL),
		wordCost: blitCyclesPerWord[(b.con0>>8)&0xF] * CPUCyclesPerColorClock,
		credit:   -blitStartupCycles * CPUCyclesPerColorClock,
		con0:     b.con0, con1: b.con1, afwm: b.afwm, alwm: b.alwm,
		amod: b.amod, bmod: b.bmod, cmod: b.cmod, dmod: b.dmod,
	}
	if b.logger != nil {
		fmt.Fprintf(b.logger, "BLITTER: %dx%d con0=$%04X con1=$%04X min=$%02X A=$%08X B=$%08X C=$%08X D=$%08X\n",
			width, height, b.con0, b.con1, byte(b.con0), r.apt, r.bpt, r.cpt, r.dpt)
	}
	b.run = r
	b.zero = true
}

// step spends cpuCycles of bus slots on the blit in progress.
func (b *Blitter) step(cpuCycles int) {
	r := b.run
	if r == nil {
		return
	}
	r.credit += cpuCycles
	for r.credit >= r.wordCost && b.run != nil {
		r.credit -= r.wordCost
		b.blitWord()
	}
}

// completeRun does whatever words remain (BBUSY is ending).
func (b *Blitter) completeRun() {
	for b.run != nil {
		b.blitWord()
	}
}

// executeBlit performs a whole area blit at once.
func (b *Blitter) executeBlit(size uint16) {
	b.beginBlit(size)
	b.completeRun()
}

// blitWord processes the next word of the blit in progress, and at the end
// of the blit clears b.run. The pointer registers follow the blit, so
// software reading them mid-blit sees how far it has got.
func (b *Blitter) blitWord() {
	r := b.run
	minterm := byte(r.con0 & 0xFF)
	useA := r.con0&0x0800 != 0
	useB := r.con0&0x0400 != 0
	useC := r.con0&0x0200 != 0
	useD := r.con0&0x0100 != 0
	shiftA := (r.con0 >> 12) & 0xF
	shiftB := (r.con1 >> 12) & 0xF
	// Descending mode (bit 1 of BLTCON1)
	desc := r.con1&0x0002 != 0
	// Fill mode bits in BLTCON1:
	//   Bit 3 (0x0008): IFE — Inclusive Fill Enable
	//   Bit 4 (0x0010): EFE — Exclusive Fill Enable
	// Fill requires descending mode (bit 1) to be set as well.
	inclusiveFill := r.con1&0x0008 != 0
	exclusiveFill := r.con1&0x0010 != 0
	fillEnable := inclusiveFill || exclusiveFill
	var buf [2]byte
	x, width := r.x, r.width

	if x == 0 {
		r.prevA, r.prevB, r.fillCarry = 0, 0, 0
	}

	// When a channel is not enabled (USEx=0), the minterm still uses
	// the channel's data register value (xDAT) rather than zero.
	aWord := b.adat
	bWord := b.bdat
	cWord := b.cdat

	// Fetch source words
	if useA {
		b.amiga.ChipRAM.Read(int(r.apt&^1), buf[:])
		aWord = uint16(buf[0])<<8 | uint16(buf[1])
		b.adat = aWord

		// Apply first/last word masks before shift (HRM order).
		if x == 0 {
			aWord &= r.afwm
		}
		if x == width-1 {
			aWord &= r.alwm
		}

		// Barrel shift A
		if shiftA != 0 {
			var combined uint32
			if desc {
				// Descending mode: shift direction reverses (left shift).
				// prevA holds the word from the higher address (processed earlier).
				combined = uint32(aWord)<<16 | uint32(r.prevA)
				aWord = uint16(combined >> (16 - shiftA))
			} else {
				combined = uint32(r.prevA)<<16 | uint32(aWord)
				aWord = uint16(combined >> shiftA)
			}
		}
		r.prevA = b.adat

		if desc {
			r.apt -= 2
		} else {
			r.apt += 2
		}
	} else {
		// USEA=0: A is the constant in BLTADAT, but AFWM/ALWM still
		// apply. graphics.library Draw uses this for pure-horizontal
		// lines (1-row area blit, ADAT=$FFFF): without the masks,
		// whole words light up and lines overrun past the true
		// endpoints to 16-pixel boundaries.
		if x == 0 {
			aWord &= r.afwm
		}
		if x == width-1 {
			aWord &= r.alwm
		}
		if shiftA != 0 {
			var combined uint32
			if desc {
				combined = uint32(aWord)<<16 | uint32(r.prevA)
				aWord = uint16(combined >> (16 - shiftA))
			} else {
				combined = uint32(r.prevA)<<16 | uint32(aWord)
				aWord = uint16(combined >> shiftA)
			}
			r.prevA = b.adat
		}
	}

	if useB {
		b.amiga.ChipRAM.Read(int(r.bpt&^1), buf[:])
		bWord = uint16(buf[0])<<8 | uint16(buf[1])
		b.bdat = bWord

		// Barrel shift B
		if shiftB != 0 {
			var combined uint32
			if desc {
				combined = uint32(bWord)<<16 | uint32(r.prevB)
				bWord = uint16(combined >> (16 - shiftB))
			} else {
				combined = uint32(r.prevB)<<16 | uint32(bWord)
				bWord = uint16(combined >> shiftB)
			}
		}
		r.prevB = b.bdat

		if desc {
			r.bpt -= 2
		} else {
			r.bpt += 2
		}
	}

	if useC {
		b.amiga.ChipRAM.Read(int(r.cpt&^1), buf[:])
		cWord = uint16(buf[0])<<8 | uint16(buf[1])
		b.cdat = cWord
		if desc {
			r.cpt -= 2
		} else {
			r.cpt += 2
		}
	}

	// Apply minterm logic
	dWord := applyMinterm(minterm, aWord, bWord, cWord)

	// Apply area fill if enabled (process bits right-to-left)
	if fillEnable {
		var filled uint16
		for bit := uint(0); bit < 16; bit++ {
			srcBit := (dWord >> bit) & 1
			if srcBit != 0 {
				r.fillCarry ^= 1
			}
			if exclusiveFill {
				// Exclusive fill: output the XOR of original bit and carry
				filled |= (srcBit ^ r.fillCarry) << bit
			} else {
				// Inclusive fill: output carry (carry already toggled by srcBit)
				filled |= r.fillCarry << bit
			}
		}
		dWord = filled
	}
	if dWord != 0 {
		b.zero = false
	}

	// Write result
	if useD {
		buf[0] = byte(dWord >> 8)
		buf[1] = byte(dWord)
		writeAddr := int(r.dpt &^ 1)
		b.amiga.ChipRAM.Write(writeAddr, buf[:])
		if b.DebugWriteHook != nil {
			b.DebugWriteHook(writeAddr, dWord, aWord, bWord, cWord, minterm)
		}
		if desc {
			r.dpt -= 2
		} else {
			r.dpt += 2
		}
	}

	r.x++
	if r.x == width {
		// Apply modulos at end of each line (signed arithmetic).
		// Per the Amiga HRM, in ascending mode modulos are ADDED to the
		// pointers; in DESCENDING mode the hardware SUBTRACTS them (just as
		// it subtracts 2 per word). KS1.3 relies on both: upward-overlap
		// copies use ascending blits with negative modulos (pitch 2w+mod),
		// while true descending blits supply the same signed modulo and
		// expect -2w-mod per row. Adding in descending mode walked the
		// pointers diagonally (2×mod error per row) — window-drag release
		// blits sprayed garbage across the screen and, for tall blits,
		// far outside the bitmap into OS structures.
		mod := func(pt uint32, m int16) uint32 {
			if desc {
				return uint32(int32(pt) - int32(m))
			}
			return uint32(int32(pt) + int32(m))
		}
		if useA {
			r.apt = mod(r.apt, r.amod)
		}
		if useB {
			r.bpt = mod(r.bpt, r.bmod)
		}
		if useC {
			r.cpt = mod(r.cpt, r.cmod)
		}
		if useD {
			r.dpt = mod(r.dpt, r.dmod)
		}
		r.x = 0
		r.y++
	}

	// The pointer registers follow the blit. ROM code (e.g. the QBlit
	// handler, multi-pass BltBitMap) reads them to continue.
	b.aptH, b.aptL = uint16(r.apt>>16), uint16(r.apt)
	b.bptH, b.bptL = uint16(r.bpt>>16), uint16(r.bpt)
	b.cptH, b.cptL = uint16(r.cpt>>16), uint16(r.cpt)
	b.dptH, b.dptL = uint16(r.dpt>>16), uint16(r.dpt)
	if r.y == r.height {
		b.run = nil
	}
}

// executeLine performs Bresenham line drawing using the blitter.
// In line-draw mode:
//   - BLTCON0[15:12] = start pixel offset within word
//   - BLTCON1 bits encode LINE/SING/octant as produced by graphics.library Draw
//   - BLTAPT low word = Bresenham error accumulator (signed 16-bit)
//   - BLTBMOD = 2*dy (minor axis increment)
//   - BLTAMOD = 2*(dy-dx) (major axis increment)
//   - BLTCMOD = BLTDMOD = destination modulo (bytes per line)
//   - BLTSIZE height = line length in pixels, width must be 2
//
// graphics.library Draw encodes direction in BLTCON1 using this scheme
// (bit0=LINE always set; bit1=SING; bits2-4 select octant):
//
//	$11: +X major, +Y minor    $01: +Y major, +X minor
//	$19: +X major, -Y minor    $05: -Y major, +X minor
//	$15: -X major, +Y minor    $09: +Y major, -X minor
//	$1D: -X major, -Y minor    $0D: -Y major, -X minor
//
// SIGN of the error term is taken from APT (not from BLTCON1 bit 2, which is
// overloaded as part of the octant encoding above).
func (b *Blitter) executeLine(size uint16) {
	length := int(size >> 6)
	if length == 0 {
		length = 1024
	}

	minterm := byte(b.con0 & 0xFF)
	// Map Draw's BLTCON1 bits 2-4 (AUL/SUL/SUD packing) to internal octants:
	// key 0..7 → octant 2,6,3,7,0,4,1,5
	keyToOctant := [8]int{2, 6, 3, 7, 0, 4, 1, 5}
	octant := keyToOctant[(b.con1>>2)&7]
	// SING (bit 1): at most one pixel per raster line (WinUAE onedot).
	// Y change clears onedot; used for area-fill edges, not a minor-step proxy.
	singleMode := b.con1&0x0002 != 0

	// Start pixel position within word (BLTCON0 bits 15-12)
	pixelBit := (b.con0 >> 12) & 0xF

	// Bresenham error accumulator (signed 16-bit in APT low word).
	// graphics.library stores two's complement and sets BLTCON1 SIGN (bit 6).
	errAcc := int16(b.aptL)
	sign := errAcc < 0
	if b.con1&0x0040 != 0 {
		// Explicit SIGN wins when APT was even-truncated (address-register quirk)
		sign = true
	}

	// C is stepped for geometry; D receives the write then tracks C (WinUAE).
	cpt := b.ptr(b.cptH, b.cptL)
	dpt := b.ptr(b.dptH, b.dptL)

	// Line mode uses CMOD for vertical steps (DMOD is ignored on real Agnus).
	lineModulo := int32(b.cmod)
	incMajor := int32(b.amod) // 2*(dy-dx) when error >= 0
	incMinor := int32(b.bmod) // 2*dy when error < 0

	// Texture from BDAT; start index is BLTCON1 bits 15-12 (not always bit 15).
	texture := b.bdat
	textureShift := uint((b.con1 >> 12) & 0xF)

	onedot := false // SING: true after plotting on current raster line
	buf := make([]byte, 2)

	if b.logger != nil {
		fmt.Fprintf(b.logger, "BLITTER LINE: len=%d oct=%d sign=%v bit=%d err=%d C=$%08X D=$%08X cmod=%d amod=%d bmod=%d min=$%02X SING=%v tex=%d\n",
			length, octant, sign, pixelBit, errAcc, cpt, dpt, b.cmod, b.amod, b.bmod, minterm, singleMode, textureShift)
	}

	stepX := func(dir int) {
		if dir > 0 {
			pixelBit++
			if pixelBit > 15 {
				pixelBit = 0
				cpt += 2
			}
		} else {
			if pixelBit == 0 {
				pixelBit = 15
				cpt -= 2
			} else {
				pixelBit--
			}
		}
	}
	stepY := func(dir int) {
		if dir > 0 {
			cpt = uint32(int32(cpt) + lineModulo)
		} else {
			cpt = uint32(int32(cpt) - lineModulo)
		}
		onedot = false // new raster line — SING may plot again
	}

	for i := 0; i < length; i++ {
		// A channel: single-pixel mask from BLTADAT=$8000 semantics
		aWord := uint16(1) << (15 - pixelBit)

		// B channel: texture bit (solid $FFFF → always on)
		var bWord uint16
		if texture&(1<<textureShift) != 0 {
			bWord = 0xFFFF
		}

		// C channel: destination word
		b.amiga.ChipRAM.Read(int(cpt&^1), buf)
		cWord := uint16(buf[0])<<8 | uint16(buf[1])

		dWord := applyMinterm(minterm, aWord, bWord, cWord)

		// SING: plot at most one pixel per horizontal raster line
		writePixel := !singleMode || !onedot
		if writePixel {
			onedot = true
			if dWord != 0 {
				b.zero = false
			}
			buf[0] = byte(dWord >> 8)
			buf[1] = byte(dWord)
			// Write through DPT, then D tracks C (hardware behaviour)
			dpt = cpt
			writeAddr := int(dpt &^ 1)
			b.amiga.ChipRAM.Write(writeAddr, buf)
			if b.DebugWriteHook != nil {
				b.DebugWriteHook(writeAddr, dWord, aWord, bWord, cWord, minterm)
			}
		}

		// Rotate texture start (decrement, wrap) — matches BLTCON1 field update
		if textureShift == 0 {
			textureShift = 15
		} else {
			textureShift--
		}

		// Bresenham: update error, then step axes
		oldSign := sign
		if sign {
			errAcc += int16(incMinor)
		} else {
			errAcc += int16(incMajor)
		}
		sign = errAcc < 0

		// Major axis always; minor when error was non-negative before update.
		// Octants: 0 +X/+Y  1 +X/-Y  2 +Y/+X  3 +Y/-X
		//          4 -X/+Y  5 -X/-Y  6 -Y/+X  7 -Y/-X
		switch octant {
		case 0, 1: // +X major
			stepX(+1)
		case 4, 5: // -X major
			stepX(-1)
		case 2, 3: // +Y major
			stepY(+1)
		case 6, 7: // -Y major
			stepY(-1)
		}
		if !oldSign {
			switch octant {
			case 0, 4: // +Y minor
				stepY(+1)
			case 1, 5: // -Y minor
				stepY(-1)
			case 2, 6: // +X minor
				stepX(+1)
			case 3, 7: // -X minor
				stepX(-1)
			}
		}
	}

	// Write back final state
	b.aptL = uint16(errAcc)
	b.cptH = uint16(cpt >> 16)
	b.cptL = uint16(cpt)
	b.dptH = uint16(cpt >> 16) // D tracks C
	b.dptL = uint16(cpt)
	// Update BLTCON0 ashift and BLTCON1 texture shift + SIGN (bit 6, not AUL bit 2!)
	b.con0 = (b.con0 & 0x0FFF) | (pixelBit << 12)
	b.con1 = (b.con1 & 0x0FFF) | (uint16(textureShift) << 12)
	if sign {
		b.con1 |= 0x0040
	} else {
		b.con1 &^= 0x0040
	}
}

// applyMinterm performs the 256-function boolean logic on three inputs.
// Each bit of the minterm byte selects which combination of A, B, C produces a 1.
func applyMinterm(minterm byte, a, b, c uint16) uint16 {
	var result uint16
	for bit := 0; bit < 16; bit++ {
		ab := (a >> uint(bit)) & 1
		bb := (b >> uint(bit)) & 1
		cb := (c >> uint(bit)) & 1
		// Index into minterm: ABC forms a 3-bit index
		idx := ab<<2 | bb<<1 | cb
		if minterm&(1<<idx) != 0 {
			result |= 1 << uint(bit)
		}
	}
	return result
}
