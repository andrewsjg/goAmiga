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
	wait   bool   // True if waiting for beam position
	waitVP uint16 // Wait vertical position
	waitHP uint16 // Wait horizontal position
	waitVM uint16 // Wait vertical mask
	waitHM uint16 // Wait horizontal mask

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
	c.wait = false
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

// StepLine processes Copper instructions for one complete scanline.
// The real Copper can start one instruction every CopperCyclesPerInstr CPU
// cycles (up to ~56 per line). When waiting, we check once whether the wait condition can be
// satisfied this line; if not, we skip entirely.
//
// Important: a WAIT whose position is already reached must not freeze the rest
// of the line. graphics.library COP2 often issues WAIT v=display_line mid-burst;
// if we stop the slot loop on that WAIT without also running the following
// MOVE (e.g. BPLCON0 enable), the first display line renders with planes off
// and bitplane pointers do not advance — the whole frame appears shifted down
// one line. That alternates with the per-cycle path (CPU running) and looks
// like vertical shake.
func (c *Copper) StepLine(hposStart, hposEnd, vpos int) {
	if c.pc < 0x1000 {
		return
	}
	// Fast path: if waiting, check if this line can satisfy the condition.
	if c.wait {
		vp := uint16(vpos) & c.waitVM
		wantVP := c.waitVP & c.waitVM
		if vp < wantVP {
			return // vpos too early — no chance this line
		}
		if vp == wantVP {
			// Might satisfy if hpos advances far enough this line
			hp := beamHP(hposEnd-1) & c.waitHM
			if hp < c.waitHP {
				return // won't reach required hpos this line
			}
		}
		// vp > waitVP or vp == waitVP && hposEnd might reach waitHP:
		// fall through to per-slot processing to handle the exact transition
	}
	// Per-slot processing: one instruction per Copper DMA slot
	maxSlots := (hposEnd - hposStart + CopperCyclesPerInstr - 1) / CopperCyclesPerInstr
	if maxSlots < 1 {
		maxSlots = 1
	}
	for slot := 0; slot < maxSlots; slot++ {
		hpos := hposStart + slot*CopperCyclesPerInstr
		if hpos >= hposEnd {
			hpos = hposEnd - 1
		}
		// Clear an already-met wait before fetching (covers WAIT decoded on a
		// previous slot of this same line for the current beam position).
		if c.wait {
			if !c.waitMet(hpos, vpos) {
				// Still waiting for a later hpos on this line — try next slot.
				if uint16(vpos)&c.waitVM == c.waitVP&c.waitVM {
					continue
				}
				return
			}
			c.wait = false
		}
		c.Step(hpos, vpos)
		if c.pc == 0 {
			return
		}
		if c.wait {
			// Just entered a wait. If it is already free at this beam position
			// (WAIT for current line decoded mid-line), clear and keep going so
			// the following MOVE still runs before RenderLine.
			if c.waitMet(hpos, vpos) {
				c.wait = false
				continue
			}
			// Wait for later this line: keep scanning slots with rising hpos.
			if uint16(vpos)&c.waitVM == c.waitVP&c.waitVM {
				continue
			}
			return // later line
		}
	}
}

// Step executes one Copper cycle. The Copper fetches a 32-bit instruction
// from chip RAM every other DMA cycle (effectively every 4 CPU cycles for
// a complete instruction). For simplicity, we execute one instruction per call.
//
// hpos is the beam tracker's position in CPU cycles (0..CPUClocksPerLine);
// see beamHP for the conversion to Copper horizontal positions.
func (c *Copper) Step(hpos, vpos int) {
	// Skip execution if PC is zero or points to the low-memory system area
	// (vectors, ExecBase jump table). Valid copper lists are always allocated
	// above the system structures. This guards against the Kickstart 1.3 bug
	// where LoadView dereferences a NULL CopList pointer, reading ExecBase
	// from address $4 and storing it as the LOF copper list pointer.
	if c.pc < 0x1000 {
		return
	}

	// If waiting, check beam position
	if c.wait {
		vp := uint16(vpos) & c.waitVM
		hp := beamHP(hpos) & c.waitHM
		// Mask the compare value the same way the beam is masked (HRM).
		wantVP := c.waitVP & c.waitVM
		if vp > wantVP || (vp == wantVP && hp >= c.waitHP) {
			c.wait = false
		} else {
			return
		}
	}

	// Fetch 32-bit instruction from chip RAM
	if c.pc == 0 {
		return
	}
	_, err := c.amiga.ChipRAM.Read(int(c.pc&0x7FFFF), c.buf[:4])
	if err != nil {
		return
	}
	ir1 := binary.BigEndian.Uint16(c.buf[0:2])
	ir2 := binary.BigEndian.Uint16(c.buf[2:4])
	c.pc += 4

	if ir1&1 == 0 {
		// MOVE: write ir2 to custom register at offset (ir1 & 0x1FE)
		regOffset := int(ir1 & 0x1FE)

		// Safety: COPCON bit 1 must be set to write to regs < $040
		if regOffset < 0x040 {
			copcon := c.amiga.Custom.ReadReg16(0x02E)
			if copcon&0x02 == 0 {
				return // write blocked
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

		// End-of-list detection: WAIT $FFFF,$FFFE
		if ir1 == 0xFFFF && ir2 == 0xFFFE {
			c.wait = true // will never match, effectively stops Copper
			return
		}

		// If the beam is already at/past this position, do not stall: the next
		// copper cycle must fetch the following instruction on this same line.
		// (Leaving wait=true here made StepLine return early and pushed the
		// following MOVE — often BPLCON0 enable — to the next scanline.)
		if c.waitMet(hpos, vpos) {
			c.wait = false
		} else {
			c.wait = true
		}
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
