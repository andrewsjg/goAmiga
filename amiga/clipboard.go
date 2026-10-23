package amiga

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

// Clipboard sharing: text copied on the host can be pasted on the Amiga,
// and text copied on the Amiga can be pasted on the host.
//
// AmigaOS keeps clips in clipboard.device, unit 0, as IFF FTXT. Only a
// program on the Amiga can use the device, so the hard disk board starts
// one: its RomTag init adds a small task (at boot, when it runs), which
// waits, woken now and then by a vertical blank interrupt server, until
// DOS has started its first process, then creates the "goAmiga clipboard"
// process and ends. The task and the process run the same kind of loop:
//
//	loop: trap CALL       ; Go sets A5 to a library function, A6 and arguments
//	      jsr (a5)
//	      trap RETURN     ; Go takes D0
//	      bra loop
//
// so everything they do is decided here, a call at a time. The process
// opens clipboard.device, then every 0.2 seconds (Delay) reads the
// current clip ID (CBD_CURRENTREADID): a new one is read and passed to the
// host; and when the host's clipboard has changed, its text is written as
// a new clip. It needs Kickstart 2.0 or later (CreateMsgPort and
// CreateIORequest), and clipboard.device in DEVS:, as every Workbench has.

// HostClipboard is the host's clipboard, as the display provides it.
type HostClipboard interface {
	// ChangeCount changes whenever the clipboard does (by anyone).
	ChangeCount() int
	// Text is the clipboard's text, if it holds some.
	Text() (string, bool)
	SetText(string)
}

// The board RAM the clipboard helper uses, above the boot nodes.
const (
	clipTask     = hdRAM + 0x5000 // struct Task (92 bytes)
	clipInt      = hdRAM + 0x5080 // struct Interrupt (22 bytes)
	clipNames    = hdRAM + 0x50C0
	clipStackLow = hdRAM + 0x5400
	clipStackTop = hdRAM + 0x6400 // 4 KB

	clipBufSize = 32 << 10 // the process's transfer buffer
	clipMaxText = 1 << 20

	clipWakeSig = 1 << 15 // SIGBREAKF_CTRL_F: the task's wake-up call
)

// Traps for the helper.
const (
	hdTrapClipInit = iota + 100
	hdTrapClipIntInit
	hdTrapClipVBL
	hdTrapClipTaskCall
	hdTrapClipTaskRet
	hdTrapClipProcCall
	hdTrapClipProcRet
)

// Library vector offsets the helper calls (with those in hdboard.go and
// netboard.go).
const (
	lvoRemIntServer    = -174
	lvoAddTask         = -282
	lvoRemTask         = -288
	lvoOpenDevice      = -444
	lvoDoIO            = -456
	lvoCreateIORequest = -654
	lvoCreateMsgPort   = -666
	lvoCreateProc      = -138 // dos.library
	lvoDelay           = -198 // dos.library
)

// IOClipReq fields (beyond IOStdReq's, in hdboard.go) and
// clipboard.device commands.
const (
	ioClipID  = 48
	ioClipReq = 52

	cmdRead          = 2
	cmdWrite         = 3
	cmdUpdate        = 4
	cbdCurrentReadID = 10
)

// The helper's steps.
const (
	clipIdle = iota
	// The task.
	clipTaskWaitDOS
	clipTaskOpenDOS
	clipTaskCreateProc
	clipTaskRemInt
	clipTaskEnd
	// The process.
	clipProcStart
	clipProcPort
	clipProcIO
	clipProcBuf
	clipProcOpen
	clipProcRetry
	clipProcDelay
	clipProcCheckID
	clipProcRead
	clipProcWrite
	clipProcUpdate
	clipProcWriteID
	clipProcDead // couldn't get memory: does nothing
)

// clipState is the helper's progress.
type clipState struct {
	step     int // what the call being made is for
	procStep int
	wakeAt   uint64 // the task's next wake-up (Amiga.cycles)

	dosBase, port, io, buf uint32
	retry                  int // seconds to the next OpenDevice try

	lastID    uint32 // the clip we last read or wrote
	lastText  string // the text last passed either way
	hostCount int    // the host clipboard's ChangeCount when last seen

	data    []byte // a clip being read or written
	dataPos int
}

// clipCall sets up the next call: A5 = the function at lvo in lib, A6 = lib.
func (h *hdBoard) clipCall(lib uint32, lvo int) {
	cpu := h.amiga.CPU
	cpu.A[6] = lib
	cpu.A[5] = uint32(int64(lib) + int64(lvo))
}

