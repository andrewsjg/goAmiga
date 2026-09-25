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
	completionCycles int // cycles until BBUSY clears
	pendingIRQs      int // BLIT completions not yet signaled via INTREQ
	lastSize         uint16
	// Pre-blit state: saved at the start of Execute() before pointer write-back
	preApt, preBpt, preCpt, preDpt uint32
	// denyLo/denyHi: half-open [lo,hi) ranges that must not be blitted into.
	// Rebuilt each Execute from the Exec freelist. Free memory sits flush
	// against WB bitplanes ($C960→$D510, $17510 after BPL2). A plane overrun
	// that only smashed freelist *headers* caused AllocMem to walk
	// next=$FFFFFF. Protecting headers alone still left free *bodies* filled
	// with plane/XOR junk; later AllocMem hands that garbage out as stacks or
	// structures → "Software error - task held". Deny the whole free chunk.
	denyLo, denyHi []int
	// GuardWrites enables the legacy heuristic D-channel write guards
	// (address bands, freelist/task deny maps, geometric span limits).
	// They were band-aids for the descending-modulo bug and are off by
	// default now that blits are hardware-accurate.
	GuardWrites bool
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

// canWriteD decides whether a D-channel store may touch chip RAM.
//
// Guru #00000004.00003342 is Illegal Instruction in input.device (task @$3342;
// stack ~$339E–$439E). High plane starts already cannot write below $8000, but
// a mid-chip start (e.g. $2000) with a large BLTSIZE can still walk into that
// stack band — that is the remaining smash path on drag/release.
//
// Guru #00000000.00000000 is the wiped-alert end-state after heap neighbors
// of a window buffer (tasks/stacks) were painted by a small blit that still
// had a multi-KB geometric window.
//
// Policy:
//   - vector page and past chip: never
//   - ExecBase / ThisTask / MemList ($600–$A00): never
//   - input.device band ($3000–$4800): never
//   - display band [$A000,$18000): start there → write only there (insert-disk and
//     WB planes; crosses $D000; blocks heap $18000+)
//   - start ≥ $8000: never write below $8000
//   - freelist deny map (buildDeny)
//   - geometric span: boot/display keep a 4KB floor; heap starts do not
func (b *Blitter) canWriteD(writeAddr int, startD uint32) bool {
	if !b.GuardWrites {
		// Hardware-accurate: the blitter may write anywhere in chip RAM.
		// The guard heuristics below were added to contain runaway blits
		// whose real cause was the descending-mode modulo bug (modulos were
		// added instead of subtracted, walking pointers diagonally out of
		// the bitmap). With that fixed, OS-programmed blits stay inside
		// their bitmaps and the guards only risk denying legitimate writes.
		return writeAddr >= 0 && writeAddr < ChipRAMSize
	}
	if writeAddr < 0x100 || writeAddr >= ChipRAMSize {
		return false
	}
	// ExecBase ~$676, ThisTask, MemList, first MemHeader.
	if writeAddr >= 0x600 && writeAddr < 0xA00 {
		return false
	}

	start := int(startD & 0x7FFFE)

	// Input stack of devices live here on KS1.3+WB:
	//   keyboard.device  base~$2C8C  LVO~$2C68
	//   gameport.device  base~$2DC4  LVO~$2DA0
	//   timer.device     base~$2F4E  LVO~$2F18
	//   input.device     task~$3342  stack~$339E–$439E
	// A blit fill of $0808 into those LVOs produced Guru #08080004.08080000.
	// Keyboard/gameport/timer LVOs, input.device stack, intuition.library.
	// Do not start below $2C00 — that regresses ADF boot (trackdisk temps).
	// Graphics LVO (~$1F90) is covered by the lib deny map instead.
	const taskLo, taskHi = 0x2C00, 0x5200
	if writeAddr >= taskLo && writeAddr < taskHi {
		return false
	}

	// Unified display/bitplane band (insert-disk + Workbench).
	const dispLo, dispHi = 0xA000, 0x18000
	if start >= dispLo && start < dispHi {
		if writeAddr < dispLo || writeAddr >= dispHi {
			return false
		}
	} else if start >= 0x8000 && writeAddr < 0x8000 {
		return false
	}

	h := int(b.lastSize >> 6)
	w := int(b.lastSize & 0x3F)
	if h == 0 {
		h = 1024
	}
	if w == 0 {
		w = 64
	}
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	mod := abs(int(b.dmod))
	if c := abs(int(b.cmod)); c > mod {
		mod = c
	}
	if mod > 256 {
		mod = 256
	}
	row := w * 2
	if b.con1&0x0001 != 0 {
		row = mod
		if row < 2 {
			row = 2
		}
	} else {
		row += mod
	}
	maxSpan := h*row + 128
	// Span policy by destination island:
	//  - Heap: no 4KB floor (neighbors are live tasks/messages). Cap fits one
	//    hi-res plane strip (~20KB). BLTSIZE height/width of 0 mean 1024×64 on
	//    real Agnus — treat that as runaway on heap and clamp harder.
	//  - Display band: keep a 4KB floor (insert-disk / WB plane programs).
	//  - Mid-chip ($2000–$A000): tight — overruns here smash allocated
	//    layer/IE structures → Illegal Instruction in input.device (#00000004.00003342).
	//  - Boot low (<$2000): keep 4KB floor for trackdisk temps.
	if start >= 0x18000 {
		if maxSpan < 128 {
			maxSpan = 128
		}
		if h >= 512 && w >= 32 && maxSpan > 0x2800 {
			maxSpan = 0x2800 // likely BLTSIZE 0,0 → 1024×64
		}
		if maxSpan > 0xA000 {
			maxSpan = 0xA000
		}
	} else if start >= 0xA000 {
		if maxSpan < 0x1000 {
			maxSpan = 0x1000
		}
		if maxSpan > 0xC000 {
			maxSpan = 0xC000
		}
	} else if start >= 0x2000 {
		// Mid-chip: do not apply the 4KB *minimum* (tiny blits were allowed to
		// paint neighbors → #00000004.00003342). Still allow large programmed
		// spans up to 24KB so boot/trackdisk paths that start here keep working.
		if maxSpan < 256 {
			maxSpan = 256
		}
		if maxSpan > 0x6000 {
			maxSpan = 0x6000
		}
	} else {
		if maxSpan < 0x1000 {
			maxSpan = 0x1000
		}
		if maxSpan > 0xC000 {
			maxSpan = 0xC000
		}
	}
	diff := writeAddr - start
	if diff < 0 {
		diff = -diff
	}
	if diff > maxSpan {
		return false
	}
	for i := range b.denyLo {
		if writeAddr >= b.denyLo[i] && writeAddr < b.denyHi[i] {
			return false
		}
	}
	return true
}

