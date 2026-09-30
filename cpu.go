package m68k

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/cavaliercoder/go-m68k/m68kmem"
)

const (
	SizeByte = iota
	SizeWord
	SizeLong
)

// Status Register bits.
// See section 1.1.4.
const (
	StatusCarry    = uint32(1) << iota // C
	StatusOverflow                     // V
	StatusZero                         // Z
	StatusNegative                     // N
	StatusExtend                       // X

	StatusMask = 0xA71F // Section 1.3.2

	// SR upper byte (supervisor byte)
	StatusIPL0       = uint32(0x0100) // Interrupt Priority Level bit 0
	StatusIPL1       = uint32(0x0200) // Interrupt Priority Level bit 1
	StatusIPL2       = uint32(0x0400) // Interrupt Priority Level bit 2
	StatusIPLMask    = uint32(0x0700) // All IPL bits
	StatusSupervisor = uint32(0x2000) // Supervisor/User mode
	StatusTrace      = uint32(0x8000) // Trace mode
)

// Comparison conditions.
// See table 3-19.
const (
	CondTrue = iota
	CondFalse
	CondHigh
	CondLowOrSame
	CondCarryClear
	CondCarrySet
	CondNotEqual
	CondEqual
	CondOverflowClean
	CondOverflowSet
	CondPlus
	CondMinus
	CondGreaterOrEqual
	CondLessThan
	CondGreaterThan
	CondLessOrEqual
)

// string suffix for conditional opcodes
// see: table 3-19
var conditionStrings = []string{
	"t",  // true
	"f",  // false
	"hi", // high
	"ls", // low or same
	"cc", // (hi) carry clear
	"cs", // (lo) carry set
	"ne", // not equal
	"eq", // equal
	"vc", // overflow clear
	"vs", // overflow set
	"pl", // plus
	"mi", // minus
	"ge", // greater or equal
	"lt", // less than
	"gt", // greater than
	"le", // less or equal
}

// RunState specifies the current running state of the Processor.
type RunState uint32

const (
	// RunStateStopped indicates that the Processor is no longer incrementing the
	// program counter and executing instructions.
	RunStateStopped RunState = iota

	// RunStateRunning indicates that the Processor is currently executing
	// instructions until interrupted.
	RunStateRunning
)

// A Processor emulates the Motorola 68000 microprocessor.
type Processor struct {
	D  [8]uint32        // Data registers
	A  [8]uint32        // Address registers
	SR uint32           // Status Register
	PC uint32           // Program Counter
	M  *m68kmem.Decoder // System memory controller

	// TraceWriter specifies where trace log output should be written to. If
	// TraceWriter is nil, no logging is performed.
	TraceWriter io.Writer

	// DebugExceptions, when true, prints to stdout whenever an exception fires
	// in user mode (useful for debugging privilege violations and mode switches).
	DebugExceptions bool

	SSP uint32 // Supervisor Stack Pointer (saved when entering user mode)
	USP uint32 // User Stack Pointer (saved when entering supervisor mode)

	pendingIPL uint32 // pending interrupt priority level (0-7)

	// ResetHandler is called when the RESET instruction executes.
	// It should reset external peripherals (not the CPU itself).
	ResetHandler func()

	// HaltOnZero, when true, causes the processor to stop when it encounters
	// opcode $0000 (ORI.B #0,D0). This is useful for simple test programs that
	// run off the end of their code into zeroed memory.
	HaltOnZero bool

	// NoAddressErrors disables 68000 address errors (exception 3 on word
	// and long accesses at odd addresses).
	NoAddressErrors bool

	// faultPCAdjust corrects the PC stacked by an address error for
	// instructions whose prefetch is not simply 2 behind (MOVE's write).
	faultPCAdjust uint32

	// tim collects what the current instruction's timing depends on.
	tim timingInfo

	// LastCycles holds the cycle count of the most recently executed instruction.
	// Minimum is 4 for the fastest 68000 instructions.
	LastCycles int

	// Model is the processor: Model68000 (the default) or Model68020
	// (m68020.go).
	Model Model
	// 68020 control registers (MOVEC). ISP is SSP.
	VBR, CACR, CAAR, SFC, DFC, MSP uint32
	instrAddr                      uint32 // address of the instruction being executed

	buf      [64]byte         // general purpose buffer
	op       uint16           // current opcode
	runState RunState         // current state
	handlers [256]TrapHandler // registered trap handlers
}

