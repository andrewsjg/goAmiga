package amiga

import (
	"fmt"
	"io"
)

// Custom chip register names (selected important ones)
var customRegNames = map[int]string{
	0x000: "BLTDDAT", 0x002: "DMACONR", 0x004: "VPOSR", 0x006: "VHPOSR",
	0x008: "DSKDATR", 0x00A: "JOY0DAT", 0x00C: "JOY1DAT", 0x00E: "CLXDAT",
	0x010: "ADKCONR", 0x012: "POT0DAT", 0x014: "POT1DAT", 0x016: "POTGOR",
	0x018: "SERDATR", 0x01A: "DSKBYTR", 0x01C: "INTENAR", 0x01E: "INTREQR",
	0x020: "DSKPT", 0x024: "DSKLEN", 0x026: "DSKDAT",
	0x028: "REFPTR", 0x02A: "VPOSW", 0x02C: "VHPOSW",
	0x02E: "COPCON", 0x030: "SERDAT", 0x032: "SERPER",
	0x034: "POTGO", 0x036: "JOYTEST",
	0x040: "BLTCON0", 0x042: "BLTCON1",
	0x044: "BLTAFWM", 0x046: "BLTALWM",
	0x048: "BLTCPT", 0x04C: "BLTBPT", 0x050: "BLTAPT", 0x054: "BLTDPT",
	0x058: "BLTSIZE", 0x05A: "BLTCON0L",
	0x060: "BLTCMOD", 0x062: "BLTBMOD", 0x064: "BLTAMOD", 0x066: "BLTDMOD",
	0x070: "BLTCDAT", 0x072: "BLTBDAT", 0x074: "BLTADAT",
	0x078: "SPRHDAT",
	0x07C: "DENISEID",
	0x07E: "DSKSYNC",
	0x080: "COP1LC", 0x084: "COP2LC",
	0x088: "COPJMP1", 0x08A: "COPJMP2",
	0x08C: "COPINS",
	0x08E: "DIWSTRT", 0x090: "DIWSTOP",
	0x092: "DDFSTRT", 0x094: "DDFSTOP",
	0x096: "DMACON",
	0x098: "CLXCON",
	0x09A: "INTENA", 0x09C: "INTREQ",
	0x09E: "ADKCON",
	0x0E0: "BPL1PT", 0x0E4: "BPL2PT", 0x0E8: "BPL3PT",
	0x0EC: "BPL4PT", 0x0F0: "BPL5PT", 0x0F4: "BPL6PT",
	0x100: "BPLCON0", 0x102: "BPLCON1", 0x104: "BPLCON2",
	0x108: "BPL1MOD", 0x10A: "BPL2MOD",
	0x110: "BPL1DAT", 0x112: "BPL2DAT", 0x114: "BPL3DAT",
	0x116: "BPL4DAT", 0x118: "BPL5DAT", 0x11A: "BPL6DAT",
	0x120: "SPR0PT", 0x124: "SPR1PT", 0x128: "SPR2PT", 0x12C: "SPR3PT",
	0x130: "SPR4PT", 0x134: "SPR5PT", 0x138: "SPR6PT", 0x13C: "SPR7PT",
	0x180: "COLOR00", 0x182: "COLOR01", 0x184: "COLOR02", 0x186: "COLOR03",
	0x188: "COLOR04", 0x18A: "COLOR05", 0x18C: "COLOR06", 0x18E: "COLOR07",
	0x190: "COLOR08", 0x192: "COLOR09", 0x194: "COLOR10", 0x196: "COLOR11",
	0x198: "COLOR12", 0x19A: "COLOR13", 0x19C: "COLOR14", 0x19E: "COLOR15",
	0x1A0: "COLOR16", 0x1A2: "COLOR17", 0x1A4: "COLOR18", 0x1A6: "COLOR19",
	0x1A8: "COLOR20", 0x1AA: "COLOR21", 0x1AC: "COLOR22", 0x1AE: "COLOR23",
	0x1B0: "COLOR24", 0x1B2: "COLOR25", 0x1B4: "COLOR26", 0x1B6: "COLOR27",
	0x1B8: "COLOR28", 0x1BA: "COLOR29", 0x1BC: "COLOR30", 0x1BE: "COLOR31",
}

// CustomChips implements the Amiga custom chip register file at $DFF000-$DFF1FF.
type CustomChips struct {
	regs   [512]byte // raw register storage
	logger io.Writer

	amiga *Amiga // back-reference for interrupt/DMA coordination
}

