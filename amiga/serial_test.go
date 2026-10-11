package amiga

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"testing"
	"time"
)

// fakeSerialHost records what the Amiga sends and feeds it bytes.
type fakeSerialHost struct {
	sent []byte
	in   []byte
}

func (h *fakeSerialHost) Send(b byte) { h.sent = append(h.sent, b) }
func (h *fakeSerialHost) Receive() (byte, bool) {
	if len(h.in) == 0 {
		return 0, false
	}
	b := h.in[0]
	h.in = h.in[1:]
	return b, true
}
func (h *fakeSerialHost) Connected() bool { return true }

// 9600 baud: SERPER = 3546895/9600 - 1 = 368, 369 colour clocks a bit.
const serper9600 = 368

func TestSerialTransmit(t *testing.T) {
	a := createTestAmiga(t)
	h := &fakeSerialHost{}
	a.Serial.Host = h
	a.Serial.WriteSERPER(serper9600)

	a.Serial.WriteSERDAT(0x0100 | 'A') // 8 data bits, one stop bit
	if a.Custom.ReadReg16(0x09C)&1 == 0 {
		t.Error("no TBE interrupt when the word went to the shift register")
	}
	if string(h.sent) != "A" {
		t.Errorf("host got %q, want \"A\"", h.sent)
	}
	st := a.Serial.ReadSERDATR()
	if st&0x2000 == 0 || st&0x1000 != 0 {
		t.Errorf("SERDATR $%04X: want TBE set, TSRE clear while shifting", st)
	}
	// A second word waits in the buffer: TBE clear until the first is out.
	a.Custom.WriteReg16(0x09C, 0)
	a.Serial.WriteSERDAT(0x0100 | 'B')
	if st := a.Serial.ReadSERDATR(); st&0x2000 != 0 {
		t.Errorf("SERDATR $%04X: TBE set with a word buffered", st)
	}
	// Start bit, 8 data bits, stop bit: 10 bits of 369 colour clocks.
	a.Serial.Tick(10*369*2 - 2)
	if string(h.sent) != "A" {
		t.Fatalf("second byte sent early (%q)", h.sent)
	}
	a.Serial.Tick(2)
	if string(h.sent) != "AB" || a.Custom.ReadReg16(0x09C)&1 == 0 {
		t.Errorf("after one frame: sent %q, TBE %v", h.sent, a.Custom.ReadReg16(0x09C)&1 != 0)
	}
	a.Serial.Tick(10 * 369 * 2)
	if st := a.Serial.ReadSERDATR(); st&0x3000 != 0x3000 {
		t.Errorf("SERDATR $%04X: want TBE and TSRE when idle", st)
	}
}

func TestSerialReceive(t *testing.T) {
	a := createTestAmiga(t)
	h := &fakeSerialHost{in: []byte("hi")}
	a.Serial.Host = h
	a.Serial.WriteSERPER(serper9600)

	a.Serial.Tick(2) // the first byte starts arriving
	a.Serial.Tick(10*369*2 - 2)
	if a.Custom.ReadReg16(0x09C)&0x0800 != 0 {
		t.Fatal("RBF before the byte arrived")
	}
	a.Serial.Tick(2)
	st := a.Serial.ReadSERDATR()
	if a.Custom.ReadReg16(0x09C)&0x0800 == 0 || st&0x4000 == 0 || st&0x1FF != 0x100|'h' {
		t.Fatalf("SERDATR $%04X after a frame: want RBF and 'h' with its stop bit", st)
	}
	// The next byte waits until the Amiga takes this one.
	a.Serial.Tick(30 * 369 * 2)
	if st := a.Serial.ReadSERDATR(); st&0xFF != 'h' || st&0x8000 != 0 {
		t.Fatalf("SERDATR $%04X: the second byte overran the first", st)
	}
	a.Custom.WriteReg16(0x09C, 0) // clear RBF
	a.Serial.Tick(2)
	a.Serial.Tick(10 * 369 * 2)
	if st := a.Serial.ReadSERDATR(); st&0xFF != 'i' {
		t.Errorf("SERDATR $%04X, want 'i'", st)
	}
}

