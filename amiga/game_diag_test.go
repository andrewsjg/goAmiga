package amiga

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestGameDiag boots a game disk and saves a screenshot every so often, to
// see how far it gets. Settings come from the environment:
//
//	GOAMIGA_GAME     disk image (ADF, zip, adz)            required
//	GOAMIGA_KS       ROM (default ../ROMS/Kick13.rom)
//	GOAMIGA_OUT      folder for PNGs (default: a temp dir, logged)
//	GOAMIGA_FRAMES   frames to run (default 3000)
//	GOAMIGA_EVERY    screenshot interval in frames (default 250)
//	GOAMIGA_MEM      chip,slow,fast KB (default 512,512,0)
//	GOAMIGA_CHIPSET  ocs, ecs-agnus, ecs (default ocs)
//	GOAMIGA_CPU      68000 or 68020 (default 68000)
//	GOAMIGA_CLICKS   comma-separated frame numbers at which to click the
//	                 left mouse button (for "click to start" screens)
//	GOAMIGA_SWAP_AT  frame,path: swap DF0: to another disk at that frame
//	GOAMIGA_MOVES    frame,dx,dy;...: move the mouse by dx,dy counts
//	                 starting at frame, 4 counts per frame
//	GOAMIGA_TRACE    frame,count: trace count instructions from that frame
//	GOAMIGA_REGLOG   from,to,filter: log custom register writes whose line
//	                 contains filter (e.g. "DSK") between those frames
//
//	GOAMIGA_GAME=../ADF/Lemmings.zip go test -tags noebitengine -run TestGameDiag -v ./amiga/
func TestGameDiag(t *testing.T) {
	game := os.Getenv("GOAMIGA_GAME")
	if game == "" {
		t.Skip("set GOAMIGA_GAME to a disk image")
	}
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	rom, err := LoadROM(env("GOAMIGA_KS", "../ROMS/Kick13.rom"))
	if err != nil {
		t.Fatal(err)
	}
	var chipKB, slowKB, fastKB int
	fmt.Sscanf(env("GOAMIGA_MEM", "512,512,0"), "%d,%d,%d", &chipKB, &slowKB, &fastKB)
	frames, _ := strconv.Atoi(env("GOAMIGA_FRAMES", "3000"))
	every, _ := strconv.Atoi(env("GOAMIGA_EVERY", "250"))
	out := env("GOAMIGA_OUT", t.TempDir())
	clicks := map[int]bool{}
	for _, f := range filepathSplitList(os.Getenv("GOAMIGA_CLICKS")) {
		n, _ := strconv.Atoi(f)
		clicks[n] = true
	}

	a, _ := New(rom, nil)
	if err := a.SetCPU(env("GOAMIGA_CPU", "68000"), 0); err != nil {
		t.Fatal(err)
	}
	if err := a.SetChipset(env("GOAMIGA_CHIPSET", "ocs")); err != nil {
		t.Fatal(err)
	}
	if err := a.SetChipRAM(chipKB); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureMemory(slowKB, fastKB); err != nil {
		t.Fatal(err)
	}
	if err := a.LoadDisk(game); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	a.Video.DisplayActive = true
	var logFrom, logTo int
	var logFilter string
	fmt.Sscanf(strings.ReplaceAll(os.Getenv("GOAMIGA_REGLOG"), ",", " "), "%d %d %s", &logFrom, &logTo, &logFilter)
	regLog := &filterLog{t: t, filter: logFilter}
	swapAt, swapPath := 0, ""
	if v := os.Getenv("GOAMIGA_SWAP_AT"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			swapAt, _ = strconv.Atoi(v[:i])
			swapPath = v[i+1:]
		}
	}
	type move struct{ dx, dy int }
	moves := map[int]move{}
	for _, m := range strings.Split(os.Getenv("GOAMIGA_MOVES"), ";") {
		var f, dx, dy int
		if n, _ := fmt.Sscanf(m, "%d,%d,%d", &f, &dx, &dy); n == 3 {
			moves[f] = move{dx, dy}
		}
	}
	var pending move
	var traceAt, traceN int
	fmt.Sscanf(os.Getenv("GOAMIGA_TRACE"), "%d,%d", &traceAt, &traceN)

	base := filepath.Base(game)
	for f := 1; f <= frames; f++ {
		if f == swapAt {
			if err := a.SwapDisk(swapPath); err != nil {
				t.Fatal(err)
			}
			t.Logf("frame %d: swapping to %s", f, filepath.Base(swapPath))
		}
		if m, ok := moves[f]; ok {
			pending = m
		}
		step := func(v *int) int {
			d := max(-4, min(4, *v))
			*v -= d
			return d
		}
		if pending.dx != 0 || pending.dy != 0 {
			a.Mouse.Move(step(&pending.dx), step(&pending.dy))
		}
		if clicks[f] {
			a.Mouse.LeftButton = true
		}
		if clicks[f-3] {
			a.Mouse.LeftButton = false
		}
		if f == traceAt {
			var b strings.Builder
			a.CPU.TraceWriter = &b
			for i := 0; i < traceN; i++ {
				a.Step()
			}
			a.CPU.TraceWriter = nil
			t.Logf("trace at frame %d:\n%s", f, b.String())
		}
		if logFilter != "" {
			if f == logFrom {
				a.Custom.logger = regLog
			}
			if f == logTo {
				a.Custom.logger = nil
			}
		}
		regLog.frame = f
		if err := a.RunFrame(); err != nil {
			t.Fatalf("frame %d: %v", f, err)
		}
		if f%every == 0 || f == frames {
			p := filepath.Join(out, fmt.Sprintf("%s-%05d.png", base, f))
			if fh, err := os.Create(p); err == nil {
				png.Encode(fh, a.Video.ScreenImage())
				fh.Close()
			}
			dr := a.Disk.DF(0)
			d := a.Disk
			if os.Getenv("GOAMIGA_DISKSTATE") != "" {
				t.Logf("  A6=$%08X INTREQR=$%04X DSKLEN=$%04X waiting=%v compl=%d sync=%d selected=%v motor=%v adf=%v side=%d adkcon=$%04X dsksync=$%04X",
					a.CPU.A[6], a.Custom.ReadReg16(0x09C), d.dsklen, d.dmaWaiting, d.completionCycles, d.syncCycles,
					dr.selected, dr.motor, dr.adf != nil, d.side, d.adkcon, d.dsksync)
			}
			t.Logf("frame %d: PC=$%06X SR=$%04X motor=%v cyl=%d  %s", f, a.CPU.PC, a.CPU.SR, dr.MotorOn(), dr.Cylinder(), p)
		}
	}
}

func filepathSplitList(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// filterLog passes log lines containing filter to the test log.
type filterLog struct {
	t      *testing.T
	filter string
	frame  int
	n      int
}

func (l *filterLog) Write(p []byte) (int, error) {
	if strings.Contains(string(p), l.filter) && l.n < 400 {
		l.n++
		l.t.Logf("[%d] %s", l.frame, strings.TrimSpace(string(p)))
	}
	return len(p), nil
}
