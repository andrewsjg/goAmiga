package amiga

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	m68k "github.com/cavaliercoder/go-m68k"
	"github.com/cavaliercoder/go-m68k/m68kmem"
)

const (
	ChipRAMSize = 512 * 1024 // 512KB chip RAM
	ROMSize     = 256 * 1024 // Kickstart 1.x ROM; 2.x/3.x ROMs are twice this

	// Memory map addresses
	ChipRAMBase  = 0x000000
	ChipRAMEnd   = 0x07FFFF
	CIASpaceBase = 0xBF0000 // both CIAs, partially decoded (see ciaSpace)
	CIASpaceEnd  = 0xBFFFFF
	CIABBase     = 0xBFD000
	CIABEnd      = 0xBFDFFF
	CIAABase     = 0xBFE000
	CIAAEnd      = 0xBFEFFF
	CustomBase   = 0xDFF000
	CustomEnd    = 0xDFFFFF
	ROMBase      = 0xF80000 // 512KB ROM area; a 256KB ROM appears twice
	ROMEnd       = 0xFFFFFF

	// PAL timing. The beam counts colour clocks (3.546895 MHz); the 68000
	// runs at twice that (7.09379 MHz), so one colour clock = 2 CPU cycles.
	// a.hpos is kept in CPU cycles; the chipset sees hpos/2 (VHPOSR, Copper).
	ColorClocksPerLine = 227                                         // PAL line: 227 colour clocks = 64µs
	CPUClocksPerLine   = ColorClocksPerLine * CPUCyclesPerColorClock // 454 CPU cycles per scanline
	// A PAL frame is 312.5 lines. Non-interlaced, every frame is a long
	// frame (313 lines, LOF=1). With BPLCON0 LACE set, frames alternate
	// long/short (313/312) so the two fields interleave.
	LinesPerLongFrame  = 313
	LinesPerShortFrame = 312
	LinesPerFrame      = LinesPerLongFrame                // nominal (non-interlaced) frame
	VBlankLine         = 0                                // VBLANK interrupt fires at line 0
	CyclesPerFrame     = CPUClocksPerLine * LinesPerFrame // 142102 CPU cycles per frame (~50 Hz)

	CPUCyclesPerColorClock = 2
	// CopperCyclesPerInstr is the Copper's throughput: it fetches one word
	// every other colour clock and each instruction is two words, so it
	// starts at most one instruction every 4 colour clocks (8 CPU cycles).
	CopperCyclesPerInstr = 8
)

// Amiga represents a complete Amiga 500 system.
type Amiga struct {
	CPU      *m68k.Processor
	ChipRAM  m68kmem.Memory
	ROM      m68kmem.Memory
	Custom   *CustomChips
	CIAA     *CIA
	CIAB     *CIA
	Copper   *Copper
	Video    *Video
	Blitter  *Blitter
	Sprites  *SpriteEngine
	Disk     *DiskDrive
	Keyboard *Keyboard
	Paula    *Paula

	// Expansion memory (ConfigureMemory): slow RAM at $C00000 and a Zorro
	// II fast RAM board. nil when not fitted.
	SlowRAM *ram
	FastRAM *zorroRAM
	Mouse   *Mouse
	Mapper  *m68kmem.Mapper

	logger io.Writer

	// Beam position tracking
	hpos   int // horizontal position (0-227)
	vpos   int // vertical position (0-311)
	cycles uint64

	// lof is the long-frame flag (VPOSR bit 15) for the current frame. It
	// sets the frame length: 313 lines when true, 312 when false.
	lof bool

	// E clock divider: CIAs tick every 10 CPU cycles
	eClockCounter int

	romSize int // 256KB or 512KB

	// ROM overlay: when true, ROM is mapped to $000000-$03FFFF (256KB mirror)
	overlay bool

	// cpuStopped tracks whether the CPU is in a STOP state (waiting for interrupt).
	// We must NOT call CPU.Step() in this state — instead we idle the beam so that
	// VBLANK/VERTB interrupts will fire and wake the CPU.

	// FastIdleSkips counts how many cycles were skipped by fast-idle optimization
	FastIdleSkips uint64
	FastIdleCount uint64 // number of times fast-idle triggered
	cpuStopped    bool

	// Debug fields for interrupt tracing
	debugIntCount int
	debugWriter   io.Writer

	// cpuChipAccesses counts CPU chip bus accesses since the last DMA
	// contention charge (see chipBusWait). DMAWaitCycles totals the CPU
	// cycles lost to DMA.
	cpuChipAccesses int
	DMAWaitCycles   uint64

	// cpuCIAAccesses counts CPU CIA accesses in the current step;
	// CIAWaitCycles totals the extra cycles spent syncing to the E clock.
	cpuCIAAccesses int
	CIAWaitCycles  uint64

	// busErrorCount limits exception storms after freelist/memory collapse
	// (otherwise the UI freezes on a white frame raising bus errors forever).
	busErrorCount int
}

