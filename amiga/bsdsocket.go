//go:build darwin || linux

package amiga

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// bsdsocket.library functions, on the host's sockets (see netboard.go).
//
// Every host socket is non-blocking. A call on a blocking Amiga socket
// that would block becomes a pending operation: the board polls its
// descriptor and the call is retried when it is ready. Amiga constants are
// BSD 4.4's (AmiTCP's), translated to the host's.

// Amiga errno values (AmiTCP: BSD).
const (
	aEINTR        = 4
	aEBADF        = 9
	aENOMEM       = 12
	aEFAULT       = 14
	aEINVAL       = 22
	aEMFILE       = 24
	aEWOULDBLOCK  = 35
	aEINPROGRESS  = 36
	aENOTSOCK     = 38
	aENOPROTOOPT  = 42
	aEPROTONOSUPP = 43
	aEOPNOTSUPP   = 45
	aEAFNOSUPPORT = 47
	aENOSYS       = 78
)

var hostToAmigaErrno = map[unix.Errno]int32{
	unix.EPERM: 1, unix.ENOENT: 2, unix.EINTR: 4, unix.EIO: 5, unix.EBADF: 9,
	unix.ENOMEM: 12, unix.EACCES: 13, unix.EFAULT: 14, unix.EBUSY: 16, unix.EEXIST: 17,
	unix.EINVAL: 22, unix.ENFILE: 23, unix.EMFILE: 24, unix.ENOSPC: 28, unix.EPIPE: 32,
	unix.EAGAIN: 35, unix.EINPROGRESS: 36, unix.EALREADY: 37, unix.ENOTSOCK: 38,
	unix.EDESTADDRREQ: 39, unix.EMSGSIZE: 40, unix.EPROTOTYPE: 41, unix.ENOPROTOOPT: 42,
	unix.EPROTONOSUPPORT: 43, unix.ESOCKTNOSUPPORT: 44, unix.EOPNOTSUPP: 45,
	unix.EPFNOSUPPORT: 46, unix.EAFNOSUPPORT: 47, unix.EADDRINUSE: 48, unix.EADDRNOTAVAIL: 49,
	unix.ENETDOWN: 50, unix.ENETUNREACH: 51, unix.ENETRESET: 52, unix.ECONNABORTED: 53,
	unix.ECONNRESET: 54, unix.ENOBUFS: 55, unix.EISCONN: 56, unix.ENOTCONN: 57,
	unix.ESHUTDOWN: 58, unix.ETIMEDOUT: 60, unix.ECONNREFUSED: 61, unix.EHOSTDOWN: 64,
	unix.EHOSTUNREACH: 65,
}

func amigaErrno(err error) int32 {
	if e, ok := err.(unix.Errno); ok {
		if v, ok := hostToAmigaErrno[e]; ok {
			return v
		}
	}
	return aEINVAL
}

var errnoText = map[int32]string{
	1: "Operation not permitted", 2: "No such file or directory", 4: "Interrupted system call",
	5: "Input/output error", 9: "Bad file descriptor", 12: "Cannot allocate memory",
	13: "Permission denied", 14: "Bad address", 22: "Invalid argument",
	24: "Too many open files", 32: "Broken pipe", 35: "Resource temporarily unavailable",
	36: "Operation now in progress", 37: "Operation already in progress",
	38: "Socket operation on non-socket", 39: "Destination address required",
	40: "Message too long", 41: "Protocol wrong type for socket", 42: "Protocol not available",
	43: "Protocol not supported", 44: "Socket type not supported", 45: "Operation not supported",
	47: "Address family not supported by protocol family", 48: "Address already in use",
	49: "Can't assign requested address", 50: "Network is down", 51: "Network is unreachable",
	53: "Software caused connection abort", 54: "Connection reset by peer",
	55: "No buffer space available", 56: "Socket is already connected",
	57: "Socket is not connected", 60: "Operation timed out", 61: "Connection refused",
	64: "Host is down", 65: "No route to host", 78: "Function not implemented",
}

// A host socket. Several descriptors (Dup2Socket, ReleaseCopyOfSocket)
// can share one.
type hsock struct {
	fd       int // -1: a descriptor reserved by Dup2Socket
	refs     int
	typ      int
	nonblock bool
	rcvTimeo uint64 // in CPU cycles; 0 = none
	sndTimeo uint64
}

func (s *hsock) release() {
	if s.refs--; s.refs <= 0 && s.fd >= 0 {
		unix.Close(s.fd)
	}
}

// sockTask is one task's library context.
type sockTask struct {
	task      uint32
	sig       int8
	spareSigs []int8 // from further OpenLibrary calls
	opens     int    // openings beyond the first
	slot      int
	buf       uint32 // netSlotSize bytes of board memory for results

	fds                 []*hsock
	errno, herrno       int32
	errnoPtr, herrnoPtr uint32
	errnoSize           uint32
	breakMask           uint32
	op                  *pendingOp
}

// Offsets in a task's buffer.
const (
	bufHostent = 0   // struct hostent, its lists, addresses and name
	bufServent = 256 // struct servent or protoent, and strings
	bufNtoA    = 384 // Inet_NtoA's string
	bufErrStr  = 400 // SocketBaseTagList's errno strings
)

func newSockTask(task uint32, sig int8, slot int, buf uint32) *sockTask {
	return &sockTask{task: task, sig: sig, slot: slot, buf: buf, fds: make([]*hsock, 64),
		breakMask: 1 << 12} // SIGBREAKF_CTRL_C
}

func (t *sockTask) closeAll() {
	for i, s := range t.fds {
		if s != nil {
			s.release()
			t.fds[i] = nil
		}
	}
	t.op = nil
}