type TrapHandler interface {
	Trap(c *Processor, vector int) error
}
type TrapHandlerFunc func(c *Processor, vector int) error

func (f TrapHandlerFunc) HandleTrap(p *Processor, v int) error {
	return f(p, v)
}

func (c *Processor) RegisterTrapHandler(vector int, f TrapHandler) error {
	if vector < 0 || vector >= len(c.handlers) {
		return errors.New("vector out of range")
	}
	c.handlers[vector] = f
	return nil
}

// Reset resets all processor and memory state.
func (c *Processor) Reset() {
	m := c.M
	tw := c.TraceWriter
	h := c.handlers
	rh := c.ResetHandler

	*c = Processor{
		M:            m,
		TraceWriter:  tw,
		handlers:     h,
		ResetHandler: rh,
		Model:        c.Model,
	}
	c.M.Reset()
}

// ResetWithVectors performs a 68000 hardware reset: reads SSP from $000000 and
// PC from $000004, enters supervisor mode with interrupts fully masked.
func (c *Processor) ResetWithVectors() error {
	m := c.M
	tw := c.TraceWriter
	h := c.handlers
	rh := c.ResetHandler

	*c = Processor{
		M:               m,
		TraceWriter:     tw,
		handlers:        h,
		ResetHandler:    rh,
		Model:           c.Model,
		NoAddressErrors: c.NoAddressErrors,
	}
	c.M.Reset()

	// Enter supervisor mode with interrupts masked
	c.SR = StatusSupervisor | StatusIPLMask

	// Read initial SSP from vector 0 ($000000)
	ssp, err := c.M.Long(0x000000)
	if err != nil {
		return err
	}
	c.A[7] = ssp
	c.SSP = ssp

	// Read initial PC from vector 1 ($000004)
	pc, err := c.M.Long(0x000004)
	if err != nil {
		return err
	}
	c.PC = pc

	return nil
}

// SetSupervisor switches to supervisor mode, saving USP and loading SSP.
func (c *Processor) SetSupervisor(super bool) {
	wasSupervisor := c.SR&StatusSupervisor != 0
	if super == wasSupervisor {
		return
	}
	if super {
		// entering supervisor: save USP, load SSP
		c.USP = c.A[7]
		c.A[7] = c.SSP
		c.SR |= StatusSupervisor
	} else {
		// entering user: save SSP, load USP
		c.SSP = c.A[7]
		c.A[7] = c.USP
		c.SR &= ^StatusSupervisor
	}
}

// IsSupervisor returns true if the CPU is in supervisor mode.
func (c *Processor) IsSupervisor() bool {
	return c.SR&StatusSupervisor != 0
}

// IPL returns the current interrupt priority mask from SR.
func (c *Processor) IPL() uint32 {
	return (c.SR & StatusIPLMask) >> 8
}

// PendingIPL returns the pending interrupt priority level (0 = none).
func (c *Processor) PendingIPL() uint32 {
	return c.pendingIPL
}

// Run executes any program loaded into memory, starting from the program
// counter value, running until completion.
func (c *Processor) Run() (err error) {
	if c.M == nil {
		return errNoProgram
	}
	for {
		err = c.Step()
		if err != nil || c.runState == RunStateStopped {
			break
		}
	}
	return
}

