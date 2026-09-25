package amiga

import (
	"fmt"
	"io"
)

// CIA register offsets (accent on even bytes for CIA-A, odd bytes for CIA-B)
const (
	CIARegPRA    = 0x00
	CIARegPRB    = 0x01
	CIARegDDRA   = 0x02
	CIARegDDRB   = 0x03
	CIARegTALO   = 0x04
	CIARegTAHI   = 0x05
	CIARegTBLO   = 0x06
	CIARegTBHI   = 0x07
	CIARegTODLO  = 0x08
	CIARegTODMID = 0x09
	CIARegTODHI  = 0x0A
	CIARegSDR    = 0x0C
	CIARegICR    = 0x0D
	CIARegCRA    = 0x0E
	CIARegCRB    = 0x0F
)

// CIA implements a MOS 8520 CIA chip.
// CIA-A is at $BFE001 (directly addressed on odd bytes)
// CIA-B is at $BFD000 (directly addressed on even bytes)
type CIA struct {
	Name   string
	regs   [16]byte
	logger io.Writer

	// Timer A
	timerALatch   uint16
	timerACounter uint16

	// Timer B
	timerBLatch   uint16
	timerBCounter uint16

	// TOD (Time of Day) counter — 24-bit binary counter on the 8520.
	// On the Amiga the TOD inputs are wired to the video sync signals:
	//   CIA-A TOD ← vertical sync  (one tick per frame, 50 Hz PAL)
	//   CIA-B TOD ← horizontal sync (one tick per scanline)
	// Both are driven by TickTOD() from the beam tracker, not the E clock.
	// graphics.library relies on the CIA-B wiring for beam-synchronised
	// blits: each VBLANK it reloads TOD with 1, then sets an alarm at the
	// raster line a queued blit must run on.
	todCounter uint32 // 24-bit: (TODHI<<16)|(TODMID<<8)|TODLO
	todAlarm   uint32 // 24-bit alarm; ICR bit 2 fires when TOD matches
	todLatch   uint32 // latched value when TODHI is read
	todLatched bool   // whether TOD is currently latched
	todStopped bool   // writing TODHI halts the counter until TODLO is written
	todClkDiv  int    // E-clock divider accumulator (legacy E-clock TOD source)
	todClkRate int    // E-clock ticks per TOD increment (0 = disabled)

	// Interrupt control
	icrMask     uint8 // ICR mask register (bits enabled for interrupt)
	icrData     uint8 // ICR data register (pending interrupts)
	irqAsserted bool  // tracks IR output state for edge-triggered firing

	// Callback when an unmasked interrupt fires
	OnInterrupt func()

	// Debug counters
	TimerAUnderflows int
	TimerBUnderflows int
	TimerBTicks      int // how many times Timer B decremented
	InterruptFires   int
	CRAWrites        int      // count CRA writes with bit 0 set (timer start)
	CRBWrites        int      // count CRB writes with bit 0 set (timer start)
	CRBWriteLog      []string // log of all CRB writes
	StepCount        int      // set externally to current CPU step for logging

	// Optional callbacks for peripheral wiring
	OnPRBWrite func(old, new byte)    // fired when PRB is written (CIA-B: disk control)
	OnPRAWrite func(old, new byte)    // fired when PRA is written (CIA-A: OVL toggle)
	OnPRARead  func(regVal byte) byte // modify PRA on read (CIA-A: mouse button, CIA-B: disk status)
	OnCRAWrite func(old, new byte)    // fired when CRA is written (CIA-A: keyboard handshake)
}

// NewCIA creates a new CIA chip.
func NewCIA(name string, logger io.Writer) *CIA {
	c := &CIA{
		Name:   name,
		logger: logger,
	}
	// Default PRA values
	if name == "CIA-A" {
		// OVL bit (bit 0) starts high (ROM overlay active)
		c.regs[CIARegPRA] = 0x03 // OVL=1, LED=1
	}
	return c
}