func (t *sockTask) setErrno(a *Amiga, e int32) {
	t.errno = e
	if t.errnoPtr != 0 {
		switch t.errnoSize {
		case 1:
			a.poke8(t.errnoPtr, byte(e))
		case 2:
			a.poke16(t.errnoPtr, uint16(e))
		default:
			a.poke32(t.errnoPtr, uint32(e))
		}
	}
}

func (t *sockTask) setHerrno(a *Amiga, e int32) {
	t.herrno = e
	if t.herrnoPtr != 0 {
		a.poke32(t.herrnoPtr, uint32(e))
	}
}

// sock returns the host socket for descriptor fd, or nil.
func (t *sockTask) sock(fd uint32) *hsock {
	if int(fd) < 0 || int(fd) >= len(t.fds) || t.fds[fd] == nil || t.fds[fd].fd < 0 {
		return nil
	}
	return t.fds[fd]
}

// newFD puts s in the lowest free descriptor, or returns -1.
func (t *sockTask) newFD(s *hsock) int32 {
	for i, f := range t.fds {
		if f == nil {
			t.fds[i] = s
			return int32(i)
		}
	}
	return -1
}

// pendingOp is a call in progress: its registers, and what it waits for.
type pendingOp struct {
	fn       int
	regs     [16]uint32 // D0-D7, A0-A7 at the call
	userMask uint32     // WaitSelect: the caller's signals to wait for too
	gotUser  uint32     // and those received
	fds      []unix.PollFd
	deadline uint64 // in Amiga.cycles; 0 = none
	started  bool   // the operation is under way (connect, lookup)
	lookup   *lookup
	woken    bool
}

func (op *pendingOp) d(i int) uint32 { return op.regs[i] }
func (op *pendingOp) a(i int) uint32 { return op.regs[8+i] }

// ready reports whether the call can be retried: a descriptor is ready,
// the lookup is done or the timeout has passed.
func (op *pendingOp) ready(now uint64) bool {
	if op.deadline != 0 && now >= op.deadline {
		return true
	}
	if op.lookup != nil {
		return op.lookup.isDone()
	}
	if len(op.fds) > 0 {
		n, err := unix.Poll(op.fds, 0)
		return n > 0 || (err != nil && err != unix.EINTR)
	}
	return false // only a timeout, the caller's signals or a break end it
}

func (op *pendingOp) cancel() {}

// lookup is a name lookup running in a goroutine.
type lookup struct {
	mu    sync.Mutex
	done  bool
	ips   []net.IP
	names []string
}

func (l *lookup) isDone() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.done }

func (l *lookup) finish(ips []net.IP, names []string) {
	l.mu.Lock()
	l.ips, l.names, l.done = ips, names, true
	l.mu.Unlock()
}

// fail sets errno and returns -1.
func (n *netBoard) fail(t *sockTask, e int32) (int32, bool) {
	t.setErrno(n.amiga, e)
	return -1, false
}

// block makes the call wait for events on fd, or for timeout cycles. If the
// timeout has passed it fails with EWOULDBLOCK.
func (n *netBoard) block(t *sockTask, op *pendingOp, fd int, events int16, timeout uint64) (int32, bool) {
	if op.deadline == 0 && timeout > 0 && !op.started {
		op.deadline = n.amiga.cycles + timeout
	}
	op.started = true
	if op.deadline != 0 && n.amiga.cycles >= op.deadline {
		return n.fail(t, aEWOULDBLOCK)
	}
	op.fds = []unix.PollFd{{Fd: int32(fd), Events: events}}
	return 0, true
}

// cyclesPerSecond is the CPU clock, for timeouts.
func (n *netBoard) cyclesPerSecond() uint64 { return 2 * uint64(n.amiga.ColorClock()) }

func (n *netBoard) timeval(addr uint32) uint64 {
	sec, usec := uint64(n.amiga.peek32(addr)), uint64(n.amiga.peek32(addr+4))
	return sec*n.cyclesPerSecond() + usec*n.cyclesPerSecond()/1e6
}

// ---- Addresses ----

func (n *netBoard) readSockaddr(addr, length uint32) (unix.Sockaddr, int32) {
	if addr == 0 || length < 8 {
		return nil, aEINVAL
	}
	if fam := n.amiga.peek8(addr + 1); fam != 2 {
		return nil, aEAFNOSUPPORT
	}
	sa := &unix.SockaddrInet4{Port: int(n.amiga.peek16(addr + 2))}
	n.amiga.peekBytes(addr+4, sa.Addr[:])
	return sa, 0
}

// writeSockaddr stores sa at addr, limited to *lenp bytes, and sets *lenp.
func (n *netBoard) writeSockaddr(addr, lenp uint32, sa unix.Sockaddr) {
	if addr == 0 || lenp == 0 {
		return
	}
	var b [16]byte
	b[0], b[1] = 16, 2
	if in, ok := sa.(*unix.SockaddrInet4); ok {
		binary.BigEndian.PutUint16(b[2:], uint16(in.Port))
		copy(b[4:], in.Addr[:])
	}
	max := n.amiga.peek32(lenp)
	n.amiga.pokeBytes(addr, b[:min(int(max), 16)])
	n.amiga.poke32(lenp, 16)
}

func hostMsgFlags(f uint32) (int, bool) {
	h := sendNoSignal
	if f&1 != 0 {
		h |= unix.MSG_OOB
	}
	if f&2 != 0 {
		h |= unix.MSG_PEEK
	}
	if f&4 != 0 {
		h |= unix.MSG_DONTROUTE
	}
	return h, f&0x80 != 0 // MSG_DONTWAIT
}