// Step executes the single instruction located at the address specified by the
// program counter register.
func (c *Processor) Step() (err error) {
	c.runState = RunStateRunning

	// Address errors: a word or long access at an odd address aborts the
	// instruction (see raiseAddressError). Register updates made before the
	// faulting access (e.g. -(An)) stand, as on the 68000.
	c.instrAddr = c.PC
	// The 68020 allows data at odd addresses (see m68020.go).
	if !c.NoAddressErrors && !c.is020() {
		snapSR := c.SR
		c.faultPCAdjust = 0
		c.M.OnOddAccess = c.oddAccess
		defer func() {
			c.M.OnOddAccess = nil
			if r := recover(); r != nil {
				ae, ok := r.(*addressError)
				if !ok {
					panic(r)
				}
				// The flags are those from before the instruction, except
				// that MOVE sets them before its write.
				isMove := c.op>>12 >= 1 && c.op>>12 <= 3
				if !(isMove && ae.write) {
					c.SR, ae.sr = snapSR, snapSR
				}
				err = c.raiseAddressError(ae)
			}
		}()
	}

	c.M.Accesses = 0

	// read from program pointer into op
	// TODO: consider prefetching a whole page of instructions and measure
	if _, err = c.M.Read(int(c.PC), c.buf[0:2]); err != nil {
		return
	}
	c.op = uint16(c.buf[0])<<8 + uint16(c.buf[1])
	if c.op == 0 && c.HaltOnZero {
		c.runState = RunStateStopped
		return
	}

	// dispatch opcode to function
	var fn stepFunc
	if c.is020() {
		fn = dispatch020(c.op)
	}
	if fn == nil {
		fn = dispatch(c.op)
	}
	if fn == nil {
		// Check for Line A and Line F emulator traps
		group := c.op >> 12
		if group == 0xA {
			if c.TraceWriter != nil {
				fmt.Fprintf(c.TraceWriter, "%08X Line-A trap $%04X (vec %d)\n", c.PC, c.op, VecLine1010)
			}
			c.PC += 2
			c.LastCycles = 34 // exception processing
			err = c.RaiseException(VecLine1010)
			return
		}
		if group == 0xF {
			if c.TraceWriter != nil {
				fmt.Fprintf(c.TraceWriter, "%08X Line-F trap $%04X (vec %d)\n", c.PC, c.op, VecLine1111)
			}
			c.PC += 2
			c.LastCycles = 34 // exception processing
			err = c.RaiseException(VecLine1111)
			return
		}
		// Unrecognized opcode → illegal instruction exception
		if c.TraceWriter != nil {
			fmt.Fprintf(c.TraceWriter, "%08X ILLEGAL opcode $%04X\n", c.PC, c.op)
		}
		c.PC += 2
		c.LastCycles = 34 // exception processing
		err = c.RaiseException(VecIllegal)
		return
	}
	c.tim = timingInfo{}
	t := fn(c)

	// Clock periods for the instruction (see timing.go).
	c.LastCycles = c.cycles()

	// Only format and write trace when tracing is enabled
	if c.TraceWriter != nil {
		tp := &t
		fmt.Fprintln(c.TraceWriter, tp.String())
	}
	if t.err != nil {
		// Illegal operand sizes and bad addresses are illegal instruction exceptions
		if t.err == errBadOpSize || t.err == errBadAddress {
			if c.TraceWriter != nil {
				fmt.Fprintf(c.TraceWriter, "  -> ILLEGAL: %v at $%08X op=$%04X\n", t.err, t.addr, c.op)
			}
			c.PC = t.addr + 2
			err = c.RaiseException(VecIllegal)
			return
		}
		err = newTraceableError(t.addr, c.op, t.err)
		return
	}

	// A jump, branch or return to an odd address faults on the prefetch
	// from the new PC, still within this instruction.
	if c.PC&1 != 0 && !c.NoAddressErrors {
		c.M.OnOddAccess = nil
		// Timed as the instruction's own time less the final prefetch
		// (8 clock periods), plus 50 for the exception.
		err = c.raiseAddressError(&addressError{addr: c.PC, program: true, pc: c.PC - 4, sr: c.SR,
			cycles: 50 + c.cycles() - 8})
		return
	}

	// check for pending interrupts after each instruction
	err = c.CheckInterrupts()
	return
}

// Stop instructs the processor to stop processing instructions.
func (c *Processor) Stop() (err error) {
	c.runState = RunStateStopped
	return
}

// print a formatted message to the configured TraceWriter.
func (c *Processor) tracef(format string, a ...interface{}) {
	if c.TraceWriter == nil {
		return
	}
	fmt.Fprintf(c.TraceWriter, format, a...)
}

// RunState returns the current run state of the Processor.
func (c *Processor) RunState() RunState {
	return c.runState
}

// print a message to the configured TraceWriter.
func (c *Processor) trace(v ...interface{}) {
	if c.TraceWriter == nil {
		return
	}
	fmt.Fprintln(c.TraceWriter, v...)
}

