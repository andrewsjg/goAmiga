package m68k

import (
	"encoding/binary"
)

// Exception vector numbers (68000)
const (
	VecResetSSP     = 0  // Reset: Initial SSP
	VecResetPC      = 1  // Reset: Initial PC
	VecBusError     = 2  // Bus Error
	VecAddressError = 3  // Address Error
	VecIllegal      = 4  // Illegal Instruction
	VecDivideByZero = 5  // Zero Divide
	VecCHK          = 6  // CHK Instruction
	VecTRAPV        = 7  // TRAPV Instruction
	VecPrivilege    = 8  // Privilege Violation
	VecTrace        = 9  // Trace
	VecLine1010     = 10 // Line 1010 Emulator (A-line)
	VecLine1111     = 11 // Line 1111 Emulator (F-line)
	VecSpurious     = 24 // Spurious Interrupt
	VecAutoVector1  = 25 // Level 1 Interrupt Autovector
	VecAutoVector2  = 26 // Level 2 Interrupt Autovector
	VecAutoVector3  = 27 // Level 3 Interrupt Autovector
	VecAutoVector4  = 28 // Level 4 Interrupt Autovector
	VecAutoVector5  = 29 // Level 5 Interrupt Autovector
	VecAutoVector6  = 30 // Level 6 Interrupt Autovector
	VecAutoVector7  = 31 // Level 7 Interrupt Autovector
	VecTrap0        = 32 // TRAP #0
)

// checkPrivilege returns true if in supervisor mode. If in user mode,
// raises a privilege violation exception and returns false. The caller
// should return immediately without executing the privileged instruction.
// pc should be the address of the offending instruction (before PC+=2).
func (c *Processor) checkPrivilege(pc uint32) (bool, error) {
	if c.SR&StatusSupervisor != 0 {
		return true, nil
	}
	// Privilege violation — on 68000 this is normal for Amiga's Supervisor() trap.
	// Route through the VecPrivilege exception handler silently.
	c.PC = pc
	err := c.RaiseException(VecPrivilege)
	return false, err
}

// setSR sets the full Status Register, handling supervisor mode transitions
// (USP/SSP swapping) if the S bit changes.
func (c *Processor) setSR(newSR uint32) {
	newSR &= StatusMask // mask out reserved bits (hardwired to 0 on 68000)
	wasSupervisor := c.SR&StatusSupervisor != 0
	willBeSupervisor := newSR&StatusSupervisor != 0
	if wasSupervisor && !willBeSupervisor {
		c.SSP = c.A[7]
		c.A[7] = c.USP
	} else if !wasSupervisor && willBeSupervisor {
		c.USP = c.A[7]
		c.A[7] = c.SSP
	}
	c.SR = newSR
}

// RaiseException processes a 68000 exception:
// 1. Save current SR
// 2. Enter supervisor mode
// 3. Push SR and PC to supervisor stack
// 4. Read new PC from exception vector table
// 5. Clear trace bit
func (c *Processor) RaiseException(vector int) error {
	c.tim.exception, c.tim.trapped = vector, true
	if c.OnException != nil {
		c.OnException(vector, c.PC, c.instrAddr)
	}
	return c.raiseInterruptException(vector, 0)
}