func (c *CIA) Read(addr int, p []byte) (int, error) {
	// CIA-A is on odd addresses, CIA-B on even. The address decoding
	// maps to register indices via bits 8-11 of the address.
	reg := (addr >> 8) & 0x0F
	n := len(p)

	var val byte
	switch reg {
	case CIARegICR:
		// Reading ICR returns and clears the data register
		val = c.icrData
		if c.icrData&c.icrMask != 0 {
			val |= 0x80 // set IR bit
		}
		c.icrData = 0
		c.irqAsserted = false // de-assert interrupt output
	case CIARegTALO:
		val = byte(c.timerACounter)
	case CIARegTAHI:
		val = byte(c.timerACounter >> 8)
	case CIARegTBLO:
		val = byte(c.timerBCounter)
	case CIARegTBHI:
		val = byte(c.timerBCounter >> 8)
	case CIARegTODHI:
		// Reading TODHI latches the counter: subsequent reads of MID/LO return latched value
		c.todLatch = c.todCounter
		c.todLatched = true
		val = byte(c.todLatch >> 16)
	case CIARegTODMID:
		if c.todLatched {
			val = byte(c.todLatch >> 8)
		} else {
			val = byte(c.todCounter >> 8)
		}
	case CIARegTODLO:
		if c.todLatched {
			val = byte(c.todLatch)
			c.todLatched = false // reading TODLO releases the latch
		} else {
			val = byte(c.todCounter)
		}
	case CIARegPRA:
		val = c.regs[reg]
		if c.OnPRARead != nil {
			val = c.OnPRARead(val)
		}
	default:
		val = c.regs[reg]
	}

	p[0] = val
	for i := 1; i < n; i++ {
		p[i] = 0
	}

	if c.logger != nil {
		regName := ciaRegName(reg)
		fmt.Fprintf(c.logger, "%s READ  %s (reg %X) = $%02X\n", c.Name, regName, reg, val)
	}

	return n, nil
}

func (c *CIA) Write(addr int, p []byte) (int, error) {
	reg := (addr >> 8) & 0x0F
	n := len(p)
	val := p[0]

	if c.logger != nil {
		regName := ciaRegName(reg)
		fmt.Fprintf(c.logger, "%s WRITE %s (reg %X) = $%02X\n", c.Name, regName, reg, val)
	}

	switch reg {
	case CIARegICR:
		// Writing ICR: bit 7 = set/clear, bits 0-4 = mask bits
		if val&0x80 != 0 {
			c.icrMask |= val & 0x1F
		} else {
			c.icrMask &^= val & 0x1F
		}

	case CIARegTALO:
		c.timerALatch = (c.timerALatch & 0xFF00) | uint16(val)
	case CIARegTAHI:
		c.timerALatch = (c.timerALatch & 0x00FF) | uint16(val)<<8
		c.timerACounter, c.regs[CIARegCRA] = timerHIWritten(c.timerACounter, c.timerALatch, c.regs[CIARegCRA])
	case CIARegTBLO:
		c.timerBLatch = (c.timerBLatch & 0xFF00) | uint16(val)
	case CIARegTBHI:
		c.timerBLatch = (c.timerBLatch & 0x00FF) | uint16(val)<<8
		c.timerBCounter, c.regs[CIARegCRB] = timerHIWritten(c.timerBCounter, c.timerBLatch, c.regs[CIARegCRB])
	case CIARegTODLO:
		// CRB bit 7 selects whether TOD writes load the alarm or the counter.
		// Writing TODLO also restarts a counter halted by a TODHI write.
		if c.regs[CIARegCRB]&0x80 != 0 {
			c.todAlarm = (c.todAlarm & 0xFFFF00) | uint32(val)
		} else {
			c.todCounter = (c.todCounter & 0xFFFF00) | uint32(val)
			c.todStopped = false
		}
	case CIARegTODMID:
		if c.regs[CIARegCRB]&0x80 != 0 {
			c.todAlarm = (c.todAlarm & 0xFF00FF) | (uint32(val) << 8)
		} else {
			c.todCounter = (c.todCounter & 0xFF00FF) | (uint32(val) << 8)
		}
	case CIARegTODHI:
		if c.regs[CIARegCRB]&0x80 != 0 {
			c.todAlarm = (c.todAlarm & 0x00FFFF) | (uint32(val) << 16)
		} else {
			c.todCounter = (c.todCounter & 0x00FFFF) | (uint32(val) << 16)
			c.todStopped = true // halted until TODLO is written
		}
	case CIARegPRB:
		old := c.regs[reg]
		c.regs[reg] = val
		if c.OnPRBWrite != nil {
			c.OnPRBWrite(old, val)
		}
		return n, nil
	case CIARegPRA:
		old := c.regs[reg]
		c.regs[reg] = val
		if c.OnPRAWrite != nil {
			c.OnPRAWrite(old, val)
		}
		return n, nil
	case CIARegCRA:
		old := c.regs[reg]
		if val&0x10 != 0 {
			// Force load
			c.timerACounter = c.timerALatch
			val &^= 0x10 // clear force-load bit
		}
		if val&0x01 != 0 {
			c.CRAWrites++
		}
		c.regs[reg] = val
		if c.OnCRAWrite != nil {
			c.OnCRAWrite(old, val)
		}
		return n, nil
	case CIARegCRB:
		oldCRB := c.regs[reg]
		if val&0x10 != 0 {
			c.timerBCounter = c.timerBLatch
			val &^= 0x10
		}
		if val&0x01 != 0 {
			c.CRBWrites++
		}
		entry := fmt.Sprintf("CRB write #%d: old=$%02X new=$%02X (orig=$%02X) latch=$%04X counter=$%04X step=%d",
			len(c.CRBWriteLog)+1, oldCRB, val, p[0], c.timerBLatch, c.timerBCounter, c.StepCount)
		if len(c.CRBWriteLog) < 100 {
			c.CRBWriteLog = append(c.CRBWriteLog, entry)
		}

	}

	c.regs[reg] = val
	return n, nil
}