// New creates a new Amiga with the given Kickstart ROM data.
func New(romData []byte, logger io.Writer) (*Amiga, error) {
	if len(romData) != ROMSize && len(romData) != ROMSize*2 {
		return nil, fmt.Errorf("invalid ROM size: %d bytes (expected %d or %d)", len(romData), ROMSize, ROMSize*2)
	}

	a := &Amiga{
		logger:      logger,
		debugWriter: os.Stderr,
		lof:         true,
	}

	// Create memory devices
	a.ChipRAM = newChipRAM(ChipRAMSize)
	a.ROM = m68kmem.NewROM(romData)
	a.romSize = len(romData)
	a.Custom = NewCustomChips(logger, a)
	a.CIAA = NewCIA("CIA-A", logger)
	a.CIAB = NewCIA("CIA-B", logger)

	// TOD clocks come from the video sync signals, not the E clock:
	//   CIA-A TOD ← vertical sync   → TickTOD() once per frame (50 Hz PAL)
	//   CIA-B TOD ← horizontal sync → TickTOD() once per scanline
	// Both are driven from the beam tracker in advanceBeam*. Leaving the
	// E-clock TOD divider disabled keeps a single source of truth.
	a.CIAA.todClkRate = 0
	a.CIAB.todClkRate = 0

	// Create Copper, Video, and Blitter
	a.Copper = NewCopper(a)
	a.Video = NewVideo(a)
	a.Blitter = NewBlitter(a)
	a.Sprites = NewSpriteEngine(a)
	a.Disk = NewDiskDrive(a)
	a.Keyboard = NewKeyboard(a)
	a.Paula = NewPaula(a)
	a.Mouse = NewMouse(a)

	// Wire CIA interrupt callbacks
	a.CIAA.OnInterrupt = func() { a.Custom.SetIntreq(1 << 3) }  // PORTS (IPL 2)
	a.CIAB.OnInterrupt = func() { a.Custom.SetIntreq(1 << 13) } // EXTER (IPL 6)

	// Wire CIA-A callbacks for keyboard, mouse, and OVL bit
	a.CIAA.OnPRAWrite = func(old, new byte) {
		// Bit 0 is the OVL (overlay) bit: 1 = ROM at $000000, 0 = chip RAM at $000000
		oldOVL := old&0x01 != 0
		newOVL := new&0x01 != 0
		if oldOVL != newOVL {
			if a.logger != nil {
				fmt.Fprintf(a.logger, "CIA-A PRA OVL changed: %v → %v (will remap)\n", oldOVL, newOVL)
			}
			a.wireMemoryMap(newOVL)
		}
	}
	a.CIAA.OnPRARead = func(regVal byte) byte {
		// CIA-A PRA: disk status on bits 5-2, joystick/mouse on bits 7-6.
		// /FIR0 (bit 6) = left mouse / joy0 fire, /FIR1 (bit 7) = joy1 fire.
		// Both are active-low open inputs pulled high when released. We must
		// force them high when idle — the PRA latch often has them clear
		// (default $03), which made left-click appear permanently pressed.
		val := a.Disk.ReadStatus(regVal) // sets bits 5-2 based on drive state
		if a.Mouse != nil && a.Mouse.LeftButton {
			val &^= 0x40 // pressed → low
		} else {
			val |= 0x40 // released → high
		}
		val |= 0x80 // /FIR1 idle high (no joy1 fire)
		return val
	}
	a.CIAA.OnCRAWrite = func(old, new byte) {
		// Keyboard handshake: detect CRA bit 6 transition high→low
		if old&0x40 != 0 && new&0x40 == 0 {
			a.Keyboard.HandshakeComplete()
		}
	}

	// Wire CIA-B callbacks for disk drive
	a.CIAB.OnPRBWrite = func(old, new byte) {
		a.Disk.HandleCIABWrite(old, new)
	}
	// CIA-B PRA: no disk status here (disk status is on CIA-A PRA)

	// Wire up memory map
	a.wireMemoryMap(true)

	// Create CPU
	decoder := m68kmem.NewDecoder(a.Mapper)
	a.CPU = &m68k.Processor{
		M: decoder,
	}
	if logger != nil {
		a.CPU.TraceWriter = logger
	}

	// Register RESET instruction handler — resets external peripherals only.
	// On the real Amiga, RESET pulses the external reset line for ~124ms.
	// It does NOT re-read reset vectors or restart the CPU.
	// We reset CIAs and custom chips but preserve the current memory map.
	// CIA-A Reset sets OVL=1 (PRA bit 0), so we re-apply the memory map
	// based on the new OVL state. Kickstart only executes RESET while the
	// PC is in ROM space, so re-enabling overlay is safe at that point.
	a.CPU.ResetHandler = func() {
		// Debug: print ExecBase checksum fields before RESET
		if a.logger != nil {
			buf := make([]byte, 4)
			a.ChipRAM.Read(4, buf)
			execBase := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
			fmt.Fprintf(a.logger, "RESET handler: ChipRAM[$4]=$%08X\n", execBase)
			if execBase > 0 && execBase < ChipRAMSize {
				// Compute LowMemChkSum: sum words from ExecBase+$22, count=25
				var sum uint16
				wbuf := make([]byte, 2)
				for i := 0; i < 25; i++ {
					off := int(execBase) + 0x22 + i*2
					a.ChipRAM.Read(off, wbuf)
					sum += uint16(wbuf[0])<<8 | uint16(wbuf[1])
				}
				fmt.Fprintf(a.logger, "RESET handler: LowMemChkSum computed=$%04X (should be $FFFF)\n", sum)
			}
		}
		a.CIAA.Reset()
		a.CIAB.Reset()
		a.Custom.Reset()
		// Re-apply memory map based on CIA-A OVL bit (now 1 after reset)
		a.wireMemoryMap(a.CIAA.OVL())
	}

	return a, nil
}

