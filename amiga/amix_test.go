package amiga

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestAMIXBoot boots Amiga Unix from a disk made by cmd/amixdisk, when
// AMIX_HDF names one: Kickstart 3.2 boots AMIX's boot partition through
// goamiga.device, and the kernel takes over with the disk on the A2091 at
// SCSI ID 6. AMIX_FRAMES sets how long to run, AMIX_PNG where to save
// screenshots, AMIX_TRACE=1 traces the boot, AMIX_TAPE puts a tape
// (kept as a directory) in a drive at SCSI ID 4, AMIX_NET=PORT fits the
// A2065 with host port PORT forwarded to its telnet.
func TestAMIXBoot(t *testing.T) {
	hdf := os.Getenv("AMIX_HDF")
	if hdf == "" {
		t.Skip("AMIX_HDF not set")
	}
	rom, err := LoadROM("../ROMS/Kick32.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	if err := a.SetCPU("68030", 0); err != nil {
		t.Fatal(err)
	}
	if err := a.SetFPU("68882"); err != nil {
		t.Fatal(err)
	}
	a.SetChipset("ecs")
	a.SetChipRAM(1024)
	if err := a.ConfigureMemory(0, 8192); err != nil {
		t.Fatal(err)
	}
	// One attachment: AmigaOS boots it through goamiga.device, which
	// AttachSCSI also attaches it to.
	if err := a.AttachSCSI(6, hdf, false); err != nil {
		t.Fatal(err)
	}
	if fw := os.Getenv("AMIX_NET"); fw != "" { // a host port forwarded to telnet
		hp, _ := strconv.Atoi(fw)
		if err := a.EnableEthernet(map[int]int{hp: 23}); err != nil {
			t.Fatal(err)
		}
		defer a.Ethernet.close()
		if os.Getenv("AMIX_TRACE") != "" {
			a.Ethernet.Trace = logWriter{t}
		}
	}
	if tape := os.Getenv("AMIX_TAPE"); tape != "" {
		if err := a.AttachTape(4, tape); err != nil {
			t.Fatal(err)
		}
	}
	a.EnableClock()
	a.RTC.Y2K = true
	if os.Getenv("AMIX_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
		a.SCSI.Trace = logWriter{t}
	}
	exc := map[int]int{}
	a.CPU.OnException = func(v int, pc, instr uint32) {
		exc[v]++
		if v != 2 && v < 24 && exc[v] <= 5 {
			t.Logf("frame %d: exception %d at $%08X", a.cycles/CyclesPerFrame, v, instr)
		}
	}
	a.Video.DisplayActive = true
	a.Reset()
	frames, _ := strconv.Atoi(os.Getenv("AMIX_FRAMES"))
	if frames == 0 {
		frames = 600
	}
	png := os.Getenv("AMIX_PNG")
	// AMIX_KEYS is typed from frame AMIX_KEYS_AT (1000 by default), a key
	// every 25 frames; "\n" is Return.
	keys := []byte(strings.ReplaceAll(os.Getenv("AMIX_KEYS"), `\n`, "\n"))
	keysAt, _ := strconv.Atoi(os.Getenv("AMIX_KEYS_AT"))
	if keysAt == 0 {
		keysAt = 1000
	}
	// AMIX_HOSTSERVER=PORT runs a server on the host that greets whoever
	// connects; AMIX_CONNECT=FRAME connects to the forwarded telnet port
	// at that frame and logs what comes back.
	if hs := os.Getenv("AMIX_HOSTSERVER"); hs != "" {
		l, err := net.Listen("tcp", "127.0.0.1:"+hs)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				t.Logf("host server: connection from %s", c.RemoteAddr())
				c.Write([]byte("HELLO FROM THE HOST\r\n"))
				buf := make([]byte, 256)
				n, _ := c.Read(buf)
				t.Logf("host server got %q", buf[:n])
				c.Close()
			}
		}()
	}
	connectAt, _ := strconv.Atoi(os.Getenv("AMIX_CONNECT"))
	var telnet net.Conn
	var telnetGot []byte
	var telnetMu sync.Mutex
	for f := 1; f <= frames; f++ {
		if f == connectAt {
			c, err := net.Dial("tcp", "127.0.0.1:"+os.Getenv("AMIX_NET"))
			if err != nil {
				t.Fatalf("connecting to the forwarded port: %v", err)
			}
			telnet = c
			defer c.Close()
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					// A minimal telnet client: refuse every option
					// (DO x -> WONT x, WILL x -> DONT x), keep the text.
					for i := 0; i < n; i++ {
						if buf[i] == 0xFF && i+2 < n {
							switch buf[i+1] {
							case 0xFD:
								c.Write([]byte{0xFF, 0xFC, buf[i+2]})
							case 0xFB:
								c.Write([]byte{0xFF, 0xFE, buf[i+2]})
							}
							i += 2
							continue
						}
						telnetMu.Lock()
						telnetGot = append(telnetGot, buf[i])
						telnetMu.Unlock()
					}
					if err != nil {
						return
					}
				}
			}()
		}
		if f >= keysAt && (f-keysAt)%25 == 0 && len(keys) > 0 {
			if keys[0] == '~' { // a pause: wait 100 frames
				keysAt, keys = f+100, keys[1:]
				continue
			}
			if code, shift, ok := amigaKey(keys[0]); ok {
				if shift {
					a.Keyboard.EnqueueKey(0x60, true) // left Shift
				}
				a.Keyboard.EnqueueKey(code, true)
				a.Keyboard.EnqueueKey(code, false)
				if shift {
					a.Keyboard.EnqueueKey(0x60, false)
				}
			}
			keys = keys[1:]
		}
		a.RunFrame()
		if f%100 == 0 {
			tc, _, _, _, _, _ := a.CPU.MMUState()
			t.Logf("frame %d: PC $%08X SR $%04X TC $%08X, bus errors %d", f, a.CPU.PC, a.CPU.SR, tc, exc[2])
			if png != "" {
				savePNG(t, a, fmt.Sprintf("%s-%04d.png", png, f))
			}
			if telnet != nil {
				telnetMu.Lock()
				t.Logf("telnet so far: %q", telnetGot)
				telnetMu.Unlock()
			}
		}
	}
}

// amigaKey is the raw key code typing c, and whether it needs Shift
// (US layout).
func amigaKey(c byte) (code byte, shift bool, ok bool) {
	rows := []struct {
		plain, shifted string
		first          byte
	}{
		{"1234567890-=", "!@#$%^&*()_+", 0x01},
		{"qwertyuiop[]", "QWERTYUIOP{}", 0x10},
		{"asdfghjkl;'", "ASDFGHJKL:\"", 0x20},
		{"zxcvbnm,./", "ZXCVBNM<>?", 0x31},
	}
	for _, r := range rows {
		if i := strings.IndexByte(r.plain, c); i >= 0 {
			return r.first + byte(i), false, true
		}
		if i := strings.IndexByte(r.shifted, c); i >= 0 {
			return r.first + byte(i), true, true
		}
	}
	switch c {
	case ' ':
		return 0x40, false, true
	case '\n':
		return 0x44, false, true
	case '`':
		return 0x00, false, true
	case '~':
		return 0x00, true, true
	case '|':
		return 0x0D, true, true
	case '\\':
		return 0x0D, false, true
	}
	return 0, false, false
}
