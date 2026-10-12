package amiga

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// Network board: a Zorro II board whose ROM adds bsdsocket.library, the
// AmiTCP socket API, implemented with the host's sockets (as UAE's
// bsdsocket emulation does). Amiga networking software — telnet and FTP
// clients, web browsers, mail readers — then reaches the network through
// the host, with no TCP/IP stack to install.
//
// The 68000 side is glue, like the hard disk board's: each library vector
// writes a trap number to the board and Go does the work, reading the
// arguments from the CPU's registers and memory. Calls that would block
// (recv, accept, connect, WaitSelect, gethostbyname, ...) don't block the
// emulator: Go returns a signal mask in D1 and the vector's stub Wait()s
// on it, then traps again to resume. The board polls the host sockets
// a few times a frame; when a waiting call can go on it raises INT2, and the
// library's PORTS interrupt server Signal()s the task. A break signal
// (Ctrl-C by default) interrupts a waiting call with EINTR and stays set.
//
// Each task that opens the library gets its own descriptor table, errno
// and buffers for the results of the netdb calls.

const (
	netBoardSize = 64 << 10
	netTrapReg   = hdTrapReg // word write: trap number
	netROM       = 0x1000
	netLibBase   = 0x4200 // after the jump table at netLibBase-netNegSize
	netIntNode   = 0x4300 // struct Interrupt for the PORTS server
	netSlots     = 0x8000 // per-task buffers
	netSlotSize  = 512
	netMaxTasks  = 48

	netNumFuncs = 46 // len(bsdFuncs): LVO -30 to -300
	netNegSize  = 6 * (4 + netNumFuncs)
)

func init() {
	if len(bsdFuncs) != netNumFuncs {
		panic("bsdFuncs and netNumFuncs disagree")
	}
}

// Trap numbers: the bsdsocket functions are 1..len(bsdFuncs) in LVO order
// (LVO -30 is 1); these follow.
const (
	netTrapDiag = 0x100 + iota
	netTrapInit
	netTrapIntNode
	netTrapOpen
	netTrapClose
	netTrapResume
	netTrapIRQ
)

// bsdFuncs names the library's functions in LVO order from -30.
var bsdFuncs = []string{
	"socket", "bind", "listen", "accept", "connect", "sendto", "send", "recvfrom",
	"recv", "shutdown", "setsockopt", "getsockopt", "getsockname", "getpeername",
	"IoctlSocket", "CloseSocket", "WaitSelect", "SetSocketSignals", "getdtablesize",
	"ObtainSocket", "ReleaseSocket", "ReleaseCopyOfSocket", "Errno", "SetErrnoPtr",
	"Inet_NtoA", "inet_addr", "Inet_LnaOf", "Inet_NetOf", "Inet_MakeAddr",
	"inet_network", "gethostbyname", "gethostbyaddr", "getnetbyname", "getnetbyaddr",
	"getservbyname", "getservbyport", "getprotobyname", "getprotobynumber",
	"vsyslog", "Dup2Socket", "sendmsg", "recvmsg", "gethostname", "gethostid",
	"SocketBaseTagList", "GetSocketEvents",
}

// Exec library vectors the glue calls.
const (
	lvoAddIntServer = -168
	lvoWait         = -318
	lvoSignal       = -324
	lvoAllocSignal  = -330
	lvoFreeSignal   = -336
	lvoAddLibrary   = -396
)

type netBoard struct {
	autoconfigState
	amiga *Amiga
	mem   [netBoardSize]byte

	diagOff, romtagOff, diagEnd uint32
	libNameAddr                 uint32

	// Trace, if set, logs every call.
	Trace io.Writer

	tasks    map[uint32]*sockTask
	slotUsed [netMaxTasks]bool
	shared   map[int32]*hsock // released for ObtainSocket, by id
	nextID   int32
	nextPoll uint64

	waits int // calls that had to wait (for tests)

	mu    sync.Mutex // guards wake and resolver results
	wake  []uint32   // tasks whose waiting call can go on
	irqUp bool       // INT2 raised and not yet served
}