// wireMemoryMap configures the memory mapper with overlay state.
func (a *Amiga) wireMemoryMap(overlay bool) {
	if a.logger != nil {
		fmt.Fprintf(a.logger, "wireMemoryMap(overlay=%v)\n", overlay)
	}
	a.overlay = overlay
	a.Mapper = m68kmem.NewMapper()
	// Unmapped addresses float rather than fault, as on real hardware.
	a.Mapper.OpenBus = true

	// Anything not mapped floats (open bus): Kickstart probes for memory
	// by reading and writing, and finds nothing there.
	if overlay {
		// The ROM overlays chip RAM's space at reset (mirrored if 256KB),
		// so the CPU finds its reset vectors at $000000.
		a.mapROM(0x000000)
	} else {
		a.Mapper.Map(&busCounter{a.ChipRAM, &a.cpuChipAccesses}, ChipRAMBase, ChipRAMEnd)
	}

	// Zorro II fast RAM: its Autoconfig ID until configured, then its RAM.
	if z := a.FastRAM; z != nil && !z.shut {
		if z.base == 0 {
			a.Mapper.Map(z, AutoconfigBase, AutoconfigEnd)
		} else {
			a.Mapper.Map(z.mem, int(z.base), int(z.base)+len(z.mem.b)-1)
		}
	}

	a.Mapper.Map(&busCounter{&ciaSpace{a.CIAA, a.CIAB}, &a.cpuCIAAccesses}, CIASpaceBase, CIASpaceEnd)
	// Slow RAM is on the chip bus: CPU accesses contend with DMA.
	if a.SlowRAM != nil {
		a.Mapper.Map(&busCounter{a.SlowRAM, &a.cpuChipAccesses}, SlowRAMBase, SlowRAMBase+len(a.SlowRAM.b)-1)
	}
	a.Mapper.Map(&busCounter{a.Custom, &a.cpuChipAccesses}, CustomBase, CustomEnd)

	a.mapROM(ROMBase)

	// Update CPU decoder if it exists
	if a.CPU != nil && a.CPU.M != nil {
		a.CPU.M = m68kmem.NewDecoder(a.Mapper)
	}
}