// testCode returns true if the given condition code is true for the current
// processor state.
func testCond(c *Processor, cc uint16) bool {
	n := c.SR&StatusNegative != 0
	z := c.SR&StatusZero != 0
	v := c.SR&StatusOverflow != 0
	carry := c.SR&StatusCarry != 0

	switch cc {
	case CondTrue:
		return true
	case CondFalse:
		return false
	case CondHigh:
		return !carry && !z
	case CondLowOrSame:
		return carry || z
	case CondCarryClear:
		return !carry
	case CondCarrySet:
		return carry
	case CondNotEqual:
		return !z
	case CondEqual:
		return z
	case CondOverflowClean:
		return !v
	case CondOverflowSet:
		return v
	case CondPlus:
		return !n
	case CondMinus:
		return n
	case CondGreaterOrEqual:
		return n == v
	case CondLessThan:
		return n != v
	case CondGreaterThan:
		return !z && (n == v)
	case CondLessOrEqual:
		return z || (n != v)
	}
	return false
}

func (c *Processor) readInt(ea, sz uint16) (i Int, opr string, err error) {
	switch sz {
	default:
		err = errBadOpSize
		return

	case SizeByte:
		var n byte
		n, opr, err = c.readByte(ea)
		if err != nil {
			break
		}
		i = NewInt(uint32(n), SizeByte)

	case SizeWord:
		var n uint16
		n, opr, err = c.readWord(ea)
		if err != nil {
			break
		}
		i = NewInt(uint32(n), SizeWord)

	case SizeLong:
		var n uint32
		n, opr, err = c.readLong(ea)
		if err != nil {
			break
		}
		i = NewInt(uint32(n), SizeLong)
	}
	return
}

// readByte reads an 8-bit byte of data from the given effective address.
func (c *Processor) readByte(ea uint16) (b byte, opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		b = byte(c.D[reg])
		opr = fmt.Sprintf("D%d", reg)

	case 0x01: // address register
		b = byte(c.A[reg])
		opr = fmt.Sprintf("A%d", reg)

	case 0x02: // memory address
		b, err = c.M.Byte(int(c.A[reg]))
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		b, err = c.M.Byte(int(c.A[reg]))
		c.A[reg]++
		if reg == 7 {
			c.A[reg]++ // align stack pointer to 16-bit
		}
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg]--
		if reg == 7 {
			c.A[reg]-- // align stack pointer to 16-bit
		}
		b, err = c.M.Byte(int(c.A[reg]))
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			return
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		b, err = c.M.Byte(addr)
		opr = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		b, err = c.M.Byte(int(addr))

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			b, err = c.M.Byte(int(int16(addr)))
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			b, err = c.M.Byte(int(addr))
			opr = fmt.Sprintf("$%X", addr)

		case 0x02: // program counter with displacement
			var d int16
			d, err = c.M.Sword(int(c.PC))
			if err != nil {
				return
			}
			addr := int(int32(c.PC) + int32(d))
			c.PC += 2
			b, err = c.M.Byte(addr)
			opr = fmt.Sprintf("($%X,PC)", d)

		case 0x03: // program counter with index
			var addr uint32
			addr, opr, err = c.decodeBriefExt(c.PC, "PC")
			if err != nil {
				return
			}
			b, err = c.M.Byte(int(addr))

		case 0x04: // immediate byte
			var n uint16
			n, err = c.M.Word(int(c.PC))
			b = byte(n)
			c.PC += 2
			opr = fmt.Sprintf("#$%X", byteToInt32(b))
		}
	}
	return
}