// NewCustomChips creates a new custom chip register set.
func NewCustomChips(logger io.Writer, a *Amiga) *CustomChips {
	return &CustomChips{
		logger: logger,
		amiga:  a,
	}
}

func (c *CustomChips) Read(addr int, p []byte) (int, error) {
	offset := addr & 0x1FF

	// Return plausible values for read registers
	n := len(p)
	if offset+n > len(c.regs) {
		n = len(c.regs) - offset
	}

	switch offset {
	case 0x002: // DMACONR
		copy(p, c.regs[0x096:0x098]) // mirror DMACON write register
		// Set bit 14 (BBUSY) when blitter is actively running
		if c.amiga != nil && c.amiga.Blitter != nil && c.amiga.Blitter.busy {
			p[0] |= 0x40
		}
	case 0x01C: // INTENAR
		copy(p, c.regs[0x09A:0x09C])
	case 0x01E: // INTREQR
		copy(p, c.regs[0x09C:0x09E])
	case 0x004: // VPOSR - LOF (bit 15), Agnus ID (bits 14-9 = 0 for OCS), V8 (bit 8)
		// For longword reads ($DFF004), bytes 0-1 = VPOSR, bytes 2-3 = VHPOSR.
		// The beam reader at FC5ECE does MOVE.L $DFF004, D0 expecting exactly this.
		if c.amiga != nil {
			var val uint16
			if c.amiga.lof {
				val |= 0x8000 // LOF
			}
			val |= uint16((c.amiga.vpos >> 8) & 1) // V8
			p[0] = byte(val >> 8)
			if len(p) > 1 {
				p[1] = byte(val)
			}
			// Longword read: bytes 2-3 = VHPOSR (V7:V0 in byte 2, H8:H1 in byte 3)
			if len(p) > 2 {
				p[2] = byte(c.amiga.vpos) // V7:V0
			}
			if len(p) > 3 {
				p[3] = byte(c.amiga.hpos / CPUCyclesPerColorClock) // H8:H1
			}
		}
	case 0x006: // VHPOSR - V7-V0 (bits 15-8), H8-H1 (bits 7-0)
		if c.amiga != nil {
			p[0] = byte(c.amiga.vpos)
			if len(p) > 1 {
				p[1] = byte(c.amiga.hpos / CPUCyclesPerColorClock)
			}
		}
	case 0x00A: // JOY0DAT - mouse/joystick 0 data
		if c.amiga != nil && c.amiga.Mouse != nil {
			val := c.amiga.Mouse.JOY0DAT()
			p[0] = byte(val >> 8)
			if len(p) > 1 {
				p[1] = byte(val)
			}
		}
	case 0x016: // POTGOR - pot port data
		if c.amiga != nil && c.amiga.Mouse != nil {
			val := c.amiga.Mouse.POTGOR()
			p[0] = byte(val >> 8)
			if len(p) > 1 {
				p[1] = byte(val)
			}
		} else {
			p[0] = 0xFF
			if len(p) > 1 {
				p[1] = 0xFF
			}
		}
	case 0x010: // ADKCONR
		if c.amiga != nil && c.amiga.Disk != nil {
			val := c.amiga.Disk.ReadADKCONR()
			p[0] = byte(val >> 8)
			if len(p) > 1 {
				p[1] = byte(val)
			}
		}
	case 0x01A: // DSKBYTR
		if c.amiga != nil && c.amiga.Disk != nil {
			val := c.amiga.Disk.ReadDSKBYTR()
			p[0] = byte(val >> 8)
			if len(p) > 1 {
				p[1] = byte(val)
			}
		}
	case 0x018: // SERDATR - serial port status/data
		// Return TBE (bit 13) + TSRE (bit 12) set (transmitter ready).
		// RBF (bit 14) = 0 (no data received).
		// Data bits = $00 (no valid frame received).
		// Kickstart serial detection checks bits 6:0 for $7F — returning $00
		// correctly indicates "no serial terminal" and avoids serial console mode.
		val := uint16(0x3000)
		p[0] = byte(val >> 8)
		if len(p) > 1 {
			p[1] = byte(val)
		}
	default:
		copy(p, c.regs[offset:offset+n])
	}

	if c.logger != nil {
		name := customRegNames[offset]
		if name == "" {
			name = fmt.Sprintf("CUSTOM+$%03X", offset)
		}
		if n == 2 && offset+1 < len(c.regs) {
			val := uint16(p[0])<<8 | uint16(p[1])
			fmt.Fprintf(c.logger, "CUSTOM READ  %s ($DFF%03X) = $%04X\n", name, offset, val)
		} else {
			fmt.Fprintf(c.logger, "CUSTOM READ  %s ($DFF%03X)\n", name, offset)
		}
	}

	return n, nil
}