// mapROM maps the Kickstart ROM across the 512KB from base: a 512KB
// (2.x/3.x) ROM once, a 256KB (1.x) ROM twice, as the A500 decodes it.
func (a *Amiga) mapROM(base int) {
	for off := 0; off < 2*ROMSize; off += a.romSize {
		a.Mapper.Map(a.ROM, base+off, base+off+a.romSize-1)
	}
}

// Reset performs a hard reset of the Amiga.
func (a *Amiga) Reset() error {
	// Expansion boards forget their configuration on reset.
	if a.FastRAM != nil {
		a.FastRAM.unconfigure()
	}
	// Enable ROM overlay for reset vectors
	a.wireMemoryMap(true)

	// Reset CPU using vector table
	if err := a.CPU.ResetWithVectors(); err != nil {
		return fmt.Errorf("CPU reset failed: %w", err)
	}

	a.hpos = 0
	a.vpos = 0
	a.lof = true // non-interlaced frames are long
	a.cycles = 0
	a.cpuStopped = false
	a.busErrorCount = 0

	if a.Keyboard != nil {
		a.Keyboard.Reset()
	}
	if a.Paula != nil {
		a.Paula.Reset()
	}

	if a.logger != nil {
		fmt.Fprintf(a.logger, "RESET: SSP=$%08X PC=$%08X\n", a.CPU.A[7], a.CPU.PC)
	}

	return nil
}

