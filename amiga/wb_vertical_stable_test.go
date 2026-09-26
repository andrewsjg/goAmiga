package amiga

import (
	"os"
	"testing"
)

// TestWorkbenchVerticalStable guards against the one-line vertical "shake":
// copper WAIT for the current display line must not push BPLCON0 enable to the
// next line when the CPU is stopped (fast StepLine path). That used to make
// odd frames render with content shifted down by one row.
func TestWorkbenchVerticalStable(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	for i := 0; i < 20_000_000; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(namePtr), 24) == "workbench.task" {
				break
			}
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}

	hashTop := func() uint64 {
		var h uint64 = 14695981039346656037
		w := a.Video.Width
		for y := 0; y < 80 && y < a.Video.Height; y++ {
			off := y * w * 4
			for x := 0; x < w*4; x++ {
				h ^= uint64(a.Video.Pixels[off+x])
				h *= 1099511628211
			}
		}
		return h
	}

	prev := hashTop()
	changes := 0
	for f := 0; f < 100; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
		h := hashTop()
		if h != prev {
			changes++
		}
		prev = h
	}
	if changes != 0 {
		t.Fatalf("top-of-screen content changed on %d/100 idle frames (vertical shake)", changes)
	}
}

// TestCopperWaitAlreadyMet ensures a WAIT for the current beam position does
// not consume the rest of the line: the following MOVE must still run.
func TestCopperWaitAlreadyMet(t *testing.T) {
	rom := make([]byte, ROMSize)
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	// WAIT v=44; MOVE BPLCON0,$A302; WAIT end
	list := []uint16{
		0x2C01, 0xFFFE,
		0x0100, 0xA302,
		0xFFFF, 0xFFFE,
	}
	buf := make([]byte, len(list)*2)
	for i, w := range list {
		buf[i*2] = byte(w >> 8)
		buf[i*2+1] = byte(w)
	}
	a.ChipRAM.Write(0x2000, buf)
	a.Copper.cop1lc = 0x2000
	a.Copper.Restart(1)
	// SET DMAEN|COPEN
	a.Custom.Write(0x096, []byte{0x82, 0x80})

	c := a.Copper
	// Simulate: WAIT already fetched, beam now on line 44
	c.pc = 0x2004
	c.wait = true
	c.waitVP = 44
	c.waitHP = 0
	c.waitVM = 0xFF
	c.waitHM = 0x7F

	c.StepLine(0, 114, 44)
	if got := a.Custom.ReadReg16(0x100); got != 0xA302 {
		t.Fatalf("BPLCON0=$%04X after same-line WAIT, want $A302", got)
	}
	if c.waitVP != 255 {
		t.Fatalf("expected to reach end WAIT (waitVP=255), got waitVP=%d wait=%v", c.waitVP, c.wait)
	}
}