// hostSockopt translates an Amiga socket option.
func hostSockopt(level, opt uint32) (int, int, bool) {
	switch level {
	case 0xFFFF:
		o, ok := map[uint32]int{
			0x1: unix.SO_DEBUG, 0x2: unix.SO_ACCEPTCONN, 0x4: unix.SO_REUSEADDR,
			0x8: unix.SO_KEEPALIVE, 0x10: unix.SO_DONTROUTE, 0x20: unix.SO_BROADCAST,
			0x80: unix.SO_LINGER, 0x100: unix.SO_OOBINLINE, 0x200: unix.SO_REUSEPORT,
			0x1001: unix.SO_SNDBUF, 0x1002: unix.SO_RCVBUF, 0x1003: unix.SO_SNDLOWAT,
			0x1004: unix.SO_RCVLOWAT, 0x1007: unix.SO_ERROR, 0x1008: unix.SO_TYPE,
		}[opt]
		return unix.SOL_SOCKET, o, ok
	case 6: // IPPROTO_TCP
		if opt == 1 {
			return unix.IPPROTO_TCP, unix.TCP_NODELAY, true
		}
	case 0: // IPPROTO_IP
		switch opt {
		case 3:
			return unix.IPPROTO_IP, unix.IP_TOS, true
		case 4:
			return unix.IPPROTO_IP, unix.IP_TTL, true
		}
	}
	return 0, 0, false
}

// ---- Dispatch ----

// dispatch performs function op.fn. It returns the result, or pending
// when the call must wait.
func (n *netBoard) dispatch(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	switch bsdFuncs[op.fn-1] {
	case "socket":
		return n.socket(t, op.d(0), op.d(1), op.d(2))
	case "bind", "connect":
		s := t.sock(op.d(0))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		sa, e := n.readSockaddr(op.a(0), op.d(1))
		if e != 0 {
			return n.fail(t, e)
		}
		if op.fn == 2 {
			if err := unix.Bind(s.fd, sa); err != nil {
				return n.fail(t, amigaErrno(err))
			}
			return 0, false
		}
		return n.connect(t, op, s, sa)
	case "listen":
		s := t.sock(op.d(0))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		if err := unix.Listen(s.fd, int(op.d(1))); err != nil {
			return n.fail(t, amigaErrno(err))
		}
		return 0, false
	case "accept":
		return n.accept(t, op)
	case "sendto", "send":
		var to unix.Sockaddr
		if op.fn == 6 && op.a(1) != 0 {
			var e int32
			if to, e = n.readSockaddr(op.a(1), op.d(3)); e != 0 {
				return n.fail(t, e)
			}
		}
		buf := make([]byte, min(op.d(1), 1<<20))
		a.peekBytes(op.a(0), buf)
		return n.send(t, op, op.d(0), [][]byte{buf}, to, op.d(2))
	case "recvfrom", "recv":
		buf := make([]byte, min(op.d(1), 1<<20))
		got, sa, res, pend := n.recv(t, op, op.d(0), buf, op.d(2))
		if res >= 0 && !pend {
			a.pokeBytes(op.a(0), buf[:got])
			if op.fn == 8 && sa != nil {
				n.writeSockaddr(op.a(1), op.a(2), sa)
			}
		}
		return res, pend
	case "shutdown":
		s := t.sock(op.d(0))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		if err := unix.Shutdown(s.fd, int(op.d(1))); err != nil {
			return n.fail(t, amigaErrno(err))
		}
		return 0, false
	case "setsockopt":
		return n.setsockopt(t, op)
	case "getsockopt":
		return n.getsockopt(t, op)
	case "getsockname", "getpeername":
		s := t.sock(op.d(0))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		get := unix.Getsockname
		if op.fn == 14 {
			get = unix.Getpeername
		}
		sa, err := get(s.fd)
		if err != nil {
			return n.fail(t, amigaErrno(err))
		}
		n.writeSockaddr(op.a(0), op.a(1), sa)
		return 0, false
	case "IoctlSocket":
		return n.ioctl(t, op)
	case "CloseSocket":
		fd := op.d(0)
		if int(fd) < 0 || int(fd) >= len(t.fds) || t.fds[fd] == nil {
			return n.fail(t, aEBADF)
		}
		t.fds[fd].release()
		t.fds[fd] = nil
		return 0, false
	case "WaitSelect":
		return n.waitSelect(t, op)
	case "SetSocketSignals":
		t.breakMask = op.d(0)
		return 0, false
	case "getdtablesize":
		return int32(len(t.fds)), false
	case "ObtainSocket":
		s := n.shared[int32(op.d(0))]
		if s == nil {
			return n.fail(t, aEWOULDBLOCK)
		}
		fd := t.newFD(s)
		if fd < 0 {
			return n.fail(t, aEMFILE)
		}
		delete(n.shared, int32(op.d(0)))
		return fd, false
	case "ReleaseSocket", "ReleaseCopyOfSocket":
		s := t.sock(op.d(0))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		id := int32(op.d(1))
		if id == -1 { // UNIQUE_ID
			for n.shared[n.nextID+0x10000] != nil {
				n.nextID++
			}
			id = n.nextID + 0x10000
			n.nextID++
		} else if n.shared[id] != nil {
			return n.fail(t, aEINVAL)
		}
		n.shared[id] = s
		if op.fn == 21 {
			t.fds[op.d(0)] = nil
		} else {
			s.refs++
		}
		return id, false
	case "Errno":
		return t.errno, false
	case "SetErrnoPtr":
		switch op.d(0) {
		case 1, 2, 4:
			t.errnoPtr, t.errnoSize = op.a(0), op.d(0)
			return 0, false
		}
		return n.fail(t, aEINVAL)
	case "Inet_NtoA":
		in := op.d(0)
		s := strconv.Itoa(int(in>>24)) + "." + strconv.Itoa(int(in>>16&0xFF)) + "." +
			strconv.Itoa(int(in>>8&0xFF)) + "." + strconv.Itoa(int(in&0xFF))
		a.pokeString(t.buf+bufNtoA, s)
		return int32(t.buf + bufNtoA), false
	case "inet_addr":
		v, ok := parseInetAddr(n.peekString(op.a(0), 64))
		if !ok {
			return -1, false // INADDR_NONE
		}
		return int32(v), false
	case "Inet_LnaOf":
		in := op.d(0)
		switch {
		case in&0x80000000 == 0:
			return int32(in & 0xFFFFFF), false
		case in&0xC0000000 == 0x80000000:
			return int32(in & 0xFFFF), false
		}
		return int32(in & 0xFF), false
	case "Inet_NetOf":
		in := op.d(0)
		switch {
		case in&0x80000000 == 0:
			return int32(in >> 24), false
		case in&0xC0000000 == 0x80000000:
			return int32(in >> 16), false
		}
		return int32(in >> 8), false
	case "Inet_MakeAddr":
		net, host := op.d(0), op.d(1)
		switch {
		case net < 128:
			return int32(net<<24 | host&0xFFFFFF), false
		case net < 65536:
			return int32(net<<16 | host&0xFFFF), false
		}
		return int32(net<<8 | host&0xFF), false
	case "inet_network":
		v, ok := parseInetNetwork(n.peekString(op.a(0), 64))
		if !ok {
			return -1, false
		}
		return int32(v), false
	case "gethostbyname":
		return n.gethostbyname(t, op)
	case "gethostbyaddr":
		return n.gethostbyaddr(t, op)
	case "getnetbyname", "getnetbyaddr":
		return 0, false
	case "getservbyname", "getservbyport":
		var e *servEntry
		var proto string
		if op.fn == 35 {
			if op.a(1) != 0 {
				proto = n.peekString(op.a(1), 16)
			}
			e = lookupServ(n.peekString(op.a(0), 32), -1, proto)
		} else {
			if op.a(0) != 0 {
				proto = n.peekString(op.a(0), 16)
			}
			e = lookupServ("", int(op.d(0)), proto)
		}
		if e == nil {
			return 0, false
		}
		return int32(n.fillServent(t, e)), false
	case "getprotobyname", "getprotobynumber":
		var p *protoEntry
		for i := range protocols {
			if op.fn == 37 && protocols[i].name == n.peekString(op.a(0), 16) ||
				op.fn == 38 && uint32(protocols[i].num) == op.d(0) {
				p = &protocols[i]
			}
		}
		if p == nil {
			return 0, false
		}
		return int32(n.fillProtoent(t, p)), false
	case "vsyslog":
		n.tracef("syslog %d: %s", op.d(0), n.peekString(op.a(0), 200))
		return 0, false
	case "Dup2Socket":
		return n.dup2(t, op)
	case "sendmsg":
		return n.sendmsg(t, op)
	case "recvmsg":
		return n.recvmsg(t, op)
	case "gethostname":
		name, err := os.Hostname()
		if err != nil {
			name = "goamiga"
		}
		if max := int(op.d(0)); max > 0 {
			if len(name) >= max {
				name = name[:max-1]
			}
			a.pokeString(op.a(0), name)
		}
		return 0, false
	case "gethostid":
		return 0x7F000001, false
	case "SocketBaseTagList":
		return n.tagList(t, op.a(0)), false
	case "GetSocketEvents":
		return -1, false
	}
	return n.fail(t, aENOSYS)
}

