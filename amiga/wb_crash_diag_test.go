package amiga

import (
	"os"
	"testing"
)

func TestWBWhatIs3342(t *testing.T) {
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
	for i := 0; i < 40; i++ {
		a.RunFrame()
	}

	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	rd32 := func(i int) uint32 {
		if i < 0 || i+3 >= len(mem) {
			return 0
		}
		return uint32(mem[i])<<24 | uint32(mem[i+1])<<16 | uint32(mem[i+2])<<8 | uint32(mem[i+3])
	}

	eb := int(rd32(4))
	t.Logf("ExecBase=$%08X", eb)
	for _, off := range []int{0x114, 0x10C, 0x110, 0x118, 0x11C, 0x120} {
		t.Logf("  EB+$%03X=$%08X", off, rd32(eb+off))
	}
	thisTask := rd32(eb + 0x114)
	t.Logf("ThisTask=$%08X", thisTask)

	dumpTask := func(task int, label string) {
		if task < 0x100 || task >= 0x80000 {
			return
		}
		np := rd32(task + 10)
		t.Logf("=== %s @$%08X name=%q type=%d pri=%d ===",
			label, task, readCStrMem(a, np, 32), mem[task+8], int8(mem[task+9]))
		t.Logf("  Succ=$%08X Pred=$%08X", rd32(task), rd32(task+4))
		// Probe SP fields at several candidate offsets
		for _, base := range []int{0x32, 0x36, 0x3A, 0x3E, 0x42, 0x46, 0x4A, 0x4E} {
			t.Logf("  +$%02X=$%08X", base, rd32(task+base))
		}
	}
	dumpTask(0x3342, "suspect")
	dumpTask(int(thisTask), "ThisTask")

	t.Logf("=== NT_TASK/PROCESS scan ===")
	for i := 0x1000; i < 0x30000; i += 2 {
		typ := mem[i+8]
		if typ != 1 && typ != 13 {
			continue
		}
		np := rd32(i + 10)
		if np < 0x1000 || np >= 0x80000 {
			continue
		}
		name := readCStrMem(a, np, 24)
		if len(name) < 3 {
			continue
		}
		ok := true
		for _, c := range name {
			if c < 32 || c > 126 {
				ok = false
				break
			}
		}
		if ok {
			t.Logf("  @$%06X type=%d pri=%d name=%q", i, typ, int8(mem[i+9]), name)
		}
	}
}
