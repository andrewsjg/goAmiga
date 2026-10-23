package amiga

import (
	"encoding/binary"
	"fmt"
	"math"
)

// AHI: goamiga.audio, a driver for AHI (the Amiga's retargetable audio
// system) that plays on the host. AHI does the mixing on the Amiga; the
// driver runs AHI's mixer and hands what it mixes to the host's sound
// output, alongside Paula's, after Paula's filters.
//
// The driver is a small library (AHIDriver generates it, for
// DEVS:AHI/goamiga.audio) whose functions jump to ahiEntry in the hard
// disk board's ROM, which traps to Go. As with the clipboard helper, Go
// answers or drives a sequence of system calls through the board's loop
// (trap, jsr (a5), trap), in the caller's own context: Start allocates
// the mixing buffer and creates the mixing process, Stop ends it.
//
// The mixing process runs the board's other loop. A vertical blank server
// wakes it when the player is due (ahiac_PlayerFreq times a second, in
// emulated time); each time it calls AHI's PlayerFunc hook, then its
// MixerFunc hook, which fills the buffer with ahiac_BuffSamples frames.
// Go converts them (16 or 32 bits, mono or stereo), resamples them to the
// host's rate and queues them for Paula's output.

// Traps.
const (
	hdTrapAHICall = iota + 120
	hdTrapAHIRet
	hdTrapAHISlave
	hdTrapAHISlaveRet
	hdTrapAHIVBL
)

// Board RAM for the drivers' interrupt servers (one per AudioCtrl).
const (
	ahiNodes    = hdRAM + 0x6800
	ahiNodeSize = 32
	ahiMaxCtrls = 8
)

// AHIsub functions, by LVO order.
const (
	ahiAllocAudio = iota
	ahiFreeAudio
	ahiDisable
	ahiEnable
	ahiStart
	ahiUpdate
	ahiStop
	ahiSetVol
	ahiSetFreq
	ahiSetSound
	ahiSetEffect
	ahiLoadSound
	ahiUnloadSound
	ahiGetAttr
	ahiHardwareControl
	ahiFuncs
)

// AHIAudioCtrlDrv fields.
const (
	acPlayerFunc  = 12
	acPlayerFreq  = 16
	acMixFreq     = 28
	acMixerFunc   = 40
	acBuffSamples = 52
	acBuffSize    = 64
	acBuffType    = 68
)

// AHI's tags and values.
const (
	ahiTag         = 0x80000000
	ahidbAudioID   = ahiTag + 100
	ahidbVolume    = ahiTag + 103
	ahidbPanning   = ahiTag + 104
	ahidbStereo    = ahiTag + 105
	ahidbHiFi      = ahiTag + 106
	ahidbMultTable = ahiTag + 108
	ahidbName      = ahiTag | 0x8000 + 109
	ahidbBits      = ahiTag + 110
	ahidbRecord    = ahiTag + 114
	ahidbFreqs     = ahiTag + 115
	ahidbFreq      = ahiTag + 117
	ahidbAuthor    = ahiTag + 118
	ahidbCopyright = ahiTag + 119
	ahidbVersion   = ahiTag + 120
	ahidbAnnot     = ahiTag + 121
	ahidbIndex     = ahiTag + 124
	ahidbRealtime  = ahiTag + 125
	ahidbFullDup   = ahiTag + 129
	ahidbInputs    = ahiTag + 136
	ahidbOutputs   = ahiTag + 139
	ahidbOutput    = ahiTag + 141

	ahistM16S = 1
	ahistS16S = 3
	ahistM32S = 8
	ahistS32S = 10

	ahieOK      = 0
	ahieNoMem   = 1
	ahieUnknown = 5

	ahisUnknown = 0xFFFFFFFF

	sigCtrlC = 1 << 12
)

// More exec vectors.
const (
	lvoForbid  = -132
	lvoPermit  = -138
	lvoFreeMem = -210 // and AllocSignal, FreeSignal in netboard.go
)

// ahiFreqs are the mixing frequencies offered.
var ahiFreqs = []uint32{8000, 11025, 16000, 22050, 32000, 44100, 48000}

// ahiCtrl is an AudioCtrl the driver has been given.
type ahiCtrl struct {
	slot       int    // its interrupt server's slot
	running    bool   // the mixing process is up
	mixBuf     uint32 // the mixing buffer, and its size
	bufSize    uint32
	masterTask uint32 // the task that started it, and its signal
	masterSig  int
	slaveTask  uint32 // the mixing process, and its signal
	slaveSig   int
	slaveStep  int
	pending    int     // player calls due
	due        float64 // player calls due, with the fraction
	lastVBL    uint64  // Amiga.cycles at the last vertical blank
}

