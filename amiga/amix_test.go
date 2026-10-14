package amiga

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestAMIXBoot boots Amiga Unix from a disk made by cmd/amixdisk, when
// AMIX_HDF names one: Kickstart 3.2 boots AMIX's boot partition through
// goamiga.device, and the kernel takes over with the disk on the A2091 at
// SCSI ID 6. AMIX_FRAMES sets how long to run, AMIX_PNG where to save
// screenshots, AMIX_TRACE=1 traces the boot, AMIX_TAPE puts a tape
// (kept as a directory) in a drive at SCSI ID 4.
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
	for f := 1; f <= frames; f++ {
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