// Step executes one CPU instruction and advances timing by the instruction's
// actual cycle count. This keeps the beam position synchronized with CPU speed.
//
// When the CPU is in a STOP state (halt waiting for interrupt), we do NOT call
// CPU.Step() — doing so would reset the stopped flag and execute whatever opcode
// happens to follow the STOP, which is wrong. Instead we idle by 4 cycles so
// that the beam keeps advancing and VBLANK/VERTB interrupts get generated.
func (a *Amiga) Step() error {
	var err error

	if a.cpuStopped {
		// CPU is halted (STOP instruction). Do not execute any instruction.
		// Fast-idle: skip ahead to the next interesting event (CIA timer
		// underflow or VBL) instead of ticking 4 cycles at a time.
		skip := CyclesPerFrame // upper bound: one full frame
		// Cycles until next VBL (line 0 wrap)
		cyclesToVBL := (a.linesThisFrame()-a.vpos)*CPUClocksPerLine - a.hpos
		if cyclesToVBL > 0 && cyclesToVBL < skip {
			skip = cyclesToVBL
		}
		// CIA timers may fire sooner — find the earliest
		if et := a.CIAA.ETicksToNextEvent(); et > 0 {
			cpuCycles := et*10 - a.eClockCounter
			if cpuCycles > 0 && cpuCycles < skip {
				skip = cpuCycles
			}
		}
		if et := a.CIAB.ETicksToNextEvent(); et > 0 {
			cpuCycles := et*10 - a.eClockCounter
			if cpuCycles > 0 && cpuCycles < skip {
				skip = cpuCycles
			}
		}
		// Wake in time to raise a pending blitter or disk interrupt.
		if a.Blitter != nil && a.Blitter.busy && a.Blitter.completionCycles < skip {
			skip = a.Blitter.completionCycles
		}
		if a.Disk != nil {
			if c := a.Disk.CyclesToNextEvent(); c > 0 && c < skip {
				skip = c
			}
		}
		if a.Paula != nil {
			if c := a.Paula.CyclesToNextInterrupt(); c > 0 && c < skip {
				skip = c
			}
		}
		if skip < 4 {
			skip = 4
		}
		a.CPU.LastCycles = skip
		a.FastIdleSkips += uint64(skip)
		a.FastIdleCount++
	} else {
		err = a.CPU.Step()
		// Detect transition into stopped state (STOP instruction just executed).
		if a.CPU.RunState() == m68k.RunStateStopped {
			a.cpuStopped = true
		}
		// Access violations should not occur: the Mapper runs in OpenBus
		// mode, so unmapped reads float to 0 and writes are dropped. If one
		// still appears, do NOT raise a fake bus error — the 68000 bus
		// error stack frame is not the short SR/PC frame RaiseException builds,
		// and that mismatch produced Guru #00000000.00000000. Just drop the
		// host error and keep the UI alive.
		if err != nil && m68kmem.IsAccessViolation(err) {
			a.busErrorCount++
			if a.logger != nil {
				fmt.Fprintf(a.logger, "suppressed access violation #%d: %v PC=$%08X\n",
					a.busErrorCount, err, a.CPU.PC)
			}
			err = nil
			if a.CPU.LastCycles < 4 {
				a.CPU.LastCycles = 4
			}
		}
	}

	// Advance beam by the number of CPU cycles consumed by this instruction
	elapsed := a.CPU.LastCycles
	if elapsed < 4 {
		elapsed = 4 // minimum 68000 instruction time
	}

	// Wait for chip bus slots held by DMA. Accesses made while servicing an
	// interrupt after the previous Step are charged here too.
	if wait := a.chipBusWait(a.cpuChipAccesses); wait > 0 {
		elapsed += wait
		a.DMAWaitCycles += uint64(wait)
	}
	a.cpuChipAccesses = 0

	// CIA accesses are synchronised to the E clock.
	if a.cpuCIAAccesses > 0 {
		wait := a.ciaAccessWait(a.cpuCIAAccesses, elapsed)
		elapsed += wait
		a.CIAWaitCycles += uint64(wait)
		a.cpuCIAAccesses = 0
	}

	// Tick blitter / disk completion timers (deferred interrupts after data
	// transfer). The blitter only advances on slots other DMA leaves free.
	if a.Blitter != nil {
		blitCycles := elapsed
		if a.Blitter.busy {
			blitCycles = a.blitterSlots(elapsed) * CPUCyclesPerColorClock
		}
		a.Blitter.TickCycles(blitCycles)
	}
	if a.Disk != nil {
		a.Disk.TickCycles(elapsed)
	}
	if a.Paula != nil {
		a.Paula.Tick(elapsed)
	}

	// Use fast per-line beam advance when the CPU is stopped (no instruction
	// side-effects to worry about). This processes complete scanlines at a time
	// instead of per-cycle, giving ~100x speedup during idle periods.
	if a.cpuStopped && elapsed > CPUClocksPerLine {
		a.advanceBeamFast(elapsed)
	} else {
		a.advanceBeamPerCycle(elapsed)
	}

	// If the CPU was stopped, check whether a newly-raised interrupt is
	// strong enough to wake it (pendingIPL > current IPL mask in SR).
	if a.cpuStopped && a.CPU.PendingIPL() > a.CPU.IPL() {
		a.cpuStopped = false
		// Service the interrupt — this vectors through the autovector table
		// and clears the pending flag inside CheckInterrupts().
		if iErr := a.CPU.CheckInterrupts(); iErr != nil {
			return iErr
		}
	}

	// Check if CIA-A OVL bit was cleared (disable overlay)
	if a.overlay && !a.CIAA.OVL() {
		a.wireMemoryMap(false)
		if a.logger != nil {
			fmt.Fprintf(a.logger, "ROM OVERLAY DISABLED\n")
		}
	}

	return err
}

// RunFrame advances the machine until the next VBLANK (vpos wraps to 0),
// leaving a fully rendered frame in Video.Pixels for the display to present.
//
// A fixed cycle budget is a poor fit here: Step() can run many cycles at once
// (STOP idle advances whole scanlines), so a budget often overshoots and stops
// mid-frame. Presenting a half-updated framebuffer looks like vertical tear or
// "shake". Syncing to VBLANK guarantees every presented image is complete.
//
// Important: when the CPU is STOP'd at vpos==0, a single Step() may fast-idle
// through an entire frame and land on vpos==0 again. The old "leave line 0"
// loop never exited in that case and froze the UI (force-quit). We detect
// full-frame idle progress via the cycle counter and return.
func (a *Amiga) RunFrame() error {
	startCycles := a.cycles

	// Leave the current line-0 VBLANK so the next vpos==0 is a new frame.
	if a.vpos == 0 {
		for a.vpos == 0 {
			if err := a.Step(); err != nil {
				return err
			}
			// STOP idle from line 0 advanced a whole frame and wrapped to 0.
			// That frame is fully rendered — present it.
			if a.cycles-startCycles >= uint64(CyclesPerFrame) {
				return nil
			}
		}
	}
	// Run until the beam returns to VBLANK.
	for a.vpos != 0 {
		if err := a.Step(); err != nil {
			return err
		}
		// Safety: never spin forever if something odd happens to vpos.
		if a.cycles-startCycles > uint64(CyclesPerFrame*4) {
			return nil
		}
	}
	return nil
}