// ahiCall is a driver function in progress in a task.
type ahiCall struct {
	fn, step int
	ac       uint32
	d        [5]uint32 // the arguments
	result   uint32
}

// ahiState is the driver's state on the board.
type ahiState struct {
	dosBase uint32
	ctrls   map[uint32]*ahiCtrl // by AudioCtrl
	calls   map[uint32]*ahiCall // by task
	slaves  map[uint32]uint32   // mixing process -> AudioCtrl
	pending uint32              // the AudioCtrl whose process is starting
}

// ahiStrings are the GetAttr strings, in the board's ROM.
var ahiStrings = []struct{ label, text string }{
	{"ahiAuthor", "goAmiga"},
	{"ahiCopyright", "Free (part of goAmiga)"},
	{"ahiVersion", "goamiga.audio 6.0 (23.10.2026)"},
	{"ahiAnnotation", "Plays on the computer goAmiga runs on"},
	{"ahiOutput", "Host"},
	{"ahiSlaveName", "goAmiga AHI"},
}

// buildAHIROM adds the driver's board code: the entry its library jumps
// to, the mixing process's segment, the vertical blank server, and the
// strings.
func buildAHIROM(s *asm68k, base uint32) {
	const bra, beq = 0x6000, 0x6700
	// ahiEntry: (sp) = function number, 4(sp) = the caller's return.
	s.align(2)
	s.label("ahiEntry")
	s.w(0x48E7, 0x7FFE) // movem.l d1-d7/a0-a6,-(sp)
	s.trap(base, hdTrapAHICall)
	s.label("ahiLoop")
	s.w(0x4A87) // tst.l d7
	s.branch(beq, "ahiDone")
	s.w(0x4E95) // jsr (a5)
	s.trap(base, hdTrapAHIRet)
	s.branch(bra, "ahiLoop")
	s.label("ahiDone")
	s.w(0x4CDF, 0x7FFE) // movem.l (sp)+,d1-d7/a0-a6
	s.w(0x588F)         // addq.l #4,sp
	s.rts()

	// The vertical blank server (a1 = is_Data, the AudioCtrl).
	s.label("ahiVBL")
	s.trap(base, hdTrapAHIVBL) // d0 = signal for the process in a1, or 0
	s.tstD0()
	s.branch(beq, "ahiVBLDone")
	s.execBase()
	s.jsrLVO(lvoSignal)
	s.label("ahiVBLDone")
	s.moveq(0)
	s.rts()

	for _, str := range ahiStrings {
		s.str(str.label, str.text)
	}

	// The mixing process: a segment.
	s.align(4)
	s.l(16)
	s.label("ahiSeg")
	s.l(0)
	s.label("ahiSlave")
	s.trap(base, hdTrapAHISlave) // d7 = 0: end
	s.w(0x4A87)
	s.branch(beq, "ahiSlaveEnd")
	s.w(0x4E95) // jsr (a5)
	s.trap(base, hdTrapAHISlaveRet)
	s.branch(bra, "ahiSlave")
	s.label("ahiSlaveEnd")
	s.moveq(0)
	s.rts()
}

func (h *hdBoard) ahiReset() {
	h.ahi = ahiState{ctrls: map[uint32]*ahiCtrl{}, calls: map[uint32]*ahiCall{}, slaves: map[uint32]uint32{}}
	if p := h.amiga.Paula; p != nil {
		p.ahi.reset()
	}
}

func (h *hdBoard) thisTask() uint32 { a := h.amiga; return a.peek32(a.peek32(4) + 0x114) }

// ahiSub sets up a system call for the board's loop (d7 = 1: call a5).
func (h *hdBoard) ahiSub(lib uint32, lvo int) {
	h.clipCall(lib, lvo)
	h.amiga.CPU.D[7] = 1
}

// ahiDone ends the driver function with its result.
func (h *hdBoard) ahiDone(c *ahiCall, task uint32) {
	cpu := h.amiga.CPU
	cpu.D[7] = 0
	cpu.D[0] = c.result
	delete(h.ahi.calls, task)
}

