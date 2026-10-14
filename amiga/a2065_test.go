package amiga

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// lanceRig is an A2065 set up as AMIX's setup_lance does: byte swapping,
// the init block at $8000, 8 receive buffers of 1520 bytes and 8
// transmit buffers.
type lanceRig struct {
	t    *testing.T
	a    *Amiga
	c    *a2065
	txi  int
	rxi  int
	base uint32
}

const (
	lrInit = 0x8000
	lrRx   = 0x8018
	lrTx   = 0x8058
	lrRxB  = 0x8100
	lrTxB  = 0xB000
)

func newLanceRig(t *testing.T, forwards map[int]int) *lanceRig {
	t.Helper()
	a := createTestAmiga(t)
	if err := a.EnableEthernet(forwards); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Ethernet.close() })
	a.wireMemoryMap(false)
	a.pokeBytes(0xE8004A, []byte{0x90})
	a.pokeBytes(0xE80048, []byte{0xE0})
	r := &lanceRig{t: t, a: a, c: a.Ethernet, base: 0xE90000}
	// The MAC, from the serial number, as get_auto_serial_number reads it.
	var mac [6]byte
	mac[0], mac[1], mac[2] = 0, 0x80, 0x10
	for i := 0; i < 3; i++ {
		hi := ^a.peek16(r.base+0x1C+uint32(4*i)) & 0xF000 >> 8
		lo := ^a.peek16(r.base+0x1E+uint32(4*i)) & 0xF000 >> 12
		mac[3+i] = byte(hi | lo)
	}
	if !bytes.Equal(mac[:], []byte{0, 0x80, 0x10, 0x0A, 0x20, 0x65}) {
		t.Fatalf("MAC from the serial number: % X", mac)
	}
	w := func(off uint32, v uint16) { a.poke16(r.base+off, v) }
	w(0x4002, 0)
	w(0x4000, csrStop)
	w(0x4002, 3)
	w(0x4000, 4) // BSWP
	w(0x4002, 1)
	w(0x4000, lrInit)
	w(0x4002, 2)
	w(0x4000, 0)
	ib := r.base + lrInit
	a.poke16(ib, 0)
	for i := 0; i < 3; i++ { // byte pairs swapped
		a.poke8(ib+2+uint32(2*i), mac[2*i+1])
		a.poke8(ib+3+uint32(2*i), mac[2*i])
	}
	a.poke16(ib+0x10, lrRx)
	a.poke16(ib+0x12, 3<<13) // 8 descriptors
	a.poke16(ib+0x14, lrTx)
	a.poke16(ib+0x16, 3<<13)
	for i := uint32(0); i < 8; i++ {
		d := r.base + lrRx + 8*i
		a.poke16(d, uint16(lrRxB+1520*i))
		a.poke16(d+2, 0x8000)
		a.poke16(d+4, uint16(0x1000-1520)|0xF000)
		a.poke16(d+6, 0)
		td := r.base + lrTx + 8*i
		a.poke16(td, uint16(lrTxB+1520*i))
		a.poke16(td+2, 0)
	}
	w(0x4002, 0)
	w(0x4000, csrInit|csrInea)
	if a.peek16(r.base+0x4000)&csrIdon == 0 {
		t.Fatal("no IDON after INIT")
	}
	w(0x4000, csrIdon|csrStrt|csrInea)
	if v := a.peek16(r.base + 0x4000); v&(csrTxon|csrRxon) != csrTxon|csrRxon {
		t.Fatalf("CSR0 $%04X after STRT", v)
	}
	return r
}

// send transmits a frame as aenxmit does.
func (r *lanceRig) send(frame []byte) {
	a := r.a
	d := r.base + lrTx + uint32(8*r.txi)
	a.pokeBytes(r.base+lrTxB+uint32(1520*r.txi), frame)
	a.poke16(d+4, uint16(0x1000-len(frame))|0xF000)
	a.poke16(d+2, 0x8000|0x0200|0x0100) // OWN STP ENP
	a.poke16(r.base+0x4000, csrTdmd|csrInea)
	if a.peek16(d+2)&0x8000 != 0 {
		r.t.Fatal("the chip didn't take the transmit descriptor")
	}
	r.txi = (r.txi + 1) % 8
}

