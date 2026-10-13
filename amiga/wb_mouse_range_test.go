package amiga

import (
	"os"
	"testing"
)

func TestWBMouseRange(t *testing.T) {
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
	for i := 0; i < 20_000_000; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(namePtr), 24) == "workbench.task" {
				break
			}
		}
		a.Step()
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 10; i++ {
		a.RunFrame()
	}

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
					a.CPU.CheckInterrupts()
				}
			} else {
				a.Step()
			}
		}
	}

	// During one frame, capture sprite0 hstart while active
	sampleHStart := func() (hs int, vs int, ok bool) {
		for a.vpos != 0 {
			advanceLine()
		}
		for {
			advanceLine()
			s := &a.Sprites.sprites[0]
			if s.state == sprActive && (s.data != 0 || s.datb != 0) {
				return s.hstart(), s.vstart(), true
			}
			if a.vpos == 0 {
				break
			}
		}
		return 0, 0, false
	}

	// Move in steps of 40, log hstart
	type pt struct{ move, hs, fbX int }
	var pts []pt
	total := 0
	for step := 0; step < 20; step++ {
		a.Mouse.Move(40, 0)
		total += 40
		for i := 0; i < 5; i++ {
			a.RunFrame()
		}
		hs, vs, ok := sampleHStart()
		diwH := int(a.Custom.ReadReg16(0x08E) & 0xFF)
		fbX := (hs - diwH) * 2
		t.Logf("moveRight total=%d hstart=%d vstart=%d fbX=%d ok=%v JOY=$%04X",
			total, hs, vs, fbX, ok, a.Mouse.JOY0DAT())
		pts = append(pts, pt{total, hs, fbX})
	}

	for step := 0; step < 25; step++ {
		a.Mouse.Move(-40, 0)
		total -= 40
		for i := 0; i < 5; i++ {
			a.RunFrame()
		}
		hs, vs, ok := sampleHStart()
		diwH := int(a.Custom.ReadReg16(0x08E) & 0xFF)
		fbX := (hs - diwH) * 2
		t.Logf("moveLeft  total=%d hstart=%d vstart=%d fbX=%d ok=%v JOY=$%04X",
			total, hs, vs, fbX, ok, a.Mouse.JOY0DAT())
		pts = append(pts, pt{total, hs, fbX})
	}

	minFB, maxFB := 9999, -9999
	for _, p := range pts {
		if p.fbX < minFB {
			minFB = p.fbX
		}
		if p.fbX > maxFB {
			maxFB = p.fbX
		}
	}
	t.Logf("FB X range observed: %d .. %d (screen 0..639)", minFB, maxFB)
	// Software left clamp must map near the playfield left edge (not ~x=126).
	if minFB > 20 {
		t.Fatalf("leftmost pointer FB x=%d, want near 0 (DIW-aligned)", minFB)
	}
	// And we should be able to reach well into the right half of a 640 screen.
	if maxFB < 500 {
		t.Fatalf("rightmost pointer FB x=%d, want >= 500", maxFB)
	}
}