func newNetBoard(a *Amiga) *netBoard {
	return &netBoard{amiga: a, tasks: map[uint32]*sockTask{}, shared: map[int32]*hsock{}, nextID: 1}
}

// EnableBSDSocket fits the network board, so that bsdsocket.library is
// available. Call it before Reset.
func (a *Amiga) EnableBSDSocket() {
	if a.Net == nil {
		a.Net = newNetBoard(a)
	}
}

func (n *netBoard) configByte(reg int) byte {
	switch reg {
	case 0x00: // er_Type: Zorro II, DiagArea valid, 64 KB
		return 0xC0 | 0x10 | sizeCode(netBoardSize)
	case 0x04: // er_Product
		return 0x03
	case 0x10: // er_Manufacturer $07DB
		return 0x07
	case 0x14:
		return 0xDB
	case 0x28: // er_InitDiagVec
		return netROM >> 8
	case 0x2C:
		return netROM & 0xFF
	}
	return 0
}

func (n *netBoard) space() m68kmem.Memory { return n }
func (n *netBoard) size() int             { return netBoardSize }
func (n *netBoard) configured(base uint32) {
	n.closeAll()
	n.buildROM(base)
}

func (n *netBoard) Read(addr int, p []byte) (int, error) { return copy(p, n.mem[addr:]), nil }

func (n *netBoard) Write(addr int, p []byte) (int, error) {
	switch {
	case addr == netTrapReg && len(p) >= 2:
		n.trap(int(binary.BigEndian.Uint16(p)))
	case addr >= netLibBase-netNegSize:
		copy(n.mem[addr:], p)
	}
	return len(p), nil
}

func (n *netBoard) Reset() error { return nil }