// Run executes instructions until the CPU stops or an error occurs.
func (a *Amiga) Run(maxCycles uint64) error {
	for i := uint64(0); i < maxCycles; i++ {
		if err := a.Step(); err != nil {
			return err
		}
		if a.CPU.RunState() == m68k.RunStateStopped {
			return nil
		}
	}
	return nil
}

// linesThisFrame is the length of the current frame: long frames have
// 313 lines, short (interlace odd-field) frames 312.
func (a *Amiga) linesThisFrame() int {
	if a.lof {
		return LinesPerLongFrame
	}
	return LinesPerShortFrame
}

// nextLine moves the beam to the start of the next scanline, starting a new
// frame when it passes the last line. It reports whether a frame started.
func (a *Amiga) nextLine() bool {
	a.hpos = 0
	a.vpos++
	a.CIAB.TickTOD() // CIA-B TOD ← horizontal sync
	if a.vpos < a.linesThisFrame() {
		return false
	}
	a.startFrame()
	return true
}

// startFrame handles vertical blank: beam to line 0, VERTB interrupt,
// Copper restart from COP1LC, and a new video frame.
func (a *Amiga) startFrame() {
	a.vpos = 0
	// Agnus toggles LOF each frame only in interlace; otherwise every frame
	// is long.
	if a.Custom.ReadReg16(0x100)&0x0004 != 0 { // BPLCON0 LACE
		a.lof = !a.lof
	} else {
		a.lof = true
	}
	a.Custom.SetIntreq(1 << 5)
	a.Copper.Restart(1)
	a.CIAA.TickTOD() // CIA-A TOD ← vertical sync
	if a.Video != nil {
		a.Video.StartFrame()
	}
	if a.Keyboard != nil {
		a.Keyboard.Tick()
	}
}

// advanceBeamPerCycle advances the beam position one cycle at a time.
// This is the accurate path used during normal CPU execution.
func (a *Amiga) advanceBeamPerCycle(elapsed int) {
	for c := 0; c < elapsed; c++ {
		a.cycles++
		a.hpos++

		if a.hpos >= CPUClocksPerLine {
			// Fetch sprite data for this scanline
			if a.Sprites != nil {
				a.Sprites.FetchLine(a.vpos)
			}
			// Render the completed scanline
			if a.Video != nil {
				a.Video.RenderLine(a.vpos)
			}

			a.nextLine()
		}

		// E clock divider: CIA ticks every 10 CPU cycles (~709KHz)
		a.eClockCounter++
		if a.eClockCounter >= 10 {
			a.eClockCounter = 0
			a.CIAA.Tick()
			a.CIAB.Tick()
		}

		// Copper step on its DMA slots (DMACON bits 9=DMAEN and 7=COPEN)
		if a.hpos%CopperCyclesPerInstr == 0 {
			dmacon := a.Custom.ReadReg16(0x096)
			if dmacon&0x0280 == 0x0280 {
				a.Copper.Step(a.hpos, a.vpos)
			}
		}
	}
}

