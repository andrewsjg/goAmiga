package amiga

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// A user-mode network for the emulated Ethernet card, like QEMU's "user"
// networking: the Amiga sits on a private network, 10.0.2.0/24, with this
// code as its gateway, and its connections are carried by the host's own
// sockets, so no privileges or host configuration are needed.
//
//	10.0.2.2   the gateway (and the host's loopback, for connections in)
//	10.0.2.3   DNS: queries go to the host's name server
//	10.0.2.15  the Amiga (any address the Amiga uses works)
//
// The gateway answers ARP for every address on the network but the
// Amiga's, and pings. UDP datagrams go out through a host UDP socket per
// Amiga port and destination. A TCP connection the Amiga opens is
// answered here: the gateway dials the destination from the host,
// completes the handshake once that succeeds (or resets it), then
// carries the data each way, acknowledging the Amiga's segments and
// sending the host's within the Amiga's window, resending what isn't
// acknowledged. Forwarded host ports (127.0.0.1:hostport) open
// connections to the Amiga the same way round.
//
// The link to the Amiga never loses or reorders frames, which keeps the
// TCP side simple; resending covers the Amiga dropping frames when its
// buffers are full.

var (
	unetGateway = net.IPv4(10, 0, 2, 2).To4()
	unetDNS     = net.IPv4(10, 0, 2, 3).To4()
	unetGuest   = net.IPv4(10, 0, 2, 15).To4()
	unetMAC     = []byte{0x52, 0x54, 0x00, 0x12, 0x35, 0x02}
)

const (
	unetMSS      = 1460
	unetWindow   = 32768
	unetResend   = 500 * time.Millisecond
	unetUDPIdle  = 60 * time.Second
	unetMaxQueue = 256
)

type userNet struct {
	mu       sync.Mutex
	out      [][]byte // frames for the Amiga
	guestMAC []byte
	guestIP  net.IP
	dns      string // host name server, host:53
	udp      map[string]*unetUDP
	tcp      map[string]*unetTCP
	forwards []net.Listener
	nextPort uint16
	closed   bool
}

func newUserNet(forwards map[int]int) (*userNet, error) {
	u := &userNet{guestIP: unetGuest, dns: hostNameServer(), udp: map[string]*unetUDP{},
		tcp: map[string]*unetTCP{}, nextPort: 40000}
	for hp, gp := range forwards {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
		if err != nil {
			u.close()
			return nil, fmt.Errorf("forwarding port %d: %v", hp, err)
		}
		u.forwards = append(u.forwards, l)
		go u.accept(l, uint16(gp))
	}
	return u, nil
}

// hostNameServer is the first name server in /etc/resolv.conf, else a
// public one.
func hostNameServer() string {
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "nameserver" && net.ParseIP(fs[1]).To4() != nil {
				return net.JoinHostPort(fs[1], "53")
			}
		}
	}
	return "8.8.8.8:53"
}

func (u *userNet) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	for _, l := range u.forwards {
		l.Close()
	}
	for _, s := range u.udp {
		s.conn.Close()
	}
	for _, c := range u.tcp {
		if c.conn != nil {
			c.conn.Close()
		}
	}
}

// queue adds a frame for the Amiga (with u.mu held).
func (u *userNet) queue(f []byte) {
	if len(u.out) < unetMaxQueue {
		u.out = append(u.out, f)
	}
}

func (u *userNet) receive() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.out) == 0 {
		return nil
	}
	f := u.out[0]
	u.out = u.out[1:]
	return f
}

// ---- Frames from the Amiga ----