// clipInit decides at boot whether to start the helper: the task's
// structure, entry point and stack for AddTask.
func (h *hdBoard) clipInit() {
	a := h.amiga
	cpu := a.CPU
	h.clip = clipState{}
	cpu.D[0] = 0
	if a.HostClipboard == nil || a.KickstartVersion() < 36 {
		return
	}
	t := h.base + clipTask
	for i := uint32(0); i < 92; i++ {
		a.poke8(t+i, 0)
	}
	a.pokeString(h.base+clipNames, "goAmiga clipboard")
	a.poke8(t+8, 1)                  // NT_TASK
	a.poke8(t+9, 0)                  // pri 0
	a.poke32(t+10, h.base+clipNames) // ln_Name
	a.poke32(t+54, h.base+clipStackTop)
	a.poke32(t+58, h.base+clipStackLow)
	a.poke32(t+62, h.base+clipStackTop)
	ml := t + 74 // tc_MemEntry: an empty list
	a.poke32(ml, ml+4)
	a.poke32(ml+4, 0)
	a.poke32(ml+8, ml)
	cpu.A[1] = t
	cpu.A[2] = h.clipTaskCode
	cpu.A[3] = 0
	cpu.D[0] = 1
	h.clip.step = clipTaskWaitDOS
	h.tracef("clipboard: helper task at $%06X", t)
}

// clipIntInit gives AddIntServer the vertical blank server.
func (h *hdBoard) clipIntInit() {
	a := h.amiga
	n := h.base + clipInt
	for i := uint32(0); i < 22; i++ {
		a.poke8(n+i, 0)
	}
	a.poke8(n+8, 2) // NT_INTERRUPT
	a.poke32(n+10, h.base+clipNames)
	a.poke32(n+18, h.clipVBLCode) // is_Code
	a.CPU.A[1] = n
	a.CPU.D[0] = 5 // INTB_VERTB
}

// clipVBL runs every frame while the task waits: about once a second it
// wakes the task (D0 = signal, A1 = task) to see whether DOS is up.
func (h *hdBoard) clipVBL() {
	a := h.amiga
	cpu := a.CPU
	cpu.D[0] = 0
	if h.clip.step == clipTaskWaitDOS && a.cycles >= h.clip.wakeAt {
		h.clip.wakeAt = a.cycles + uint64(CyclesPerFrame)*50
		cpu.D[0] = clipWakeSig
		cpu.A[1] = h.base + clipTask
	}
}

// dosStarted reports whether DOS has started a process: by then it has
// booted and assigned DEVS:.
func (h *hdBoard) dosStarted() bool {
	a := h.amiga
	eb := a.peek32(4)
	for _, list := range []uint32{eb + 0x196, eb + 0x1A4} { // TaskReady, TaskWait
		for n, i := a.peek32(list), 0; n != 0 && a.peek32(n) != 0 && i < 256; n, i = a.peek32(n), i+1 {
			if a.peek8(n+8) == 13 { // NT_PROCESS
				return true
			}
		}
	}
	return false
}

// clipTaskCall sets up the task's next call.
func (h *hdBoard) clipTaskCall() {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	c := &h.clip
	switch c.step {
	case clipTaskWaitDOS:
		if h.dosStarted() {
			c.step = clipTaskOpenDOS
			a.pokeString(h.base+clipNames+32, "dos.library")
			h.clipCall(eb, lvoOpenLibrary)
			cpu.A[1] = h.base + clipNames + 32
			cpu.D[0] = 36
			return
		}
		h.clipCall(eb, lvoWait)
		cpu.D[0] = clipWakeSig
	case clipTaskCreateProc:
		h.clipCall(c.dosBase, lvoCreateProc)
		cpu.D[1] = h.base + clipNames
		cpu.D[2] = 0
		cpu.D[3] = h.clipSeg >> 2
		cpu.D[4] = 4096
	case clipTaskRemInt:
		h.clipCall(eb, lvoRemIntServer)
		cpu.D[0] = 5
		cpu.A[1] = h.base + clipInt
	default: // done: end the task
		c.step = clipTaskEnd
		h.clipCall(eb, lvoRemTask)
		cpu.A[1] = 0
	}
}

