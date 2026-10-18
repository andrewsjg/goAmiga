package amiga

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// newA4000 is an A4000/040 with the A4000 Kickstart: AGA, 2 MB of chip
// RAM, 16 MB on the motherboard, its IDE interface and the clock.
func newA4000(t *testing.T) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/kicka4000.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	if err := a.SetCPU("68040", 0); err != nil {
		t.Fatal(err)
	}
	a.SetFPU("auto")
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 0)
	if err := a.EnableMotherboard("a4000", 16384); err != nil {
		t.Fatal(err)
	}
	a.EnableA4000IDE()
	a.EnableClock()
	return a
}

// The A4000 Kickstart boots Workbench 3.2 from floppy: exec finds the
// 040 and its FPU, Ramsey, and the motherboard's 16 MB of fast RAM.
func TestA4000BootsFloppy(t *testing.T) {
	a := newA4000(t)
	adf, err := os.ReadFile("../Workbench/Install3.2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(adf)
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start on the A4000")
	}
	eb := a.peek32(4)
	if attn := a.peek16(eb + 0x128); attn&0x0F != 0x0F || attn&0x40 == 0 {
		t.Errorf("AttnFlags $%04X: want AFF_68010-68040 and AFF_FPU40", attn)
	}
	regions := memRegions(a)
	t.Logf("MemList: %v", regions)
	var r [2]uint32
	ok := false
	for lower, v := range regions {
		if lower>>24 == 0x07 {
			r, ok = v, true
		}
	}
	if !ok {
		t.Fatalf("no MemList region at $07000000 (motherboard RAM): %v", regions)
	}
	if r[1]&4 == 0 || r[0] < 15<<20 {
		t.Errorf("motherboard region: %d bytes free, attributes $%X; want fast RAM, about 16 MB", r[0], r[1])
	}
	if a.Motherboard.gary[2] != 0 {
		t.Errorf("Kickstart left the cold boot bit set")
	}
	if a.Motherboard.ramseyCtl == 0 {
		t.Errorf("Kickstart didn't set up Ramsey")
	}
}

// TestA4000ROMProbe boots the A4000 Kickstart on a 68040 and reports
// what it touches where nothing is mapped (GOAMIGA_DIAG=1).
func TestA4000ROMProbe(t *testing.T) {
	if os.Getenv("GOAMIGA_DIAG") == "" {
		t.Skip("GOAMIGA_DIAG not set")
	}
	a := newA4000(t)
	type key struct {
		addr  int
		write bool
	}
	seen := map[key]int{}
	var order []key
	a.OnOpenBus = func(addr, n int, write bool) {
		k := key{addr, write}
		if seen[k] == 0 && (addr < 0xF00000 || addr >= 0xF80000) {
			order = append(order, k)
			if len(order) <= 200 {
				t.Logf("open bus: %s $%08X (%d) PC $%08X", map[bool]string{true: "write", false: "read "}[write], addr, n, a.CPU.PC)
			}
		}
		seen[k]++
	}
	exc := map[int]int{}
	a.CPU.OnException = func(v int, pc, instr uint32) {
		if v < 24 || v >= 32 {
			exc[v]++
			if exc[v] <= 3 {
				t.Logf("exception %d at $%08X", v, instr)
			}
		}
	}
	a.Video.DisplayActive = true
	a.Reset()
	for f := 0; f < 600; f++ {
		a.RunFrame()
		if f%100 == 0 {
			t.Logf("frame %d PC $%08X", f, a.CPU.PC)
		}
	}
	keys := make([]key, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return seen[keys[i]] > seen[keys[j]] })
	for i, k := range keys {
		if i < 30 {
			t.Logf("%6d× %v $%08X", seen[k], k.write, k.addr)
		}
	}
	if p := os.Getenv("GOAMIGA_DIAG_PNG"); p != "" {
		savePNG(t, a, p)
	}
}

