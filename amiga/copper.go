package amiga

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Copper implements the Amiga Copper coprocessor.
// The Copper executes a simple instruction stream from chip RAM,
// with two instructions: MOVE (write to custom register) and WAIT/SKIP
// (synchronize with beam position).
type Copper struct {
	cop1lc uint32 // Copper list 1 pointer
	cop2lc uint32 // Copper list 2 pointer
	pc     uint32 // Current instruction pointer

	// halted is set when a MOVE targets a register the Copper may not
	// write; it stops until restarted (VBLANK or a COPJMP strobe).
	halted  bool
	wait    bool // True if waiting for beam position
	compare bool // a WAIT was fetched: compare the beam next cycle
	second  bool // the first word (ir1) of an instruction has been fetched
	ir1     uint16
	waitVP  uint16 // Wait vertical position
	waitHP  uint16 // Wait horizontal position
	waitVM  uint16 // Wait vertical mask
	waitHM  uint16 // Wait horizontal mask

	// The beam position of the Copper's last word fetch, for the CPU's
	// slot (ExactBus).
	fetched        bool
	fetchV, fetchH int

	amiga  *Amiga
	logger io.Writer
	buf    [4]byte
}

// NewCopper creates a new Copper coprocessor.
func NewCopper(a *Amiga) *Copper {
	return &Copper{
		amiga:  a,
		logger: a.logger,
	}
}

// GetCop1LC returns the Copper list 1 pointer.
func (c *Copper) GetCop1LC() uint32 { return c.cop1lc }

// GetCop2LC returns the Copper list 2 pointer.
func (c *Copper) GetCop2LC() uint32 { return c.cop2lc }

// Restart sets the Copper PC to COP1LC (which=1) or COP2LC (which=2).
func (c *Copper) Restart(which int) {
	c.wait, c.compare, c.second = false, false, false
	c.halted = false
	if which == 1 {
		c.pc = c.cop1lc
	} else {
		c.pc = c.cop2lc
	}
}

// beamHP converts the beam tracker's hpos (CPU cycles) to the value the
// Copper compares against WAIT/SKIP HP fields. The instruction holds beam
// bits H8-H2 (colour clocks with bit 0 dropped); waitHP/HM store them
// shifted down by one, so the beam is shifted the same way.
func beamHP(hpos int) uint16 {
	return uint16(hpos/CPUCyclesPerColorClock) >> 1
}

// waitMet reports whether the current beam position satisfies the copper wait.
func (c *Copper) waitMet(hpos, vpos int) bool {
	vp := uint16(vpos) & c.waitVM
	hp := beamHP(hpos) & c.waitHM
	wantVP := c.waitVP & c.waitVM
	return vp > wantVP || (vp == wantVP && hp >= c.waitHP)
}

// StepLine runs the Copper over the part of a scanline from hposStart to
// hposEnd (CPU cycles), one bus cycle every CopperCyclesPerWord. It is the
// batched equivalent of calling Step on each of those cycles.
func (c *Copper) StepLine(hposStart, hposEnd, vpos int) {
	if c.halted || c.wait && !c.canWake(hposEnd, vpos) {
		return
	}
	for h := (hposStart + CopperCyclesPerWord - 1) &^ (CopperCyclesPerWord - 1); h < hposEnd; h += CopperCyclesPerWord {
		c.Step(h, vpos)
		if c.halted || c.wait && !c.canWake(hposEnd, vpos) {
			return
		}
	}
}

// canWake reports whether a pending WAIT can end on line vpos before
// hposEnd.
func (c *Copper) canWake(hposEnd, vpos int) bool {
	vp := uint16(vpos) & c.waitVM
	wantVP := c.waitVP & c.waitVM
	return vp > wantVP || vp == wantVP && beamHP(hposEnd-1)&c.waitHM >= c.waitHP
}