// advanceBeamFast advances the beam position one scanline at a time.
// Used during fast-idle (CPU STOP) when no instruction side-effects need
// per-cycle accuracy. This gives ~100x speedup over per-cycle processing.
func (a *Amiga) advanceBeamFast(elapsed int) {
	displayActive := a.Video != nil && a.Video.DisplayActive

	// Batch CIA ticks for the entire elapsed period in one call.
	eTicks := (a.eClockCounter + elapsed) / 10
	a.eClockCounter = (a.eClockCounter + elapsed) % 10
	if eTicks > 0 {
		a.CIAA.TickN(eTicks)
		a.CIAB.TickN(eTicks)
	}

	if !displayActive {
		// Headless fast path: no pixel rendering / sprite DMA, but still step
		// the Copper once per scanline. Skipping Copper during STOP left COP2
		// (bitplane ptrs, DIW, mid-frame BPLCON0) unexecuted for whole frames.
		a.cycles += uint64(elapsed)

		dmacon := a.Custom.ReadReg16(0x096)
		copperEnabled := dmacon&0x0280 == 0x0280

		remaining := elapsed
		for remaining > 0 {
			lineRemain := CPUClocksPerLine - a.hpos
			if lineRemain > remaining {
				lineRemain = remaining
			}
			if copperEnabled {
				// Full horizontal range so HP waits complete on this line
				a.Copper.StepLine(0, CPUClocksPerLine, a.vpos)
			}
			a.hpos += lineRemain
			remaining -= lineRemain

			if a.hpos >= CPUClocksPerLine {
				if a.nextLine() {
					dmacon = a.Custom.ReadReg16(0x096)
					copperEnabled = dmacon&0x0280 == 0x0280
				}
			}
		}
		return
	}

	// Display-active path: per-line processing for accurate Copper + rendering.
	dmacon := a.Custom.ReadReg16(0x096)
	copperEnabled := dmacon&0x0280 == 0x0280

	remaining := elapsed
	for remaining > 0 {
		lineRemain := CPUClocksPerLine - a.hpos
		if lineRemain > remaining {
			lineRemain = remaining
		}

		a.cycles += uint64(lineRemain)
		remaining -= lineRemain

		if copperEnabled {
			a.Copper.StepLine(a.hpos, a.hpos+lineRemain, a.vpos)
		}

		a.hpos += lineRemain

		if a.hpos >= CPUClocksPerLine {
			if a.Sprites != nil {
				a.Sprites.FetchLine(a.vpos)
			}
			a.Video.RenderLine(a.vpos)

			if a.nextLine() {
				dmacon = a.Custom.ReadReg16(0x096)
				copperEnabled = dmacon&0x0280 == 0x0280
			}
		}
	}
}

// LoadDisk loads an ADF disk image into the floppy drive (DF0:). Unless
// the drive is write-protected, sectors the Amiga writes are saved back to
// the file.
func (a *Amiga) LoadDisk(path string) error {
	return a.LoadDiskUnit(0, path)
}

// LoadDiskUnit inserts the ADF at path into drive DFunit: (0-3),
// connecting that drive if needed. Tracks the Amiga writes are saved back to
// the file unless the drive is write-protected.
func (a *Amiga) LoadDiskUnit(unit int, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read ADF: %w", err)
	}
	dr := a.Disk.DF(0)
	if unit > 0 {
		dr = a.Disk.ConnectDrive(unit)
	}
	if err := dr.InsertDisk(data); err != nil {
		return err
	}
	dr.path = path
	return nil
}

// SwapDisk ejects the disk in DF0: and inserts the ADF at path once the
// Amiga has noticed the drive is empty (see DiskDrive.SwapDisk). Writes to
// the new disk are saved to path.
func (a *Amiga) SwapDisk(path string) error {
	return a.SwapDiskUnit(0, path)
}

// SwapDiskUnit is SwapDisk for drive DFunit:, which must be connected.
func (a *Amiga) SwapDiskUnit(unit int, path string) error {
	dr := a.Disk.DF(unit)
	if dr == nil {
		return fmt.Errorf("DF%d: is not connected", unit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read ADF: %w", err)
	}
	return dr.SwapDisk(data, path)
}

// ROMChecksum returns the Kickstart checksum of a ROM image: the sum of its
// longwords with end-around carry. Kickstart verifies it at boot (a red
// screen if wrong); a good image sums to $FFFFFFFF.
func ROMChecksum(rom []byte) uint32 {
	var sum uint64
	for i := 0; i+3 < len(rom); i += 4 {
		sum += uint64(binary.BigEndian.Uint32(rom[i:]))
		if sum > 0xFFFFFFFF {
			sum = sum&0xFFFFFFFF + 1
		}
	}
	return uint32(sum)
}

// LoadROM loads a Kickstart ROM from the given file path.
func LoadROM(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read ROM: %w", err)
	}
	return data, nil
}