// ahiCallTrap starts a driver function: the arguments are as AHI passed
// them, the function number above the saved registers.
func (h *hdBoard) ahiCallTrap() {
	a := h.amiga
	cpu := a.CPU
	task := h.thisTask()
	c := &ahiCall{fn: int(a.peek32(cpu.A[7] + 56)), ac: cpu.A[2]}
	c.d = [5]uint32{cpu.D[0], cpu.D[1], cpu.D[2], cpu.D[3], cpu.D[4]}
	h.ahi.calls[task] = c
	h.ahiStep(c, task)
}

// ahiRetTrap continues a driver function after a system call.
func (h *hdBoard) ahiRetTrap() {
	task := h.thisTask()
	c := h.ahi.calls[task]
	if c == nil {
		h.amiga.CPU.D[7] = 0
		return
	}
	h.ahiStep(c, task)
}

// ahiStep runs a driver function: each step either sets up a system call
// (and returns, to be called again with its result in D0) or finishes.
func (h *hdBoard) ahiStep(c *ahiCall, task uint32) {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	st := &h.ahi
	ctrl := st.ctrls[c.ac]
	d0 := cpu.D[0]
	defer func() { c.step++ }()
	switch c.fn {
	case ahiAllocAudio:
		if len(st.ctrls) >= ahiMaxCtrls {
			c.result = 1 // AHISF_ERROR
			h.ahiDone(c, task)
			return
		}
		used := map[int]bool{}
		for _, x := range st.ctrls {
			used[x.slot] = true
		}
		slot := 0
		for used[slot] {
			slot++
		}
		st.ctrls[c.ac] = &ahiCtrl{slot: slot, masterSig: -1, slaveSig: -1}
		c.result = 1<<1 | 1<<2 | 1<<3 | 1<<4 // MIXING, TIMING, KNOWSTEREO, KNOWHIFI
		h.tracef("AHI: AllocAudio $%06X", c.ac)
		h.ahiDone(c, task)

	case ahiFreeAudio:
		if ctrl != nil && ctrl.running && h.ahiStopStep(c, ctrl, d0) {
			return
		}
		delete(st.ctrls, c.ac)
		h.ahiDone(c, task)

	case ahiDisable:
		if c.step == 0 {
			h.ahiSub(eb, lvoForbid)
			return
		}
		h.ahiDone(c, task)
	case ahiEnable:
		if c.step == 0 {
			h.ahiSub(eb, lvoPermit)
			return
		}
		h.ahiDone(c, task)

	case ahiStart:
		c.result = ahieOK
		if ctrl == nil || c.d[0]&1 == 0 || ctrl.running && c.step == 0 {
			if c.d[0]&2 != 0 {
				c.result = ahieUnknown // no recording
			}
			h.ahiDone(c, task)
			return
		}
		if !h.ahiStartStep(c, ctrl, d0) {
			h.ahiDone(c, task)
		}

	case ahiStop:
		if ctrl != nil && c.d[0]&1 != 0 && ctrl.running && h.ahiStopStep(c, ctrl, d0) {
			return
		}
		h.ahiDone(c, task)

	case ahiUpdate, ahiHardwareControl:
		c.result = 0
		h.ahiDone(c, task)

	case ahiGetAttr:
		c.result = h.ahiGetAttr(c.d[0], c.d[1], c.d[2])
		h.ahiDone(c, task)

	default: // Set*, LoadSound, UnloadSound: AHI's mixer does them
		c.result = ahisUnknown
		h.ahiDone(c, task)
	}
}