// buildROM generates the DiagArea, RomTag and library glue for a board at
// base.
func (n *netBoard) buildROM(base uint32) {
	s := &asm68k{org: base + netROM, labels: map[string]uint32{}}
	const bra, beq, bne, bmi = 0x6000, 0x6700, 0x6600, 0x6B00
	trap := func(t int) { s.w(0x33FC, uint16(t)); s.l(base + netTrapReg) }

	s.label("diag")
	s.code = append(s.code, 0x90, 0x00) // DAC_WORDWIDE|DAC_CONFIGTIME
	s.offset("diagEnd", "diag")
	s.offset("diagPoint", "diag")
	s.offset("bootPoint", "diag")
	s.offset("diagName", "diag")
	s.w(0, 0)

	s.label("diagPoint") // a2 = the RAM copy: patch its RomTag
	trap(netTrapDiag)
	s.label("bootPoint") // not a boot device
	s.moveq(1)
	s.rts()

	s.align(2)
	s.label("romtag")
	s.w(0x4AFC)
	s.l(0)
	s.l(0)
	s.code = append(s.code, 1, 4, 9, 0) // RTF_COLDSTART, version 4, NT_LIBRARY, pri 0
	s.addr("libName")
	s.addr("idString")
	s.addr("init")
	s.str("libName", "bsdsocket.library")
	s.str("idString", "bsdsocket.library 4.0 (11.10.2026) goAmiga host sockets\r\n")
	s.str("diagName", "goAmiga network")
	s.align(4)
	s.label("diagEnd")

	// RomTag init (a6 = ExecBase): add the library and the PORTS server.
	s.label("init")
	s.w(0x48E7, 0x3F3E) // movem.l d2-d7/a2-a6,-(sp)
	trap(netTrapInit)   // a1 = library
	s.jsrLVO(lvoAddLibrary)
	trap(netTrapIntNode) // a1 = interrupt, d0 = INTB_PORTS
	s.jsrLVO(lvoAddIntServer)
	s.w(0x4CDF, 0x7CFC) // movem.l (sp)+,d2-d7/a2-a6
	s.moveq(0)
	s.rts()

	// Open (a6 = library): a signal for waiting, then the task's context.
	s.label("libOpen")
	s.w(0x2F0E) // move.l a6,-(sp)
	s.execBase()
	s.moveq(-1)
	s.jsrLVO(lvoAllocSignal)
	s.w(0x2C5F)       // movea.l (sp)+,a6
	trap(netTrapOpen) // d0 = library base or 0
	s.rts()
	s.label("libClose")
	trap(netTrapClose) // d0 = signal to free, or -1
	s.tstD0()
	s.branch(bmi, "closeDone")
	s.w(0x2F0E)
	s.execBase()
	s.jsrLVO(lvoFreeSignal)
	s.w(0x2C5F)
	s.label("closeDone")
	s.moveq(0)
	s.rts()
	s.label("libNull") // Expunge, the reserved vector
	s.moveq(0)
	s.rts()

	// A call: trap; if d1 is set, Wait() on it and trap again to resume.
	// After a resume, a0 holds break signals to set again (a1 = task).
	s.label("call")
	s.w(0x4A81) // tst.l d1
	s.branch(bne, "callWait")
	s.rts()
	s.label("callWait")
	s.w(0x2F0E)  // move.l a6,-(sp)
	s.w(0x2001)  // move.l d1,d0
	s.execBase() // movea.l 4.w,a6
	s.jsrLVO(lvoWait)
	s.w(0x2C5F) // movea.l (sp)+,a6
	trap(netTrapResume)
	s.w(0x4A81) // tst.l d1
	s.branch(bne, "callWait")
	s.w(0x2208) // move.l a0,d1
	s.branch(beq, "callDone")
	s.w(0x2F00) // move.l d0,-(sp)
	s.w(0x2F0E) // move.l a6,-(sp)
	s.w(0x2001) // move.l d1,d0
	s.execBase()
	s.jsrLVO(lvoSignal) // a1 = task
	s.w(0x2C5F)
	s.w(0x201F) // move.l (sp)+,d0
	s.label("callDone")
	s.rts()

	for i := range bsdFuncs {
		s.label(fmt.Sprintf("f%d", i+1))
		trap(i + 1)
		s.w(0x4EF9) // jmp call
		s.addr("call")
	}

	// PORTS interrupt server: Signal the tasks whose calls can go on.
	s.label("server")
	trap(netTrapIRQ) // d1 = 1: a1 = task, d0 = signals
	s.w(0x4A81)
	s.branch(beq, "serverDone")
	s.execBase()
	s.jsrLVO(lvoSignal)
	s.branch(bra, "server")
	s.label("serverDone")
	s.moveq(0) // let the other PORTS servers run
	s.rts()
	s.str("serverName", "bsdsocket.library")
	s.align(2)

	s.resolve()
	if len(s.code) > netLibBase-netNegSize-netROM {
		panic("netBoard: ROM too big")
	}
	n.mem = [netBoardSize]byte{}
	copy(n.mem[netROM:], s.code)
	off := func(l string) uint32 { return s.labels[l] - base }
	n.diagOff, n.romtagOff, n.diagEnd = off("diag"), off("romtag"), off("diagEnd")
	n.libNameAddr = s.labels["libName"]

	vectors := []string{"libOpen", "libClose", "libNull", "libNull"}
	for i := range bsdFuncs {
		vectors = append(vectors, fmt.Sprintf("f%d", i+1))
	}
	for i, fn := range vectors {
		v := netLibBase - 6*(i+1)
		binary.BigEndian.PutUint16(n.mem[v:], 0x4EF9)
		binary.BigEndian.PutUint32(n.mem[v+2:], s.labels[fn])
	}
	lib := n.mem[netLibBase:]
	lib[8] = 9 // NT_LIBRARY
	binary.BigEndian.PutUint32(lib[10:], n.libNameAddr)
	lib[14] = 6 // LIBF_SUMUSED|LIBF_CHANGED
	binary.BigEndian.PutUint16(lib[16:], uint16(netNegSize))
	binary.BigEndian.PutUint16(lib[18:], 34)
	binary.BigEndian.PutUint16(lib[20:], 4) // lib_Version
	binary.BigEndian.PutUint32(lib[24:], s.labels["idString"])

	in := n.mem[netIntNode:]
	in[8] = 2 // NT_INTERRUPT
	binary.BigEndian.PutUint32(in[10:], s.labels["serverName"])
	binary.BigEndian.PutUint32(in[18:], s.labels["server"]) // is_Code
}