func (n *netBoard) peekString(addr uint32, max int) string {
	if addr == 0 {
		return ""
	}
	b := make([]byte, max)
	n.amiga.peekBytes(addr, b)
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// ---- Sockets ----

func (n *netBoard) socket(t *sockTask, domain, typ, proto uint32) (int32, bool) {
	if domain != 2 {
		return n.fail(t, aEAFNOSUPPORT)
	}
	ht := map[uint32]int{1: unix.SOCK_STREAM, 2: unix.SOCK_DGRAM, 3: unix.SOCK_RAW}[typ]
	if ht == 0 {
		return n.fail(t, aEPROTONOSUPP)
	}
	fd, err := unix.Socket(unix.AF_INET, ht, int(proto))
	if err != nil {
		return n.fail(t, amigaErrno(err))
	}
	unix.SetNonblock(fd, true)
	unix.CloseOnExec(fd)
	noSigPipe(fd)
	s := &hsock{fd: fd, refs: 1, typ: int(typ)}
	r := t.newFD(s)
	if r < 0 {
		unix.Close(fd)
		return n.fail(t, aEMFILE)
	}
	return r, false
}

func (n *netBoard) connect(t *sockTask, op *pendingOp, s *hsock, sa unix.Sockaddr) (int32, bool) {
	if op.started { // retried once writable: how did it go?
		v, err := unix.GetsockoptInt(s.fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			return n.fail(t, amigaErrno(err))
		}
		if v != 0 {
			return n.fail(t, amigaErrno(unix.Errno(v)))
		}
		return 0, false
	}
	err := unix.Connect(s.fd, sa)
	switch {
	case err == nil:
		return 0, false
	case err == unix.EINPROGRESS && !s.nonblock:
		return n.block(t, op, s.fd, unix.POLLOUT, s.sndTimeo)
	}
	return n.fail(t, amigaErrno(err))
}

func (n *netBoard) accept(t *sockTask, op *pendingOp) (int32, bool) {
	s := t.sock(op.d(0))
	if s == nil {
		return n.fail(t, aEBADF)
	}
	nfd, sa, err := unix.Accept(s.fd)
	if err == unix.EAGAIN && !s.nonblock {
		return n.block(t, op, s.fd, unix.POLLIN, s.rcvTimeo)
	}
	if err != nil {
		return n.fail(t, amigaErrno(err))
	}
	unix.SetNonblock(nfd, true)
	unix.CloseOnExec(nfd)
	noSigPipe(nfd)
	r := t.newFD(&hsock{fd: nfd, refs: 1, typ: s.typ})
	if r < 0 {
		unix.Close(nfd)
		return n.fail(t, aEMFILE)
	}
	n.writeSockaddr(op.a(0), op.a(1), sa)
	return r, false
}

func (n *netBoard) send(t *sockTask, op *pendingOp, fd uint32, bufs [][]byte, to unix.Sockaddr, flags uint32) (int32, bool) {
	s := t.sock(fd)
	if s == nil {
		return n.fail(t, aEBADF)
	}
	hf, dontwait := hostMsgFlags(flags)
	var data []byte
	for _, b := range bufs {
		data = append(data, b...)
	}
	sent, err := unix.SendmsgN(s.fd, data, nil, to, hf)
	if err == unix.EAGAIN && !s.nonblock && !dontwait {
		return n.block(t, op, s.fd, unix.POLLOUT, s.sndTimeo)
	}
	if err != nil {
		return n.fail(t, amigaErrno(err))
	}
	return int32(sent), false
}

func (n *netBoard) recv(t *sockTask, op *pendingOp, fd uint32, buf []byte, flags uint32) (int, unix.Sockaddr, int32, bool) {
	s := t.sock(fd)
	if s == nil {
		r, p := n.fail(t, aEBADF)
		return 0, nil, r, p
	}
	hf, dontwait := hostMsgFlags(flags)
	hf &^= sendNoSignal
	got, sa, err := unix.Recvfrom(s.fd, buf, hf)
	if err == unix.EAGAIN && !s.nonblock && !dontwait {
		r, p := n.block(t, op, s.fd, unix.POLLIN, s.rcvTimeo)
		return 0, nil, r, p
	}
	if err != nil {
		r, p := n.fail(t, amigaErrno(err))
		return 0, nil, r, p
	}
	return got, sa, int32(got), false
}

func (n *netBoard) setsockopt(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	s := t.sock(op.d(0))
	if s == nil {
		return n.fail(t, aEBADF)
	}
	level, opt, val, vlen := op.d(1), op.d(2), op.a(0), op.d(3)
	if level == 0xFFFF && (opt == 0x1005 || opt == 0x1006) { // SO_SNDTIMEO, SO_RCVTIMEO
		if vlen < 8 {
			return n.fail(t, aEINVAL)
		}
		if opt == 0x1005 {
			s.sndTimeo = n.timeval(val)
		} else {
			s.rcvTimeo = n.timeval(val)
		}
		return 0, false
	}
	hl, ho, ok := hostSockopt(level, opt)
	if !ok {
		return n.fail(t, aENOPROTOOPT)
	}
	var err error
	if ho == unix.SO_LINGER && hl == unix.SOL_SOCKET {
		if vlen < 8 {
			return n.fail(t, aEINVAL)
		}
		err = unix.SetsockoptLinger(s.fd, hl, ho, &unix.Linger{Onoff: int32(a.peek32(val)), Linger: int32(a.peek32(val + 4))})
	} else {
		var v uint32
		switch {
		case vlen >= 4:
			v = a.peek32(val)
		case vlen >= 1:
			v = uint32(a.peek8(val))
		}
		err = unix.SetsockoptInt(s.fd, hl, ho, int(int32(v)))
	}
	if err != nil {
		return n.fail(t, amigaErrno(err))
	}
	return 0, false
}

func (n *netBoard) getsockopt(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	s := t.sock(op.d(0))
	if s == nil {
		return n.fail(t, aEBADF)
	}
	level, opt, val, lenp := op.d(1), op.d(2), op.a(0), op.a(1)
	put := func(vs ...uint32) {
		for i, v := range vs {
			a.poke32(val+uint32(4*i), v)
		}
		if lenp != 0 {
			a.poke32(lenp, uint32(4*len(vs)))
		}
	}
	tv := func(c uint64) {
		put(uint32(c/n.cyclesPerSecond()), uint32(c%n.cyclesPerSecond()*1e6/n.cyclesPerSecond()))
	}
	switch {
	case level == 0xFFFF && opt == 0x1005:
		tv(s.sndTimeo)
		return 0, false
	case level == 0xFFFF && opt == 0x1006:
		tv(s.rcvTimeo)
		return 0, false
	case level == 0xFFFF && opt == 0x1008: // SO_TYPE
		put(uint32(s.typ))
		return 0, false
	}
	hl, ho, ok := hostSockopt(level, opt)
	if !ok {
		return n.fail(t, aENOPROTOOPT)
	}
	if ho == unix.SO_LINGER && hl == unix.SOL_SOCKET {
		l, err := unix.GetsockoptLinger(s.fd, hl, ho)
		if err != nil {
			return n.fail(t, amigaErrno(err))
		}
		put(uint32(l.Onoff), uint32(l.Linger))
		return 0, false
	}
	v, err := unix.GetsockoptInt(s.fd, hl, ho)
	if err != nil {
		return n.fail(t, amigaErrno(err))
	}
	if ho == unix.SO_ERROR && hl == unix.SOL_SOCKET && v != 0 {
		v = int(amigaErrno(unix.Errno(v)))
	}
	put(uint32(v))
	return 0, false
}

func (n *netBoard) ioctl(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	s := t.sock(op.d(0))
	if s == nil {
		return n.fail(t, aEBADF)
	}
	switch op.d(1) {
	case 0x8004667E: // FIONBIO
		s.nonblock = a.peek32(op.a(0)) != 0
	case 0x8004667D: // FIOASYNC: no SIGIO, accepted
	case 0x4004667F: // FIONREAD
		v, err := unix.IoctlGetInt(s.fd, hostFIONREAD)
		if err != nil {
			return n.fail(t, amigaErrno(err))
		}
		a.poke32(op.a(0), uint32(v))
	case 0x40047307: // SIOCATMARK
		a.poke32(op.a(0), 0)
	default:
		return n.fail(t, aEINVAL)
	}
	return 0, false
}

// waitSelect is select(): readiness of descriptors, a timeout, and the
// caller's own signals.
func (n *netBoard) waitSelect(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	nfds := min(int(op.d(0)), len(t.fds))
	sets := [3]uint32{op.a(0), op.a(1), op.a(2)}
	tvp, maskp := op.a(3), op.d(1)
	isSet := func(set uint32, fd int) bool {
		return set != 0 && a.peek32(set+uint32(fd/32*4))&(1<<(fd%32)) != 0
	}
	var pfds []unix.PollFd
	var which []int
	for fd := 0; fd < nfds; fd++ {
		var ev int16
		if isSet(sets[0], fd) {
			ev |= unix.POLLIN
		}
		if isSet(sets[1], fd) {
			ev |= unix.POLLOUT
		}
		if isSet(sets[2], fd) {
			ev |= unix.POLLPRI
		}
		if ev == 0 {
			continue
		}
		s := t.sock(uint32(fd))
		if s == nil {
			return n.fail(t, aEBADF)
		}
		pfds = append(pfds, unix.PollFd{Fd: int32(s.fd), Events: ev})
		which = append(which, fd)
	}
	if !op.started {
		op.started = true
		if maskp != 0 {
			op.userMask = a.peek32(maskp)
		}
		if tvp != 0 {
			if c := n.timeval(tvp); c > 0 {
				op.deadline = a.cycles + c
			}
		}
	}
	// writeSets clears the sets and sets the ready bits; it returns how
	// many were set.
	writeSets := func(ready []unix.PollFd) int32 {
		for _, set := range sets {
			if set != 0 {
				for w := 0; w < (nfds+31)/32; w++ {
					a.poke32(set+uint32(4*w), 0)
				}
			}
		}
		count := int32(0)
		for i, p := range ready {
			fd := which[i]
			for k, bit := range []int16{unix.POLLIN | unix.POLLHUP | unix.POLLERR, unix.POLLOUT | unix.POLLHUP | unix.POLLERR, unix.POLLPRI} {
				if sets[k] != 0 && p.Events&[]int16{unix.POLLIN, unix.POLLOUT, unix.POLLPRI}[k] != 0 && p.Revents&bit != 0 {
					addr := sets[k] + uint32(fd/32*4)
					a.poke32(addr, a.peek32(addr)|1<<(fd%32))
					count++
				}
			}
		}
		return count
	}
	if op.gotUser != 0 { // the caller's signals came first
		writeSets(nil)
		a.poke32(maskp, op.gotUser)
		return 0, false
	}
	if len(pfds) > 0 {
		if _, err := unix.Poll(pfds, 0); err != nil && err != unix.EINTR {
			return n.fail(t, amigaErrno(err))
		}
	}
	got := writeSetsCount(pfds)
	if got > 0 || (tvp != 0 && op.deadline == 0) || (op.deadline != 0 && a.cycles >= op.deadline) {
		r := writeSets(pfds)
		if maskp != 0 {
			a.poke32(maskp, 0)
		}
		return r, false
	}
	op.fds = pfds
	return 0, true
}

// writeSetsCount counts the descriptors poll found ready for what was
// asked.
func writeSetsCount(pfds []unix.PollFd) int {
	c := 0
	for _, p := range pfds {
		if p.Revents&(p.Events|unix.POLLHUP|unix.POLLERR) != 0 {
			c++
		}
	}
	return c
}

func (n *netBoard) dup2(t *sockTask, op *pendingOp) (int32, bool) {
	fd1, fd2 := int32(op.d(0)), int32(op.d(1))
	var s *hsock
	if fd1 == -1 {
		s = &hsock{fd: -1, refs: 1} // reserve fd2
	} else {
		if s = t.sock(uint32(fd1)); s == nil {
			return n.fail(t, aEBADF)
		}
	}
	if fd2 == -1 {
		for i, f := range t.fds {
			if f == nil {
				fd2 = int32(i)
				break
			}
		}
		if fd2 == -1 {
			return n.fail(t, aEMFILE)
		}
	}
	if int(fd2) < 0 || int(fd2) >= len(t.fds) {
		return n.fail(t, aEBADF)
	}
	if old := t.fds[fd2]; old != nil {
		old.release()
	}
	if s.fd >= 0 {
		s.refs++
	}
	t.fds[fd2] = s
	return fd2, false
}

// iovecs reads a struct msghdr's buffers.
func (n *netBoard) iovecs(msg uint32) (addrs []uint32, lens []uint32) {
	a := n.amiga
	iov, cnt := a.peek32(msg+8), a.peek32(msg+12)
	for i := uint32(0); i < min(cnt, 64); i++ {
		addrs = append(addrs, a.peek32(iov+8*i))
		lens = append(lens, a.peek32(iov+8*i+4))
	}
	return
}

func (n *netBoard) sendmsg(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	msg := op.a(0)
	var to unix.Sockaddr
	if name := a.peek32(msg); name != 0 {
		var e int32
		if to, e = n.readSockaddr(name, a.peek32(msg+4)); e != 0 {
			return n.fail(t, e)
		}
	}
	addrs, lens := n.iovecs(msg)
	var bufs [][]byte
	for i := range addrs {
		b := make([]byte, min(lens[i], 1<<20))
		a.peekBytes(addrs[i], b)
		bufs = append(bufs, b)
	}
	return n.send(t, op, op.d(0), bufs, to, op.d(1))
}

func (n *netBoard) recvmsg(t *sockTask, op *pendingOp) (int32, bool) {
	a := n.amiga
	msg := op.a(0)
	addrs, lens := n.iovecs(msg)
	total := uint32(0)
	for _, l := range lens {
		total += l
	}
	buf := make([]byte, min(total, 1<<20))
	got, sa, res, pend := n.recv(t, op, op.d(0), buf, op.d(1))
	if res < 0 || pend {
		return res, pend
	}
	rest := buf[:got]
	for i := range addrs {
		k := min(int(lens[i]), len(rest))
		a.pokeBytes(addrs[i], rest[:k])
		rest = rest[k:]
	}
	if name := a.peek32(msg); name != 0 && sa != nil {
		n.writeSockaddr(name, msg+4, sa)
	}
	a.poke32(msg+20, 0) // msg_controllen
	a.poke32(msg+24, 0) // msg_flags
	return res, false
}

// ---- Names ----

func (n *netBoard) gethostbyname(t *sockTask, op *pendingOp) (int32, bool) {
	name := n.peekString(op.a(0), 256)
	if v, ok := parseInetAddr(name); ok && strings.Count(name, ".") == 3 {
		ip := net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
		return int32(n.fillHostent(t, name, []net.IP{ip})), false
	}
	if !op.started {
		op.started = true
		l := &lookup{}
		op.lookup = l
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			ips, _ := net.DefaultResolver.LookupIP(ctx, "ip4", name)
			l.finish(ips, nil)
		}()
		return 0, true
	}
	if !op.lookup.isDone() {
		return 0, true
	}
	if len(op.lookup.ips) == 0 {
		t.setHerrno(n.amiga, 1) // HOST_NOT_FOUND
		return 0, false
	}
	return int32(n.fillHostent(t, name, op.lookup.ips)), false
}

