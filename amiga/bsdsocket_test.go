//go:build darwin || linux

package amiga

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// pcAsm assembles position-independent 68000 code (no relocations), for
// test programs run from disk.
type pcAsm struct {
	code   []byte
	labels map[string]int
	fix    []struct {
		pos   int
		label string
	}
}

func (s *pcAsm) w(ws ...uint16) {
	for _, v := range ws {
		s.code = binary.BigEndian.AppendUint16(s.code, v)
	}
}
func (s *pcAsm) label(l string) { s.labels[l] = len(s.code) }

// rel emits a word displacement to label from this word's address, as
// d16(PC) operands and word branches use.
func (s *pcAsm) rel(l string) {
	s.fix = append(s.fix, struct {
		pos   int
		label string
	}{len(s.code), l})
	s.w(0)
}
func (s *pcAsm) lea(reg int, l string)   { s.w(0x41FA | uint16(reg)<<9); s.rel(l) } // lea l(pc),aN
func (s *pcAsm) jsr(lvo int16)           { s.w(0x4EAE, uint16(lvo)) }
func (s *pcAsm) moveq(n int8, reg int)   { s.w(0x7000 | uint16(reg)<<9 | uint16(uint8(n))) }
func (s *pcAsm) bra(op uint16, l string) { s.w(op); s.rel(l) } // $6700 beq.w, $6B00 bmi.w
func (s *pcAsm) bytes(l string, b []byte) {
	s.label(l)
	s.code = append(s.code, b...)
	if len(s.code)%2 != 0 {
		s.code = append(s.code, 0)
	}
}
func (s *pcAsm) link() []byte {
	for _, f := range s.fix {
		binary.BigEndian.PutUint16(s.code[f.pos:], uint16(s.labels[f.label]-f.pos))
	}
	return s.code
}

// hunkExecutable wraps code in an AmigaDOS load file with one code hunk.
func hunkExecutable(code []byte) []byte {
	for len(code)%4 != 0 {
		code = append(code, 0)
	}
	n := uint32(len(code) / 4)
	var b []byte
	for _, v := range []uint32{0x3F3, 0, 1, 0, 0, n, 0x3E9, n} {
		b = binary.BigEndian.AppendUint32(b, v)
	}
	b = append(b, code...)
	return binary.BigEndian.AppendUint32(b, 0x3F2)
}

// ofsReplace overwrites the contents of file dir/name in an OFS image with
// data, which must fit in the file's existing data blocks.
func ofsReplace(t *testing.T, adf []byte, dir, name string, data []byte) {
	t.Helper()
	blk := func(n uint32) []byte { return adf[n*512 : n*512+512] }
	l := func(b []byte, i int) uint32 { return binary.BigEndian.Uint32(b[4*i:]) }
	hash := func(s string) int {
		h := uint32(len(s))
		for _, c := range strings.ToUpper(s) {
			h = (h*13 + uint32(c)) & 0x7FF
		}
		return int(h % 72)
	}
	find := func(d []byte, s string) []byte {
		for k := l(d, 6+hash(s)); k != 0; {
			e := blk(k)
			if strings.EqualFold(string(e[0x1B1:0x1B1+int(e[0x1B0])]), s) {
				return e
			}
			k = l(e, 124)
		}
		t.Fatalf("%s not found", s)
		return nil
	}
	h := find(find(blk(880), dir), name)
	blocks := int(l(h, 2))
	need := (len(data) + 487) / 488
	if need > blocks {
		t.Fatalf("%s: %d bytes don't fit in %d blocks", name, len(data), blocks)
	}
	for i := 0; i < blocks; i++ {
		if i >= need {
			binary.BigEndian.PutUint32(h[4*(77-i):], 0)
			continue
		}
		db := blk(l(h, 77-i))
		chunk := data[i*488 : min(len(data), (i+1)*488)]
		next := uint32(0)
		if i+1 < need {
			next = l(h, 77-i-1)
		}
		binary.BigEndian.PutUint32(db[8:], uint32(i+1))
		binary.BigEndian.PutUint32(db[12:], uint32(len(chunk)))
		binary.BigEndian.PutUint32(db[16:], next)
		clear(db[24:])
		copy(db[24:], chunk)
		ofsFixChecksum(db)
	}
	binary.BigEndian.PutUint32(h[8:], uint32(need))
	binary.BigEndian.PutUint32(h[0x144:], uint32(len(data)))
	ofsFixChecksum(h)
}