// clipTaskRet takes the result of the task's call.
func (h *hdBoard) clipTaskRet() {
	c := &h.clip
	d0 := h.amiga.CPU.D[0]
	switch c.step {
	case clipTaskOpenDOS:
		c.dosBase = d0
		c.step = clipTaskCreateProc
		if d0 == 0 {
			c.step = clipTaskRemInt
		}
	case clipTaskCreateProc:
		h.tracef("clipboard: process started (port $%06X)", d0)
		c.procStep = clipProcStart
		c.step = clipTaskRemInt
	case clipTaskRemInt:
		c.step = clipTaskEnd
	}
}

// clipProcCall sets up the process's next call.
func (h *hdBoard) clipProcCall() {
	a := h.amiga
	cpu := a.CPU
	eb := a.peek32(4)
	c := &h.clip
	delay := func(ticks uint32) {
		c.procStep = clipProcDelay
		h.clipCall(c.dosBase, lvoDelay)
		cpu.D[1] = ticks
	}
	doIO := func(step, cmd int) {
		c.procStep = step
		a.poke16(c.io+ioCommand, uint16(cmd))
		h.clipCall(eb, lvoDoIO)
		cpu.A[1] = c.io
	}
	switch c.procStep {
	case clipProcStart:
		// No requesters for this process ("Please insert volume ...").
		a.poke32(a.peek32(eb+0x114)+184, 0xFFFFFFFF) // pr_WindowPtr
		c.procStep = clipProcPort
		h.clipCall(eb, lvoCreateMsgPort)
	case clipProcIO:
		h.clipCall(eb, lvoCreateIORequest)
		cpu.A[0] = c.port
		cpu.D[0] = ioClipReq
	case clipProcBuf:
		h.clipCall(eb, lvoAllocMem)
		cpu.D[0] = clipBufSize
		cpu.D[1] = 1 // MEMF_PUBLIC
	case clipProcOpen:
		a.pokeString(h.base+clipNames+48, "clipboard.device")
		h.clipCall(eb, lvoOpenDevice)
		cpu.A[0] = h.base + clipNames + 48
		cpu.D[0] = 0
		cpu.A[1] = c.io
		cpu.D[1] = 0
	case clipProcRetry:
		// Not there yet (or no clipboard.device in DEVS:): try again later,
		// less and less often.
		c.retry = min(max(c.retry*2, 5), 60)
		c.procStep = clipProcOpen
		h.clipCall(c.dosBase, lvoDelay)
		cpu.D[1] = uint32(c.retry * 50)
	case clipProcIdle, clipProcDelay:
		if c.procStep == clipProcIdle {
			delay(10)
			return
		}
		if text, ok := h.hostChanged(); ok {
			c.data = ftxt(toAmigaText(text))
			c.lastText = text
			c.dataPos = 0
			a.poke32(c.io+ioOffset, 0)
			a.poke32(c.io+ioClipID, 0)
			h.clipWriteChunk()
			return
		}
		doIO(clipProcCheckID, cbdCurrentReadID)
	case clipProcRead:
		a.poke32(c.io+ioData, c.buf)
		a.poke32(c.io+ioLength, clipBufSize)
		doIO(clipProcRead, cmdRead)
	case clipProcWrite:
		h.clipWriteChunk()
	case clipProcUpdate:
		doIO(clipProcUpdate, cmdUpdate)
	case clipProcWriteID:
		doIO(clipProcWriteID, cbdCurrentReadID)
	default: // clipProcDead
		h.clipCall(c.dosBase, lvoDelay)
		cpu.D[1] = 3000
	}
}

// clipProcIdle is the step between polls (the process's next call is a
// Delay).
const clipProcIdle = clipIdle

// hostChanged reports new text on the host's clipboard, to copy to the
// Amiga.
func (h *hdBoard) hostChanged() (string, bool) {
	hc := h.amiga.HostClipboard
	c := &h.clip
	if hc == nil {
		return "", false
	}
	n := hc.ChangeCount()
	if n == c.hostCount {
		return "", false
	}
	c.hostCount = n
	text, ok := hc.Text()
	if !ok || text == c.lastText || len(text) > clipMaxText {
		return "", false
	}
	return text, true
}

// clipWriteChunk writes the next part of the clip being written.
func (h *hdBoard) clipWriteChunk() {
	a := h.amiga
	c := &h.clip
	n := min(len(c.data)-c.dataPos, clipBufSize)
	a.pokeBytes(c.buf, c.data[c.dataPos:c.dataPos+n])
	c.dataPos += n
	a.poke32(c.io+ioData, c.buf)
	a.poke32(c.io+ioLength, uint32(n))
	c.procStep = clipProcWrite
	a.poke16(c.io+ioCommand, cmdWrite)
	h.clipCall(a.peek32(4), lvoDoIO)
	a.CPU.A[1] = c.io
}