func (u *userNet) send(f []byte) {
	if len(f) < 14 {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.guestMAC = append(u.guestMAC[:0], f[6:12]...)
	switch binary.BigEndian.Uint16(f[12:]) {
	case 0x0806:
		u.arp(f[14:])
	case 0x0800:
		u.ip(f[14:])
	}
}

func (u *userNet) ether(typ uint16, payload []byte) []byte {
	f := make([]byte, 14, 14+len(payload))
	copy(f, u.guestMAC)
	if u.guestMAC == nil { // not heard from yet: broadcast
		copy(f, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	}
	copy(f[6:], unetMAC)
	binary.BigEndian.PutUint16(f[12:], typ)
	f = append(f, payload...)
	for len(f) < 60 { // the minimum frame, without the FCS
		f = append(f, 0)
	}
	return f
}

func (u *userNet) arp(p []byte) {
	if len(p) < 28 || binary.BigEndian.Uint16(p[6:]) != 1 { // requests only
		return
	}
	target := net.IP(p[24:28])
	sender := net.IP(p[14:18])
	if !target.Equal(sender) && sender.To4() != nil && !sender.Equal(net.IPv4zero) {
		u.guestIP = append(net.IP(nil), sender.To4()...)
	}
	if target.Equal(u.guestIP) || !(target[0] == 10 && target[1] == 0 && target[2] == 2) {
		return
	}
	r := make([]byte, 28)
	copy(r, p[:6])
	binary.BigEndian.PutUint16(r[6:], 2) // reply
	copy(r[8:], unetMAC)
	copy(r[14:], target)
	copy(r[18:], p[8:14])
	copy(r[24:], p[14:18])
	u.queue(u.ether(0x0806, r))
}

func (u *userNet) ip(p []byte) {
	if len(p) < 20 || p[0]>>4 != 4 {
		return
	}
	hl := int(p[0]&0xF) * 4
	total := int(binary.BigEndian.Uint16(p[2:]))
	if total > len(p) || hl > total || binary.BigEndian.Uint16(p[6:])&0x3FFF != 0 {
		return // truncated, or a fragment
	}
	src, dst := net.IP(p[12:16]), net.IP(p[16:20])
	u.guestIP = append(net.IP(nil), src.To4()...)
	body := p[hl:total]
	switch p[9] {
	case 1:
		u.icmp(dst, body)
	case 17:
		u.udpOut(dst, body)
	case 6:
		u.tcpIn(dst, body)
	}
}

// ipPacket builds an IPv4 packet from src to the Amiga.
func (u *userNet) ipPacket(src net.IP, proto byte, body []byte) []byte {
	p := make([]byte, 20+len(body))
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	p[8] = 64
	p[9] = proto
	copy(p[12:], src.To4())
	copy(p[16:], u.guestIP)
	binary.BigEndian.PutUint16(p[10:], checksum(p[:20], 0))
	copy(p[20:], body)
	return u.ether(0x0800, p)
}

func checksum(b []byte, sum uint32) uint16 {
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum > 0xFFFF {
		sum = sum>>16 + sum&0xFFFF
	}
	return ^uint16(sum)
}

// pseudo is the TCP/UDP pseudo-header sum.
func (u *userNet) pseudo(src net.IP, proto byte, n int) uint32 {
	s := src.To4()
	g := u.guestIP
	word := func(hi, lo byte) uint32 { return uint32(hi)<<8 | uint32(lo) }
	return word(s[0], s[1]) + word(s[2], s[3]) + word(g[0], g[1]) + word(g[2], g[3]) + uint32(proto) + uint32(n)
}

func (u *userNet) icmp(dst net.IP, b []byte) {
	if len(b) < 8 || b[0] != 8 { // echo requests
		return
	}
	r := append([]byte(nil), b...)
	r[0] = 0
	r[2], r[3] = 0, 0
	binary.BigEndian.PutUint16(r[2:], checksum(r, 0))
	u.queue(u.ipPacket(dst, 1, r))
}

// ---- UDP ----

type unetUDP struct {
	conn  *net.UDPConn
	from  net.IP // the address the Amiga sent to (replies come from it)
	sport uint16 // the Amiga's port
	dport uint16
	last  time.Time
}

func (u *userNet) udpOut(dst net.IP, b []byte) {
	if len(b) < 8 {
		return
	}
	sport, dport := binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
	n := min(int(binary.BigEndian.Uint16(b[4:])), len(b))
	if n < 8 {
		return
	}
	data := b[8:n]
	key := fmt.Sprintf("%d>%s:%d", sport, dst, dport)
	s := u.udp[key]
	if s == nil {
		target := net.JoinHostPort(hostIP(dst).String(), fmt.Sprint(dport))
		if dst.Equal(unetDNS) && dport == 53 {
			target = u.dns
		}
		ra, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			return
		}
		c, err := net.DialUDP("udp4", nil, ra)
		if err != nil {
			return
		}
		s = &unetUDP{conn: c, from: append(net.IP(nil), dst.To4()...), sport: sport, dport: dport}
		u.udp[key] = s
		go u.udpRead(key, s)
	}
	s.last = time.Now()
	s.conn.Write(data)
}

// hostIP is where an address on the Amiga's network leads on the host:
// the gateway is the host itself.
func hostIP(ip net.IP) net.IP {
	if ip.Equal(unetGateway) {
		return net.IPv4(127, 0, 0, 1)
	}
	return ip
}

func (u *userNet) udpRead(key string, s *unetUDP) {
	buf := make([]byte, 65536)
	for {
		s.conn.SetReadDeadline(time.Now().Add(unetUDPIdle))
		n, err := s.conn.Read(buf)
		u.mu.Lock()
		if err != nil || u.closed {
			delete(u.udp, key)
			s.conn.Close()
			u.mu.Unlock()
			return
		}
		h := make([]byte, 8+n)
		binary.BigEndian.PutUint16(h, s.dport)
		binary.BigEndian.PutUint16(h[2:], s.sport)
		binary.BigEndian.PutUint16(h[4:], uint16(8+n))
		copy(h[8:], buf[:n])
		binary.BigEndian.PutUint16(h[6:], checksum(h, u.pseudo(s.from, 17, len(h))))
		u.queue(u.ipPacket(s.from, 17, h))
		u.mu.Unlock()
	}
}

// ---- TCP ----

type tcpState int

const (
	tcpSynSent     tcpState = iota // we sent SYN to the Amiga (a forwarded connection)
	tcpDialing                     // the Amiga's SYN came; dialling the host
	tcpSynRcvd                     // we sent SYN|ACK; waiting for its ACK
	tcpEstablished                 // carrying data
	tcpClosed
)

// unetTCP is one connection between the Amiga and a host socket.
type unetTCP struct {
	key            string
	peer           net.IP // the address the Amiga talks to
	peerPort, port uint16 // that address's port; the Amiga's
	state          tcpState
	conn           net.Conn

	rcvNxt         uint32 // next byte expected from the Amiga
	sndUna, sndNxt uint32 // our oldest unacknowledged byte; the next to send
	iss            uint32
	window         uint32 // the Amiga's window
	mss            int

	toGuest  []byte // host data not yet acknowledged by the Amiga, from sndUna
	hostEOF  bool   // the host closed its side: send FIN after the data
	finSent  bool
	guestFIN bool // the Amiga closed its side
	lastSend time.Time
	writes   chan []byte
}

func tcpHeader(sport, dport uint16, seq, ack uint32, flags byte, window uint16, opts []byte) []byte {
	h := make([]byte, 20+len(opts))
	binary.BigEndian.PutUint16(h, sport)
	binary.BigEndian.PutUint16(h[2:], dport)
	binary.BigEndian.PutUint32(h[4:], seq)
	binary.BigEndian.PutUint32(h[8:], ack)
	h[12] = byte(len(h)/4) << 4
	h[13] = flags
	binary.BigEndian.PutUint16(h[14:], window)
	copy(h[20:], opts)
	return h
}

const (
	tcpFIN = 1
	tcpSYN = 2
	tcpRST = 4
	tcpPSH = 8
	tcpACK = 16
)

// segment sends the Amiga a segment of c (with u.mu held).
func (u *userNet) segment(c *unetTCP, seq uint32, flags byte, data []byte) {
	var opts []byte
	if flags&tcpSYN != 0 {
		opts = []byte{2, 4, unetMSS >> 8, unetMSS & 0xFF}
	}
	h := tcpHeader(c.peerPort, c.port, seq, c.rcvNxt, flags|tcpACK*b2u(c.state != tcpSynSent), unetWindow, opts)
	seg := append(h, data...)
	binary.BigEndian.PutUint16(seg[16:], checksum(seg, u.pseudo(c.peer, 6, len(seg))))
	u.queue(u.ipPacket(c.peer, 6, seg))
}

func b2u(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// reset answers a segment with RST (with u.mu held).
func (u *userNet) reset(src net.IP, sport, dport uint16, seq, ack uint32, ackFlag bool) {
	flags := byte(tcpRST)
	if ackFlag {
		flags |= tcpACK
	}
	h := tcpHeader(sport, dport, seq, ack, flags, 0, nil)
	binary.BigEndian.PutUint16(h[16:], checksum(h, u.pseudo(src, 6, len(h))))
	u.queue(u.ipPacket(src, 6, h))
}

func mssOption(h []byte) int {
	opts := h[20:]
	for i := 0; i < len(opts); {
		switch opts[i] {
		case 0:
			return 0
		case 1:
			i++
			continue
		}
		if i+1 >= len(opts) || opts[i+1] < 2 {
			return 0
		}
		if opts[i] == 2 && opts[i+1] == 4 && i+3 < len(opts) {
			return int(binary.BigEndian.Uint16(opts[i+2:]))
		}
		i += int(opts[i+1])
	}
	return 0
}

// tcpIn handles a segment from the Amiga (with u.mu held).
func (u *userNet) tcpIn(dst net.IP, b []byte) {
	if len(b) < 20 {
		return
	}
	sport, dport := binary.BigEndian.Uint16(b), binary.BigEndian.Uint16(b[2:])
	seq, ack := binary.BigEndian.Uint32(b[4:]), binary.BigEndian.Uint32(b[8:])
	off := int(b[12]>>4) * 4
	if off < 20 || off > len(b) {
		return
	}
	flags := b[13]
	window := uint32(binary.BigEndian.Uint16(b[14:]))
	data := b[off:]
	key := fmt.Sprintf("%d>%s:%d", sport, dst, dport)
	c := u.tcp[key]

	if c == nil {
		switch {
		case flags&tcpRST != 0:
			return
		case flags&tcpSYN == 0 || flags&tcpACK != 0:
			u.reset(dst, dport, sport, ack, seq+uint32(len(data)), flags&tcpACK == 0)
			return
		}
		// A new connection: dial the host, answer when that's done.
		c = &unetTCP{key: key, peer: append(net.IP(nil), dst.To4()...), peerPort: dport, port: sport,
			state: tcpDialing, rcvNxt: seq + 1, iss: uint32(time.Now().UnixNano()), window: window,
			mss: 536, writes: make(chan []byte, 64)}
		if m := mssOption(b[:off]); m > 0 {
			c.mss = min(m, unetMSS)
		}
		c.sndUna, c.sndNxt = c.iss, c.iss
		u.tcp[key] = c
		addr := net.JoinHostPort(hostIP(dst).String(), fmt.Sprint(dport))
		go u.dial(c, addr)
		return
	}

	if flags&tcpRST != 0 {
		u.drop(c)
		return
	}
	switch c.state {
	case tcpSynSent: // our SYN to the Amiga: its SYN|ACK
		if flags&(tcpSYN|tcpACK) == tcpSYN|tcpACK && ack == c.iss+1 {
			c.rcvNxt = seq + 1
			c.sndUna, c.sndNxt = ack, ack
			c.window = window
			if m := mssOption(b[:off]); m > 0 {
				c.mss = min(m, unetMSS)
			}
			c.state = tcpEstablished
			u.segment(c, c.sndNxt, 0, nil)
			go u.pump(c)
		}
		return
	case tcpDialing:
		return // still dialling; the Amiga will resend its SYN
	case tcpSynRcvd:
		if flags&tcpSYN != 0 { // its SYN again: answer again
			u.segment(c, c.iss, tcpSYN, nil)
			return
		}
		if flags&tcpACK != 0 && ack == c.iss+1 {
			c.state = tcpEstablished
			c.sndUna, c.sndNxt = ack, ack
		}
	}
	if c.state != tcpEstablished {
		return
	}
	if flags&tcpACK != 0 {
		c.window = window
		if d := int32(ack - c.sndUna); d > 0 && int32(c.sndNxt-ack) >= 0 {
			n := min(int(d), len(c.toGuest))
			c.toGuest = c.toGuest[n:]
			c.sndUna = ack
			c.lastSend = time.Now()
		}
	}
	if len(data) > 0 || flags&tcpFIN != 0 {
		if seq != c.rcvNxt { // a resend of what we have: acknowledge again
			u.segment(c, c.sndNxt, 0, nil)
			return
		}
		if len(data) > 0 && !c.guestFIN {
			select {
			case c.writes <- append([]byte(nil), data...):
				c.rcvNxt += uint32(len(data))
			default: // the host is slow: drop it, the Amiga resends
			}
		}
		if flags&tcpFIN != 0 && !c.guestFIN {
			c.guestFIN = true
			c.rcvNxt++
			close(c.writes)
		}
		u.segment(c, c.sndNxt, 0, nil)
	}
	u.flush(c)
	if c.guestFIN && c.finSent && c.sndUna == c.sndNxt {
		u.drop(c)
	}
}

// dial connects to the host for c, then completes the handshake.
func (u *userNet) dial(c *unetTCP, addr string) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed || u.tcp[c.key] != c {
		if conn != nil {
			conn.Close()
		}
		return
	}
	if err != nil {
		u.reset(c.peer, c.peerPort, c.port, 0, c.rcvNxt, true)
		delete(u.tcp, c.key)
		return
	}
	c.conn = conn
	c.state = tcpSynRcvd
	u.segment(c, c.iss, tcpSYN, nil)
	c.sndNxt = c.iss + 1
	go u.pump(c)
}

// pump copies between the host socket and the connection: reads into
// toGuest, writes from the writes channel.
func (u *userNet) pump(c *unetTCP) {
	u.mu.Lock()
	writes := c.writes
	u.mu.Unlock()
	go func() {
		for b := range writes {
			if _, err := c.conn.Write(b); err != nil {
				break
			}
		}
		if tc, ok := c.conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()
	buf := make([]byte, 16384)
	for {
		n, err := c.conn.Read(buf)
		u.mu.Lock()
		if n > 0 {
			c.toGuest = append(c.toGuest, buf[:n]...)
			u.flush(c)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && u.tcp[c.key] == c && !c.guestFIN {
				u.reset(c.peer, c.peerPort, c.port, c.sndNxt, c.rcvNxt, true)
				u.drop(c)
			} else {
				c.hostEOF = true
				u.flush(c)
			}
			u.mu.Unlock()
			return
		}
		// Don't run far ahead of the Amiga.
		for len(c.toGuest) > 256<<10 && u.tcp[c.key] == c {
			u.mu.Unlock()
			time.Sleep(10 * time.Millisecond)
			u.mu.Lock()
		}
		u.mu.Unlock()
	}
}

// flush sends what the Amiga's window allows (with u.mu held).
func (u *userNet) flush(c *unetTCP) {
	if c.state != tcpEstablished {
		return
	}
	for {
		// The data in flight: what's sent and unacknowledged, less our
		// FIN if that's among it.
		sent := int(c.sndNxt - c.sndUna)
		if c.finSent && sent > 0 {
			sent--
		}
		avail := len(c.toGuest) - sent
		room := int(c.window) - sent
		if avail <= 0 || room <= 0 {
			break
		}
		n := min(avail, room, c.mss)
		u.segment(c, c.sndNxt, tcpPSH, c.toGuest[sent:sent+n])
		c.sndNxt += uint32(n)
		c.lastSend = time.Now()
	}
	if c.hostEOF && !c.finSent && int(c.sndNxt-c.sndUna) == len(c.toGuest) {
		// Everything is sent: the FIN follows it.
		u.segment(c, c.sndNxt, tcpFIN, nil)
		c.sndNxt++
		c.finSent = true
		c.lastSend = time.Now()
	}
}

func (u *userNet) drop(c *unetTCP) {
	if c.conn != nil {
		c.conn.Close()
	}
	if c.writes != nil && !c.guestFIN {
		c.guestFIN = true
		close(c.writes)
	}
	c.state = tcpClosed
	delete(u.tcp, c.key)
}

// tick resends what the Amiga hasn't acknowledged.
func (u *userNet) tick() {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	for _, c := range u.tcp {
		if c.state == tcpSynSent && now.Sub(c.lastSend) > unetResend {
			u.segment(c, c.iss, tcpSYN, nil)
			c.lastSend = now
			continue
		}
		if c.state != tcpEstablished || c.sndUna == c.sndNxt || now.Sub(c.lastSend) < unetResend {
			continue
		}
		// Go back to the oldest unacknowledged byte.
		c.sndNxt = c.sndUna
		fin := c.finSent
		c.finSent = false
		if fin && len(c.toGuest) == 0 {
			c.hostEOF = true
		}
		u.flush(c)
	}
}

// accept takes connections on a forwarded host port and opens them to
// the Amiga.
func (u *userNet) accept(l net.Listener, guestPort uint16) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		u.mu.Lock()
		if u.closed {
			u.mu.Unlock()
			conn.Close()
			return
		}
		u.nextPort++
		if u.nextPort < 40000 {
			u.nextPort = 40000
		}
		c := &unetTCP{peer: unetGateway, peerPort: u.nextPort, port: guestPort, state: tcpSynSent,
			conn: conn, iss: uint32(time.Now().UnixNano()), mss: 536, writes: make(chan []byte, 64)}
		c.key = fmt.Sprintf("%d>%s:%d", guestPort, unetGateway, c.peerPort)
		c.sndUna, c.sndNxt = c.iss, c.iss+1
		u.tcp[c.key] = c
		u.segment(c, c.iss, tcpSYN, nil)
		c.lastSend = time.Now()
		u.mu.Unlock()
	}
}
