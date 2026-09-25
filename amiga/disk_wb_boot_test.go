package amiga

import (
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

// TestWorkbenchBoots is the regression gate for disk→Workbench progress.
// After the MOVEM (d16,PC) fix, Kickstart 1.3 + WB 1.3.3 should:
//   - execute the bootblock
//   - start workbench.task
//   - open a 640×256 Workbench screen with free-memory title
//   - drive a 2-plane hires display (classic 4-colour palette)
func TestWorkbenchBoots(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	wbStep := 0
	bbOK := false
	for i := 0; i < 20_000_000; i++ {
		pc := a.CPU.PC
		if pc == 0xFE85CA && a.CPU.D[0] == 0 {
			bbOK = true
		}
		if pc == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			name := readCStrMem(a, uint32(namePtr), 24)
			if name == "workbench.task" {
				wbStep = i
				break
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	if !bbOK {
		t.Error("bootblock did not return success")
	}
	if wbStep == 0 {
		t.Fatal("workbench.task never started")
	}
	t.Logf("workbench.task at step %d", wbStep)

	// Let WB finish opening its screen
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}

	// Screen title must mention free memory (classic WB 1.3 title bar)
	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	title := findString(mem, "free memory")
	if title == "" {
		title = findString(mem, "Workbench release")
	}
	if title == "" {
		t.Error("Workbench screen title not found in chip RAM")
	} else {
		t.Logf("title fragment: %q", title)
	}

	// BPLCON0 is rewritten by the Copper every frame (planes are switched
	// off below the display window), so sample it in the middle of the display.
	// Advance the beam directly: a STOP'd CPU's Step() skips whole lines.
	for a.vpos != 150 {
		a.advanceBeamPerCycle(1)
	}
	bplcon0 := a.Custom.ReadReg16(0x100)
	planes := (bplcon0 >> 12) & 7
	hires := bplcon0&0x8000 != 0
	if planes < 2 {
		t.Errorf("expected ≥2 bitplanes, BPLCON0=$%04X", bplcon0)
	}
	if !hires {
		t.Errorf("expected a hires Workbench screen, BPLCON0=$%04X", bplcon0)
	}
	t.Logf("BPLCON0=$%04X planes=%d hires=%v COLOR00=$%04X DMACON=$%04X",
		bplcon0, planes, hires, a.Custom.ReadReg16(0x180), a.Custom.ReadReg16(0x096))

	// Snapshot PNG for manual inspection
	path := "/tmp/workbench_boot.png"
	w, h := a.Video.Width, a.Video.Height
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	copy(img.Pix, a.Video.Pixels)
	f, err := os.Create(path)
	if err == nil {
		png.Encode(f, img)
		f.Close()
		// also copy into workspace if possible
		if out, err2 := os.Create("../workbench_boot.png"); err2 == nil {
			png.Encode(out, img)
			out.Close()
		}
		t.Logf("wrote %s (%dx%d)", path, w, h)
	}

	eb, _ := a.CPU.M.Long(4)
	la, _ := a.CPU.M.Long(int(eb) + 0x202)
	if la != 0 && la != 0xFFFFFFFF {
		t.Errorf("LastAlert=$%08X (unexpected)", la)
	}
}

func readCStrMem(a *Amiga, addr uint32, n int) string {
	if addr == 0 {
		return ""
	}
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := a.CPU.M.Byte(int(addr) + i)
		if err != nil || v == 0 {
			return string(b[:i])
		}
		b[i] = byte(v)
	}
	return string(b)
}

func findString(mem []byte, sub string) string {
	b := []byte(sub)
	for i := 0; i+len(b) <= len(mem); i++ {
		ok := true
		for j := 0; j < len(b); j++ {
			if mem[i+j] != b[j] {
				ok = false
				break
			}
		}
		if ok {
			// expand to nearby printable
			start := i
			for start > 0 && mem[start-1] >= 32 && mem[start-1] < 127 {
				start--
			}
			end := i + len(b)
			for end < len(mem) && mem[end] >= 32 && mem[end] < 127 {
				end++
			}
			s := string(mem[start:end])
			if strings.Contains(s, sub) {
				return s
			}
		}
	}
	return ""
}