// The A4000's IDE decoding: IDENTIFY through $DD2020/$DD2022+4n, the
// interrupt status at $DD3020, and the bus floating high where nothing
// decodes.
func TestA4000IDERegisters(t *testing.T) {
	img := make([]byte, 1<<20)
	path := filepath.Join(t.TempDir(), "ide.hdf")
	os.WriteFile(path, img, 0o644)
	a := createTestAmiga(t)
	a.EnableA4000IDE()
	if err := a.AttachIDE(path, false); err != nil {
		t.Fatal(err)
	}
	g := a.Gayle
	rd := func(addr int) byte {
		var b [1]byte
		a.Mapper.Read(addr, b[:])
		return b[0]
	}
	if v := rd(0xDD2010); v != 0xFF {
		t.Errorf("$DD2010 = $%02X, want the bus floating ($FF)", v)
	}
	if v := rd(0xDA0000 + 7*4); v != 0 {
		t.Errorf("Gayle's address answers on the A4000 ($%02X)", v)
	}
	g.w8(6, 0xA0)
	g.w8(7, 0xEC) // IDENTIFY
	g.waitReady(t, true)
	if rd(a4000IDEIRQ)&0x80 == 0 {
		t.Error("no interrupt at $DD3020 after the command")
	}
	if a.Custom.ReadReg16(0x09C)&0x08 == 0 {
		t.Error("the drive's interrupt didn't reach INT2 (PORTS)")
	}
	var id [256]uint16
	for i := range id {
		w := g.rData()
		id[i] = w>>8 | w<<8
	}
	if lba := uint32(id[61])<<16 | uint32(id[60]); lba != 2048 {
		t.Errorf("IDENTIFY gave %d sectors, want 2048", lba)
	}
	g.r8(7) // reading the status acknowledges the interrupt
	if rd(a4000IDEIRQ)&0x80 != 0 {
		t.Error("$DD3020 still shows the interrupt after the status was read")
	}
}

// The A4000 Kickstart's scsi.device finds an IDE drive and boots
// Workbench from the RDB partition on it.
func TestA4000BootsIDE(t *testing.T) {
	a := newA4000(t)
	path := filepath.Join(t.TempDir(), "ide-rdb.hdf")
	if err := os.WriteFile(path, rdbImageFS(readDisk(t, "../Workbench/Install3.2.adf")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.AttachIDE(path, true); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GAYLE_TRACE") != "" {
		a.Gayle.Trace = logWriter{t}
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench didn't start from the A4000's IDE drive")
	}
	if active, _ := a.Gayle.Activity(); !active && a.Gayle.lastIO == 0 {
		t.Error("the IDE drive was never read")
	}
	if p := os.Getenv("GOAMIGA_DIAG_PNG"); p != "" {
		a.Video.DisplayActive = true
		for i := 0; i < 300; i++ {
			a.RunFrame()
		}
		savePNG(t, a, p)
	}
}

// expansion.library configures a Zorro III RAM board in $FF000000 space,
// puts it at $40000000 and adds it to the free memory; a Zorro II board
// on the same chain still goes in the 8 MB space.
func TestA4000Zorro3RAM(t *testing.T) {
	a := newA4000(t)
	if err := a.EnableZorro3RAM(64); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureMemory(0, 4096); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ide-rdb.hdf")
	if err := os.WriteFile(path, rdbImageFS(readDisk(t, "../Workbench/Install3.2.adf")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.AttachIDE(path, true); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench didn't start")
	}
	if base := a.Z3RAM.Configured(); base != 0x40000000 {
		t.Errorf("Zorro III board at $%08X, want $40000000", base)
	}
	if base := a.FastRAM.Configured(); base != 0x200000 {
		t.Errorf("Zorro II board at $%08X, want $200000", base)
	}
	regions := memRegions(a)
	t.Logf("MemList: %v", regions)
	found := false
	for lower, r := range regions {
		if lower>>24 == 0x40 {
			found = true
			if r[0] < 63<<20 {
				t.Errorf("Zorro III region has %d bytes free, want about 64 MB", r[0])
			}
		}
	}
	if !found {
		t.Errorf("no MemList region for the Zorro III board")
	}
	// The RAM works: a long written at its top reads back.
	a.poke32(0x43FFFFFC, 0xDEADBEEF)
	if v := a.peek32(0x43FFFFFC); v != 0xDEADBEEF {
		t.Errorf("Zorro III RAM reads $%08X", v)
	}
}