// clipProcRet takes the result of the process's call.
func (h *hdBoard) clipProcRet() {
	a := h.amiga
	c := &h.clip
	d0 := a.CPU.D[0]
	switch c.procStep {
	case clipProcPort:
		c.port, c.procStep = d0, clipProcIO
	case clipProcIO:
		c.io, c.procStep = d0, clipProcBuf
	case clipProcBuf:
		c.buf, c.procStep = d0, clipProcOpen
		if c.port == 0 || c.io == 0 || c.buf == 0 {
			c.procStep = clipProcDead
		}
	case clipProcOpen:
		if d0 != 0 {
			c.procStep = clipProcRetry
			return
		}
		h.tracef("clipboard: clipboard.device open")
		// What's on the host clipboard now is the host's own business:
		// only later changes go to the Amiga.
		if hc := a.HostClipboard; hc != nil {
			c.hostCount = hc.ChangeCount()
		}
		c.lastID = 0xFFFFFFFF
		c.procStep = clipProcIdle
	case clipProcDelay:
		// The next call polls (clipProcCall sees clipProcDelay).
	case clipProcCheckID:
		id := a.peek32(c.io + ioClipID)
		if id == c.lastID {
			c.procStep = clipProcIdle
			return
		}
		c.lastID = id
		c.data = c.data[:0]
		a.poke32(c.io+ioOffset, 0)
		a.poke32(c.io+ioClipID, 0)
		c.procStep = clipProcRead
	case clipProcRead:
		n := int(a.peek32(c.io + ioActual))
		if n > 0 && len(c.data) < clipMaxText+64 {
			chunk := make([]byte, n)
			a.peekBytes(c.buf, chunk)
			c.data = append(c.data, chunk...)
		}
		if n > 0 && a.peek8(c.io+ioError) == 0 {
			return // read until the device has no more (it then ends the read)
		}
		if text, ok := ftxtText(c.data); ok && text != "" {
			text = fromAmigaText(text)
			if text != c.lastText {
				c.lastText = text
				if hc := a.HostClipboard; hc != nil {
					hc.SetText(text)
					c.hostCount = hc.ChangeCount()
				}
				h.tracef("clipboard: %d characters to the host", len(text))
			}
		}
		c.data = nil
		c.procStep = clipProcIdle
	case clipProcWrite:
		if c.dataPos < len(c.data) {
			return // the next chunk
		}
		c.procStep = clipProcUpdate
	case clipProcUpdate:
		c.procStep = clipProcWriteID
		h.tracef("clipboard: %d bytes from the host", len(c.data))
		c.data = nil
	case clipProcWriteID:
		c.lastID = a.peek32(c.io + ioClipID) // our own clip: don't read it back
		c.procStep = clipProcIdle
	}
}

// ftxt makes an IFF FTXT clip holding text.
func ftxt(text []byte) []byte {
	be := binary.BigEndian
	n := len(text)
	pad := n & 1
	out := []byte("FORM")
	out = be.AppendUint32(out, uint32(4+8+n+pad))
	out = append(out, "FTXTCHRS"...)
	out = be.AppendUint32(out, uint32(n))
	out = append(out, text...)
	if pad != 0 {
		out = append(out, 0)
	}
	return out
}

// ftxtText reads the text (its CHRS chunks) of an IFF FTXT clip.
func ftxtText(b []byte) (string, bool) {
	be := binary.BigEndian
	if len(b) < 12 || string(b[0:4]) != "FORM" || string(b[8:12]) != "FTXT" {
		return "", false
	}
	end := min(len(b), 8+int(be.Uint32(b[4:])))
	var text bytes.Buffer
	for p := 12; p+8 <= end; {
		id, size := string(b[p:p+4]), int(be.Uint32(b[p+4:]))
		p += 8
		if size < 0 || p+size > end {
			break
		}
		if id == "CHRS" {
			text.Write(b[p : p+size])
		}
		p += size + size&1
	}
	return text.String(), true
}

// toAmigaText makes host text Amiga text: ISO-8859-1 (characters beyond
// it become ?) with LF line ends.
func toAmigaText(s string) []byte {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// fromAmigaText makes Amiga text (ISO-8859-1) host text (UTF-8).
func fromAmigaText(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		b = utf8.AppendRune(b, rune(s[i]))
	}
	return string(b)
}