// readWord reads a 16-bit word of data from the given effective address.
func (c *Processor) readWord(ea uint16) (n uint16, opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		n = uint16(c.D[reg])
		opr = fmt.Sprintf("D%d", reg)

	case 0x01: // address register
		n = uint16(c.A[reg])
		opr = fmt.Sprintf("A%d", reg)

	case 0x02: // memory address
		n, err = c.M.Word(int(c.A[reg]))
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		// Incremented before the access: an address error leaves it
		// incremented, as on the 68000.
		addr := c.A[reg]
		c.A[reg] += 2
		n, err = c.M.Word(int(addr))
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg] -= 2
		n, err = c.M.Word(int(c.A[reg]))
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		n, err = c.M.Word(addr)
		opr = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		n, err = c.M.Word(int(addr))

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			n, err = c.M.Word(int(int16(addr)))
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			n, err = c.M.Word(int(addr))
			opr = fmt.Sprintf("$%X", addr)

		case 0x02: // program counter with displacement
			var d int16
			d, err = c.M.Sword(int(c.PC))
			if err != nil {
				return
			}
			addr := int(int32(c.PC) + int32(d))
			c.PC += 2
			n, err = c.M.Word(addr)
			opr = fmt.Sprintf("($%X,PC)", d)

		case 0x03: // program counter with index
			var addr uint32
			addr, opr, err = c.decodeBriefExt(c.PC, "PC")
			if err != nil {
				return
			}
			n, err = c.M.Word(int(addr))

		case 0x04: // immediate word
			n, err = c.M.Word(int(c.PC))
			c.PC += 2
			opr = fmt.Sprintf("#$%X", wordToInt32(n))
		}
	}
	return
}

// readLong reads a 32-bit long-word of data from the given effective address.
func (c *Processor) readLong(ea uint16) (n uint32, opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		n = c.D[reg]
		opr = fmt.Sprintf("D%d", reg)

	case 0x01: // address register
		n = c.A[reg]
		opr = fmt.Sprintf("A%d", reg)

	case 0x02: // memory address
		n, err = c.M.Long(int(c.A[reg]))
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		// Incremented before the access: an address error leaves it
		// incremented, as on the 68000.
		addr := c.A[reg]
		c.A[reg] += 4
		n, err = c.M.Long(int(addr))
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg] -= 4
		n, err = c.M.Long(int(c.A[reg]))
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		n, err = c.M.Long(addr)
		opr = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		n, err = c.M.Long(int(addr))

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			n, err = c.M.Long(int(int16(addr)))
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			n, err = c.M.Long(int(addr))
			opr = fmt.Sprintf("$%X", addr)

		case 0x02: // program counter with displacement
			var d int16
			d, err = c.M.Sword(int(c.PC))
			if err != nil {
				return
			}
			addr := int(int32(c.PC) + int32(d))
			c.PC += 2
			n, err = c.M.Long(addr)
			opr = fmt.Sprintf("($%X,PC)", d)

		case 0x03: // program counter with index
			var addr uint32
			addr, opr, err = c.decodeBriefExt(c.PC, "PC")
			if err != nil {
				return
			}
			n, err = c.M.Long(int(addr))

		case 0x04: // immediate long
			n, err = c.M.Long(int(c.PC))
			c.PC += 4
			opr = fmt.Sprintf("#$%X", int32(n))
		}
	}
	return
}

// readBytes reads a slice of byte data from the given effective address.
func (c *Processor) readBytes(ea uint16, b []byte) (opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x02: // memory address
		_, err = c.M.Read(int(c.A[reg]), b)
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		addr := c.A[reg]
		c.A[reg] += uint32(len(b))
		_, err = c.M.Read(int(addr), b)
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg] -= uint32(len(b))
		_, err = c.M.Read(int(c.A[reg]), b)
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		_, err = c.M.Read(addr, b)
		opr = fmt.Sprintf("($%X,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		_, err = c.M.Read(int(addr), b)

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			_, err = c.M.Read(int(int16(addr)), b)
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			_, err = c.M.Read(int(addr), b)
			opr = fmt.Sprintf("$%X", addr)

		case 0x02: // program counter with displacement
			// Used by MOVEM.L (d16,PC),regs and similar multi-byte reads.
			var d int16
			d, err = c.M.Sword(int(c.PC))
			if err != nil {
				return
			}
			addr := int(int32(c.PC) + int32(d))
			c.PC += 2
			_, err = c.M.Read(addr, b)
			opr = fmt.Sprintf("($%X,PC)", d)

		case 0x03: // program counter with index
			var addr uint32
			addr, opr, err = c.decodeBriefExt(c.PC, "PC")
			if err != nil {
				return
			}
			_, err = c.M.Read(int(addr), b)
		}
	}
	return
}