// raiseInterruptException is like RaiseException but also sets the IPL mask
// in SR to newIPL AFTER saving the original SR to the stack. This matches the
// real 68000 hardware interrupt acknowledge sequence where the CPU:
//  1. Latches the current (pre-interrupt) SR for the exception frame
//  2. Enters supervisor mode
//  3. Sets the IPL mask to the acknowledged interrupt level
//  4. Pushes the latched SR and return PC to the supervisor stack
//
// When newIPL == 0 the behaviour is identical to a plain RaiseException.
func (c *Processor) raiseInterruptException(vector int, newIPL uint32) error {
	// Any exception ends the STOP state. This matters when STOP lowers the
	// mask below an already-pending interrupt: CheckInterrupts takes it at
	// the end of the same Step, and the processor must then run the handler
	// rather than report itself stopped with PC at the handler entry.
	c.runState = RunStateRunning

	// Save current SR (before any modification — this is what gets pushed to stack)
	oldSR := uint16(c.SR & 0xFFFF)

	// Enter supervisor mode (if not already)
	if c.SR&StatusSupervisor == 0 {
		c.USP = c.A[7]
		c.A[7] = c.SSP
		c.SR |= StatusSupervisor
	}

	// Set new IPL mask to the acknowledged interrupt level (interrupt exceptions
	// only — newIPL == 0 means no change, for non-interrupt exceptions)
	if newIPL != 0 {
		c.SR = (c.SR & ^StatusIPLMask) | (newIPL << 8)
	}

	// Clear trace bit
	c.SR &= ^StatusTrace

	// The 68020 frame starts with a format/vector word.
	if c.is020() {
		if err := c.pushFrame020(vector); err != nil {
			return err
		}
	}

	// Push PC to supervisor stack (long)
	c.A[7] -= 4
	binary.BigEndian.PutUint32(c.buf[:4], c.PC)
	if _, err := c.M.Write(int(c.A[7]), c.buf[:4]); err != nil {
		return err
	}

	// Push the original (pre-interrupt) SR to supervisor stack (word)
	c.A[7] -= 2
	binary.BigEndian.PutUint16(c.buf[:2], oldSR)
	if _, err := c.M.Write(int(c.A[7]), c.buf[:2]); err != nil {
		return err
	}

	// Read new PC from vector table (at VBR on the 68020)
	newPC, err := c.M.Long(int(c.VBR) + vector*4)
	if err != nil {
		return err
	}
	c.PC = newPC

	return nil
}

// RaiseInterrupt sets a pending interrupt at the given priority level (1-7).
// The interrupt will be processed at the start of the next instruction cycle
// if the level is higher than the current IPL mask.
func (c *Processor) RaiseInterrupt(level uint32) {
	if level > 7 {
		level = 7
	}
	if level > c.pendingIPL {
		c.pendingIPL = level
	}
}

// ClearInterrupt clears a pending interrupt at the given level.
func (c *Processor) ClearInterrupt(level uint32) {
	if c.pendingIPL == level {
		c.pendingIPL = 0
	}
}

// SetInterruptLevel directly sets pendingIPL to the given level.
// Used by hardware that continuously drives IPL pins (like Amiga Paula).
func (c *Processor) SetInterruptLevel(level uint32) {
	if level > 7 {
		level = 7
	}
	c.pendingIPL = level
}

// CheckInterrupts checks if any pending interrupt should be serviced.
// An interrupt is serviced if its level is greater than the current IPL mask,
// or if it's level 7 (NMI, always serviced).
func (c *Processor) CheckInterrupts() error {
	if c.pendingIPL == 0 {
		return nil
	}

	currentIPL := c.IPL()

	// Level 7 (NMI) is always serviced; others must exceed mask
	if c.pendingIPL > currentIPL || c.pendingIPL == 7 {
		level := c.pendingIPL
		c.pendingIPL = 0

		// Use autovector (vector = 24 + level).
		// NOTE: do NOT modify SR before calling RaiseException — RaiseException
		// saves the current SR (with original IPL mask) to the stack first, then
		// sets the new IPL mask. This matches real 68000 hardware behaviour and
		// ensures that RTE properly restores the original IPL mask.
		return c.raiseInterruptException(int(VecSpurious+level), level)
	}

	return nil
}

// raiseAddressError020 takes exception 3 with the 68020's short bus cycle
// fault frame (format $A, 32 bytes). On the 68020 only instruction fetches
// fault (a jump to an odd address); the special status word marks a fault
// on pipe stage B, to be rerun.
func (c *Processor) raiseAddressError020(ae *addressError, fc uint16) error {
	ssw := fc
	if ae.program {
		ssw |= 0x4000 | 0x1000 // FB, RB
	} else {
		ssw |= 0x0100 // DF
		if !ae.write {
			ssw |= 0x0040 // RW: read
		}
	}
	for _, f := range []struct {
		v    uint32
		size int
	}{
		{0, 4},                          // internal registers
		{0, 4},                          // data output buffer
		{0, 4},                          // internal registers
		{ae.addr, 4},                    // data cycle fault address
		{0, 2},                          // instruction pipe stage B
		{uint32(c.op), 2},               // instruction pipe stage C
		{uint32(ssw), 2},                // special status word
		{0, 2},                          // internal register
		{0xA000 | VecAddressError*4, 2}, // format and vector
		{c.instrAddr, 4},
		{ae.sr & 0xFFFF, 2},
	} {
		var err error
		if f.size == 4 {
			err = c.push32(f.v)
		} else {
			err = c.push16(uint16(f.v))
		}
		if err != nil {
			return err
		}
	}
	newPC, err := c.M.Long(int(c.VBR) + VecAddressError*4)
	if err != nil {
		return err
	}
	c.PC = newPC
	c.LastCycles = 50
	return nil
}