func (n *netBoard) tracef(format string, args ...any) {
	if n.Trace != nil {
		fmt.Fprintf(n.Trace, "bsdsocket: "+format+"\n", args...)
	}
}

func (n *netBoard) trap(t int) {
	a := n.amiga
	cpu := a.CPU
	chip, cia := a.cpuChipAccesses, a.cpuCIAAccesses
	defer func() { a.cpuChipAccesses, a.cpuCIAAccesses = chip, cia }()

	switch t {
	case netTrapDiag:
		cp := cpu.A[2]
		rt := cp + n.romtagOff - n.diagOff
		a.poke32(rt+2, rt)
		a.poke32(rt+6, cp+n.diagEnd-n.diagOff)
		cpu.D[0] = 1
	case netTrapInit:
		cpu.A[1] = n.base + netLibBase
		n.tracef("library at $%06X", cpu.A[1])
	case netTrapIntNode:
		cpu.A[1] = n.base + netIntNode
		cpu.D[0] = 3 // INTB_PORTS
	case netTrapOpen:
		cpu.D[0] = n.open(int8(cpu.D[0]))
	case netTrapClose:
		cpu.D[0] = n.close()
	case netTrapResume:
		n.resume()
	case netTrapIRQ:
		n.serveIRQ()
	default:
		if t >= 1 && t <= len(bsdFuncs) {
			n.call(t)
		}
	}
}

// thisTask is the task making the call.
func (n *netBoard) thisTask() uint32 { return n.amiga.peek32(n.amiga.peek32(4) + 0x114) }

// open gives the calling task a context. sig is the signal AllocSignal
// gave it (-1 if none was free).
func (n *netBoard) open(sig int8) uint32 {
	task := n.thisTask()
	lib := n.base + netLibBase
	if t := n.tasks[task]; t != nil {
		t.opens++
		if sig >= 0 { // only one signal per task is needed
			t.spareSigs = append(t.spareSigs, sig)
		}
	} else {
		slot := -1
		for i, used := range n.slotUsed {
			if !used {
				slot = i
				break
			}
		}
		if slot < 0 || sig < 0 {
			n.tracef("open by $%06X refused (signal %d, slot %d)", task, sig, slot)
			return 0
		}
		n.slotUsed[slot] = true
		n.tasks[task] = newSockTask(task, sig, slot, n.base+netSlots+uint32(slot*netSlotSize))
		n.tracef("opened by $%06X (signal %d)", task, sig)
	}
	n.amiga.poke16(lib+32, n.amiga.peek16(lib+32)+1)
	return lib
}

// close ends an opening; the last one closes the task's sockets. It
// returns a signal to free, or -1.
func (n *netBoard) close() uint32 {
	task := n.thisTask()
	lib := n.base + netLibBase
	n.amiga.poke16(lib+32, n.amiga.peek16(lib+32)-1)
	t := n.tasks[task]
	if t == nil {
		return 0xFFFFFFFF
	}
	if t.opens > 0 {
		t.opens--
		if k := len(t.spareSigs); k > 0 {
			sig := t.spareSigs[k-1]
			t.spareSigs = t.spareSigs[:k-1]
			return uint32(sig)
		}
		return 0xFFFFFFFF
	}
	t.closeAll()
	delete(n.tasks, task)
	n.slotUsed[t.slot] = false
	n.tracef("closed by $%06X", task)
	return uint32(t.sig)
}

// closeAll drops every task's sockets (board reset or state load).
func (n *netBoard) closeAll() {
	for _, t := range n.tasks {
		t.closeAll()
	}
	for _, s := range n.shared {
		s.release()
	}
	n.tasks = map[uint32]*sockTask{}
	n.shared = map[int32]*hsock{}
	n.slotUsed = [netMaxTasks]bool{}
	n.mu.Lock()
	n.wake, n.irqUp = nil, false
	n.mu.Unlock()
}