// timerHIWritten applies a write to a timer's high byte (8520 datasheet):
// a stopped timer loads the latch into the counter; in one-shot mode the
// write also loads the counter and starts the timer, even if it is running.
// It returns the new counter and control register.
func timerHIWritten(counter, latch uint16, cr byte) (uint16, byte) {
	if cr&0x08 != 0 { // RUNMODE one-shot
		return latch, cr | 0x01
	}
	if cr&0x01 == 0 {
		return latch, cr
	}
	return counter, cr
}

func (c *CIA) Reset() error {
	c.regs = [16]byte{}
	c.timerALatch = 0xFFFF
	c.timerBLatch = 0xFFFF
	c.timerACounter = 0xFFFF
	c.timerBCounter = 0xFFFF
	c.icrMask = 0
	c.icrData = 0
	c.irqAsserted = false
	c.todCounter = 0
	c.todAlarm = 0
	c.todLatch = 0
	c.todLatched = false
	c.todStopped = false
	c.todClkDiv = 0
	if c.Name == "CIA-A" {
		c.regs[CIARegPRA] = 0x03
	}
	return nil
}

// Tick advances CIA timers by one E clock cycle (~709KHz).
func (c *CIA) Tick() {
	timerAUnderflow := false

	// Timer A: CRA bit 0 = start, bit 5 = CNT input (0 = E clock)
	if c.regs[CIARegCRA]&0x01 != 0 && c.regs[CIARegCRA]&0x20 == 0 {
		if c.timerACounter == 0 {
			timerAUnderflow = true
			c.icrData |= 0x01 // Timer A underflow
			c.timerACounter = c.timerALatch
			c.TimerAUnderflows++
			if c.regs[CIARegCRA]&0x08 != 0 {
				// One-shot: stop timer
				c.regs[CIARegCRA] &^= 0x01
			}
		} else {
			c.timerACounter--
		}
	}

	// Timer B: CRB bit 0 = start, bits 6-5 (INMODE) select the source:
	// 00 E clock, 01 CNT pin, 10 Timer A underflows, 11 Timer A underflows
	// while CNT is high. CNT is idle (high, no edges) on both Amiga CIAs as
	// far as the emulator is concerned, so 01 never counts and 11 = 10.
	if c.regs[CIARegCRB]&0x01 != 0 {
		var countPulse bool
		switch c.regs[CIARegCRB] & 0x60 {
		case 0x00:
			countPulse = true
		case 0x20:
			countPulse = false
		default:
			countPulse = timerAUnderflow
		}
		if countPulse {
			if c.timerBCounter == 0 {
				c.icrData |= 0x02 // Timer B underflow
				c.timerBCounter = c.timerBLatch
				c.TimerBUnderflows++

				if c.regs[CIARegCRB]&0x08 != 0 {
					c.regs[CIARegCRB] &^= 0x01
				}
			} else {
				c.timerBCounter--
				c.TimerBTicks++
			}
		}
	}

	c.fireIfPending()

	// Legacy E-clock TOD source. The Amiga wires TOD to the video sync
	// signals instead (see TickTOD), so this stays disabled (rate 0) unless
	// a test drives it explicitly.
	if c.todClkRate > 0 {
		c.todClkDiv++
		if c.todClkDiv >= c.todClkRate {
			c.todClkDiv = 0
			c.bumpTOD()
		}
	}
}