func (c *CustomChips) Write(addr int, p []byte) (int, error) {
	// The Amiga custom chip register bus is 16-bit. Long (4-byte) writes
	// are split into two consecutive word writes, each triggering independent
	// register side effects. Without this split, a long write to e.g. $094
	// would overwrite $096 (DMACON) raw bytes, bypassing set/clear logic.
	if len(p) == 4 {
		c.Write(addr, p[0:2])
		c.Write(addr+2, p[2:4])
		return 4, nil
	}

	offset := addr & 0x1FF

	n := len(p)
	if offset+n > len(c.regs) {
		n = len(c.regs) - offset
	}
	// Do NOT raw-copy into set/clear registers — their switch cases handle
	// the bit manipulation and store the result. For all other registers,
	// the raw copy is the correct behavior.
	switch offset {
	case 0x096, 0x09A, 0x09C, 0x09E: // DMACON, INTENA, INTREQ, ADKCON — set/clear registers
		// handled in switch below — skip raw copy so set/clear logic works correctly
	default:
		copy(c.regs[offset:offset+n], p[:n])
	}

	if c.logger != nil {
		name := customRegNames[offset]
		if name == "" {
			name = fmt.Sprintf("CUSTOM+$%03X", offset)
		}
		if n == 2 {
			val := uint16(p[0])<<8 | uint16(p[1])
			fmt.Fprintf(c.logger, "CUSTOM WRITE %s ($DFF%03X) = $%04X\n", name, offset, val)
		} else {
			fmt.Fprintf(c.logger, "CUSTOM WRITE %s ($DFF%03X)\n", name, offset)
		}
	}

	// Handle special register writes
	switch offset {
	case 0x096: // DMACON
		// set/clear mode: bit 15 = set (1) or clear (0)
		val := uint16(p[0])<<8 | uint16(p[1])
		oldDmacon := uint16(c.regs[0x096])<<8 | uint16(c.regs[0x097])
		if val&0x8000 != 0 {
			// set bits
			oldDmacon |= val & 0x7FFF
		} else {
			// clear bits
			oldDmacon &^= val & 0x7FFF
		}
		c.regs[0x096] = byte(oldDmacon >> 8)
		c.regs[0x097] = byte(oldDmacon)

	case 0x09A: // INTENA
		val := uint16(p[0])<<8 | uint16(p[1])
		if val&0x8000 != 0 {
			old := uint16(c.regs[0x09A])<<8 | uint16(c.regs[0x09B])
			old |= val & 0x7FFF
			c.regs[0x09A] = byte(old >> 8)
			c.regs[0x09B] = byte(old)
		} else {
			old := uint16(c.regs[0x09A])<<8 | uint16(c.regs[0x09B])
			old &^= val & 0x7FFF
			c.regs[0x09A] = byte(old >> 8)
			c.regs[0x09B] = byte(old)
		}
		c.UpdateInterrupts()

	case 0x09C: // INTREQ
		val := uint16(p[0])<<8 | uint16(p[1])
		if val&0x8000 != 0 {
			old := uint16(c.regs[0x09C])<<8 | uint16(c.regs[0x09D])
			old |= val & 0x7FFF
			c.regs[0x09C] = byte(old >> 8)
			c.regs[0x09D] = byte(old)
		} else {
			old := uint16(c.regs[0x09C])<<8 | uint16(c.regs[0x09D])
			old &^= val & 0x7FFF
			c.regs[0x09C] = byte(old >> 8)
			c.regs[0x09D] = byte(old)
		}
		c.UpdateInterrupts()

	case 0x080: // COP1LCH
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.cop1lc = (c.amiga.Copper.cop1lc & 0x0000FFFF) | uint32(p[0])<<24 | uint32(p[1])<<16
		}
	case 0x082: // COP1LCL
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.cop1lc = (c.amiga.Copper.cop1lc & 0xFFFF0000) | uint32(p[0])<<8 | uint32(p[1])
		}
	case 0x084: // COP2LCH
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.cop2lc = (c.amiga.Copper.cop2lc & 0x0000FFFF) | uint32(p[0])<<24 | uint32(p[1])<<16
		}
	case 0x086: // COP2LCL
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.cop2lc = (c.amiga.Copper.cop2lc & 0xFFFF0000) | uint32(p[0])<<8 | uint32(p[1])
		}
	case 0x0E0, 0x0E4, 0x0E8, 0x0EC, 0x0F0, 0x0F4: // BPLxPTH (high word)
		if c.amiga != nil && c.amiga.Video != nil {
			planeIdx := (offset - 0x0E0) / 4
			hi := uint32(p[0])<<24 | uint32(p[1])<<16
			newPt := (c.amiga.Video.bplpt[planeIdx] & 0x0000FFFF) | hi
			// KS copper list 1 blanks BPL1PTL early each frame; applying that
			// zero to the working display pointer before COP2 reloads it makes
			// plane 0 disappear / jump for a line and looks like vertical shake.
			// Only push non-zero reloads (and always allow high-word updates that
			// leave a non-zero address).
			if newPt != 0 {
				c.amiga.Video.bplpt[planeIdx] = newPt
			}
		}
	case 0x0E2, 0x0E6, 0x0EA, 0x0EE, 0x0F2, 0x0F6: // BPLxPTL (low word)
		if c.amiga != nil && c.amiga.Video != nil {
			planeIdx := (offset - 0x0E2) / 4
			lo := uint32(p[0])<<8 | uint32(p[1])
			newPt := (c.amiga.Video.bplpt[planeIdx] & 0xFFFF0000) | lo
			if newPt != 0 {
				c.amiga.Video.bplpt[planeIdx] = newPt
			}
		}

	case 0x088: // COPJMP1
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.Restart(1)
		}
	case 0x08A: // COPJMP2
		if c.amiga != nil && c.amiga.Copper != nil {
			c.amiga.Copper.Restart(2)
		}

	// Blitter registers
	case 0x040: // BLTCON0
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.con0 = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x042: // BLTCON1
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.con1 = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x044: // BLTAFWM
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.afwm = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x046: // BLTALWM
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.alwm = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x048: // BLTCPTH
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.cptH = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x04A: // BLTCPTL
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.cptL = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x04C: // BLTBPTH
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.bptH = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x04E: // BLTBPTL
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.bptL = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x050: // BLTAPTH
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.aptH = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x052: // BLTAPTL
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.aptL = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x054: // BLTDPTH
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.dptH = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x056: // BLTDPTL
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.dptL = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x058: // BLTSIZE — triggers blit operation
		if c.amiga != nil && c.amiga.Blitter != nil {
			dmacon := c.ReadReg16(0x096)
			size := uint16(p[0])<<8 | uint16(p[1])
			if dmacon&0x0240 == 0x0240 { // DMAEN (bit 9) + BLTEN (bit 6)
				c.amiga.Blitter.Execute(size)
			}
		}
	case 0x060: // BLTCMOD
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.cmod = int16(uint16(p[0])<<8 | uint16(p[1]))
		}
	case 0x062: // BLTBMOD
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.bmod = int16(uint16(p[0])<<8 | uint16(p[1]))
		}
	case 0x064: // BLTAMOD
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.amod = int16(uint16(p[0])<<8 | uint16(p[1]))
		}
	case 0x066: // BLTDMOD
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.dmod = int16(uint16(p[0])<<8 | uint16(p[1]))
		}
	// Sprite pointer registers ($120-$13E)
	case 0x120, 0x124, 0x128, 0x12C, 0x130, 0x134, 0x138, 0x13C: // SPRxPTH
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x120) / 4
			c.amiga.Sprites.SetPT(sprite, true, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x122, 0x126, 0x12A, 0x12E, 0x132, 0x136, 0x13A, 0x13E: // SPRxPTL
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x122) / 4
			c.amiga.Sprites.SetPT(sprite, false, uint16(p[0])<<8|uint16(p[1]))
		}

	// Sprite data registers ($140-$17E)
	case 0x140, 0x148, 0x150, 0x158, 0x160, 0x168, 0x170, 0x178: // SPRxPOS
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x140) / 8
			c.amiga.Sprites.SetPOS(sprite, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x142, 0x14A, 0x152, 0x15A, 0x162, 0x16A, 0x172, 0x17A: // SPRxCTL
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x142) / 8
			c.amiga.Sprites.SetCTL(sprite, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x144, 0x14C, 0x154, 0x15C, 0x164, 0x16C, 0x174, 0x17C: // SPRxDATA
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x144) / 8
			c.amiga.Sprites.SetDATA(sprite, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x146, 0x14E, 0x156, 0x15E, 0x166, 0x16E, 0x176, 0x17E: // SPRxDATB
		if c.amiga != nil && c.amiga.Sprites != nil {
			sprite := (offset - 0x146) / 8
			c.amiga.Sprites.SetDATB(sprite, uint16(p[0])<<8|uint16(p[1]))
		}

	// Disk registers
	case 0x020: // DSKPTH
		if c.amiga != nil && c.amiga.Disk != nil {
			c.amiga.Disk.WriteDSKPT(true, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x022: // DSKPTL
		if c.amiga != nil && c.amiga.Disk != nil {
			c.amiga.Disk.WriteDSKPT(false, uint16(p[0])<<8|uint16(p[1]))
		}
	case 0x024: // DSKLEN
		if c.amiga != nil && c.amiga.Disk != nil {
			c.amiga.Disk.WriteDSKLEN(uint16(p[0])<<8 | uint16(p[1]))
		}
	case 0x07E: // DSKSYNC
		if c.amiga != nil && c.amiga.Disk != nil {
			c.amiga.Disk.WriteDSKSYNC(uint16(p[0])<<8 | uint16(p[1]))
		}
	case 0x09E: // ADKCON
		if c.amiga != nil && c.amiga.Disk != nil {
			c.amiga.Disk.WriteADKCON(uint16(p[0])<<8 | uint16(p[1]))
		}

	case 0x070: // BLTCDAT
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.cdat = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x072: // BLTBDAT
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.bdat = uint16(p[0])<<8 | uint16(p[1])
		}
	case 0x074: // BLTADAT
		if c.amiga != nil && c.amiga.Blitter != nil {
			c.amiga.Blitter.adat = uint16(p[0])<<8 | uint16(p[1])
		}
	}

	return n, nil
}