func (c *Processor) readImmInt(sz uint16) (i Int, opr string, err error) {
	var n uint32
	switch sz {
	default:
		err = errBadOpSize
		return

	case SizeByte:
		var b byte
		b, opr, err = c.readImmByte()
		n = uint32(b)

	case SizeWord:
		var w uint16
		w, opr, err = c.readImmWord()
		n = uint32(w)

	case SizeLong:
		n, opr, err = c.readImmLong()
	}

	i = NewInt(n, int(sz))
	return
}

// readImmByte reads an 8-bit byte of immediate data from the current program
// counter address and increments the counter by 2 (16-bits) for word alignment.
func (c *Processor) readImmByte() (b byte, opr string, err error) {
	var n uint16
	n, err = c.M.Word(int(c.PC))
	if err != nil {
		return
	}
	b = byte(n) // are bytes signed???
	c.PC += 2
	opr = fmt.Sprintf("#$%X", byteToInt32(b))
	return
}

// readImmWord reads a 16-bit word of immediate data from the current program
// counter address and increments the counter by 2 (16-bits).
func (c *Processor) readImmWord() (n uint16, opr string, err error) {
	n, err = c.M.Word(int(c.PC))
	if err != nil {
		return
	}
	c.PC += 2
	opr = fmt.Sprintf("#$%X", wordToInt32(n))
	return
}

// readImmLong reads a 32-bit long-word of immediate data from the current
// program counter address and increments the counter by 4 (32-bits).
func (c *Processor) readImmLong() (n uint32, opr string, err error) {
	n, err = c.M.Long(int(c.PC))
	if err != nil {
		return
	}
	c.PC += 4
	opr = fmt.Sprintf("#$%X", int32(n))
	return
}

func (c *Processor) writeInt(ea uint16, v Int) (opr string, err error) {
	switch v.Size() {
	default:
		err = errBadOpSize
		return

	case SizeByte:
		opr, err = c.writeByte(ea, v.Byte())

	case SizeWord:
		opr, err = c.writeWord(ea, v.Word())

	case SizeLong:
		opr, err = c.writeLong(ea, v.Long())
	}
	return
}

func (c *Processor) writeByte(ea uint16, b byte) (opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		c.D[reg] = c.D[reg]&0xFFFFFF00 | uint32(b)
		opr = fmt.Sprintf("D%d", reg)

	case 0x02: // memory address
		_, err = c.M.Write(int(c.A[reg]), []byte{b})
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		_, err = c.M.Write(int(c.A[reg]), []byte{b})
		c.A[reg]++
		if reg == 7 {
			c.A[reg]++ // align stack pointer to 16-bit
		}
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg]--
		if reg == 7 {
			c.A[reg]-- // align stack pointer to 16-bit
		}
		_, err = c.M.Write(int(c.A[reg]), []byte{b})
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		_, err = c.M.Write(int(addr), []byte{b})
		opr = fmt.Sprintf("(%d,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		_, err = c.M.Write(int(addr), []byte{b})

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			_, err = c.M.Write(int(int16(addr)), []byte{b})
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			_, err = c.M.Write(int(addr), []byte{b})
			opr = fmt.Sprintf("$%X", addr)
		}
	}
	return
}

func (c *Processor) writeWord(ea uint16, v uint16) (opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		c.D[reg] = c.D[reg]&0xFFFF0000 | uint32(v)
		opr = fmt.Sprintf("D%d", reg)

	case 0x01: // address register
		c.A[reg] = uint32(wordToInt32(v))
		opr = fmt.Sprintf("A%d", reg)

	case 0x02: // memory address
		binary.BigEndian.PutUint16(c.buf[:2], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:2])
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		binary.BigEndian.PutUint16(c.buf[:2], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:2])
		c.A[reg] += 2
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg] -= 2
		binary.BigEndian.PutUint16(c.buf[:2], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:2])
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		binary.BigEndian.PutUint16(c.buf[:2], v)
		_, err = c.M.Write(addr, c.buf[:2])
		opr = fmt.Sprintf("(%d,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		binary.BigEndian.PutUint16(c.buf[:2], v)
		_, err = c.M.Write(int(addr), c.buf[:2])

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			binary.BigEndian.PutUint16(c.buf[:2], v)
			_, err = c.M.Write(int(int16(addr)), c.buf[:2])
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			binary.BigEndian.PutUint16(c.buf[:2], v)
			_, err = c.M.Write(int(addr), c.buf[:2])
			opr = fmt.Sprintf("$%X", addr)
		}
	}
	return
}