// fireIfPending raises the interrupt callback on the 0→1 transition of the
// CIA interrupt output. The real 8520 asserts IRQ continuously, but Paula
// latches on the rising edge.
func (c *CIA) fireIfPending() {
	active := c.icrData&c.icrMask != 0
	if active && !c.irqAsserted && c.OnInterrupt != nil {
		c.InterruptFires++
		c.OnInterrupt()
	}
	c.irqAsserted = active
}

// bumpTOD advances the TOD counter one tick and raises the ALARM interrupt
// (ICR bit 2) when it matches the alarm register.
func (c *CIA) bumpTOD() {
	c.todCounter = (c.todCounter + 1) & 0x00FFFFFF
	if c.todCounter == c.todAlarm {
		c.icrData |= 0x04 // ALARM
		c.fireIfPending()
	}
}

// TickTOD advances the TOD counter from its video sync source: one call per
// scanline for CIA-B (horizontal sync) and one per frame for CIA-A (vertical
// sync). Writing TODHI halts counting until TODLO is written.
//
// graphics.library's beam-synchronised blitter queue depends on this: its
// VBLANK server reloads CIA-B TOD with 1 each frame and arms an alarm at the
// raster line a queued blit must start on. Without a scanline-rate TOD and a
// working alarm, QBSBlit blits are never started and any task waiting on one
// (Workbench dragging an icon, for example) blocks forever while holding the
// layer and IntuitionBase locks — freezing the whole UI.
func (c *CIA) TickTOD() {
	if c.todStopped {
		return
	}
	c.bumpTOD()
}

// SetFLG signals a falling edge on the /FLAG pin (ICR bit 4). CIA-B's
// /FLAG is the floppy index pulse.
func (c *CIA) SetFLG() {
	c.icrData |= 0x10
	c.fireIfPending()
}

// SetSDR writes the serial data register and fires SP interrupt (ICR bit 3).
func (c *CIA) SetSDR(val byte) {
	c.regs[CIARegSDR] = val
	c.icrData |= 0x08 // SP interrupt
	// Fire on 0→1 transition
	active := c.icrData&c.icrMask != 0
	if active && !c.irqAsserted && c.OnInterrupt != nil {
		c.OnInterrupt()
	}
	c.irqAsserted = active
}

// TickN advances CIA timers by n E-clock cycles. When both timers are stopped
// (common during idle), it skips per-cycle overhead and batch-updates the TOD
// counter directly. Falls back to per-tick processing when a timer is running.
func (c *CIA) TickN(n int) {
	// Fast path: if both timers are stopped, only the TOD counter matters.
	if c.regs[CIARegCRA]&0x01 == 0 && c.regs[CIARegCRB]&0x01 == 0 {
		if c.todClkRate > 0 {
			totalTod := c.todClkDiv + n
			todTicks := totalTod / c.todClkRate
			c.todClkDiv = totalTod % c.todClkRate
			for i := 0; i < todTicks; i++ {
				c.bumpTOD()
			}
		}
		return
	}
	// Slow path: timer(s) running — tick one at a time.
	for i := 0; i < n; i++ {
		c.Tick()
	}
}

// ETicksToNextEvent returns the number of E-clock ticks until the next
// timer underflow on this CIA. Only running timers are considered. Returns
// -1 if no timer is running.
func (c *CIA) ETicksToNextEvent() int {
	min := -1

	// Timer A: running if CRA bit 0 set and using E-clock (bit 5 clear)
	if c.regs[CIARegCRA]&0x01 != 0 && c.regs[CIARegCRA]&0x20 == 0 {
		ticks := int(c.timerACounter) + 1 // counter counts down to 0, then underflows
		if min < 0 || ticks < min {
			min = ticks
		}
	}

	// Timer B: running if CRB bit 0 set
	if c.regs[CIARegCRB]&0x01 != 0 {
		if c.regs[CIARegCRB]&0x60 == 0 {
			// Timer B counting E-clock directly
			ticks := int(c.timerBCounter) + 1
			if min < 0 || ticks < min {
				min = ticks
			}
		}
		// If Timer B counts Timer A underflows, it depends on Timer A
		// timing — don't try to predict, let normal tick handle it
	}

	return min
}

