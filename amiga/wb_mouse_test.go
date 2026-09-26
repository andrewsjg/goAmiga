package amiga

import (
	"os"
	"testing"
)

// TestWorkbenchMousePointer checks that sprite 0 DMA displays the Workbench
// mouse pointer after boot (pointer-coloured pixels in the framebuffer).
func TestWorkbenchMousePointer(t *testing.T) {
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
	a.Mouse.Move(50, 40)
	for i := 0; i < 20; i++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}

	// Line-accurate advance so we observe sprite state mid-frame (a single
	// STOP idle Step can skip an entire frame and only leave sprIdle).
	advanceLine := func() {
		target := a.vpos
		for a.vpos == target {
			if a.cpuStopped {
				lr := CPUClocksPerLine - a.hpos
				if lr <= 0 {
					lr = CPUClocksPerLine
				}
				a.advanceBeamFast(lr)
				if a.cpuStopped && a.CPU.PendingIPL() > a.CPU.IPL() {
					a.cpuStopped = false
					_ = a.CPU.CheckInterrupts()
				}
			} else {
				_ = a.Step()
			}
		}
	}

	for a.vpos != 0 {
		advanceLine()
	}
	sawActive := false
	for {
		completed := a.vpos
		advanceLine()
		// completed line was `completed` before advance; sprite state is for
		// the line that just finished (FetchLine ran for that vpos).
		s := &a.Sprites.sprites[0]
		// After advanceLine, vpos has moved; FetchLine for `completed` already ran.
		// Sample current state — during pointer lines it should still be active.
		if s.state == sprActive && (s.data != 0 || s.datb != 0) {
			if !sawActive {
				t.Logf("pointer active vstart=%d hstart=%d data=$%04X datb=$%04X (after beam v=%d)",
					s.vstart(), s.hstart(), s.data, s.datb, completed)
			}
			sawActive = true
		}
		if a.vpos == 0 {
			break
		}
	}
	if !sawActive {
		t.Fatal("sprite 0 never displayed pointer data during a frame")
	}

	if err := a.RunFrame(); err != nil {
		t.Fatal(err)
	}
	a.Video.UpdatePalette()
	c0 := a.Video.palette[0]
	n := 0
	for y := 0; y < a.Video.Height; y++ {
		for x := 0; x < a.Video.Width; x++ {
			o := (y*a.Video.Width + x) * 4
			r, g, b := a.Video.Pixels[o], a.Video.Pixels[o+1], a.Video.Pixels[o+2]
			if r == c0[0] && g == c0[1] && b == c0[2] {
				continue
			}
			for ci := 17; ci <= 19; ci++ {
				c := a.Video.palette[ci]
				if r == c[0] && g == c[1] && b == c[2] {
					n++
					break
				}
			}
		}
	}
	t.Logf("pointer palette pixels in FB: %d (COLOR17-19)", n)
	if n < 10 {
		t.Fatalf("expected visible pointer pixels in framebuffer, found %d", n)
	}

	a.Mouse.LeftButton = true
	buf := make([]byte, 1)
	a.CIAA.Read(0x0000, buf)
	if buf[0]&0x40 != 0 {
		t.Error("left button pressed but CIA-A PRA bit 6 still set")
	}
}