func (c *Processor) writeLong(ea uint16, v uint32) (opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x00: // data register
		c.D[reg] = v
		opr = fmt.Sprintf("D%d", reg)

	case 0x01: // address register
		c.A[reg] = v
		opr = fmt.Sprintf("A%d", reg)

	case 0x02: // memory address
		binary.BigEndian.PutUint32(c.buf[:4], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:4])
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		binary.BigEndian.PutUint32(c.buf[:4], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:4])
		c.A[reg] += 4
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		// The 68000 writes a long to -(An) low word first, so an address
		// error leaves An decremented by only 2.
		if (c.A[reg]-2)&1 != 0 && c.M.OnOddAccess != nil {
			c.A[reg] -= 2
			c.M.OnOddAccess(int(c.A[reg]), true)
		}
		c.A[reg] -= 4
		binary.BigEndian.PutUint32(c.buf[:4], v)
		_, err = c.M.Write(int(c.A[reg]), c.buf[:4])
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		binary.BigEndian.PutUint32(c.buf[:4], v)
		_, err = c.M.Write(addr, c.buf[:4])
		opr = fmt.Sprintf("(%d,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		binary.BigEndian.PutUint32(c.buf[:4], v)
		_, err = c.M.Write(int(addr), c.buf[:4])

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			binary.BigEndian.PutUint32(c.buf[:4], v)
			_, err = c.M.Write(int(int16(addr)), c.buf[:4])
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			binary.BigEndian.PutUint32(c.buf[:4], v)
			_, err = c.M.Write(int(addr), c.buf[:4])
			opr = fmt.Sprintf("$%X", addr)
		}
	}
	return
}

