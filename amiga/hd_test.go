package amiga

import (
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logWriter sends driver traces to the test log.
type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// hardfileFromADF copies an ADF to a temporary .hdf. An ADF is a valid
// plain hardfile: 1760 blocks = 55 cylinders of 32 sectors, with the root
// block in the middle, where the filesystem looks for it.
func hardfileFromADF(t *testing.T, adf string) string {
	t.Helper()
	data, err := os.ReadFile(adf)
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(t.TempDir(), "dh0.hdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Kickstart 1.3 boots Workbench from a hardfile with no floppy inserted.
func TestHardfileBootKS13(t *testing.T) {
	bootHardfile(t, "../ROMS/Kick13.rom", "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
}

// Kickstart 3.2 does too, through the same 1.3-style BootPoint.
func TestHardfileBootKS32(t *testing.T) {
	bootHardfile(t, "../ROMS/Kick32.rom", "../disks/Install3.2.adf")
}

// bootHardfile boots from adf as a hardfile until Workbench starts.
func bootHardfile(t *testing.T, romPath, adf string) *Amiga {
	t.Helper()
	return bootHardfileMem(t, romPath, adf, 512, 512, 1024)
}

// bootHardfileMem is bootHardfile with the given chip, slow and fast RAM.
func bootHardfileMem(t *testing.T, romPath, adf string, chipKB, slowKB, fastKB int) *Amiga {
	t.Helper()
	rom, err := LoadROM(romPath)
	if err != nil {
		t.Skip(err)
	}
	hdf := hardfileFromADF(t, adf)
	a, _ := New(rom, nil)
	// Fast RAM too: two boards in the Autoconfig chain.
	if err := a.SetChipRAM(chipKB); err != nil {
		t.Fatal(err)
	}
	if err := a.ConfigureMemory(slowKB, fastKB); err != nil {
		t.Fatal(err)
	}
	if err := a.AttachHardfile(hdf, false); err != nil {
		t.Fatal(err)
	}
	a.HardDisk.Trace = logWriter{t}
	a.Reset()
	for i := 0; i < 150_000_000; i++ {
		eb, _ := a.CPU.M.Long(4)
		if a.CPU.PC == eb-282 { // AddTask
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			name := readCStrMem(a, uint32(np), 24)
			t.Logf("AddTask %q", name)
			if name == "workbench.task" || name == "Workbench" {
				t.Logf("%s started after %.0f frames; controller at $%06X", name,
					float64(a.cycles)/CyclesPerFrame, a.HardDisk.Configured())
				if a.FastRAM != nil && a.FastRAM.Configured() == 0 {
					t.Error("fast RAM board not configured")
				}
				return a
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	if os.Getenv("GOAMIGA_DIAG") != "" {
		a.Video.DisplayActive = true
		a.RunFrame()
		a.RunFrame()
		if f, err := os.Create(os.Getenv("GOAMIGA_DIAG_PNG")); err == nil {
			png.Encode(f, a.Video.ScreenImage())
			f.Close()
		}
	}
	t.Fatal("Workbench did not start")
	return nil
}

func TestHardfileRDB(t *testing.T) {
	img := make([]byte, 2<<20)
	put := func(off int, v uint32) { binary.BigEndian.PutUint32(img[off:], v) }
	copy(img[0:], "RDSK")
	put(28, 1) // rdb_PartitionList
	put(64, 64)
	put(68, 32)
	put(72, 2) // 64 cylinders, 32 sectors, 2 heads
	part := func(blk int, next uint32, flags uint32, name string, low, high uint32, dosType uint32) {
		b := blk * 512
		copy(img[b:], "PART")
		put(b+16, next)
		put(b+20, flags)
		img[b+36] = byte(len(name))
		copy(img[b+37:], name)
		env := []uint32{16, 128, 0, 2, 1, 32, 2, 0, 0, low, high, 30, 0, 0x7FFFFFFF, 0xFFFFFFFE, 3, dosType}
		for i, v := range env {
			put(b+128+4*i, v)
		}
	}
	part(1, 2, 1, "DH5", 2, 40, 0x444F5303)
	part(2, 3, 2, "NOMOUNT", 41, 50, 0x444F5301) // PBFF_NOMOUNT
	part(3, 0xFFFFFFFF, 0, "Work", 51, 63, 0x444F5301)
	path := filepath.Join(t.TempDir(), "rdb.hdf")
	os.WriteFile(path, img, 0o644)

	h, err := OpenHardfile(path, true, 0, 0x444F5300)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if h.Cylinders != 64 || h.Heads != 2 || h.Sectors != 32 {
		t.Errorf("geometry %d/%d/%d, want 64/2/32", h.Cylinders, h.Heads, h.Sectors)
	}
	if len(h.Partitions) != 2 {
		t.Fatalf("got %d partitions, want 2 (one is NOMOUNT)", len(h.Partitions))
	}
	p0, p1 := h.Partitions[0], h.Partitions[1]
	if p0.Name != "DH5" || !p0.Bootable || p0.Env[deLowCyl] != 2 || p0.Env[deHighCyl] != 40 || p0.Env[deDosType] != 0x444F5303 {
		t.Errorf("partition 0: %+v", p0)
	}
	if p1.Name != "Work" || p1.Bootable || p1.Env[deLowCyl] != 51 {
		t.Errorf("partition 1: %+v", p1)
	}
}

// hdTestDevice attaches a blank plain hardfile to a test machine and
// configures the controller at $E90000, as expansion.library would.
func hdTestDevice(t *testing.T, readOnly bool) (*Amiga, *hdBoard, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blank.hdf")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	a := createTestAmiga(t)
	if err := a.AttachHardfile(path, readOnly); err != nil {
		t.Fatal(err)
	}
	a.wireMemoryMap(false) // chip RAM at 0, not the ROM overlay
	a.pokeBytes(0xE8004A, []byte{0x90})
	a.pokeBytes(0xE80048, []byte{0xE0})
	h := a.HardDisk
	if h.Configured() != 0xE90000 {
		t.Fatalf("board at $%06X, want $E90000", h.Configured())
	}
	if p := h.Files[0].Partitions[0]; p.Name != "DH0" || p.Env[deHighCyl] != 63 {
		t.Fatalf("plain partition %+v", p)
	}
	return a, h, path
}

const hdTestIO = 0x10000 // an IOStdReq in chip RAM

// hdDoIO runs an I/O request through BeginIO; it returns the trap's
// "reply" result, io_Error and io_Actual.
func hdDoIO(a *Amiga, h *hdBoard, cmd uint16, flags byte, data, length, offset uint32) (reply uint32, errCode int8, actual uint32) {
	io := uint32(hdTestIO)
	a.poke16(io+ioCommand, cmd)
	a.poke8(io+ioFlags, flags)
	a.poke32(io+ioData, data)
	a.poke32(io+ioLength, length)
	a.poke32(io+ioOffset, offset)
	a.CPU.A[1] = io
	h.trap(hdTrapBeginIO)
	return a.CPU.D[0], int8(a.peek8(io + ioError)), a.peek32(io + ioActual)
}

func TestHDDeviceIO(t *testing.T) {
	a, h, path := hdTestDevice(t, false)
	if got := a.peek8(0xE90000 + hdROM); got != 0x90 {
		t.Errorf("DiagArea da_Config $%02X, want $90", got)
	}

	a.CPU.A[1], a.CPU.D[0] = hdTestIO, 1
	h.trap(hdTrapOpen)
	if e := int8(a.peek8(hdTestIO + ioError)); e != -1 {
		t.Errorf("open unit 1: io_Error %d, want IOERR_OPENFAIL", e)
	}
	a.CPU.A[1], a.CPU.D[0] = hdTestIO, 0
	h.trap(hdTrapOpen)
	if e := a.peek8(hdTestIO + ioError); e != 0 {
		t.Fatalf("open unit 0: io_Error %d", e)
	}

	pattern := make([]byte, 1024)
	for i := range pattern {
		pattern[i] = byte(i * 7)
	}
	a.pokeBytes(0x11000, pattern)
	if reply, e, n := hdDoIO(a, h, 3, iofQuick, 0x11000, 1024, 2048); reply != 0 || e != 0 || n != 1024 {
		t.Fatalf("CMD_WRITE: reply %d, error %d, actual %d", reply, e, n)
	}
	disk, _ := os.ReadFile(path)
	if string(disk[2048:3072]) != string(pattern) {
		t.Error("CMD_WRITE did not reach the file")
	}

	// A request sent without IOF_QUICK must be replied.
	if reply, e, _ := hdDoIO(a, h, 2, 0, 0x12000, 1024, 2048); reply != 1 || e != 0 {
		t.Fatalf("CMD_READ: reply %d, error %d", reply, e)
	}
	got := make([]byte, 1024)
	a.peekBytes(0x12000, got)
	if string(got) != string(pattern) {
		t.Error("CMD_READ returned different data")
	}

	if _, e, _ := hdDoIO(a, h, 2, iofQuick, 0x12000, 1024, 1<<20); e != -4 {
		t.Errorf("read past the end: error %d, want IOERR_BADLENGTH", e)
	}
	if _, e, _ := hdDoIO(a, h, 0x7777, iofQuick, 0, 0, 0); e != -3 {
		t.Errorf("unknown command: error %d, want IOERR_NOCMD", e)
	}
	if _, e, n := hdDoIO(a, h, 22, iofQuick, 0x13000, 32, 0); e != 0 || n != 32 ||
		a.peek32(0x13000) != 512 || a.peek32(0x13004) != 2048 || a.peek32(0x13008) != 64 {
		t.Errorf("TD_GETGEOMETRY: error %d, actual %d, sector size %d, sectors %d, cylinders %d",
			e, n, a.peek32(0x13000), a.peek32(0x13004), a.peek32(0x13008))
	}
	// TD_ADDCHANGEINT stays with the device: not quick, not replied.
	if reply, _, _ := hdDoIO(a, h, 20, iofQuick, 0, 0, 0); reply != 0 || a.peek8(hdTestIO+ioFlags)&iofQuick != 0 {
		t.Error("TD_ADDCHANGEINT should clear IOF_QUICK and not be replied")
	}
}

func TestHDDeviceWriteProtect(t *testing.T) {
	a, h, _ := hdTestDevice(t, true)
	a.CPU.A[1], a.CPU.D[0] = hdTestIO, 0
	h.trap(hdTrapOpen)
	if _, e, _ := hdDoIO(a, h, 3, iofQuick, 0x11000, 512, 0); e != tderrWriteProt {
		t.Errorf("write to read-only hardfile: error %d, want TDERR_WriteProt", e)
	}
	if _, _, n := hdDoIO(a, h, 15, iofQuick, 0, 0, 0); n == 0 {
		t.Error("TD_PROTSTATUS should report protected")
	}
}
