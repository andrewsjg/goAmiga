package amiga

import (
	"os"
	"testing"
)

// The Autoconfig protocol as expansion.library drives it: read the ID
// nibbles, write the base address, and the RAM appears there.
func TestZorroAutoconfig(t *testing.T) {
	a := createTestAmiga(t)
	if err := a.ConfigureMemory(0, 2048); err != nil {
		t.Fatal(err)
	}
	a.wireMemoryMap(false)
	rd := func(off int) byte {
		b := make([]byte, 1)
		a.CPU.M.Read(AutoconfigBase+off, b)
		return b[0]
	}
	reg := func(off int, inverted bool) byte {
		hi, lo := rd(off)>>4, rd(off+2)>>4
		if inverted {
			hi, lo = ^hi&0xF, ^lo&0xF
		}
		return hi<<4 | lo
	}
	if got := reg(0x00, false); got != 0xE6 { // Zorro II, memory list, 2 MB
		t.Errorf("er_Type = $%02X, want $E6", got)
	}
	if got := reg(0x0C, true); got != 0 {
		t.Errorf("er_Reserved03 = $%02X, want 0", got)
	}
	if got := uint16(reg(0x10, true))<<8 | uint16(reg(0x14, true)); got != 0x07DB {
		t.Errorf("manufacturer = $%04X", got)
	}

	a.CPU.M.Write(AutoconfigBase+0x4A, []byte{0x00}) // A19-A16 = 0
	a.CPU.M.Write(AutoconfigBase+0x48, []byte{0x20}) // A23-A20 = 2 → $200000
	if a.FastRAM.Configured() != 0x200000 {
		t.Fatalf("board at $%06X, want $200000", a.FastRAM.Configured())
	}
	a.CPU.M.Write(0x200000, []byte{0xCA, 0xFE})
	a.CPU.M.Write(0x3FFFFE, []byte{0xBE, 0xEF})
	b := make([]byte, 2)
	if a.CPU.M.Read(0x200000, b); b[0] != 0xCA || b[1] != 0xFE {
		t.Errorf("fast RAM start reads % X", b)
	}
	if a.CPU.M.Read(0x3FFFFE, b); b[0] != 0xBE || b[1] != 0xEF {
		t.Errorf("fast RAM end reads % X", b)
	}
	if rd(0x00) != 0 {
		t.Error("configured board still answers in the Autoconfig space")
	}

	a.Reset()
	if a.FastRAM.Configured() != 0 || rd(0x00) == 0 {
		t.Error("reset did not return the board to the Autoconfig space")
	}
}

// memRegions returns exec's MemList: lower bound → (free bytes, attributes).
func memRegions(a *Amiga) map[uint32][2]uint32 {
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	eb := rd(4)
	out := map[uint32][2]uint32{}
	for n := rd(eb + 0x142); n != 0 && rd(n) != 0; n = rd(n) {
		attr, _ := a.CPU.M.Word(int(n + 14))
		out[rd(n+20)] = [2]uint32{rd(n + 28), uint32(attr)}
	}
	return out
}

// bootWBWith boots Workbench with the given expansion memory.
func bootWBWith(t *testing.T, slowKB, fastKB int) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	if err := a.ConfigureMemory(slowKB, fastKB); err != nil {
		t.Fatal(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	// Workbench starts when exec's AddTask (LVO -282, just below ExecBase,
	// which moves into slow RAM when there is some) creates workbench.task.
	for i := 0; i < 30_000_000; i++ {
		eb, _ := a.CPU.M.Long(4)
		if a.CPU.PC == eb-282 {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(np), 24) == "workbench.task" {
				return a
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	t.Fatal("Workbench did not start")
	return nil
}

func TestSlowRAMFound(t *testing.T) {
	a := bootWBWith(t, 512, 0)
	regions := memRegions(a)
	t.Logf("MemList: %v", regions)
	slow, ok := regions[SlowRAMBase]
	if !ok {
		// exec puts the MemHeader at the start of the region.
		for lower, r := range regions {
			if lower >= SlowRAMBase && lower < SlowRAMBase+0x1000 {
				slow, ok = r, true
			}
		}
	}
	if !ok {
		t.Fatalf("no MemList region at $C00000: %v", regions)
	}
	if slow[0] < 400*1024 {
		t.Errorf("slow RAM region has %d bytes free, want most of 512 KB", slow[0])
	}
}

func TestFastRAMAutoconfigured(t *testing.T) {
	a := bootWBWith(t, 0, 2048)
	regions := memRegions(a)
	t.Logf("MemList: %v; board at $%06X", regions, a.FastRAM.Configured())
	if a.FastRAM.Configured() == 0 {
		t.Fatal("expansion.library did not configure the board")
	}
	base := a.FastRAM.Configured()
	var fast [2]uint32
	found := false
	for lower, r := range regions {
		if lower >= base && lower < base+0x1000 {
			fast, found = r, true
		}
	}
	if !found {
		t.Fatalf("no MemList region for the board at $%06X: %v", base, regions)
	}
	const memfFast = 4
	if fast[1]&memfFast == 0 {
		t.Errorf("board memory attributes $%X lack MEMF_FAST", fast[1])
	}
	if fast[0] < 1800*1024 {
		t.Errorf("fast RAM has %d bytes free, want most of 2 MB", fast[0])
	}
}

func TestSlowAndFastRAMTogether(t *testing.T) {
	a := bootWBWith(t, 1024, 4096)
	var total uint32
	for _, r := range memRegions(a) {
		total += r[0]
	}
	t.Logf("MemList: %v; %d KB free", memRegions(a), total/1024)
	// 512 KB chip + 1 MB slow + 4 MB fast, less the OS.
	if total < 5*1024*1024 {
		t.Errorf("only %d KB free with 5.5 MB fitted", total/1024)
	}
}