// An Amiga program uses bsdsocket.library over the host's network: it
// looks up "localhost" (an asynchronous lookup), connects to a server here,
// sends a greeting, waits in recv() for the reply (a blocking call: its task
// sleeps until the board's interrupt wakes it) and echoes the reply back.
func TestBSDSocketTCP(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	adf, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s := &pcAsm{labels: map[string]int{}}
	s.w(0x2C78, 0x0004) // movea.l 4.w,a6
	s.lea(1, "libName")
	s.moveq(3, 0)
	s.jsr(-552) // OpenLibrary
	s.w(0x4A80) // tst.l d0
	s.bra(0x6700, "fail")
	s.w(0x2C40) // movea.l d0,a6
	s.lea(0, "hostName")
	s.jsr(-210) // gethostbyname
	s.w(0x4A80)
	s.bra(0x6700, "close")
	s.w(0x2040, 0x2068, 0x0010, 0x2050, 0x2C10) // a0 = hostent, h_addr_list, [0]; d6 = address
	s.lea(1, "sockaddr")
	s.w(0x2346, 0x0004) // move.l d6,4(a1)
	s.moveq(2, 0)
	s.moveq(1, 1)
	s.moveq(0, 2)
	s.jsr(-30)  // socket(AF_INET, SOCK_STREAM, 0)
	s.w(0x2E00) // move.l d0,d7
	s.bra(0x6B00, "close")
	s.w(0x2007) // move.l d7,d0
	s.lea(0, "sockaddr")
	s.moveq(16, 1)
	s.jsr(-54) // connect
	s.w(0x4A80)
	s.bra(0x6B00, "closeSock")
	s.w(0x2007)
	s.lea(0, "hello")
	s.moveq(int8(len("hello amiga\n")), 1)
	s.moveq(0, 2)
	s.jsr(-66) // send
	s.w(0x2007)
	s.lea(0, "buf")
	s.moveq(64, 1)
	s.moveq(0, 2)
	s.jsr(-78)  // recv: waits for the server
	s.w(0x2200) // move.l d0,d1
	s.bra(0x6B00, "closeSock")
	s.w(0x2007)
	s.lea(0, "buf")
	s.moveq(0, 2)
	s.jsr(-66) // send it back
	s.label("closeSock")
	s.w(0x2007)
	s.jsr(-120) // CloseSocket
	s.label("close")
	s.w(0x224E, 0x2C78, 0x0004) // movea.l a6,a1; movea.l 4.w,a6
	s.jsr(-414)                 // CloseLibrary
	s.label("fail")
	s.moveq(0, 0)
	s.w(0x4E75)
	s.bytes("libName", []byte("bsdsocket.library\x00"))
	s.bytes("hostName", []byte("localhost\x00"))
	s.bytes("hello", []byte("hello amiga\n"))
	sa := make([]byte, 16)
	sa[0], sa[1] = 16, 2
	binary.BigEndian.PutUint16(sa[2:], uint16(port))
	s.bytes("sockaddr", sa)
	s.bytes("buf", make([]byte, 64))

	ofsReplace(t, adf, "c", "DiskDoctor", hunkExecutable(s.link()))
	b := adf[1184*512 : 1185*512] // s/startup-sequence
	old := []byte(`echo "Amiga Workbench Disk. Release 1.3.3 version 34.34"`)
	i := bytes.Index(b, old)
	if i < 0 {
		t.Skip("startup-sequence not as expected on this ADF")
	}
	copy(b[i:], append([]byte("DiskDoctor"), bytes.Repeat([]byte(" "), len(old)-10)...))
	ofsFixChecksum(b)

	a, _ := New(rom, nil)
	a.EnableBSDSocket()
	a.Disk.InsertDisk(adf)
	if testing.Verbose() {
		a.Net.Trace = os.Stdout
	}
	a.Reset()

	type result struct {
		greeting, echo string
		err            error
	}
	done := make(chan result, 1)
	go func() {
		ln.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second))
		c, err := ln.Accept()
		if err != nil {
			done <- result{err: err}
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(30 * time.Second))
		r := bufio.NewReader(c)
		g, err := r.ReadString('\n')
		if err != nil {
			done <- result{err: err}
			return
		}
		time.Sleep(200 * time.Millisecond) // so the Amiga is waiting in recv()
		c.Write([]byte("pong from host\n"))
		e, err := r.ReadString('\n')
		done <- result{g, e, err}
	}()
	for f := 0; f < 6000; f++ {
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("server: %v", r.err)
			}
			if r.greeting != "hello amiga\n" || r.echo != "pong from host\n" {
				t.Fatalf("greeting %q, echo %q", r.greeting, r.echo)
			}
			if a.Net.waits < 2 { // the lookup and recv() at least
				t.Errorf("%d calls waited; want the lookup and recv() to", a.Net.waits)
			}
			return
		default:
		}
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
		if f%100 == 99 {
			time.Sleep(10 * time.Millisecond) // let the network goroutines run
		}
	}
	t.Fatal("the Amiga program didn't complete the exchange")
}

