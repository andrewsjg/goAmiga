package amiga

import (
	"os"
	"testing"
)

// Capture alert/requester text at the mouse-wait stall and try clicking past it.
func TestDiskRequester(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	dumpMemStrs := func(label string) {
		// Scan chip RAM for printable runs that look like messages
		buf := make([]byte, 0x80000)
		a.ChipRAM.Read(0, buf)
		seen := map[string]bool{}
		for i := 0; i < len(buf)-8; i++ {
			if buf[i] < 0x20 || buf[i] > 0x7e {
				continue
			}
			j := i
			for j < len(buf) && buf[j] >= 0x20 && buf[j] <= 0x7e {
				j++
			}
			if j-i >= 10 {
				s := string(buf[i:j])
				// filter interesting
				low := s
				if (containsAny(low, "Disk", "Volume", "Error", "DOS", "Workbench",
					"Insert", "Retry", "Cancel", "Not a", "has a", "read", "write",
					"Software", "Failure", "Task", "Alert", "DF0", "validated",
					"Please", "Click", "busy", "Checksum", "Invalid")) && !seen[s] {
					seen[s] = true
					t.Logf("%s str@$%06X: %q", label, i, s)
				}
				i = j
			}
		}
	}

	clicked := false
	postClickSteps := 0
	var firstWait int

	for i := 0; i < 20_000_000; i++ {
		pc := a.CPU.PC

		if pc == 0xFD423C {
			if firstWait == 0 {
				firstWait = i
				t.Logf("step=%d first mouse_wait D6=$%08X A2=$%08X A3=$%08X A5=$%08X",
					i, a.CPU.D[6], a.CPU.A[2], a.CPU.A[3], a.CPU.A[5])
				// Dump stack return addresses
				sp := a.CPU.A[7]
				t.Logf("  SP=$%08X", sp)
				for k := 0; k < 8; k++ {
					v, _ := a.CPU.M.Long(int(sp) + k*4)
					t.Logf("  stack[%d]=$%08X", k, v)
				}
				// A5 locals often hold text pointers
				for _, off := range []int32{-0xD8, -0x40, -0x4C, -0x1C, 0, 4, 8} {
					addr := uint32(int32(a.CPU.A[5]) + off)
					v, _ := a.CPU.M.Long(int(addr))
					t.Logf("  A5%+d @$%08X = $%08X", off, addr, v)
					if v > 0 && v < 0x80000 {
						b := make([]byte, 64)
						a.ChipRAM.Read(int(v), b)
						s := cstring(b)
						if len(s) > 2 {
							t.Logf("    -> %q", s)
						}
					}
				}
				// A2 often points at alert body stream
				if a.CPU.A[2] > 0 && a.CPU.A[2] < 0x1000000 {
					b := make([]byte, 128)
					for j := 0; j < 128; j++ {
						v, err := a.CPU.M.Byte(int(a.CPU.A[2]) + j)
						if err != nil {
							break
						}
						b[j] = byte(v)
					}
					t.Logf("  A2 stream: %q hex=%02X", filterPrint(b), b[:32])
				}
				dumpMemStrs("at_wait")
			}
			// After a few wait loops, press left mouse button
			if !clicked && i > firstWait+50000 {
				a.Mouse.LeftButton = true
				clicked = true
				t.Logf("step=%d CLICK left mouse", i)
			}
		}

		if clicked {
			postClickSteps++
			// Release after a bit
			if postClickSteps == 100000 {
				a.Mouse.LeftButton = false
				t.Logf("step=%d RELEASE mouse", i)
			}
			// Log interesting progress after click
			switch pc {
			case 0xFEA652:
				t.Logf("step=%d postclick decode D0=$%02X cyl=%d", i, a.CPU.D[0]&0xFF, a.Disk.cylinder)
			case 0x055C:
				namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
				t.Logf("step=%d postclick AddTask initPC=$%08X name=%q", i, a.CPU.A[0], readCStr(a, uint32(namePtr)))
			case 0x044E:
				t.Logf("step=%d postclick OpenLib %q", i, readCStr(a, a.CPU.A[1]))
			case 0xFF3E94:
				t.Logf("step=%d postclick dos_init", i)
			}
			if postClickSteps > 5_000_000 {
				break
			}
		}

		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if i > 0 && i%5_000_000 == 0 {
			t.Logf("--- %dM PC=$%08X click=%v cyl=%d ---", i/1_000_000, pc, clicked, a.Disk.cylinder)
		}
	}

	t.Logf("FINAL PC=$%08X clicked=%v firstWait=%d cyl=%d", a.CPU.PC, clicked, firstWait, a.Disk.cylinder)
	dumpMemStrs("final")
}

func containsAny(s string, keys ...string) bool {
	for _, k := range keys {
		if len(k) == 0 {
			continue
		}
		for i := 0; i+len(k) <= len(s); i++ {
			if s[i:i+len(k)] == k {
				return true
			}
		}
	}
	return false
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func filterPrint(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c <= 0x7e {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func readCStr(a *Amiga, addr uint32) string {
	if addr == 0 {
		return ""
	}
	b := make([]byte, 40)
	for i := 0; i < 40; i++ {
		v, err := a.CPU.M.Byte(int(addr) + i)
		if err != nil || v == 0 {
			return string(b[:i])
		}
		b[i] = byte(v)
	}
	return string(b)
}
