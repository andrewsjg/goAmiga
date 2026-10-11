package amiga

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

// NetSerial is a SerialHost that listens on a TCP port: whoever connects
// is on the other end of the Amiga's serial cable. One connection at a
// time; a second is told the port is busy.
//
// In telnet mode it speaks enough of the protocol for a terminal: it asks
// the client for character-at-a-time input with remote echo (so the
// Amiga's echo is what you see), strips option negotiation from the input,
// turns the client's CR LF or CR NUL into the CR the Amiga's terminal
// handlers expect, and sends the Amiga's bare LF as CR LF. Raw mode passes
// bytes through untouched, for file transfers or tools like nc.
type NetSerial struct {
	Telnet bool
	ln     net.Listener

	mu   sync.Mutex
	conn net.Conn
	in   []byte // received, waiting for the Amiga
	out  chan []byte
	tel  telnetState
	gen  int // connection number, to ignore a closed one's writer
	lfCR bool
}

const netSerialMaxIn = 1 << 16

// ListenSerial parses spec — "telnet:PORT", "tcp:PORT", or either with
// HOST:PORT — and starts listening. A bare port listens on the loopback
// interface only.
func ListenSerial(spec string) (*NetSerial, error) {
	mode, addr, ok := strings.Cut(spec, ":")
	if !ok || (mode != "telnet" && mode != "tcp") {
		return nil, fmt.Errorf("serial: want telnet:PORT or tcp:PORT, not %q", spec)
	}
	if !strings.Contains(addr, ":") {
		addr = "127.0.0.1:" + addr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	s := &NetSerial{Telnet: mode == "telnet", ln: ln}
	go s.accept()
	return s, nil
}

// Addr is the address being listened on.
func (s *NetSerial) Addr() net.Addr { return s.ln.Addr() }

// Close stops listening and drops any connection.
func (s *NetSerial) Close() error {
	s.mu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.mu.Unlock()
	return s.ln.Close()
}

func (s *NetSerial) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.conn != nil {
			s.mu.Unlock()
			c.Write([]byte("goAmiga: the serial port is in use\r\n"))
			c.Close()
			continue
		}
		s.gen++
		s.conn, s.in, s.tel = c, nil, telnetState{}
		s.out = make(chan []byte, 256)
		gen, out := s.gen, s.out
		s.mu.Unlock()
		if s.Telnet {
			// IAC WILL ECHO, IAC WILL SUPPRESS-GO-AHEAD, IAC DO SUPPRESS-GO-AHEAD
			c.Write([]byte{255, 251, 1, 255, 251, 3, 255, 253, 3})
		}
		go s.writer(c, out)
		go s.reader(c, gen)
	}
}

func (s *NetSerial) reader(c net.Conn, gen int) {
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		s.mu.Lock()
		if s.gen != gen {
			s.mu.Unlock()
			return
		}
		for _, b := range buf[:n] {
			if s.Telnet {
				var keep bool
				if b, keep = s.tel.input(b); !keep {
					continue
				}
			}
			if len(s.in) < netSerialMaxIn {
				s.in = append(s.in, b)
			}
		}
		if err != nil {
			s.conn.Close()
			s.conn = nil
			close(s.out)
			s.out = nil
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

func (s *NetSerial) writer(c net.Conn, out chan []byte) {
	for p := range out {
		if _, err := c.Write(p); err != nil {
			c.Close() // the reader notices and cleans up
			for range out {
			}
			return
		}
	}
}

// Send passes a byte from the Amiga to the client. With no client it goes
// nowhere, like sending with no cable plugged in.
func (s *NetSerial) Send(b byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.out == nil {
		return
	}
	var p []byte
	switch {
	case !s.Telnet:
		p = []byte{b}
	case b == 255:
		p = []byte{255, 255}
	case b == '\n' && !s.lfCR:
		p = []byte{'\r', '\n'}
	default:
		p = []byte{b}
	}
	s.lfCR = b == '\r'
	select {
	case s.out <- p:
	default: // the client isn't keeping up; drop rather than stall the Amiga
	}
}

// Receive returns the next byte the client sent.
func (s *NetSerial) Receive() (byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.in) == 0 {
		return 0, false
	}
	b := s.in[0]
	s.in = s.in[1:]
	return b, true
}

// Connected reports whether a client is connected (the Amiga sees carrier).
func (s *NetSerial) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

// telnetState strips telnet commands from the input stream.
type telnetState struct {
	state  int // 0 data, 1 after IAC, 2 option byte follows, 3 in subnegotiation, 4 IAC in subnegotiation
	lastCR bool
}

// input takes one byte from the network and reports the data byte, if
// any, it carries.
func (t *telnetState) input(b byte) (byte, bool) {
	switch t.state {
	case 1:
		switch {
		case b == 255: // escaped 255
			t.state = 0
			return 255, true
		case b >= 251 && b <= 254: // WILL, WONT, DO, DONT: an option follows
			t.state = 2
		case b == 250: // SB
			t.state = 3
		default: // NOP, GA, break and the rest
			t.state = 0
		}
		return 0, false
	case 2:
		t.state = 0
		return 0, false
	case 3:
		if b == 255 {
			t.state = 4
		}
		return 0, false
	case 4:
		t.state = 3
		if b == 240 { // SE
			t.state = 0
		}
		return 0, false
	}
	if b == 255 {
		t.state = 1
		return 0, false
	}
	// A client sends Enter as CR LF or CR NUL; the Amiga wants CR.
	if t.lastCR && (b == '\n' || b == 0) {
		t.lastCR = false
		return 0, false
	}
	t.lastCR = b == '\r'
	return b, true
}