func (n *netBoard) gethostbyaddr(t *sockTask, op *pendingOp) (int32, bool) {
	if op.d(0) != 4 || op.d(1) != 2 {
		t.setHerrno(n.amiga, 1)
		return 0, false
	}
	var b [4]byte
	n.amiga.peekBytes(op.a(0), b[:])
	ip := net.IPv4(b[0], b[1], b[2], b[3])
	if !op.started {
		op.started = true
		l := &lookup{}
		op.lookup = l
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			names, _ := net.DefaultResolver.LookupAddr(ctx, ip.String())
			l.finish(nil, names)
		}()
		return 0, true
	}
	if !op.lookup.isDone() {
		return 0, true
	}
	if len(op.lookup.names) == 0 {
		t.setHerrno(n.amiga, 1)
		return 0, false
	}
	return int32(n.fillHostent(t, strings.TrimSuffix(op.lookup.names[0], "."), []net.IP{ip})), false
}

// fillHostent builds a struct hostent in the task's buffer.
func (n *netBoard) fillHostent(t *sockTask, name string, ips []net.IP) uint32 {
	a := n.amiga
	h := t.buf + bufHostent
	var v4 [][4]byte
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil && len(v4) < 8 {
			v4 = append(v4, [4]byte(ip4))
		}
	}
	aliases := h + 20
	list := aliases + 4
	addrs := list + 36
	nameAt := addrs + 32
	if len(name) > 127 {
		name = name[:127]
	}
	a.poke32(h, nameAt)
	a.poke32(h+4, aliases)
	a.poke32(h+8, 2) // AF_INET
	a.poke32(h+12, 4)
	a.poke32(h+16, list)
	a.poke32(aliases, 0)
	for i, ip := range v4 {
		a.poke32(list+uint32(4*i), addrs+uint32(4*i))
		a.pokeBytes(addrs+uint32(4*i), ip[:])
	}
	a.poke32(list+uint32(4*len(v4)), 0)
	a.pokeString(nameAt, name)
	return h
}