// runAmigaProgram boots Workbench 1.3 with bsdsocket.library and runs code
// from the startup-sequence, while host runs alongside; it fails the test
// if host doesn't finish.
func runAmigaProgram(t *testing.T, code []byte, host func() error) {
	t.Helper()
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	adf, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	ofsReplace(t, adf, "c", "DiskDoctor", hunkExecutable(code))
	b := adf[1184*512 : 1185*512]
	old := []byte(`echo "Amiga Workbench Disk. Release 1.3.3 version 34.34"`)
	i := bytes.Index(b, old)
	if i < 0 {
		t.Skip("startup-sequence not as expected on this ADF")
	}
	copy(b[i:], append([]byte("DiskDoctor"), bytes.Repeat([]byte(" "), len(old)-10)...))
	ofsFixChecksum(b)
	a, _ := New(rom, nil)
	a.EnableBSDSocket()
	a.Disk.InsertDisk(adf)
	if testing.Verbose() {
		a.Net.Trace = os.Stdout
	}
	a.Reset()
	done := make(chan error, 1)
	go func() { done <- host() }()
	for f := 0; f < 6000; f++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		default:
		}
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
		if f%100 == 99 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("timed out")
}

// The Amiga as a server: bind, listen, WaitSelect with a timeout, accept,
// recv, and errno for a bad descriptor.
func TestBSDSocketServer(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0") // find a free port
	if err != nil {
		t.Skip(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	s := &pcAsm{labels: map[string]int{}}
	s.w(0x2C78, 0x0004)
	s.lea(1, "libName")
	s.moveq(3, 0)
	s.jsr(-552) // OpenLibrary
	s.w(0x4A80)
	s.bra(0x6700, "fail")
	s.w(0x2C40) // movea.l d0,a6
	s.moveq(2, 0)
	s.moveq(1, 1)
	s.moveq(0, 2)
	s.jsr(-30)  // socket
	s.w(0x2E00) // move.l d0,d7
	s.w(0x2007)
	s.w(0x223C, 0x0000, 0xFFFF) // move.l #SOL_SOCKET,d1
	s.moveq(4, 2)               // SO_REUSEADDR
	s.lea(0, "one")
	s.moveq(4, 3)
	s.jsr(-90) // setsockopt
	s.w(0x2007)
	s.lea(0, "sockaddr")
	s.moveq(16, 1)
	s.jsr(-36) // bind
	s.w(0x4A80)
	s.bra(0x6B00, "close")
	s.w(0x2007)
	s.moveq(5, 1)
	s.jsr(-42) // listen
	// WaitSelect(d7+1, {d7}, NULL, NULL, 10 s, NULL)
	s.moveq(1, 0)
	s.w(0xEFA8) // lsl.l d7,d0
	s.lea(0, "set")
	s.w(0x2080)         // move.l d0,(a0)
	s.w(0x2007, 0x5280) // move.l d7,d0; addq.l #1,d0
	s.w(0x93C9, 0x95CA) // suba.l a1,a1; suba.l a2,a2
	s.lea(3, "tv")
	s.moveq(0, 1)
	s.jsr(-126) // WaitSelect
	s.w(0x4A80)
	s.bra(0x6F00, "close")      // ble: timed out
	s.w(0x2007, 0x91C8, 0x93C9) // move.l d7,d0; suba.l a0,a0; suba.l a1,a1
	s.jsr(-48)                  // accept
	s.w(0x2C00)                 // move.l d0,d6
	s.bra(0x6B00, "close")
	s.w(0x2006)
	s.lea(0, "buf")
	s.moveq(64, 1)
	s.moveq(0, 2)
	s.jsr(-78) // recv
	s.w(0x2200)
	s.w(0x2006)
	s.lea(0, "buf")
	s.moveq(0, 2)
	s.jsr(-66) // send it back
	s.moveq(99, 0)
	s.jsr(-120)         // CloseSocket(99): EBADF
	s.jsr(-162)         // Errno
	s.w(0x0600, 0x0030) // addi.b #'0',d0
	s.lea(0, "buf")
	s.w(0x1080, 0x117C, 0x000A, 0x0001) // move.b d0,(a0); move.b #10,1(a0)
	s.w(0x2006)
	s.moveq(2, 1)
	s.moveq(0, 2)
	s.jsr(-66)
	s.w(0x2006)
	s.jsr(-120)
	s.label("close")
	s.w(0x2007)
	s.jsr(-120)
	s.w(0x224E, 0x2C78, 0x0004)
	s.jsr(-414)
	s.label("fail")
	s.moveq(0, 0)
	s.w(0x4E75)
	s.bytes("libName", []byte("bsdsocket.library\x00"))
	s.bytes("one", []byte{0, 0, 0, 1})
	s.bytes("set", make([]byte, 8))
	s.bytes("tv", []byte{0, 0, 0, 30, 0, 0, 0, 0})
	sa := []byte{16, 2, 0, 0, 127, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(sa[2:], uint16(port))
	s.bytes("sockaddr", sa)
	s.bytes("buf", make([]byte, 64))

	runAmigaProgram(t, s.link(), func() error {
		var c net.Conn
		var err error
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if c, err = net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); err == nil {
				break
			}
		}
		if err != nil {
			return err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(30 * time.Second))
		time.Sleep(200 * time.Millisecond)
		c.Write([]byte("abc\n"))
		r := bufio.NewReader(c)
		echo, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		errno, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if echo != "abc\n" || errno != "9\n" {
			return fmt.Errorf("echo %q, errno line %q; want \"abc\\n\" and EBADF \"9\\n\"", echo, errno)
		}
		return nil
	})
}

func TestInetAddr(t *testing.T) {
	for s, want := range map[string]uint32{
		"127.0.0.1": 0x7F000001, "10.1": 0x0A000001, "192.168.257": 0xC0A80101,
		"0x7f.1": 0x7F000001, "4294967295": 0xFFFFFFFF,
	} {
		if v, ok := parseInetAddr(s); !ok || v != want {
			t.Errorf("inet_addr(%q) = $%08X %v, want $%08X", s, v, ok, want)
		}
	}
	for _, s := range []string{"", "1.2.3.4.5", "256.1.1.1", "a.b.c.d"} {
		if _, ok := parseInetAddr(s); ok {
			t.Errorf("inet_addr(%q) accepted", s)
		}
	}
}

// SocketBaseTagList: set the errno pointer, read the break mask by value
// and by reference, and get an errno string.
func TestSocketBaseTagList(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false) // chip RAM at $000000, not the ROM overlay
	n := newNetBoard(a)
	tk := newSockTask(0x1000, 20, 0, 0x30000)
	const tags, errnoVar, ref = 0x20000, 0x20100, 0x20104
	tag := func(code uint32, set, byRef bool) uint32 {
		v := uint32(0x80000000) | code<<1
		if set {
			v |= 1
		}
		if byRef {
			v |= 0x8000
		}
		return v
	}
	list := []uint32{
		tag(24, true, false), errnoVar, // SBTC_ERRNOLONGPTR = &errnoVar
		tag(1, false, false), 0, // SBTC_BREAKMASK, by value
		tag(1, false, true), ref, // and into *ref
		tag(14, false, false), 61, // SBTC_ERRNOSTRPTR for ECONNREFUSED
		0,
	}
	for i, v := range list {
		a.poke32(tags+uint32(4*i), v)
	}
	if r := n.tagList(tk, tags); r != 0 {
		t.Fatalf("SocketBaseTagList = %d", r)
	}
	if a.peek32(tags+12) != 1<<12 || a.peek32(ref) != 1<<12 {
		t.Errorf("break mask $%X / $%X, want SIGBREAKF_CTRL_C", a.peek32(tags+12), a.peek32(ref))
	}
	if s := n.peekString(a.peek32(tags+28), 64); s != "Connection refused" {
		t.Errorf("errno string %q", s)
	}
	tk.setErrno(a, 35)
	if a.peek32(errnoVar) != 35 {
		t.Errorf("errno variable %d, want 35", a.peek32(errnoVar))
	}
	a.poke32(tags, 0x80001234)
	if r := n.tagList(tk, tags); r != 1 {
		t.Errorf("an unknown tag gave %d, want 1", r)
	}
}