// buildDeny snapshots Exec freelist + all tasks/stacks that a runaway
// D-channel must not touch. Called once per Execute. Safe no-op before
// ExecBase exists.
//
// Guru #00000004.00022028 is Illegal Instruction in a heap-resident task at
// $22028 (not input.device). Fixed address bands cannot cover every task;
// walking TaskReady/TaskWait/ThisTask and denying each Task + [SPLower,SPUpper)
// does.
func (b *Blitter) buildDeny() {
	b.denyLo = b.denyLo[:0]
	b.denyHi = b.denyHi[:0]
	if !b.GuardWrites {
		return
	}
	if b.amiga == nil || b.amiga.ChipRAM == nil {
		return
	}
	rd32 := func(addr int) uint32 {
		if addr < 0 || addr+3 >= ChipRAMSize {
			return 0
		}
		buf := make([]byte, 4)
		b.amiga.ChipRAM.Read(addr, buf)
		return uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	}
	add := func(lo, hi int) {
		if lo < 0 {
			lo = 0
		}
		if hi > ChipRAMSize {
			hi = ChipRAMSize
		}
		if hi <= lo {
			return
		}
		b.denyLo = append(b.denyLo, lo)
		b.denyHi = append(b.denyHi, hi)
	}
	eb := int(rd32(4))
	if eb < 0x100 || eb >= ChipRAMSize-0x200 {
		return
	}

	// --- Tasks: ThisTask + TaskReady + TaskWait ---
	// struct Task: tc_SPLower @ $3A, tc_SPUpper @ $3E (after Node+$24 of fields).
	const (
		tcSPLower = 0x3A
		tcSPUpper = 0x3E
		taskBody  = 0x5C // enough to cover Task core (Process is larger; stack is separate)
		taskReady = 0x196
		taskWait  = 0x1A4
		thisTask  = 0x114
	)
	denyTask := func(t uint32) {
		ti := int(t)
		if ti < 0x100 || ti+taskBody > ChipRAMSize {
			return
		}
		// type should be NT_TASK (1) or NT_PROCESS (13)
		typ := make([]byte, 1)
		b.amiga.ChipRAM.Read(ti+8, typ)
		if typ[0] != 1 && typ[0] != 13 {
			return
		}
		add(ti, ti+taskBody)
		lo := int(rd32(ti + tcSPLower))
		hi := int(rd32(ti + tcSPUpper))
		sp := int(rd32(ti + 0x36)) // tc_SPReg
		// Only protect stacks in the heap. File System uses SP ~$0F3C–$1284 in
		// low chip shared with boot/trackdisk buffers — denying that breaks ADF boot.
		// Also accept SP slightly outside [lo,hi] (interrupt frames) so a
		// half-clobbered SPReg does not drop stack protection entirely.
		if lo >= 0x2000 && hi > lo && hi <= ChipRAMSize && hi-lo >= 256 && hi-lo <= 0x8000 {
			if sp == 0 || (sp >= lo-64 && sp < hi+64) {
				if lo >= 32 {
					lo -= 32
				}
				if hi+32 <= ChipRAMSize {
					hi += 32
				}
				add(lo, hi)
			}
		}
	}
	walkTaskList := func(listOff int) {
		head := rd32(eb + listOff)
		seen := map[uint32]bool{}
		for n, t := 0, head; n < 48 && t != 0; n++ {
			if int(t) < 0x100 || int(t)+4 >= ChipRAMSize {
				break
			}
			succ := rd32(int(t))
			if succ == 0 {
				break // list tail sentinel
			}
			if seen[t] {
				break
			}
			seen[t] = true
			denyTask(t)
			t = succ
		}
	}
	walkTaskList(taskReady)
	walkTaskList(taskWait)
	if tt := rd32(eb + thisTask); tt != 0 {
		denyTask(tt)
	}

	// --- Libraries + devices: protect LVO jump tables ---
	// JSR -LVO(A6) with A6 = library/device base; vectors live at
	// [base-NegSize, base). Name strings are usually in ROM (read via CPU.M).
	//
	// Name allowlists missed keyboard/gameport/timer devices just below
	// $3000; blits filled their LVOs with $0808 → Guru #08080004.08080000.
	// Protect every LibList/DeviceList node with a plausible Library header.
	const (
		libList    = 0x17A
		deviceList = 0x15E
		libNegSize = 0x10 // UWORD
		libPosSize = 0x12 // UWORD
	)
	readU16 := func(addr int) int {
		if addr < 0 || addr+1 >= ChipRAMSize {
			return 0
		}
		wbuf := make([]byte, 2)
		b.amiga.ChipRAM.Read(addr, wbuf)
		return int(wbuf[0])<<8 | int(wbuf[1])
	}
	protectLibNode := func(node uint32) {
		ni := int(node)
		if ni < 0x100 || ni+0x20 > ChipRAMSize {
			return
		}
		neg := readU16(ni + libNegSize)
		pos := readU16(ni + libPosSize)
		// Each LVO slot is 6 bytes (JMP.W); NegSize is the jump-table span.
		if neg < 6 || neg > 0x800 {
			return
		}
		if pos < 0x14 {
			pos = 0x30
		}
		// IntuitionBase PosSize is ~$564 and holds semaphores past +$200.
		// Capping at $200 left IBase locks open → blitter fill $0808 →
		// Guru #81080008 (AN_SemCorrupt) with addr on input.device stack.
		// Devices may claim huge PosSize (input.device ~$1232); keep a bound.
		if pos > 0x800 {
			pos = 0x800
		}
		lo := ni - neg
		hi := ni + pos
		if lo < 0x100 {
			lo = 0x100
		}
		add(lo, hi)
	}
	walkLibOrDevList := func(listOff int) {
		head := rd32(eb + listOff)
		seen := map[uint32]bool{}
		for n, node := 0, head; n < 64 && node != 0; n++ {
			if int(node) < 0x100 || int(node)+4 >= ChipRAMSize {
				break
			}
			succ := rd32(int(node))
			if succ == 0 {
				break
			}
			if seen[node] {
				break
			}
			seen[node] = true
			protectLibNode(node)
			node = succ
		}
	}
	walkLibOrDevList(libList)
	walkLibOrDevList(deviceList)

	// --- Interrupt / SoftInt servers ---
	// is_Code pointers live in these nodes; smashing them → Illegal Instruction
	// in whatever task was running (often input.device during drag IRQs).
	const (
		intrList     = 0x16C
		interruptLen = 0x16 // Node(14)+is_Data(4)+is_Code(4)
	)
	{
		head := rd32(eb + intrList)
		seen := map[uint32]bool{}
		for n, node := 0, head; n < 64 && node != 0; n++ {
			if int(node) < 0x100 || int(node)+4 >= ChipRAMSize {
				break
			}
			succ := rd32(int(node))
			if succ == 0 {
				break
			}
			if seen[node] {
				break
			}
			seen[node] = true
			add(int(node), int(node)+interruptLen)
			node = succ
		}
	}

	// --- MsgPorts + queued Messages (IDCMP) ---
	// Guru #8408000D / #8400000D = AN_BadMessage: Intuition got a corrupt
	// IntuiMessage. Blitter overruns into heap MsgPorts/messages produce that
	// (second field often a nonsense ptr like $FC081A00 → chip $081A00).
	const (
		portList    = 0x188
		msgPortLen  = 0x22 // Node+flags+SigTask+MsgList header
		mpMsgList   = 0x16 // List lh_Head (Node14 + 4 flag bytes + SigTask4)
		intuiMsgLen = 0x34 // IntuiMessage comfortably
	)
	{
		head := rd32(eb + portList)
		seenPort := map[uint32]bool{}
		for n, port := 0, head; n < 48 && port != 0; n++ {
			if int(port) < 0x100 || int(port)+msgPortLen > ChipRAMSize {
				break
			}
			succ := rd32(int(port))
			if succ == 0 {
				break
			}
			if seenPort[port] {
				break
			}
			seenPort[port] = true
			add(int(port), int(port)+msgPortLen)
			// Walk messages waiting on this port
			msgHead := rd32(int(port) + mpMsgList)
			seenMsg := map[uint32]bool{}
			for m, msg := 0, msgHead; m < 32 && msg != 0; m++ {
				if int(msg) < 0x100 || int(msg)+4 >= ChipRAMSize {
					break
				}
				msucc := rd32(int(msg))
				if msucc == 0 {
					break
				}
				if seenMsg[msg] {
					break
				}
				seenMsg[msg] = true
				hi := int(msg) + intuiMsgLen
				if hi > ChipRAMSize {
					hi = ChipRAMSize
				}
				add(int(msg), hi)
				msg = msucc
			}
			port = succ
		}
	}

	// --- MemList freelist ---
	// Amiga list walk: for (n = lh_Head; n->ln_Succ; n = n->ln_Succ)
	memList := eb + 0x142
	head := rd32(memList)
	seenMH := map[uint32]bool{}
	for n, mh := 0, head; n < 8 && mh != 0; n++ {
		if int(mh) < 0x100 || int(mh)+4 >= ChipRAMSize {
			break
		}
		succ := rd32(int(mh))
		if succ == 0 {
			break // tail sentinel (&lh_Tail)
		}
		if seenMH[mh] {
			break
		}
		seenMH[mh] = true
		if int(mh)+0x20 >= ChipRAMSize {
			break
		}
		add(int(mh)+0x10, int(mh)+0x20)
		first := rd32(int(mh) + 0x10)
		lower := rd32(int(mh) + 0x14)
		upper := rd32(int(mh) + 0x18)
		if upper <= lower || upper > ChipRAMSize {
			mh = succ
			continue
		}
		seenMC := map[uint32]bool{}
		for c, mc := 0, first; mc != 0 && c < 64; c++ {
			if seenMC[mc] {
				break
			}
			seenMC[mc] = true
			mci := int(mc)
			if mci < int(lower) || mci+8 > int(upper) || mci+8 > ChipRAMSize {
				break
			}
			sz := int(rd32(mci + 4))
			if sz < 8 || sz > int(upper)-mci {
				sz = 8
			}
			lo, hi := mci, mci+sz
			// Never deny WB plane pixel storage. Free gaps *inside* the wider
			// display band (e.g. $C960→$D510) are still denied in full so
			// AllocMem does not later hand out blit-poisoned memory.
			const wbPlaneLo, wbPlaneHi = 0xD510, 0x17510
			if lo < wbPlaneHi && hi > wbPlaneLo {
				if lo < wbPlaneLo {
					add(lo, wbPlaneLo)
				}
				if hi > wbPlaneHi {
					add(wbPlaneHi, hi)
				}
				if lo >= wbPlaneLo && lo < wbPlaneHi {
					add(lo, lo+8)
				}
			} else {
				add(lo, hi)
			}
			mc = rd32(mci)
		}
		mh = succ
	}
}