type servEntry struct {
	name  string
	port  int
	proto string
}

var services = []servEntry{
	{"echo", 7, "tcp"}, {"echo", 7, "udp"}, {"discard", 9, "tcp"}, {"daytime", 13, "tcp"},
	{"ftp-data", 20, "tcp"}, {"ftp", 21, "tcp"}, {"ssh", 22, "tcp"}, {"telnet", 23, "tcp"},
	{"smtp", 25, "tcp"}, {"time", 37, "tcp"}, {"domain", 53, "udp"}, {"domain", 53, "tcp"},
	{"gopher", 70, "tcp"}, {"finger", 79, "tcp"}, {"http", 80, "tcp"}, {"www", 80, "tcp"},
	{"pop3", 110, "tcp"}, {"sunrpc", 111, "udp"}, {"auth", 113, "tcp"}, {"nntp", 119, "tcp"},
	{"ntp", 123, "udp"}, {"imap", 143, "tcp"}, {"snmp", 161, "udp"}, {"irc", 194, "tcp"},
	{"https", 443, "tcp"}, {"login", 513, "tcp"}, {"shell", 514, "tcp"}, {"printer", 515, "tcp"},
	{"submission", 587, "tcp"}, {"imaps", 993, "tcp"}, {"pop3s", 995, "tcp"}, {"ircd", 6667, "tcp"},
}

