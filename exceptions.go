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

	// Read new PC from vector table
	newPC, err := c.M.Long(vector * 4)
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