// OVL returns the state of the OVL (overlay) bit in CIA-A PRA.
func (c *CIA) OVL() bool {
	return c.regs[CIARegPRA]&0x01 != 0
}

func ciaRegName(reg int) string {
	names := []string{
		"PRA", "PRB", "DDRA", "DDRB",
		"TALO", "TAHI", "TBLO", "TBHI",
		"TODLO", "TODMID", "TODHI", "",
		"SDR", "ICR", "CRA", "CRB",
	}
	if reg < len(names) && names[reg] != "" {
		return names[reg]
	}
	return fmt.Sprintf("REG%X", reg)
}

// Debug accessors
func (c *CIA) Regs(reg int) byte     { return c.regs[reg] }
func (c *CIA) IcrMask() uint8        { return c.icrMask }
func (c *CIA) IcrData() uint8        { return c.icrData }
func (c *CIA) TimerACounter() uint16 { return c.timerACounter }
func (c *CIA) TimerALatch() uint16   { return c.timerALatch }
func (c *CIA) TimerBCounter() uint16 { return c.timerBCounter }
func (c *CIA) TimerBLatch() uint16   { return c.timerBLatch }

// ciaSpace is the CPU's view of the CIAs: $BF0000-$BFFFFF, partially
// decoded. CIA-A is selected when A12 is low and drives the odd byte
// (D7-D0); CIA-B is selected when A13 is low and drives the even byte
// (D15-D8). A11-A8 select the register. So $BFE001 is CIA-A, $BFD000 is
// CIA-B, a word access at $BFC000 reaches both, and a byte lane neither
// chip drives floats (reads 0, like the rest of the open bus).
type ciaSpace struct {
	ciaA, ciaB *CIA
}

// chip returns the CIA driving byte address addr, or nil.
func (s *ciaSpace) chip(addr int) *CIA {
	if addr&1 != 0 {
		if addr&0x1000 == 0 {
			return s.ciaA
		}
	} else if addr&0x2000 == 0 {
		return s.ciaB
	}
	return nil
}

func (s *ciaSpace) Read(addr int, p []byte) (int, error) {
	for i := range p {
		p[i] = 0
		if c := s.chip(addr + i); c != nil {
			c.Read(addr+i, p[i:i+1])
		}
	}
	return len(p), nil
}

func (s *ciaSpace) Write(addr int, p []byte) (int, error) {
	for i := range p {
		if c := s.chip(addr + i); c != nil {
			c.Write(addr+i, p[i:i+1])
		}
	}
	return len(p), nil
}

func (s *ciaSpace) Reset() error {
	s.ciaA.Reset()
	return s.ciaB.Reset()
}

// E clock timing. The CIAs sit on the 68000's synchronous (6800-style) bus:
// the E clock is the CPU clock divided by 10 (6 cycles low, 4 high), and a
// CIA access asserts VPA, waits for the start of the next E cycle, then
// takes a whole E cycle. An access therefore takes 10-19 CPU cycles instead
// of the normal 4, depending on the E clock phase when it starts.
const eClockCycles = 10

// ciaAccessWait returns the extra CPU cycles for n back-to-back CIA accesses
// in an instruction that otherwise takes elapsed cycles from the current
// beam position. The accesses are taken to be the instruction's last bus
// cycles, as the CIA operand normally is (MOVE.B $BFE001,D0; the write of
// MOVE.B D0,$BFD100).
func (a *Amiga) ciaAccessWait(n, elapsed int) int {
	if n == 0 {
		return 0
	}
	// Cycles into the current E cycle when the first access starts.
	start := elapsed - 4*n
	if start < 0 {
		start = 0
	}
	phase := (a.eClockCounter + start) % eClockCycles
	sync := (eClockCycles - phase) % eClockCycles
	// First access: wait for the E cycle to start, then one E cycle. Each
	// following access begins on an E cycle boundary and takes one more.
	return sync + n*eClockCycles - 4*n
}