func lookupServ(name string, port int, proto string) *servEntry {
	for i := range services {
		s := &services[i]
		if (name != "" && s.name == name || port >= 0 && s.port == port) && (proto == "" || s.proto == proto) {
			return s
		}
	}
	return nil
}

// fillServent builds a struct servent: s_name, s_aliases, s_port, s_proto.
func (n *netBoard) fillServent(t *sockTask, e *servEntry) uint32 {
	a := n.amiga
	p := t.buf + bufServent
	aliases, nameAt, protoAt := p+16, p+20, p+52
	a.poke32(p, nameAt)
	a.poke32(p+4, aliases)
	a.poke32(p+8, uint32(e.port))
	a.poke32(p+12, protoAt)
	a.poke32(aliases, 0)
	a.pokeString(nameAt, e.name)
	a.pokeString(protoAt, e.proto)
	return p
}

type protoEntry struct {
	name string
	num  int
}

var protocols = []protoEntry{{"ip", 0}, {"icmp", 1}, {"tcp", 6}, {"udp", 17}}

// fillProtoent builds a struct protoent: p_name, p_aliases, p_proto.
func (n *netBoard) fillProtoent(t *sockTask, e *protoEntry) uint32 {
	a := n.amiga
	p := t.buf + bufServent
	aliases, nameAt := p+12, p+16
	a.poke32(p, nameAt)
	a.poke32(p+4, aliases)
	a.poke32(p+8, uint32(e.num))
	a.poke32(aliases, 0)
	a.pokeString(nameAt, e.name)
	return p
}