// ahiStartStep is Start: the mixing buffer, dos.library, a signal for
// the process to answer on, the process, then the interrupt server. It
// reports whether a system call is set up.
func (h *hdBoard) ahiStartStep(c *ahiCall, ctrl *ahiCtrl, d0 uint32) bool {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	st := &h.ahi
	switch c.step {
	case 0:
		ctrl.bufSize = a.peek32(c.ac + acBuffSize)
		h.ahiSub(eb, lvoAllocMem)
		cpu.D[0] = ctrl.bufSize
		cpu.D[1] = 1 | memfClear // MEMF_PUBLIC
	case 1:
		ctrl.mixBuf = d0
		if d0 == 0 {
			c.result = ahieNoMem
			return false
		}
		if st.dosBase != 0 {
			c.step++ // skip opening dos.library
			return h.ahiStartStep(c, ctrl, st.dosBase)
		}
		h.ahiSub(eb, lvoOpenLibrary)
		cpu.A[1] = h.dosNameAddr
		cpu.D[0] = 36
	case 2:
		st.dosBase = d0
		h.ahiSub(eb, lvoAllocSignal)
		cpu.D[0] = 0xFFFFFFFF
	case 3:
		ctrl.masterSig = int(int32(d0))
		ctrl.masterTask = h.thisTask()
		st.pending = c.ac
		h.ahiSub(st.dosBase, lvoCreateProc)
		cpu.D[1] = h.ahiSlaveName
		cpu.D[2] = 10 // above the program, as sound should be
		cpu.D[3] = h.ahiSeg >> 2
		cpu.D[4] = 4096
	case 4:
		if d0 == 0 {
			c.result = ahieNoMem
			return false
		}
		h.ahiSub(eb, lvoWait) // until the process is up
		cpu.D[0] = 1 << ctrl.masterSig
	case 5:
		n := h.base + ahiNodes + uint32(ctrl.slot*ahiNodeSize)
		for i := uint32(0); i < 22; i++ {
			a.poke8(n+i, 0)
		}
		a.poke8(n+8, 2) // NT_INTERRUPT
		a.poke32(n+10, h.ahiSlaveName)
		a.poke32(n+14, c.ac) // is_Data
		a.poke32(n+18, h.ahiVBLCode)
		ctrl.running, ctrl.due, ctrl.pending, ctrl.lastVBL = true, 0, 0, a.cycles
		h.ahiSub(eb, lvoAddIntServer)
		cpu.D[0] = 5
		cpu.A[1] = n
		h.tracef("AHI: Start, %d Hz, %d-sample buffers of type %d", a.peek32(c.ac+acMixFreq),
			a.peek32(c.ac+acBuffSamples), a.peek32(c.ac+acBuffType))
	default:
		return false
	}
	return true
}

// ahiStopStep is Stop: the interrupt server off, the process told to
// end, its answer, then the signal and buffer freed. It reports whether a
// system call is set up.
func (h *hdBoard) ahiStopStep(c *ahiCall, ctrl *ahiCtrl, d0 uint32) bool {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	switch c.step {
	case 0:
		h.ahiSub(eb, lvoRemIntServer)
		cpu.D[0] = 5
		cpu.A[1] = h.base + ahiNodes + uint32(ctrl.slot*ahiNodeSize)
	case 1:
		h.ahiSub(eb, lvoSignal)
		cpu.A[1] = ctrl.slaveTask
		cpu.D[0] = sigCtrlC
	case 2:
		h.ahiSub(eb, lvoWait)
		cpu.D[0] = 1 << ctrl.masterSig
	case 3:
		h.ahiSub(eb, lvoFreeSignal)
		cpu.D[0] = uint32(ctrl.masterSig)
	case 4:
		h.ahiSub(eb, lvoFreeMem)
		cpu.A[1] = ctrl.mixBuf
		cpu.D[0] = ctrl.bufSize
	default:
		ctrl.running = false
		delete(h.ahi.slaves, ctrl.slaveTask)
		ctrl.slaveTask = 0
		h.tracef("AHI: Stop")
		return false
	}
	return true
}