// Carrier on CIA-B: /CD low with a client, /DSR and /CTS always low.
func TestSerialModemLines(t *testing.T) {
	a := createTestAmiga(t)
	if v := a.Serial.ciabPRA(0xFF); v&0x38 != 0x20 {
		t.Errorf("no host: PRA bits 5-3 = %03b, want 100 (no carrier)", v>>3&7)
	}
	a.Serial.Host = &fakeSerialHost{}
	if v := a.Serial.ciabPRA(0xFF); v&0x38 != 0 {
		t.Errorf("connected: PRA bits 5-3 = %03b, want 000", v>>3&7)
	}
}

// Telnet over a real connection: negotiation, CR LF in, LF out.
func TestNetSerialTelnet(t *testing.T) {
	s, err := ListenSerial("telnet:0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	neg := make([]byte, 9)
	if _, err := r.Read(neg); err != nil || neg[0] != 255 || neg[1] != 251 || neg[2] != 1 {
		t.Fatalf("negotiation % X, %v", neg, err)
	}
	for !s.Connected() {
		time.Sleep(time.Millisecond)
	}
	// The client answers an option, types "dir" and Enter, and an escaped 255.
	c.Write([]byte{255, 253, 1, 'd', 'i', 'r', '\r', '\n', 255, 255})
	var got []byte
	for deadline := time.Now().Add(5 * time.Second); len(got) < 5 && time.Now().Before(deadline); {
		if b, ok := s.Receive(); ok {
			got = append(got, b)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	if string(got) != "dir\r\xff" {
		t.Errorf("Amiga received %q, want \"dir\\r\\xff\"", got)
	}
	for _, b := range []byte("ok\n") {
		s.Send(b)
	}
	line, err := r.ReadString('\n')
	if err != nil || line != "ok\r\n" {
		t.Errorf("client read %q, %v; want \"ok\\r\\n\"", line, err)
	}
}

// Workbench 1.3 starts a shell on AUX: (serial.device's terminal handler);
// a command typed over the serial port runs and its output comes back.
func TestSerialAUXShell(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	adf, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	b := adf[1184*512 : 1185*512] // s/startup-sequence, first data block
	for _, r := range [][2]string{
		{`echo "Amiga Workbench Disk. Release 1.3.3 version 34.34"`, "mount aux:"},
		{"mount newcon:", "newshell aux:"},
	} {
		i := bytes.Index(b, []byte(r[0]))
		if i < 0 {
			t.Skip("startup-sequence not as expected on this ADF")
		}
		copy(b[i:], append([]byte(r[1]), bytes.Repeat([]byte(" "), len(r[0])-len(r[1]))...))
	}
	ofsFixChecksum(b)

	a, _ := New(rom, nil)
	a.Disk.InsertDisk(adf)
	h := &fakeSerialHost{}
	a.Serial.Host = h
	a.Reset()
	// waitFor runs until what has appeared n times in the output.
	waitFor := func(what string, n, frames int) bool {
		for f := 0; f < frames; f++ {
			if bytes.Count(h.sent, []byte(what)) >= n {
				return true
			}
			if err := a.RunFrame(); err != nil {
				t.Fatal(err)
			}
		}
		return false
	}
	if !waitFor("> ", 1, 3000) {
		t.Fatalf("no shell prompt on the serial port; it sent %q", h.sent)
	}
	// The handler echoes the line as it's typed; the second copy is the
	// command's output.
	h.in = []byte("echo \"hello serial\"\r")
	if !waitFor("hello serial", 2, 500) {
		t.Fatalf("command output didn't come back; the serial port sent %q", h.sent)
	}
	t.Logf("serial output: %q", h.sent)
}