// parseInetAddr is inet_addr(): a.b.c.d, a.b.c, a.b or a, each part
// decimal, octal (0) or hex (0x).
func parseInetAddr(s string) (uint32, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) == 0 || len(parts) > 4 {
		return 0, false
	}
	var v []uint64
	for _, p := range parts {
		x, err := strconv.ParseUint(p, 0, 32)
		if err != nil {
			return 0, false
		}
		v = append(v, x)
	}
	switch len(v) {
	case 1:
		return uint32(v[0]), true
	case 2:
		if v[0] > 255 || v[1] > 0xFFFFFF {
			return 0, false
		}
		return uint32(v[0]<<24 | v[1]), true
	case 3:
		if v[0] > 255 || v[1] > 255 || v[2] > 0xFFFF {
			return 0, false
		}
		return uint32(v[0]<<24 | v[1]<<16 | v[2]), true
	}
	for _, x := range v {
		if x > 255 {
			return 0, false
		}
	}
	return uint32(v[0]<<24 | v[1]<<16 | v[2]<<8 | v[3]), true
}

// parseInetNetwork is inet_network(): the parts make a network number.
func parseInetNetwork(s string) (uint32, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) == 0 || len(parts) > 4 {
		return 0, false
	}
	v := uint32(0)
	for _, p := range parts {
		x, err := strconv.ParseUint(p, 0, 8)
		if err != nil {
			return 0, false
		}
		v = v<<8 | uint32(x)
	}
	return v, true
}

// tagList is SocketBaseTagList: it returns 0, or the number of the first
// tag it doesn't know.
func (n *netBoard) tagList(t *sockTask, tags uint32) int32 {
	a := n.amiga
	idx := int32(0)
	for i := 0; i < 256 && tags != 0; i++ {
		tag, data := a.peek32(tags), a.peek32(tags+4)
		switch tag {
		case 0: // TAG_END
			return 0
		case 1: // TAG_IGNORE
			tags += 8
			continue
		case 2: // TAG_MORE
			tags = data
			continue
		case 3: // TAG_SKIP
			tags += 8 * (data + 1)
			continue
		}
		idx++
		if tag&0x80000000 == 0 {
			return idx
		}
		code, set, ref := tag>>1&0x3FFF, tag&1 != 0, tag&0x8000 != 0
		// The value: in ti_Data, or where it points.
		get := func() uint32 {
			if ref {
				return a.peek32(data)
			}
			return data
		}
		put := func(v uint32) {
			if ref {
				a.poke32(data, v)
			} else {
				a.poke32(tags+4, v)
			}
		}
		switch code {
		case 1: // SBTC_BREAKMASK
			if set {
				t.breakMask = get()
			} else {
				put(t.breakMask)
			}
		case 2, 3, 4: // SIGIO, SIGURG, SIGEVENT masks: accepted, never raised
			if !set {
				put(0)
			}
		case 6: // SBTC_ERRNO
			if set {
				t.setErrno(a, int32(get()))
			} else {
				put(uint32(t.errno))
			}
		case 7: // SBTC_HERRNO
			if set {
				t.setHerrno(a, int32(get()))
			} else {
				put(uint32(t.herrno))
			}
		case 8: // SBTC_DTABLESIZE
			if set {
				if size := int(get()); size > len(t.fds) && size <= 1024 {
					t.fds = append(t.fds, make([]*hsock, size-len(t.fds))...)
				}
			} else {
				put(uint32(len(t.fds)))
			}
		case 9, 10, 11, 12, 13: // fd callback, syslog settings: accepted
			if !set {
				put(0)
			}
		case 14, 15, 16, 17, 18: // errno strings: in goes the number, out a string
			text := errnoText[int32(get())]
			if code == 15 {
				text = map[uint32]string{1: "Unknown host", 2: "Host name lookup failure",
					3: "Unknown server error", 4: "No address associated with name"}[get()]
			}
			if text == "" {
				text = "Unknown error"
			}
			a.pokeString(t.buf+bufErrStr, text)
			put(t.buf + bufErrStr)
		case 21, 22, 24: // SBTC_ERRNOBYTEPTR, WORDPTR, LONGPTR
			if set {
				t.errnoPtr = get()
				t.errnoSize = map[uint32]uint32{21: 1, 22: 2, 24: 4}[code]
			} else {
				put(t.errnoPtr)
			}
		case 25: // SBTC_HERRNOLONGPTR
			if set {
				t.herrnoPtr = get()
			} else {
				put(t.herrnoPtr)
			}
		case 29: // SBTC_RELEASESTRPTR
			if !set {
				a.pokeString(t.buf+bufErrStr, "goAmiga bsdsocket.library (host sockets)")
				put(t.buf + bufErrStr)
			}
		default:
			return idx
		}
		tags += 8
	}
	return 0
}
