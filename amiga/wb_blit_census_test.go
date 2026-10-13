package amiga

import (
	"os"
	"sort"
	"testing"
)

func TestWBBlitCensus(t *testing.T) {
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
	for i := 0; i < 30; i++ {
		a.RunFrame()
	}

	type key struct {
		con0, con1, size uint16
	}
	counts := map[key]int{}
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		counts[key{con0, con1, size}]++
	}

	// home + double clicks + drags
	for session := 0; session < 8; session++ {
		for i := 0; i < 20; i++ {
			a.Mouse.Move(-40, -30)
			a.RunFrame()
		}
		a.Mouse.Move(40+session*30, 20+session*5)
		for i := 0; i < 5; i++ {
			a.RunFrame()
		}
		for c := 0; c < 2; c++ {
			a.Mouse.LeftButton = true
			for i := 0; i < 3; i++ {
				a.RunFrame()
			}
			a.Mouse.LeftButton = false
			for i := 0; i < 3; i++ {
				a.RunFrame()
			}
		}
		for i := 0; i < 20; i++ {
			a.RunFrame()
		}
		a.Mouse.LeftButton = true
		for i := 0; i < 80; i++ {
			a.Mouse.Move(3-((i/8)%3), (i%3)-1)
			a.RunFrame()
		}
		a.Mouse.LeftButton = false
		for i := 0; i < 10; i++ {
			a.RunFrame()
		}
	}

	type kv struct {
		k key
		n int
	}
	var list []kv
	total := 0
	for k, n := range counts {
		list = append(list, kv{k, n})
		total += n
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	t.Logf("total blits=%d unique=%d", total, len(list))
	for i, e := range list {
		if i >= 40 {
			break
		}
		h := e.k.size >> 6
		w := e.k.size & 0x3F
		if h == 0 {
			h = 1024
		}
		if w == 0 {
			w = 64
		}
		t.Logf("  n=%4d con0=$%04X con1=$%04X size=%dx%d line=%v desc=%v min=$%02X shiftA=%d",
			e.n, e.k.con0, e.k.con1, w, h, e.k.con1&1 != 0, e.k.con1&2 != 0,
			e.k.con0&0xFF, e.k.con0>>12)
	}
}