// addressError describes a word or long access at an odd address.
type addressError struct {
	addr     uint32 // the faulting address (full 32 bits)
	write    bool
	program  bool   // an instruction fetch (prefetch from an odd PC)
	pc       uint32 // PC value to stack
	sr       uint32 // SR at the fault
	accesses int    // bus cycles completed before the fault
	cycles   int    // total clock periods, if known (program faults)
}

// oddAccess is the Decoder's OnOddAccess hook during an instruction: it
// aborts the instruction with an address error. The PC stacked is the PC at
// the fault less 2, the 68000's prefetch lag (for an instruction with one
// memory operand that is the address of its last extension word).
func (c *Processor) oddAccess(addr int, write bool) {
	pc := c.PC - 2
	if write {
		pc += c.faultPCAdjust
	}
	panic(&addressError{addr: uint32(addr), write: write, pc: pc, sr: c.SR, accesses: c.M.Accesses})
}

// raiseAddressError takes exception 3 with the 68000's group 0 frame (14
// bytes): from the new stack pointer upwards, the special status word, the
// access address, the instruction register, the SR and the PC. The SSW has
// the opcode's top 11 bits, then R/W (1 = read), I/N (1 = instruction
// fetch) and the function code. A second fault while stacking (odd SSP)
// halts the processor, as on the 68000.
func (c *Processor) raiseAddressError(ae *addressError) error {
	c.M.OnOddAccess = nil
	if c.OnException != nil {
		c.OnException(VecAddressError, ae.pc, c.instrAddr)
	}
	c.runState = RunStateRunning
	super := ae.sr&StatusSupervisor != 0
	fc := uint16(1) // user data
	switch {
	case ae.program && super:
		fc = 6
	case ae.program:
		fc = 2
	case super:
		fc = 5
	}
	ssw := uint16(c.op)&0xFFE0 | fc
	if !ae.write {
		ssw |= 0x10
	}
	if ae.program {
		ssw |= 0x08
	}

	if c.SR&StatusSupervisor == 0 {
		c.USP = c.A[7]
		c.A[7] = c.SSP
		c.SR |= StatusSupervisor
	}
	c.SR &^= StatusTrace
	if c.A[7]&1 != 0 && !c.is020() {
		c.runState = RunStateStopped // double fault
		return nil
	}
	if c.is020() {
		return c.raiseAddressError020(ae, fc)
	}
	push := func(v uint32, size int) {
		c.A[7] -= uint32(size)
		if size == 4 {
			binary.BigEndian.PutUint32(c.buf[:4], v)
		} else {
			binary.BigEndian.PutUint16(c.buf[:2], uint16(v))
		}
		c.M.Write(int(c.A[7]), c.buf[:size])
	}
	push(ae.pc, 4)
	push(ae.sr&0xFFFF, 2)
	push(uint32(c.op), 2)
	push(ae.addr, 4)
	push(uint32(ssw), 2)

	// 50 clock periods, plus the bus cycles the instruction completed
	// before the fault (4 each, not counting the opcode fetch) and the
	// internal time of -(An) and indexed address calculations.
	cyc := ae.cycles
	if cyc == 0 {
		cyc = 50 + 4*(ae.accesses-1)
		m := c.op >> 3 & 7
		predecX := c.op&0xB130 == 0x9100 && c.op&0x08 != 0 // ADDX/SUBX/ABCD/SBCD -(An)
		movem := c.op&0xFB80 == 0x4880
		if (m == 4 && !movem) || m == 6 || c.op&0x3F == 0x3B || predecX {
			cyc += 2
		}
		if g := c.op >> 12; g >= 1 && g <= 3 && ae.write {
			if c.op>>6&7 == 6 { // MOVE to (d8,An,Xn)
				cyc += 2
			}
			switch c.faultPCAdjust {
			case ^uint32(1): // written before the last extension word
				cyc -= 4
			case 2: // -(An): the next instruction was prefetched first
				cyc += 4
			}
		}
	}
	newPC, err := c.M.Long(int(c.VBR) + VecAddressError*4)
	if err != nil {
		return err
	}
	c.PC = newPC
	c.LastCycles = cyc
	return nil
}
