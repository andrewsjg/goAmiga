package amiga

import (
	"os"
	"testing"
	"time"
)

// TestTimingProfile measures how long Workbench takes to boot, in emulated
// and host time, and how fast the emulator runs once it is up. Use it to
// compare timing changes:
//
//	GOAMIGA_DIAG=1 go test -run TestTimingProfile -v ./amiga/
func TestTimingProfile(t *testing.T) {
	requireDiag(t)
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Disk.InsertDisk(data)
	a.Reset()

	// Boot until Workbench creates its task (PC $055C is exec AddTask).
	start := time.Now()
	steps := 0
	found := false
	for ; steps < 100_000_000 && !found; steps++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			found = readCStrMem(a, uint32(np), 24) == "workbench.task"
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	if !found {
		t.Fatalf("workbench.task not created after %d steps", steps)
	}
	host := time.Since(start)
	frames := float64(a.cycles) / CyclesPerFrame
	t.Logf("boot to workbench.task: %d steps, %d cycles, %.0f frames = %.1fs emulated, %.1fs host",
		steps, a.cycles, frames, frames/50, host.Seconds())
	t.Logf("  idle: %.0f%% of cycles spent in STOP, %.1f%% waiting for DMA, %.1f%% syncing to the CIA E clock",
		100*float64(a.FastIdleSkips)/float64(a.cycles), 100*float64(a.DMAWaitCycles)/float64(a.cycles),
		100*float64(a.CIAWaitCycles)/float64(a.cycles))

	// Throughput with rendering on, once Workbench is idle.
	defer func() { t.Logf("  disk: %d sectors written during boot and idle", a.Disk.SectorsWritten) }()
	a.Video.DisplayActive = true
	a.Paula.Output = NewAudioBuffer(AudioSampleRate) // mix audio, as the app does
	for range 100 {
		_ = a.RunFrame()
	}
	const n = 250
	c0, s0 := a.cycles, a.FastIdleSkips
	start = time.Now()
	for range n {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	host = time.Since(start)
	busy := float64((a.cycles-c0)-(a.FastIdleSkips-s0)) / float64(a.cycles-c0)
	t.Logf("workbench idle, display on: %d frames in %.2fs host = %.0f fps (%.1fx real time), CPU busy %.0f%%",
		n, host.Seconds(), n/host.Seconds(), n/host.Seconds()/50, 100*busy)
	t.Logf("  cycles/frame %d (%d per line)", CyclesPerFrame, CPUClocksPerLine)
}