// ahiGetAttr answers GetAttr.
func (h *hdBoard) ahiGetAttr(attr, arg, def uint32) uint32 {
	switch attr {
	case ahidbBits:
		return 16
	case ahidbFreqs:
		return uint32(len(ahiFreqs))
	case ahidbFreq:
		if int(arg) < len(ahiFreqs) {
			return ahiFreqs[arg]
		}
		return ahiFreqs[len(ahiFreqs)-1]
	case ahidbIndex:
		best := 0
		for i, f := range ahiFreqs {
			if absDiff(f, arg) < absDiff(ahiFreqs[best], arg) {
				best = i
			}
		}
		return uint32(best)
	case ahidbAuthor:
		return h.ahiStr["ahiAuthor"]
	case ahidbCopyright:
		return h.ahiStr["ahiCopyright"]
	case ahidbVersion:
		return h.ahiStr["ahiVersion"]
	case ahidbAnnot:
		return h.ahiStr["ahiAnnotation"]
	case ahidbRecord, ahidbFullDup, ahidbInputs:
		return 0
	case ahidbRealtime:
		return 1
	case ahidbOutputs:
		return 1
	case ahidbOutput:
		return h.ahiStr["ahiOutput"]
	}
	return def
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// ahiVBL wakes a mixing process when its player is due: a1 is the
// AudioCtrl; d0 = its signal, a1 = the process, or d0 = 0.
func (h *hdBoard) ahiVBL() {
	a := h.amiga
	cpu := a.CPU
	cpu.D[0] = 0
	ctrl := h.ahi.ctrls[cpu.A[1]]
	if ctrl == nil || !ctrl.running || ctrl.slaveTask == 0 || ctrl.slaveSig < 0 {
		return
	}
	freq := float64(a.peek32(cpu.A[1]+acPlayerFreq)) / 65536
	if freq <= 0 {
		freq = 50
	}
	cyclesPerSec := float64(CyclesPerFrame) * a.FrameRate()
	ctrl.due += float64(a.cycles-ctrl.lastVBL) * freq / cyclesPerSec
	ctrl.lastVBL = a.cycles
	if n := int(ctrl.due); n > 0 {
		ctrl.due -= float64(n)
		ctrl.pending = min(ctrl.pending+n, 16) // never more than a few behind
		cpu.D[0] = 1 << ctrl.slaveSig
		cpu.A[1] = ctrl.slaveTask
	}
}

// The mixing process's steps.
const (
	slaveAlloc = iota
	slaveHello
	slaveWait
	slavePlayer
	slaveMixer
	slaveForbid
	slaveBye
	slaveEnd
)

// ahiSlaveTrap sets up the mixing process's next call (d7 = 0: end).
func (h *hdBoard) ahiSlaveTrap() {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	task := h.thisTask()
	st := &h.ahi
	ac, ok := st.slaves[task]
	if !ok { // a new process: Start's
		ac = st.pending
		st.slaves[task] = ac
		if ctrl := st.ctrls[ac]; ctrl != nil {
			ctrl.slaveTask, ctrl.slaveStep = task, slaveAlloc
		}
	}
	ctrl := st.ctrls[ac]
	if ctrl == nil {
		cpu.D[7] = 0
		return
	}
	cpu.D[7] = 1
	hook := func(field uint32, msg uint32) {
		hk := a.peek32(ac + field)
		cpu.A[0], cpu.A[1], cpu.A[2] = hk, msg, ac
		cpu.A[5] = a.peek32(hk + 8) // h_Entry
	}
	switch ctrl.slaveStep {
	case slaveAlloc:
		h.clipCall(eb, lvoAllocSignal)
		cpu.D[0] = 0xFFFFFFFF
	case slaveHello:
		h.clipCall(eb, lvoSignal)
		cpu.A[1] = ctrl.masterTask
		cpu.D[0] = 1 << ctrl.masterSig
	case slaveWait:
		if ctrl.pending > 0 {
			ctrl.slaveStep = slavePlayer
			h.ahiSlaveTrap()
			return
		}
		h.clipCall(eb, lvoWait)
		cpu.D[0] = 1<<ctrl.slaveSig | sigCtrlC
	case slavePlayer:
		hook(acPlayerFunc, 0)
	case slaveMixer:
		hook(acMixerFunc, ctrl.mixBuf)
	case slaveForbid:
		h.clipCall(eb, lvoForbid)
	case slaveBye:
		h.clipCall(eb, lvoSignal)
		cpu.A[1] = ctrl.masterTask
		cpu.D[0] = 1 << ctrl.masterSig
	default:
		cpu.D[7] = 0 // the process ends (with Forbid on, until it has)
	}
}

// ahiSlaveRetTrap takes the result of the mixing process's call.
func (h *hdBoard) ahiSlaveRetTrap() {
	a := h.amiga
	d0 := a.CPU.D[0]
	ctrl := h.ahi.ctrls[h.ahi.slaves[h.thisTask()]]
	if ctrl == nil {
		return
	}
	switch ctrl.slaveStep {
	case slaveAlloc:
		ctrl.slaveSig = int(int32(d0))
		ctrl.slaveStep = slaveHello
	case slaveHello:
		ctrl.slaveStep = slaveWait
	case slaveWait:
		if d0&sigCtrlC != 0 {
			ctrl.slaveStep = slaveForbid
		}
	case slavePlayer:
		ctrl.slaveStep = slaveMixer
	case slaveMixer:
		h.ahiTakeMix(h.ahi.slaves[h.thisTask()], ctrl)
		ctrl.pending--
		ctrl.slaveStep = slaveWait
	case slaveForbid:
		ctrl.slaveStep = slaveBye
	case slaveBye:
		ctrl.slaveStep = slaveEnd
	}
}

// ahiTakeMix queues what the mixer put in the buffer for the host.
func (h *hdBoard) ahiTakeMix(ac uint32, ctrl *ahiCtrl) {
	a := h.amiga
	if a.Paula == nil {
		return
	}
	frames := int(a.peek32(ac + acBuffSamples))
	typ := a.peek32(ac + acBuffType)
	rate := float64(a.peek32(ac + acMixFreq))
	var size, chans int
	switch typ {
	case ahistM16S:
		size, chans = 2, 1
	case ahistS16S:
		size, chans = 2, 2
	case ahistM32S:
		size, chans = 4, 1
	case ahistS32S:
		size, chans = 4, 2
	default:
		return
	}
	n := frames * size * chans
	if frames <= 0 || n > int(ctrl.bufSize) || rate <= 0 {
		return
	}
	raw := make([]byte, n)
	a.peekBytes(ctrl.mixBuf, raw)
	out := make([][2]float32, frames)
	for i := range out {
		for ch := 0; ch < 2; ch++ {
			k := min(ch, chans-1)
			o := (i*chans + k) * size
			var v float32
			if size == 2 {
				v = float32(int16(binary.BigEndian.Uint16(raw[o:]))) / 32768
			} else {
				v = float32(int32(binary.BigEndian.Uint32(raw[o:]))) / 2147483648
			}
			out[i][ch] = v
		}
	}
	a.Paula.ahi.push(out, rate)
}

// ---- The host side: a queue Paula's output adds in ----

// ahiOut holds mixed AHI sound at the host's rate until Paula's output
// takes it, a sample at a time, as emulated time passes. It starts once
// two mixer buffers' worth are queued, so the bursts the mixer delivers
// at each player call come out evenly.
type ahiOut struct {
	buf        [][2]float32
	start      int
	primed     bool
	prime      int        // how much to queue before starting
	last       [2]float32 // the previous source frame, for interpolation
	pos        float64    // resampling position in the next source buffer
	underflows int
	pushed     int // host samples queued in all
}

func (o *ahiOut) reset() { *o = ahiOut{} }

// push resamples frames at rate to the host's rate and queues them.
func (o *ahiOut) push(frames [][2]float32, rate float64) {
	step := rate / AudioSampleRate
	before := len(o.buf) - o.start
	for ; o.pos < float64(len(frames)); o.pos += step {
		i := int(o.pos)
		f := float32(o.pos - float64(i))
		prev := o.last
		if i > 0 {
			prev = frames[i-1]
		}
		cur := frames[i]
		// The position is between the frame before i and frame i.
		o.buf = append(o.buf, [2]float32{prev[0] + (cur[0]-prev[0])*f, prev[1] + (cur[1]-prev[1])*f})
		o.pushed++
	}
	o.pos -= float64(len(frames))
	if len(frames) > 0 {
		o.last = frames[len(frames)-1]
	}
	o.prime = 2 * (len(o.buf) - o.start - before)
	// Compact now and then; and never hold more than a second.
	if o.start > 4096 {
		o.buf = append(o.buf[:0], o.buf[o.start:]...)
		o.start = 0
	}
	if extra := len(o.buf) - o.start - AudioSampleRate; extra > 0 {
		o.start += extra
	}
}

// next is the next host sample (silence when there is none).
func (o *ahiOut) next() (l, r float64) {
	queued := len(o.buf) - o.start
	if !o.primed {
		if queued == 0 || queued < o.prime {
			return 0, 0
		}
		o.primed = true
	}
	if queued == 0 {
		o.primed = false
		o.underflows++
		return 0, 0
	}
	f := o.buf[o.start]
	o.start++
	return float64(f[0]), float64(f[1])
}

// AHIDriver is the driver library, DEVS:AHI/goamiga.audio: an Amiga
// executable whose functions jump to the board's ahiEntry (found with
// FindConfigDev), the function number on the stack.
func AHIDriver() []byte {
	entry := ahiEntryOffset()
	s := &asm68k{org: 0, labels: map[string]uint32{}}
	const bra, beq = 0x6000, 0x6700
	s.moveq(-1) // not a program
	s.rts()

	s.label("romtag")
	s.w(0x4AFC)
	s.addr("romtag")
	s.addr("end")
	s.code = append(s.code, 0x80, 6, 9, 0) // RTF_AUTOINIT, version 6, NT_LIBRARY, pri 0
	s.addr("name")
	s.addr("id")
	s.addr("initTable")
	s.str("name", "goamiga.audio")
	s.str("id", "goamiga.audio 6.0 (23.10.2026)\r\n")
	s.str("expName", "expansion.library")
	s.align(4)
	s.label("initTable")
	s.l(42) // library size: the base, the segment list, the board
	s.addr("funcTable")
	s.l(0)
	s.addr("init")
	s.label("funcTable")
	for _, f := range []string{"open", "close", "expunge", "null"} {
		s.addr(f)
	}
	for i := 0; i < ahiFuncs; i++ {
		s.addr(fmt.Sprintf("fn%d", i))
	}
	s.l(0xFFFFFFFF)

	// init (d0 = library, a0 = segment list, a6 = ExecBase): find the
	// board, or fail.
	s.label("init")
	s.w(0x48E7, 0x2020) // movem.l d2/a2,-(sp)
	s.w(0x2440)         // movea.l d0,a2
	s.w(0x2548, 0x0022) // move.l a0,34(a2)
	s.w(0x43F9)         // lea expName,a1
	s.addr("expName")
	s.moveq(0)
	s.jsrLVO(lvoOpenLibrary)
	s.tstD0()
	s.branch(beq, "initFail")
	s.w(0x2C40) // movea.l d0,a6
	s.w(0x91C8) // suba.l a0,a0
	s.w(0x203C) // move.l #2011,d0
	s.l(2011)
	s.w(0x7202)   // moveq #2,d1
	s.jsrLVO(-72) // FindConfigDev
	s.w(0x2400)   // move.l d0,d2
	s.w(0x224E)   // movea.l a6,a1
	s.execBase()
	s.jsrLVO(lvoCloseLibrary)
	s.w(0x4A82) // tst.l d2
	s.branch(beq, "initFail")
	s.w(0x2042)                 // movea.l d2,a0
	s.w(0x2568, 0x0020, 0x0026) // move.l 32(a0),38(a2): cd_BoardAddr
	s.w(0x200A)                 // move.l a2,d0
	s.branch(bra, "initDone")
	s.label("initFail")
	s.moveq(0)
	s.label("initDone")
	s.w(0x4CDF, 0x0404) // movem.l (sp)+,d2/a2
	s.rts()

	s.label("open")
	s.w(0x526E, 0x0020)         // addq.w #1,32(a6)
	s.w(0x08AE, 0x0003, 0x000E) // bclr #3,14(a6): LIBF_DELEXP
	s.w(0x200E)                 // move.l a6,d0
	s.rts()
	s.label("close")
	s.w(0x536E, 0x0020) // subq.w #1,32(a6)
	s.moveq(0)
	s.rts()
	s.label("expunge")
	s.label("null")
	s.moveq(0)
	s.rts()

	// Each function puts its number on the stack and comes here, to go on
	// to the board's ahiEntry.
	s.label("common")
	s.w(0x2F2E, 0x0026) // move.l 38(a6),-(sp)
	s.w(0x0697)         // addi.l #entry,(sp)
	s.l(entry)
	s.rts()
	for i := 0; i < ahiFuncs; i++ {
		s.label(fmt.Sprintf("fn%d", i))
		s.w(0x2F3C) // move.l #i,-(sp)
		s.l(uint32(i))
		s.w(0x4EF9) // jmp common
		s.addr("common")
	}
	s.align(4)
	s.label("end")
	s.resolve()

	var relocs []uint32
	for _, f := range s.fixups {
		if f.kind == 'L' {
			relocs = append(relocs, uint32(f.pos))
		}
	}
	return hunkFile(s.code, relocs)
}

// ahiEntryOffset is where ahiEntry sits in the board.
func ahiEntryOffset() uint32 {
	h := &hdBoard{}
	h.buildROM(0)
	return h.ahiEntry
}

// hunkFile makes an AmigaDOS executable of one code hunk with 32-bit
// relocations.
func hunkFile(code []byte, relocs []uint32) []byte {
	be := binary.BigEndian
	for len(code)%4 != 0 {
		code = append(code, 0)
	}
	var b []byte
	for _, v := range []uint32{0x3F3, 0, 1, 0, 0, uint32(len(code) / 4), 0x3E9, uint32(len(code) / 4)} {
		b = be.AppendUint32(b, v)
	}
	b = append(b, code...)
	b = be.AppendUint32(b, 0x3EC)
	b = be.AppendUint32(b, uint32(len(relocs)))
	b = be.AppendUint32(b, 0)
	for _, r := range relocs {
		b = be.AppendUint32(b, r)
	}
	b = be.AppendUint32(b, 0)
	b = be.AppendUint32(b, 0x3F2)
	return b
}

// AHIModes is the audio mode file, DEVS:AudioModes/GOAMIGA: IFF AHIM
// naming the driver and its modes.
func AHIModes() []byte {
	be := binary.BigEndian
	chunk := func(id string, data []byte) []byte {
		out := append([]byte(id), be.AppendUint32(nil, uint32(len(data)))...)
		out = append(out, data...)
		if len(data)%2 != 0 {
			out = append(out, 0)
		}
		return out
	}
	body := []byte("AHIM")
	body = append(body, chunk("AUDN", []byte("goamiga\x00"))...)
	for i, m := range []struct {
		name         string
		stereo, hifi bool
	}{
		{"GOAMIGA:16 bit stereo++", true, false},
		{"GOAMIGA:HiFi 32 bit stereo++", true, true},
		{"GOAMIGA:16 bit mono", false, false},
		{"GOAMIGA:HiFi 32 bit mono", false, true},
	} {
		b2u := func(v bool) uint32 {
			if v {
				return 1
			}
			return 0
		}
		tags := [][2]uint32{
			{ahidbAudioID, 0x00720001 + uint32(i)},
			{ahidbVolume, 1}, {ahidbPanning, 1},
			{ahidbStereo, b2u(m.stereo)}, {ahidbHiFi, b2u(m.hifi)},
			{ahidbMultTable, 0},
			{ahidbName, 0}, // the name's offset in the chunk, below
		}
		var d []byte
		nameOff := uint32(len(tags)*8 + 4)
		for _, tg := range tags {
			if tg[0] == ahidbName {
				tg[1] = nameOff
			}
			d = be.AppendUint32(d, tg[0])
			d = be.AppendUint32(d, tg[1])
		}
		d = be.AppendUint32(d, 0) // TAG_DONE
		d = append(d, m.name...)
		d = append(d, 0)
		body = append(body, chunk("AUDM", d)...)
	}
	return append(append([]byte("FORM"), be.AppendUint32(nil, uint32(len(body)))...), body...)
}

// ahiPeak is a measure of the queued AHI sound (tests).
func (o *ahiOut) peak() float64 {
	p := 0.0
	for _, f := range o.buf[o.start:] {
		p = math.Max(p, math.Max(math.Abs(float64(f[0])), math.Abs(float64(f[1]))))
	}
	return p
}

// AmigaFiles are the files goAmiga offers the Amiga on its "goAmiga"
// volume: the AHI driver and its mode file, a script that installs them,
// and a ReadMe. Paths are the volume's, slash-separated.
func AmigaFiles() map[string][]byte {
	script := `; Installs goAmiga's AHI driver, so AHI programs play on this computer.
; AHI itself must be installed first: Aminet driver/audio/m68k-amigaos-ahiusr.lha
If NOT EXISTS DEVS:AHI
  Echo "AHI isn't installed: install it first (Aminet driver/audio/m68k-amigaos-ahiusr.lha)."
  Quit 5
EndIf
Copy goAmiga:Devs/AHI/goamiga.audio DEVS:AHI/ QUIET
Copy goAmiga:Devs/AudioModes/GOAMIGA DEVS:AudioModes/ QUIET
If EXISTS C:AddAudioModes
  C:AddAudioModes QUIET
EndIf
Echo "goAmiga's AHI driver is installed. In Prefs/AHI, choose a GOAMIGA mode for each unit."
`
	readme := `goAmiga's files for the Amiga
=============================

Install-AHI-Driver
  Installs goamiga.audio, an AHI driver that plays on the computer goAmiga
  runs on, and its modes (GOAMIGA:16 bit stereo++ and others). Install
  AHI first (Aminet: driver/audio/m68k-amigaos-ahiusr.lha), then run
  "Execute goAmiga:Install-AHI-Driver" in a Shell, and choose a GOAMIGA
  mode for each unit in Prefs/AHI. The driver needs goAmiga's hard disk
  board (any hardfile or shared folder), as this volume does.
`
	return map[string][]byte{
		"Devs/AHI/goamiga.audio":  AHIDriver(),
		"Devs/AudioModes/GOAMIGA": AHIModes(),
		"Install-AHI-Driver":      []byte(script),
		"ReadMe":                  []byte(readme),
	}
}
