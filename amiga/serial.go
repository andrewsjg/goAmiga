package amiga

// Serial port: Paula's UART and the RS-232 control lines on CIA-B.
//
// Paula has a one-word transmit buffer (SERDAT) in front of a shift
// register, and a receive register read through SERDATR. SERPER sets the
// bit time in colour clocks (period+1; bit 15 selects 9-bit data). A word
// written to SERDAT carries its own stop bits: the shift register sends a
// start bit, then the word LSB first up to its highest set bit. TBE
// (INTREQ bit 0, level 1) is raised when the buffer moves into the shift
// register, and RBF (bit 11, level 5) when a received byte is ready.
// SERDATR's RBF bit is INTREQ's.
//
// The other end of the cable is a SerialHost, such as a telnet or TCP
// connection (serial_host.go). Bytes from it arrive at the baud rate, but
// the next one waits until the Amiga has taken the last (RBF cleared), so
// pasted text isn't lost to overruns. A real UART would overrun; no
// software depends on that.
//
// CIA-B port A carries the modem lines: /DSR (bit 3), /CTS (bit 4) and /CD
// (bit 5) are inputs, active low; RTS (bit 6) and DTR (bit 7) outputs.
// DSR and CTS are always asserted; CD follows the host connection.

// SerialHost is the device on the end of the serial cable.
type SerialHost interface {
	// Send passes on a byte the Amiga transmitted.
	Send(b byte)
	// Receive returns the next byte for the Amiga, if one is waiting.
	Receive() (byte, bool)
	// Connected reports carrier: something is on the other end.
	Connected() bool
}

// Serial is Paula's UART.
type Serial struct {
	amiga *Amiga
	Host  SerialHost

	serper uint16

	txBuf     uint16 // SERDAT, waiting for the shift register
	txBufFull bool
	txShift   uint16 // the word being shifted out
	txLeft    int    // colour clocks until the shift register is empty (0: empty)

	rxData uint16 // the last word received (data and stop bit)
	ovrun  bool
	rxNext byte // a byte on its way in
	rxLeft int  // colour clocks until it has arrived (0: none)

	cpuRem int
}

// NewSerial returns a UART with no cable attached.
func NewSerial(a *Amiga) *Serial { return &Serial{amiga: a} }

// Reset clears the UART (the host connection stays).
func (s *Serial) Reset() {
	h := s.Host
	*s = Serial{amiga: s.amiga, Host: h}
}

// bitTime is one bit in colour clocks.
func (s *Serial) bitTime() int { return int(s.serper&0x7FFF) + 1 }

// frameBits is the number of bits a word takes on the line: the start bit,
// then the word up to and including its highest set bit (the last stop
// bit). A word of zero still sends a start bit.
func frameBits(w uint16) int {
	n := 1
	for ; w != 0; w >>= 1 {
		n++
	}
	return n
}

// WriteSERPER sets the baud rate and word length.
func (s *Serial) WriteSERPER(v uint16) { s.serper = v }

// WriteSERDAT queues a word for transmission.
func (s *Serial) WriteSERDAT(v uint16) {
	s.txBuf, s.txBufFull = v, true
	if s.txLeft == 0 {
		s.loadShifter()
	}
}

// loadShifter moves the buffered word into the shift register: the buffer
// is empty again (TBE) and the word goes out.
func (s *Serial) loadShifter() {
	s.txShift, s.txBufFull = s.txBuf, false
	s.txLeft = frameBits(s.txShift) * s.bitTime()
	if s.amiga != nil && s.amiga.Custom != nil {
		s.amiga.Custom.SetIntreq(1 << 0) // TBE
	}
	if s.Host != nil && s.amiga != nil && s.amiga.Custom.ReadReg16(0x09E)&0x0800 == 0 { // not UARTBRK
		mask := uint16(0xFF)
		if s.serper&0x8000 != 0 {
			mask = 0x1FF
		}
		s.Host.Send(byte(s.txShift & mask))
	}
}

// ReadSERDATR returns the receive data and status register.
func (s *Serial) ReadSERDATR() uint16 {
	v := s.rxData & 0x3FF
	rbf := s.amiga != nil && s.amiga.Custom.ReadReg16(0x09C)&0x0800 != 0
	if rbf {
		v |= 0x4000
		if s.ovrun {
			v |= 0x8000
		}
	} else {
		s.ovrun = false
	}
	if !s.txBufFull {
		v |= 0x2000 // TBE
		if s.txLeft == 0 {
			v |= 0x1000 // TSRE
		}
	}
	v |= 0x0800 // RXD: the line idles high between bytes
	return v
}

// Tick advances the UART by cpuCycles CPU cycles.
func (s *Serial) Tick(cpuCycles int) {
	s.cpuRem += cpuCycles
	cck := s.cpuRem / CPUCyclesPerColorClock
	s.cpuRem %= CPUCyclesPerColorClock
	if cck == 0 {
		return
	}
	if s.txLeft > 0 {
		if s.txLeft -= cck; s.txLeft <= 0 {
			s.txLeft = 0
			if s.txBufFull {
				s.loadShifter()
			}
		}
	}
	if s.rxLeft > 0 {
		if s.rxLeft -= cck; s.rxLeft <= 0 {
			s.rxLeft = 0
			s.receive(s.rxNext)
		}
	}
	// Start the next byte once the last has been taken.
	if s.rxLeft == 0 && s.Host != nil && s.amiga.Custom.ReadReg16(0x09C)&0x0800 == 0 {
		if b, ok := s.Host.Receive(); ok {
			s.rxNext = b
			s.rxLeft = 10 * s.bitTime() // start, 8 data bits, stop
		}
	}
}

// receive completes a byte: it goes into SERDATR with its stop bit.
func (s *Serial) receive(b byte) {
	if s.amiga.Custom.ReadReg16(0x09C)&0x0800 != 0 {
		s.ovrun = true
	}
	stop := uint16(0x100)
	if s.serper&0x8000 != 0 {
		stop = 0x200
	}
	s.rxData = uint16(b) | stop
	s.amiga.Custom.SetIntreq(1 << 11) // RBF
}

// CyclesToNextEvent is the CPU cycles until the UART next raises an
// interrupt, or -1.
func (s *Serial) CyclesToNextEvent() int {
	next := -1
	for _, c := range []int{s.txLeft, s.rxLeft} {
		if c > 0 && (next < 0 || c < next) {
			next = c
		}
	}
	if next > 0 {
		next = next*CPUCyclesPerColorClock - s.cpuRem
	}
	return next
}

// ciabPRA sets the modem status inputs in CIA-B port A.
func (s *Serial) ciabPRA(v byte) byte {
	v &^= 0x18 // DSR, CTS asserted
	v |= 0x20  // no carrier
	if s.Host != nil && s.Host.Connected() {
		v &^= 0x20
	}
	return v
}

func (s *Serial) state(sc *stateCodec) {
	num(sc, &s.serper)
	num(sc, &s.txBuf)
	sc.bool(&s.txBufFull)
	num(sc, &s.txShift)
	num(sc, &s.txLeft)
	num(sc, &s.rxData)
	sc.bool(&s.ovrun)
	num(sc, &s.rxNext)
	num(sc, &s.rxLeft)
	num(sc, &s.cpuRem)
}