// call runs library function fn for the calling task.
func (n *netBoard) call(fn int) {
	cpu := n.amiga.CPU
	t := n.tasks[n.thisTask()]
	if t == nil { // called without opening the library: a context of its own
		cpu.D[0], cpu.D[1] = 0xFFFFFFFF, 0
		return
	}
	var regs [16]uint32
	copy(regs[:8], cpu.D[:])
	copy(regs[8:], cpu.A[:])
	t.op = &pendingOp{fn: fn, regs: regs}
	n.run(t)
	if t.op != nil {
		n.waits++
	}
}

// run performs (or retries) the task's current call and sets the result
// registers: D0 the result, D1 a signal mask to wait on if it must block.
func (n *netBoard) run(t *sockTask) {
	cpu := n.amiga.CPU
	op := t.op
	res, pending := n.dispatch(t, op)
	cpu.A[0] = 0
	if pending {
		op.woken = false
		cpu.D[1] = 1<<uint(t.sig) | t.breakMask | op.userMask
		return
	}
	cpu.D[0], cpu.D[1] = uint32(res), 0
	if n.Trace != nil {
		n.tracef("$%06X %s = %d (errno %d)", t.task, bsdFuncs[op.fn-1], res, t.errno)
	}
	t.op = nil
}

// resume continues a waiting call after Wait() returned the signals in D0.
func (n *netBoard) resume() {
	cpu := n.amiga.CPU
	t := n.tasks[n.thisTask()]
	if t == nil || t.op == nil {
		cpu.D[0], cpu.D[1], cpu.A[0] = 0xFFFFFFFF, 0, 0
		return
	}
	got := cpu.D[0]
	op := t.op
	if brk := got & t.breakMask; brk != 0 {
		t.setErrno(n.amiga, 4) // EINTR
		cpu.D[0], cpu.D[1] = 0xFFFFFFFF, 0
		cpu.A[0], cpu.A[1] = brk, t.task // the break signals stay set
		t.op.cancel()
		t.op = nil
		return
	}
	if user := got & op.userMask; user != 0 { // WaitSelect's own signals
		op.gotUser = user
	}
	// Woken early (our signal was already set, say): keep waiting. A
	// connect retried before it has finished would read SO_ERROR too soon.
	if op.gotUser == 0 && !op.ready(n.amiga.cycles) {
		op.woken = false
		cpu.D[1] = 1<<uint(t.sig) | t.breakMask | op.userMask
		cpu.A[0] = 0
		return
	}
	n.run(t)
}

// serveIRQ hands the PORTS server the next task to signal.
func (n *netBoard) serveIRQ() {
	cpu := n.amiga.CPU
	n.mu.Lock()
	defer n.mu.Unlock()
	for len(n.wake) > 0 {
		task := n.wake[0]
		n.wake = n.wake[1:]
		if t := n.tasks[task]; t != nil && t.op != nil {
			cpu.A[1], cpu.D[0], cpu.D[1] = task, 1<<uint(t.sig), 1
			return
		}
	}
	n.irqUp = false
	cpu.D[1] = 0
}

// tick polls waiting calls a few times a frame and raises INT2 when one
// can go on.
func (n *netBoard) tick() {
	a := n.amiga
	if a.cycles < n.nextPoll {
		return
	}
	n.nextPoll = a.cycles + uint64(CyclesPerFrame/8)
	woke := false
	for task, t := range n.tasks {
		if op := t.op; op != nil && !op.woken && op.ready(a.cycles) {
			op.woken = true
			n.mu.Lock()
			n.wake = append(n.wake, task)
			n.mu.Unlock()
			woke = true
		}
	}
	n.mu.Lock()
	if woke || (n.irqUp && len(n.wake) > 0) {
		n.irqUp = true
		n.mu.Unlock()
		a.Custom.SetIntreq(1 << 3) // PORTS (INT2)
		return
	}
	n.mu.Unlock()
}

// waiting reports whether any call is waiting (the CPU should not sleep
// past the next poll).
func (n *netBoard) waiting() bool {
	for _, t := range n.tasks {
		if t.op != nil {
			return true
		}
	}
	return false
}

func (n *netBoard) state(s *stateCodec) {
	n.autoconfigState.state(s)
	if s.load {
		n.closeAll() // host sockets can't be restored
		if n.base != 0 {
			n.buildROM(n.base)
		}
	}
	s.fixed(n.mem[:])
}