func (c *CustomChips) Reset() error {
	c.regs = [512]byte{}
	return nil
}

// ReadReg16 returns a 16-bit register value at the given offset.
func (c *CustomChips) ReadReg16(offset int) uint16 {
	return uint16(c.regs[offset])<<8 | uint16(c.regs[offset+1])
}

// WriteReg16 writes a 16-bit value to the raw register storage at the given offset.
// Used by the Copper to write registers without triggering side effects or logging.
func (c *CustomChips) WriteReg16(offset int, val uint16) {
	c.regs[offset] = byte(val >> 8)
	c.regs[offset+1] = byte(val)
}

// SetIntreq sets bits in INTREQ and calls UpdateInterrupts.
func (c *CustomChips) SetIntreq(bits uint16) {
	old := c.ReadReg16(0x09C)
	old |= bits
	c.regs[0x09C] = byte(old >> 8)
	c.regs[0x09D] = byte(old)
	c.UpdateInterrupts()
}

// UpdateInterrupts maps INTREQ & INTENA to 68000 IPL levels and sets CPU interrupt level.
func (c *CustomChips) UpdateInterrupts() {
	if c.amiga == nil || c.amiga.CPU == nil {
		return
	}

	intena := c.ReadReg16(0x09A)
	intreq := c.ReadReg16(0x09C)

	// Master enable is bit 14 of INTENA
	if intena&0x4000 == 0 {
		c.amiga.CPU.SetInterruptLevel(0)
		return
	}

	active := intreq & intena & 0x3FFF

	// Map active bits to highest 68000 IPL level.
	// Amiga hardware manual, interrupt bits:
	//   Bit 0 TBE, 1 DSKBLK, 2 SOFT   → IPL 1
	//   Bit 3 PORTS (CIA-A)            → IPL 2
	//   Bit 4 COPER, 5 VERTB, 6 BLIT  → IPL 3
	//   Bit 7 AUD0, 8 AUD1, 9 AUD2, 10 AUD3 → IPL 4
	//   Bit 11 RBF, 12 DSKSYN          → IPL 5
	//   Bit 13 EXTER (CIA-B)           → IPL 6
	var level uint32
	switch {
	case active&0x2000 != 0: // bit 13: EXTER (CIA-B)
		level = 6
	case active&0x1800 != 0: // bits 11-12: RBF, DSKSYN
		level = 5
	case active&0x0780 != 0: // bits 7-10: AUD0-3
		level = 4
	case active&0x0070 != 0: // bits 4-6: COPER, VERTB, BLIT
		level = 3
	case active&0x0008 != 0: // bit 3: PORTS (CIA-A)
		level = 2
	case active&0x0007 != 0: // bits 0-2: TBE, DSKBLK, SOFT
		level = 1
	}

	c.amiga.CPU.SetInterruptLevel(level)
}
