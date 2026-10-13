package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

// Probe Workbench after workbench.task starts: libraries, copper, BPL, hotspots, PNG.
func TestWorkbenchProbe(t *testing.T) {
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
	a.Video.DisplayActive = true

	readName := func(addr uint32) string {
		if addr == 0 {
			return ""
		}
		b := make([]byte, 48)
		for i := 0; i < 48; i++ {
			v, err := a.CPU.M.Byte(int(addr) + i)
			if err != nil || v == 0 {
				return string(b[:i])
			}
			b[i] = byte(v)
		}
		return string(b)
	}

	wbStep := 0
	tasks := map[string]int{}
	libs := map[string]int{}
	devs := map[string]int{}
	hot := map[uint32]int{}
	dmas := 0
	prevBusy := false
	planeOnSteps := 0
	maxPlanes := 0
	var copHist []string

	savePNG := func(path string) {
		w, h := a.Video.Width, a.Video.Height
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		copy(img.Pix, a.Video.Pixels)
		f, err := os.Create(path)
		if err != nil {
			t.Logf("png create %s: %v", path, err)
			return
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Logf("png encode: %v", err)
			return
		}
		// crude non-zero pixel count
		nz := 0
		cols := map[[3]byte]int{}
		for i := 0; i+3 < len(a.Video.Pixels); i += 4 {
			r, g, b := a.Video.Pixels[i], a.Video.Pixels[i+1], a.Video.Pixels[i+2]
			if r|g|b != 0 {
				nz++
			}
			if len(cols) < 16 {
				cols[[3]byte{r, g, b}]++
			} else {
				cols[[3]byte{r, g, b}]++
			}
		}
		t.Logf("saved %s nz=%d/%d colors~%d", path, nz, w*h, len(cols))
	}

	const maxSteps = 30_000_000
	for i := 0; i < maxSteps; i++ {
		pc := a.CPU.PC

		if a.Disk.completionCycles > 0 && !prevBusy {
			dmas++
		}
		prevBusy = a.Disk.completionCycles > 0

		switch pc {
		case 0x055C: // AddTask
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			name := readName(uint32(namePtr))
			tasks[name]++
			if tasks[name] <= 2 {
				t.Logf("step=%d AddTask %q initPC=$%08X", i, name, a.CPU.A[0])
			}
			if name == "workbench.task" && wbStep == 0 {
				wbStep = i
				t.Logf("step=%d *** workbench.task ***", i)
			}
		case 0x044E: // OpenLibrary
			name := readName(a.CPU.A[1])
			libs[name]++
			if libs[name] <= 2 && (name == "icon.library" || name == "intuition.library" ||
				name == "graphics.library" || name == "layers.library" || name == "diskfont.library" ||
				name == "workbench.library" || name == "gadtools.library") {
				t.Logf("step=%d OpenLib %q", i, name)
			}
		case 0x04BA: // OpenDevice
			name := readName(a.CPU.A[0])
			devs[name]++
		}

		// Sample after WB
		if wbStep > 0 && i > wbStep && i%100 == 0 {
			hot[pc]++
		}

		bplcon0 := a.Custom.ReadReg16(0x100)
		planes := int((bplcon0 >> 12) & 7)
		if planes > maxPlanes {
			maxPlanes = planes
		}
		if planes > 0 {
			planeOnSteps++
		}

		// Track copper list changes after WB
		if wbStep > 0 && i%500_000 == 0 {
			cop := a.Copper.cop1lc
			dmacon := a.Custom.ReadReg16(0x096)
			entry := fmt.Sprintf("step=%d COP1LC=$%08X BPLCON0=$%04X planes=%d DMACON=$%04X PC=$%08X dmas=%d",
				i, cop, bplcon0, planes, dmacon, pc, dmas)
			copHist = append(copHist, entry)
			t.Log(entry)
		}

		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		// Snapshots
		if wbStep > 0 {
			if i == wbStep+100_000 {
				savePNG("/tmp/wb_plus100k.png")
			}
			if i == wbStep+2_000_000 {
				savePNG("/tmp/wb_plus2m.png")
			}
		}
		if i == 15_000_000 {
			savePNG("/tmp/wb_15m.png")
		}
		if i == 25_000_000 {
			savePNG("/tmp/wb_25m.png")
		}
	}

	savePNG("/tmp/wb_final.png")
	t.Logf("wbStep=%d dmas=%d maxPlanes=%d planeOnSteps=%d", wbStep, dmas, maxPlanes, planeOnSteps)
	t.Logf("tasks=%v", tasks)
	t.Logf("key libs icon=%d intuition=%d gfx=%d layers=%d workbench=%d diskfont=%d",
		libs["icon.library"], libs["intuition.library"], libs["graphics.library"],
		libs["layers.library"], libs["workbench.library"], libs["diskfont.library"])
	t.Logf("devs=%v", devs)
	t.Logf("FINAL BPLCON0=$%04X DMACON=$%04X COP1LC=$%08X COLOR00=$%04X PC=$%08X",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x096), a.Copper.cop1lc,
		a.Custom.ReadReg16(0x180), a.CPU.PC)

	// top hot PCs
	type kv struct {
		pc uint32
		n  int
	}
	var top []kv
	for pc, n := range hot {
		top = append(top, kv{pc, n})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].n > top[i].n {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	t.Log("hot PCs after WB:")
	for i := 0; i < 15 && i < len(top); i++ {
		t.Logf("  $%08X x%d", top[i].pc, top[i].n)
	}
	for _, e := range copHist {
		t.Log(e)
	}
}