// Step runs one Copper bus cycle at beam position (hpos, vpos). The Copper
// owns the even colour clocks, one every CopperCyclesPerWord CPU cycles, and
// in each it either fetches one instruction word or does its internal work:
//
//   - An instruction is two words; it executes when the second arrives.
//   - A WAIT compares the beam in the cycle after its second word. When
//     that cycle falls on the next line, the comparison sees the new line:
//     this is what makes WAIT $FFDE followed by WAIT $2C wait for line
//     256+$2C rather than end at once on line 255.
//   - Waking from a WAIT takes a cycle.
//   - Bitplane DMA outranks the Copper: a word cannot be fetched in a slot
//     the bitplanes take. Hires with 4 planes takes every even slot of the
//     fetch window (lores with 6, and hires with 3, half of them), so the
//     Copper stalls there. graphics.library relies on this for hires
//     screens: it ends the display with WAIT (255,$38); WAIT (44,0), and
//     the second WAIT is only fetched after the fetch window, at the very
//     end of line 255, so it is compared on line 256.
//
// hpos is the beam tracker's position in CPU cycles (0..Amiga.lineCycles);
// see beamHP for the conversion to Copper horizontal positions.
func (c *Copper) Step(hpos, vpos int) {
	if c.halted {
		return
	}
	if c.wait {
		if c.waitMet(hpos, vpos) {
			c.wait = false // waking takes this cycle
		}
		return
	}
	if c.compare {
		c.compare = false
		c.wait = !c.waitMet(hpos, vpos)
		return
	}
	if c.amiga.bitplaneDMA(vpos).uses(hpos / CPUCyclesPerColorClock) {
		return
	}
	if _, err := c.amiga.ChipRAM.Read(int(c.pc), c.buf[:2]); err != nil {
		return
	}
	c.fetched, c.fetchV, c.fetchH = true, vpos, hpos
	c.pc += 2
	w := binary.BigEndian.Uint16(c.buf[0:2])
	if !c.second {
		c.ir1, c.second = w, true
		return
	}
	c.second = false
	c.execute(c.ir1, w, hpos, vpos)
}

// execute runs a fetched instruction.
func (c *Copper) execute(ir1, ir2 uint16, hpos, vpos int) {
	if ir1&1 == 0 {
		// MOVE: write ir2 to custom register at offset (ir1 & 0x1FE)
		regOffset := int(ir1 & 0x1FE)

		// OCS: the Copper can never write $000-$03E, and $040-$07E only
		// with COPCON CDANG set. An illegal MOVE halts it until the next
		// VBLANK or COPJMP. (Kickstart 1.3 relies on this: with no
		// view copper list, COP2LC is 0 and the system list's COPJMP2
		// sends the Copper to address 0, whose first word is a MOVE to
		// $000 — so it just stops.)
		if regOffset < 0x080 {
			cdang := c.amiga.Custom.ReadReg16(0x02E)&0x02 != 0
			if regOffset < 0x040 || !cdang {
				c.halted = true
				return
			}
		}

		if c.logger != nil {
			name := customRegNames[regOffset]
			if name == "" {
				name = fmt.Sprintf("$%03X", regOffset)
			}
			fmt.Fprintf(c.logger, "COPPER MOVE %s = $%04X\n", name, ir2)
		}

		// Write via the Memory interface to trigger side effects
		c.buf[0] = byte(ir2 >> 8)
		c.buf[1] = byte(ir2)
		c.amiga.Custom.copperHpos = hpos
		c.amiga.Custom.Write(regOffset, c.buf[:2])
		c.amiga.Custom.copperHpos = -1

	} else if ir2&1 == 0 {
		// WAIT: wait until beam reaches position
		c.waitVP = (ir1 >> 8) & 0xFF
		c.waitHP = (ir1 & 0xFE) >> 1
		c.waitVM = ((ir2 >> 8) & 0x7F) | 0x80 // bit 15 is blitter-finished-disable
		c.waitHM = (ir2 & 0xFE) >> 1

		// End-of-list: WAIT $FFFF,$FFFE never matches.
		if ir1 == 0xFFFF && ir2 == 0xFFFE {
			c.wait = true
			return
		}
		c.compare = true // in the next cycle
	} else {
		// SKIP: skip next instruction if beam is past position
		skipVP := (ir1 >> 8) & 0xFF
		skipHP := (ir1 & 0xFE) >> 1
		skipVM := ((ir2 >> 8) & 0x7F) | 0x80
		skipHM := (ir2 & 0xFE) >> 1

		vp := uint16(vpos) & skipVM
		hp := beamHP(hpos) & skipHM
		wantVP := skipVP & skipVM
		if vp > wantVP || (vp == wantVP && hp >= skipHP) {
			c.pc += 4 // skip next instruction
		}
	}
}

// busyWords estimates how many words the Copper will fetch before it next
// waits: 0 while it waits, is halted or has no DMA; otherwise the words
// up to and including the next WAIT or SKIP, read ahead from its list
// without executing anything. The CPU and blitter lose that many even
// bus slots to it (see chipBusWait, blitterSlots).
func (c *Copper) busyWords() int {
	if c.halted || c.wait || c.amiga.Custom.ReadReg16(0x096)&0x0280 != 0x0280 {
		return 0
	}
	words := 0
	pc := c.pc
	if c.second { // half-way through an instruction
		ir2Addr := pc
		words++
		if c.ir1&1 != 0 {
			return words
		}
		pc = ir2Addr + 2
	}
	var b [2]byte
	for words < 64 {
		c.amiga.ChipRAM.Read(int(pc), b[:])
		words += 2
		if b[1]&1 != 0 { // WAIT or SKIP ends the burst
			break
		}
		pc += 4
	}
	return words
}