func (c *Processor) writeBytes(ea uint16, b []byte) (opr string, err error) {
	mod := ea & 0x38 >> 3
	reg := ea & 0x07
	switch mod {
	default:
		err = errBadAddress
		return

	case 0x02: // memory address
		_, err = c.M.Write(int(c.A[reg]), b)
		opr = fmt.Sprintf("(A%d)", reg)

	case 0x03: // memory address with post-increment
		_, err = c.M.Write(int(c.A[reg]), b)
		c.A[reg] += uint32(len(b))
		opr = fmt.Sprintf("(A%d)+", reg)

	case 0x04: // memory address with pre-decrement
		c.A[reg] -= uint32(len(b))
		_, err = c.M.Write(int(c.A[reg]), b)
		opr = fmt.Sprintf("-(A%d)", reg)

	case 0x05: // memory address with displacement
		var d int16
		d, err = c.M.Sword(int(c.PC))
		if err != nil {
			break
		}
		c.PC += 2
		addr := int(c.A[reg]) + int(d)
		_, err = c.M.Write(addr, b)
		opr = fmt.Sprintf("(%d,A%d)", d, reg)

	case 0x06: // address register indirect with index
		var addr uint32
		addr, opr, err = c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
		if err != nil {
			return
		}
		_, err = c.M.Write(int(addr), b)

	case 0x07: // other
		switch reg {
		default:
			err = errBadAddress
			return

		case 0x00: // absolute word (sign-extended to 32 bits)
			var addr uint16
			addr, err = c.M.Word(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 2
			_, err = c.M.Write(int(int16(addr)), b)
			opr = fmt.Sprintf("$%X", addr)

		case 0x01: // absolute long
			var addr uint32
			addr, err = c.M.Long(int(c.PC))
			if err != nil {
				return
			}
			c.PC += 4
			_, err = c.M.Write(int(addr), b)
			opr = fmt.Sprintf("$%X", addr)
		}
	}
	return
}

func (c *Processor) setStatusForByte(b byte) {
	if b == 0 {
		c.SR |= StatusZero
	}
	if b&0x80 != 0 {
		c.SR |= StatusNegative
	}
}

func (c *Processor) setStatusForWord(v uint16) {
	if v == 0 {
		c.SR |= StatusZero
	}
	if v&0x8000 != 0 {
		c.SR |= StatusNegative
	}
}

func (c *Processor) setStatusForLong(v uint32) {
	if v == 0 {
		c.SR |= StatusZero
	}
	if v&0x80000000 != 0 {
		c.SR |= StatusNegative
	}
}

// readPredec reads an operand at -(An) the way ADDX, SUBX and CMPM do: a
// byte moves A7 by 2 to keep the stack even, and a long is fetched as two
// words, low word first, so an address error leaves An decremented by only
// 2. It returns the value and its address.
func (c *Processor) readPredec(reg, sz uint16) (Int, uint32, error) {
	switch sz {
	case SizeByte:
		addr := c.A[reg] - 1
		if reg == 7 {
			addr--
		}
		c.A[reg] = addr
		b, err := c.M.Byte(int(addr))
		return NewInt(uint32(b), SizeByte), addr, err
	case SizeWord:
		c.A[reg] -= 2
		w, err := c.M.Word(int(c.A[reg]))
		return NewInt(uint32(w), SizeWord), c.A[reg], err
	default:
		c.A[reg] -= 2
		lo, err := c.M.Word(int(c.A[reg]))
		if err != nil {
			return NewInt(0, SizeLong), 0, err
		}
		c.A[reg] -= 2
		hi, err := c.M.Word(int(c.A[reg]))
		return NewInt(uint32(hi)<<16|uint32(lo), SizeLong), c.A[reg], err
	}
}

// writeAt writes v to memory at addr.
func (c *Processor) writeAt(addr uint32, v Int) error {
	var err error
	switch v.Size() {
	case SizeByte:
		_, err = c.M.Write(int(addr), []byte{v.Byte()})
	case SizeWord:
		binary.BigEndian.PutUint16(c.buf[:2], v.Word())
		_, err = c.M.Write(int(addr), c.buf[:2])
	default:
		binary.BigEndian.PutUint32(c.buf[:4], v.Long())
		_, err = c.M.Write(int(addr), c.buf[:4])
	}
	return err
}

// controlAddr computes a control addressing mode's address (as LEA does):
// (An), (d16,An), (d8,An,Xn), (xxx).W, (xxx).L, (d16,PC), (d8,PC,Xn).
func (c *Processor) controlAddr(ea uint16) (addr uint32, opr string, err error) {
	reg := ea & 7
	switch ea >> 3 & 7 {
	case 2:
		return c.A[reg], fmt.Sprintf("(A%d)", reg), nil
	case 5:
		var d int16
		if d, err = c.M.Sword(int(c.PC)); err != nil {
			return
		}
		c.PC += 2
		return uint32(int32(c.A[reg]) + int32(d)), fmt.Sprintf("($%X,A%d)", d, reg), nil
	case 6:
		return c.decodeBriefExt(c.A[reg], fmt.Sprintf("A%d", reg))
	case 7:
		switch reg {
		case 0:
			var n uint16
			n, _, err = c.readImmWord()
			return uint32(wordToInt32(n)), fmt.Sprintf("$%X", int32(n)), err
		case 1:
			var n uint32
			n, _, err = c.readImmLong()
			return n, fmt.Sprintf("$%X", n), err
		case 2:
			var n uint16
			n, _, err = c.readImmWord()
			disp := wordToInt32(n)
			return uint32(int32(c.PC) + disp - 2), fmt.Sprintf("($%X,PC)", disp), err
		case 3:
			return c.decodeBriefExt(c.PC, "PC")
		}
	}
	return 0, "", errBadAddress
}

// ProcessorState is the processor's state between instructions: what a
// save state needs to resume it exactly.
type ProcessorState struct {
	D, A       [8]uint32
	SR, PC     uint32
	SSP, USP   uint32
	PendingIPL uint32
	RunState   RunState
}

// State returns the processor's state between instructions.
func (c *Processor) State() ProcessorState {
	return ProcessorState{D: c.D, A: c.A, SR: c.SR, PC: c.PC, SSP: c.SSP, USP: c.USP,
		PendingIPL: c.pendingIPL, RunState: c.runState}
}

// SetState restores a state returned by State.
func (c *Processor) SetState(s ProcessorState) {
	c.D, c.A, c.SR, c.PC, c.SSP, c.USP = s.D, s.A, s.SR, s.PC, s.SSP, s.USP
	c.pendingIPL, c.runState = s.PendingIPL, s.RunState
}