// Execute runs the blitter operation triggered by writing BLTSIZE.
// Data transfer is immediate; BBUSY is deferred so WaitBlit sees a short busy.
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
		b.busy = false
		b.completionCycles = 0
		b.pendingIRQs++
	}

	b.lastSize = size
	// Save pre-blit pointer state (before pointer write-back modifies them)
	b.preApt = b.ptr(b.aptH, b.aptL)
	b.preBpt = b.ptr(b.bptH, b.bptL)
	b.preCpt = b.ptr(b.cptH, b.cptL)
	b.preDpt = b.ptr(b.dptH, b.dptL)
	b.buildDeny()
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

	// Check for line-draw mode (BLTCON1 bit 0)
	if b.con1&0x0001 != 0 {
		b.executeLine(size)
	} else {
		b.executeBlit(size)
	}

	// Soft busy timing: full HRM H×W×ch×2 stalls drag (WaitBlit for 80k+
	// cycles). Cap so WaitBlit returns promptly but still delays a few
	// instructions after BLTSIZE (boot races).
	channels := 0
	if b.con0&0x0800 != 0 {
		channels++
	}
	if b.con0&0x0400 != 0 {
		channels++
	}
	if b.con0&0x0200 != 0 {
		channels++
	}
	if b.con0&0x0100 != 0 {
		channels++
	}
	if channels == 0 {
		channels = 1
	}
	words := height * width
	if b.con1&0x0001 != 0 {
		words = height
	}
	blitCycles := words * channels * 2
	if blitCycles < 8 {
		blitCycles = 8
	}
	const maxBlitBusyCycles = 512
	if blitCycles > maxBlitBusyCycles {
		blitCycles = maxBlitBusyCycles
	}
	b.completionCycles = blitCycles
}