// recv waits for a frame in the receive ring and gives the buffer back.
func (r *lanceRig) recv() []byte {
	a := r.a
	for i := 0; i < 400; i++ {
		d := r.base + lrRx + uint32(8*r.rxi)
		if f := a.peek16(d + 2); f&0x8000 == 0 {
			if f&0x0300 != 0x0300 {
				r.t.Fatalf("descriptor flags $%04X: want STP and ENP", f)
			}
			n := int(a.peek16(d+6)&0xFFF) - 4 // less the FCS
			buf := make([]byte, n)
			a.peekBytes(r.base+lrRxB+uint32(1520*r.rxi), buf)
			a.poke16(d+2, 0x8000)
			r.rxi = (r.rxi + 1) % 8
			verifyChecksums(r.t, buf)
			return buf
		}
		r.c.tick(2000)
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatal("no frame received")
	return nil
}

var rigMAC = []byte{0, 0x80, 0x10, 0x0A, 0x20, 0x65}

func ethFrame(dst []byte, typ uint16, payload []byte) []byte {
	f := append(append(append([]byte(nil), dst...), rigMAC...), byte(typ>>8), byte(typ))
	return append(f, payload...)
}

func ipv4(src, dst net.IP, proto byte, body []byte) []byte {
	p := make([]byte, 20+len(body))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[8], p[9] = 64, proto
	copy(p[12:], src.To4())
	copy(p[16:], dst.To4())
	binary.BigEndian.PutUint16(p[10:], checksum(p[:20], 0))
	copy(p[20:], body)
	return p
}

func TestA2065ARPAndPing(t *testing.T) {
	r := newLanceRig(t, nil)
	req := make([]byte, 28)
	binary.BigEndian.PutUint16(req, 1)
	binary.BigEndian.PutUint16(req[2:], 0x0800)
	req[4], req[5] = 6, 4
	binary.BigEndian.PutUint16(req[6:], 1)
	copy(req[8:], rigMAC)
	copy(req[14:], unetGuest)
	copy(req[24:], unetGateway)
	r.send(ethFrame([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, 0x0806, req))
	reply := r.recv()
	if binary.BigEndian.Uint16(reply[12:]) != 0x0806 || binary.BigEndian.Uint16(reply[20:]) != 2 ||
		!bytes.Equal(reply[22:28], unetMAC) {
		t.Fatalf("ARP reply % X", reply)
	}
	if r.a.peek16(r.base+0x4000)&csrRint == 0 {
		t.Error("no RINT")
	}
	// Ping the gateway.
	echo := []byte{8, 0, 0, 0, 0x12, 0x34, 0, 1, 'p', 'i', 'n', 'g'}
	binary.BigEndian.PutUint16(echo[2:], checksum(echo, 0))
	r.send(ethFrame(unetMAC, 0x0800, ipv4(unetGuest, unetGateway, 1, echo)))
	pong := r.recv()
	if pong[23] != 1 || pong[34] != 0 || !bytes.Equal(pong[42:46], []byte("ping")) {
		t.Errorf("echo reply % X", pong)
	}
}

// tcpSeg builds a TCP segment from the Amiga.
func tcpSeg(dst net.IP, sport, dport uint16, seq, ack uint32, flags byte, data []byte) []byte {
	h := tcpHeader(sport, dport, seq, ack, flags, 8192, nil)
	seg := append(h, data...)
	// The checksum isn't checked by the gateway.
	return ethFrame(unetMAC, 0x0800, ipv4(unetGuest, dst, 6, seg))
}

// The Amiga connects to a host service through the gateway: handshake,
// data each way, close.
func TestUserNetTCPOut(t *testing.T) {
	r := newLanceRig(t, nil)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	got := make(chan string, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 5)
		io.ReadFull(c, buf)
		got <- string(buf)
		c.Write([]byte("world"))
	}()
	r.send(tcpSeg(unetGateway, 1025, port, 1000, 0, tcpSYN, nil))
	sa := r.recv()
	tcp := sa[34:]
	if tcp[13] != tcpSYN|tcpACK || binary.BigEndian.Uint32(tcp[8:]) != 1001 {
		t.Fatalf("expected SYN|ACK acknowledging 1001: flags $%02X ack %d", tcp[13], binary.BigEndian.Uint32(tcp[8:]))
	}
	iss := binary.BigEndian.Uint32(tcp[4:])
	r.send(tcpSeg(unetGateway, 1025, port, 1001, iss+1, tcpACK, nil))
	r.send(tcpSeg(unetGateway, 1025, port, 1001, iss+1, tcpACK|tcpPSH, []byte("hello")))
	select {
	case s := <-got:
		if s != "hello" {
			t.Errorf("host got %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the host didn't get the data")
	}
	// The gateway's ACK for "hello", then "world" (either order of
	// arrival: collect until the data shows up).
	var data []byte
	for i := 0; i < 4 && data == nil; i++ {
		f := r.recv()
		seg := f[34:]
		// The payload's length from the IP header: short frames are
		// padded to 60 bytes.
		hl := int(seg[12]>>4) * 4
		if n := int(binary.BigEndian.Uint16(f[16:])) - 20 - hl; n > 0 {
			data = seg[hl : hl+n]
		}
	}
	if string(data) != "world" {
		t.Errorf("the Amiga got %q", data)
	}
}

// A forwarded host port opens a connection to the Amiga.
func TestUserNetForward(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	hp := l.Addr().(*net.TCPAddr).Port
	l.Close()
	r := newLanceRig(t, map[int]int{hp: 23})
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(hp)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	syn := r.recv()
	seg := syn[34:]
	if seg[13] != tcpSYN || binary.BigEndian.Uint16(seg[2:]) != 23 {
		t.Fatalf("expected a SYN to port 23: flags $%02X port %d", seg[13], binary.BigEndian.Uint16(seg[2:]))
	}
	sport := binary.BigEndian.Uint16(seg)
	iss := binary.BigEndian.Uint32(seg[4:])
	// The Amiga accepts and sends a banner.
	r.send(tcpSeg(unetGateway, 23, sport, 5000, iss+1, tcpSYN|tcpACK, nil))
	r.recv() // the gateway's ACK
	r.send(tcpSeg(unetGateway, 23, sport, 5001, iss+1, tcpACK|tcpPSH, []byte("login: ")))
	buf := make([]byte, 7)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "login: " {
		t.Fatalf("the host got %q, %v", buf, err)
	}
}

// UDP goes out through a host socket and the answer comes back.
func TestUserNetUDP(t *testing.T) {
	r := newLanceRig(t, nil)
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	go func() {
		buf := make([]byte, 100)
		n, from, err := pc.ReadFrom(buf)
		if err == nil {
			pc.WriteTo(append([]byte("re:"), buf[:n]...), from)
		}
	}()
	u := make([]byte, 8+4)
	binary.BigEndian.PutUint16(u, 3000)
	binary.BigEndian.PutUint16(u[2:], port)
	binary.BigEndian.PutUint16(u[4:], 12)
	copy(u[8:], "ping")
	r.send(ethFrame(unetMAC, 0x0800, ipv4(unetGuest, unetGateway, 17, u)))
	f := r.recv()
	if f[23] != 17 || string(f[42:49]) != "re:ping" || binary.BigEndian.Uint16(f[36:]) != 3000 {
		t.Errorf("UDP reply % X", f)
	}
}

func itoa(n int) string {
	b := []byte{}
	for n > 0 || len(b) == 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// verifyChecksums checks the IP, TCP and UDP checksums of a frame the
// gateway sent, as the Amiga's stack would.
func verifyChecksums(t *testing.T, f []byte) {
	t.Helper()
	if len(f) < 34 || binary.BigEndian.Uint16(f[12:]) != 0x0800 {
		return
	}
	ip := f[14:]
	if checksum(ip[:20], 0) != 0 {
		t.Errorf("bad IP header checksum")
	}
	total := int(binary.BigEndian.Uint16(ip[2:]))
	body := ip[20:total]
	if ip[9] != 6 && ip[9] != 17 {
		return
	}
	var sum uint32
	for i := 12; i < 20; i += 2 {
		sum += uint32(ip[i])<<8 | uint32(ip[i+1])
	}
	sum += uint32(ip[9]) + uint32(len(body))
	if checksum(body, sum) != 0 {
		t.Errorf("bad %s checksum", map[byte]string{6: "TCP", 17: "UDP"}[ip[9]])
	}
}