// finish marks one blit complete: BBUSY clear + queue a BLIT INTREQ for
// TickCycles to deliver when idle.
func (b *Blitter) finish() {
	b.busy = false
	b.completionCycles = 0
	b.pendingIRQs++
}

// TickCycles advances the busy timer and delivers deferred BLIT IRQs.
// Called from Amiga.Step() after each CPU instruction (never re-enters the
// instruction that wrote BLTSIZE).
func (b *Blitter) TickCycles(cpuCycles int) {
	if b.busy {
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

// executeBlit performs a normal area blit with optional fill.
func (b *Blitter) executeBlit(size uint16) {
	height := int(size >> 6)
	width := int(size & 0x3F)
	if height == 0 {
		height = 1024
	}
	if width == 0 {
		width = 64
	}

	minterm := byte(b.con0 & 0xFF)
	useA := b.con0&0x0800 != 0
	useB := b.con0&0x0400 != 0
	useC := b.con0&0x0200 != 0
	useD := b.con0&0x0100 != 0

	shiftA := (b.con0 >> 12) & 0xF
	shiftB := (b.con1 >> 12) & 0xF

	// Descending mode (bit 1 of BLTCON1)
	desc := b.con1&0x0002 != 0

	// Fill mode bits in BLTCON1:
	//   Bit 3 (0x0008): IFE — Inclusive Fill Enable
	//   Bit 4 (0x0010): EFE — Exclusive Fill Enable
	// Fill requires descending mode (bit 1) to be set as well.
	inclusiveFill := b.con1&0x0008 != 0
	exclusiveFill := b.con1&0x0010 != 0
	fillEnable := inclusiveFill || exclusiveFill

	apt := b.ptr(b.aptH, b.aptL)
	bpt := b.ptr(b.bptH, b.bptL)
	cpt := b.ptr(b.cptH, b.cptL)
	dpt := b.ptr(b.dptH, b.dptL)

	if b.logger != nil {
		fmt.Fprintf(b.logger, "BLITTER: %dx%d con0=$%04X con1=$%04X min=$%02X A=$%08X B=$%08X C=$%08X D=$%08X\n",
			width, height, b.con0, b.con1, minterm, apt, bpt, cpt, dpt)
	}

	var prevA uint16 // previous A word for shifting
	var prevB uint16 // previous B word for shifting
	buf := make([]byte, 2)

	for y := 0; y < height; y++ {
		prevA = 0
		prevB = 0
		var fillCarry uint16 // fill carry bit per line

		for x := 0; x < width; x++ {
			// When a channel is not enabled (USEx=0), the minterm still uses
			// the channel's data register value (xDAT) rather than zero.
			aWord := b.adat
			bWord := b.bdat
			cWord := b.cdat

			// Fetch source words
			if useA {
				b.amiga.ChipRAM.Read(int(apt&0x7FFFE), buf)
				aWord = uint16(buf[0])<<8 | uint16(buf[1])
				b.adat = aWord

				// Apply first/last word masks before shift (HRM order).
				if x == 0 {
					aWord &= b.afwm
				}
				if x == width-1 {
					aWord &= b.alwm
				}

				// Barrel shift A
				if shiftA != 0 {
					var combined uint32
					if desc {
						// Descending mode: shift direction reverses (left shift).
						// prevA holds the word from the higher address (processed earlier).
						combined = uint32(aWord)<<16 | uint32(prevA)
						aWord = uint16(combined >> (16 - shiftA))
					} else {
						combined = uint32(prevA)<<16 | uint32(aWord)
						aWord = uint16(combined >> shiftA)
					}
				}
				prevA = b.adat

				if desc {
					apt -= 2
				} else {
					apt += 2
				}
			} else {
				// USEA=0: A is the constant in BLTADAT, but AFWM/ALWM still
				// apply. graphics.library Draw uses this for pure-horizontal
				// lines (1-row area blit, ADAT=$FFFF): without the masks,
				// whole words light up and lines overrun past the true
				// endpoints to 16-pixel boundaries.
				if x == 0 {
					aWord &= b.afwm
				}
				if x == width-1 {
					aWord &= b.alwm
				}
				if shiftA != 0 {
					var combined uint32
					if desc {
						combined = uint32(aWord)<<16 | uint32(prevA)
						aWord = uint16(combined >> (16 - shiftA))
					} else {
						combined = uint32(prevA)<<16 | uint32(aWord)
						aWord = uint16(combined >> shiftA)
					}
					prevA = b.adat
				}
			}

			if useB {
				b.amiga.ChipRAM.Read(int(bpt&0x7FFFE), buf)
				bWord = uint16(buf[0])<<8 | uint16(buf[1])
				b.bdat = bWord

				// Barrel shift B
				if shiftB != 0 {
					var combined uint32
					if desc {
						combined = uint32(bWord)<<16 | uint32(prevB)
						bWord = uint16(combined >> (16 - shiftB))
					} else {
						combined = uint32(prevB)<<16 | uint32(bWord)
						bWord = uint16(combined >> shiftB)
					}
				}
				prevB = b.bdat

				if desc {
					bpt -= 2
				} else {
					bpt += 2
				}
			}

			if useC {
				b.amiga.ChipRAM.Read(int(cpt&0x7FFFE), buf)
				cWord = uint16(buf[0])<<8 | uint16(buf[1])
				b.cdat = cWord
				if desc {
					cpt -= 2
				} else {
					cpt += 2
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
						fillCarry ^= 1
					}
					if exclusiveFill {
						// Exclusive fill: output the XOR of original bit and carry
						filled |= (srcBit ^ fillCarry) << bit
					} else {
						// Inclusive fill: output carry (carry already toggled by srcBit)
						filled |= fillCarry << bit
					}
				}
				dWord = filled
			}

			// Write result
			if useD {
				buf[0] = byte(dWord >> 8)
				buf[1] = byte(dWord)
				writeAddr := int(dpt & 0x7FFFE)
				if b.canWriteD(writeAddr, b.preDpt) {
					b.amiga.ChipRAM.Write(writeAddr, buf)
				}
				if b.DebugWriteHook != nil {
					b.DebugWriteHook(writeAddr, dWord, aWord, bWord, cWord, minterm)
				}
				if desc {
					dpt -= 2
				} else {
					dpt += 2
				}
			}
		}

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
		if useA {
			if desc {
				apt = uint32(int32(apt) - int32(b.amod))
			} else {
				apt = uint32(int32(apt) + int32(b.amod))
			}
		}
		if useB {
			if desc {
				bpt = uint32(int32(bpt) - int32(b.bmod))
			} else {
				bpt = uint32(int32(bpt) + int32(b.bmod))
			}
		}
		if useC {
			if desc {
				cpt = uint32(int32(cpt) - int32(b.cmod))
			} else {
				cpt = uint32(int32(cpt) + int32(b.cmod))
			}
		}
		if useD {
			if desc {
				dpt = uint32(int32(dpt) - int32(b.dmod))
			} else {
				dpt = uint32(int32(dpt) + int32(b.dmod))
			}
		}
	}

	// Write back updated pointer registers. On real hardware, the blitter
	// updates APT/BPT/CPT/DPT as it processes each word. After completion,
	// the registers reflect the final position. ROM code (e.g., QBlit handler,
	// multi-pass BltBitMap) may read these to determine blit state.
	b.aptH = uint16(apt >> 16)
	b.aptL = uint16(apt)
	b.bptH = uint16(bpt >> 16)
	b.bptL = uint16(bpt)
	b.cptH = uint16(cpt >> 16)
	b.cptL = uint16(cpt)
	b.dptH = uint16(dpt >> 16)
	b.dptL = uint16(dpt)
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
		b.amiga.ChipRAM.Read(int(cpt&0x7FFFE), buf)
		cWord := uint16(buf[0])<<8 | uint16(buf[1])

		dWord := applyMinterm(minterm, aWord, bWord, cWord)

		// SING: plot at most one pixel per horizontal raster line
		writePixel := !singleMode || !onedot
		if writePixel {
			onedot = true
			buf[0] = byte(dWord >> 8)
			buf[1] = byte(dWord)
			// Write through DPT, then D tracks C (hardware behaviour)
			dpt = cpt
			writeAddr := int(dpt & 0x7FFFE)
			// Line starts use preCpt/preDpt (same address when Draw programs both).
			if b.canWriteD(writeAddr, b.preDpt) {
				b.amiga.ChipRAM.Write(writeAddr, buf)
			}
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
