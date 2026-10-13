package amiga

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adfExtract copies an ADF's files into a host folder.
func adfExtract(t *testing.T, d []byte, dst string) {
	t.Helper()
	var walk func(dir int, to string)
	walk = func(dir int, to string) {
		names, blocks, types := adfEntries(d, dir)
		for i, n := range names {
			p := filepath.Join(to, hostFileName(n))
			if types[i] == 2 {
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
				walk(blocks[i], p)
				continue
			}
			if err := os.WriteFile(p, adfFileData(d, blocks[i]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	walk(880, dst)
}

// bootFolder boots rom from a host folder holding the files of adf.
func bootFolder(t *testing.T, romPath, adf string) (*Amiga, string) {
	t.Helper()
	rom, err := LoadROM(romPath)
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "Workbench")
	os.Mkdir(dir, 0o755)
	adfExtract(t, readDisk(t, adf), dir)
	a, _ := New(rom, nil)
	if err := a.ConfigureMemory(512, 1024); err != nil {
		t.Fatal(err)
	}
	if err := a.AttachDirectory(dir, "", false); err != nil {
		t.Fatal(err)
	}
	if !a.HardDisk.Dirs[0].Bootable {
		t.Fatal("a folder with S/Startup-Sequence should be bootable")
	}
	var trace strings.Builder
	if os.Getenv("HOSTFS_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
	} else {
		a.HardDisk.Trace = &trace
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		s := trace.String()
		t.Log(s[max(0, len(s)-6000):])
		t.Fatal("Workbench did not start from the folder")
	}
	return a, dir
}

func TestFolderBootKS13(t *testing.T) {
	bootFolder(t, "../ROMS/Kick13.rom", "../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
}

func TestFolderBootKS32(t *testing.T) {
	bootFolder(t, "../ROMS/Kick32.rom", "../Workbench/Install3.2.adf")
}

// hostfsRig drives a folder's packet handling directly, with its
// structures in chip RAM.
type hostfsRig struct {
	t   *testing.T
	a   *Amiga
	d   *HostDir
	str uint32 // next free scratch address for strings
}

func newHostfsRig(t *testing.T, readOnly bool) *hostfsRig {
	t.Helper()
	a := createTestAmiga(t)
	dir := t.TempDir()
	if err := a.AttachDirectory(dir, "Test", readOnly); err != nil {
		t.Fatal(err)
	}
	a.wireMemoryMap(false)
	d := a.HardDisk.Dirs[0]
	d.reset()
	d.port, d.pool = 0x1000, 0x10000
	d.volNode, d.poolTop, d.poolEnd = d.pool, d.pool+hostVolSize, d.pool+hostPoolSize
	d.mountVolume(a)
	return &hostfsRig{t: t, a: a, d: d, str: 0x30000}
}

// bstr puts a BCPL string in memory and returns its BPTR.
func (r *hostfsRig) bstr(s string) uint32 {
	addr := r.str
	r.a.pokeBString(addr, s, 255)
	r.str += 260
	return addr >> 2
}

// pktRes is a packet's Res1 and Res2.
type pktRes struct{ res1, res2 uint32 }

// pkt sends a packet; string arguments become BSTRs.
func (r *hostfsRig) pkt(typ uint32, args ...any) pktRes {
	var arg [7]uint32
	for i, v := range args {
		switch v := v.(type) {
		case string:
			arg[i] = r.bstr(v)
		case int:
			arg[i] = uint32(v)
		case uint32:
			arg[i] = v
		}
	}
	res1, res2 := r.d.packet(r.a, typ, arg)
	return pktRes{res1, res2}
}

func (r *hostfsRig) ok(what string, p pktRes) uint32 {
	r.t.Helper()
	if p.res2 != 0 || p.res1 == 0 {
		r.t.Fatalf("%s: res1 %X, error %d", what, p.res1, int32(p.res2))
	}
	return p.res1
}

func (r *hostfsRig) fails(what string, want uint32, p pktRes) {
	r.t.Helper()
	if p.res2 != want {
		r.t.Errorf("%s: res1 %X, error %d; want error %d", what, p.res1, p.res2, want)
	}
}

const (
	rigFH  = 0x40000 // a FileHandle
	rigFIB = 0x40100 // a FileInfoBlock
	rigBuf = 0x41000
)

func (r *hostfsRig) open(typ uint32, lock uint32, name string) uint32 {
	r.t.Helper()
	r.ok("open "+name, r.pkt(typ, rigFH>>2, lock, name))
	return r.a.peek32(rigFH + 36)
}

func (r *hostfsRig) host(name string) string { return filepath.Join(r.d.Path, name) }

func TestHostfsFiles(t *testing.T) {
	r := newHostfsRig(t, false)
	a := r.a

	// Write a new file and read it back.
	fh := r.open(actionFindOutput, 0, "Test:Hello.txt")
	a.pokeBytes(rigBuf, []byte("Hello, Amiga"))
	if n := r.ok("write", r.pkt(actionWrite, fh, rigBuf, 12)); n != 12 {
		t.Errorf("wrote %d", n)
	}
	r.ok("close", r.pkt(actionEnd, fh))
	if b, _ := os.ReadFile(r.host("Hello.txt")); string(b) != "Hello, Amiga" {
		t.Errorf("host file holds %q", b)
	}
	fh = r.open(actionFindInput, 0, "hello.TXT") // any case
	if p := r.pkt(actionSeek, fh, 7, -1); p.res1 != 0 || p.res2 != 0 {
		t.Errorf("seek returned old position %d, error %d", p.res1, p.res2)
	}
	n := r.pkt(actionRead, fh, rigBuf, 100).res1
	buf := make([]byte, n)
	a.peekBytes(rigBuf, buf)
	if string(buf) != "Amiga" {
		t.Errorf("read %q after seeking", buf)
	}
	r.fails("seek past the end", errSeek, r.pkt(actionSeek, fh, 100, -1))
	r.fails("delete an open file", errObjectInUse, r.pkt(actionDeleteObject, 0, "Hello.txt"))
	r.fails("MODE_NEWFILE on an open file", errObjectInUse, r.pkt(actionFindOutput, rigFH>>2, 0, "Hello.txt"))
	r.ok("close", r.pkt(actionEnd, fh))

	// Directories, locks relative to locks, parents.
	dir := r.ok("create dir", r.pkt(actionCreateDir, 0, "Drawer"))
	r.ok("free", r.pkt(actionFreeLock, dir))
	dir = r.ok("lock dir", r.pkt(actionLocateObject, 0, "drawer", -2))
	fh = r.open(actionFindOutput, dir, "Inner")
	r.ok("close", r.pkt(actionEnd, fh))
	if _, err := os.Stat(r.host("Drawer/Inner")); err != nil {
		t.Error(err)
	}
	r.fails("exclusive lock on a locked dir", errObjectInUse, r.pkt(actionLocateObject, 0, "Drawer", -1))
	up := r.ok("lock /", r.pkt(actionLocateObject, dir, "/Hello.txt", -2))
	r.ok("free", r.pkt(actionFreeLock, up))
	par := r.ok("parent", r.pkt(actionParent, dir))
	r.ok("examine root", r.pkt(actionExamineObject, par, rigFIB>>2))
	if typ := int32(a.peek32(rigFIB + fibType)); typ != 1 || a.peekBString(rigFIB+fibName) != "Test" {
		t.Errorf("root examines as type %d %q", typ, a.peekBString(rigFIB+fibName))
	}
	var names []string
	for {
		p := r.pkt(actionExamineNext, par, rigFIB>>2)
		if p.res1 == 0 {
			if p.res2 != errNoMoreEntries {
				t.Errorf("ExNext ended with error %d", p.res2)
			}
			break
		}
		names = append(names, a.peekBString(rigFIB+fibName))
	}
	if strings.Join(names, ",") != "Drawer,Hello.txt" {
		t.Errorf("root lists %q", names)
	}
	r.ok("free", r.pkt(actionFreeLock, par))

	// Rename moves locks with it; a dir with something in it stays.
	r.ok("rename", r.pkt(actionRenameObject, 0, "Drawer", 0, "Renamed"))
	r.ok("examine renamed lock", r.pkt(actionExamineObject, dir, rigFIB>>2))
	if a.peekBString(rigFIB+fibName) != "Renamed" {
		t.Errorf("lock examines as %q after the rename", a.peekBString(rigFIB+fibName))
	}
	r.ok("free", r.pkt(actionFreeLock, dir))
	r.fails("delete a full dir", errDirNotEmpty, r.pkt(actionDeleteObject, 0, "Renamed"))
	r.ok("delete", r.pkt(actionDeleteObject, 0, "Renamed/Inner"))
	os.WriteFile(r.host("Renamed/.DS_Store"), nil, 0o644)
	r.ok("delete the dir", r.pkt(actionDeleteObject, 0, "Renamed"))
	r.fails("lock a missing file", errObjectNotFound, r.pkt(actionLocateObject, 0, "Renamed", -2))
	r.fails("lock in a missing dir", errDirNotFound, r.pkt(actionLocateObject, 0, "Nope/x", -2))

	// Protection bits and comments go in a .uaem file, which the Amiga
	// doesn't see.
	r.ok("protect", r.pkt(actionSetProtect, 0, 0, "Hello.txt", 0x40|protDelete)) // +s -d
	r.ok("comment", r.pkt(actionSetComment, 0, 0, "Hello.txt", "Hi there"))
	if b, _ := os.ReadFile(r.host("Hello.txt.uaem")); !strings.HasPrefix(string(b), "-s--rwe- ") ||
		!strings.HasSuffix(string(b), " Hi there\n") {
		t.Errorf(".uaem holds %q", b)
	}
	lock := r.ok("lock", r.pkt(actionLocateObject, 0, "Hello.txt", -2))
	r.ok("examine", r.pkt(actionExamineObject, lock, rigFIB>>2))
	if p := a.peek32(rigFIB + fibProtection); p != 0x41 || a.peekBString(rigFIB+fibComment) != "Hi there" {
		t.Errorf("examined protection $%X comment %q", p, a.peekBString(rigFIB+fibComment))
	}
	r.ok("free", r.pkt(actionFreeLock, lock))
	r.fails("delete a delete-protected file", errDeleteProtected, r.pkt(actionDeleteObject, 0, "Hello.txt"))
	r.ok("protect", r.pkt(actionSetProtect, 0, 0, "Hello.txt", 0))
	r.ok("comment", r.pkt(actionSetComment, 0, 0, "Hello.txt", ""))
	if _, err := os.Stat(r.host("Hello.txt.uaem")); err == nil {
		t.Error(".uaem file kept with default attributes")
	}

	// Latin-1 names become UTF-8 on the host.
	fh = r.open(actionFindOutput, 0, "Caf\xe9")
	r.ok("close", r.pkt(actionEnd, fh))
	if _, err := os.Stat(r.host("Café")); err != nil {
		t.Error(err)
	}

	r.ok("info", r.pkt(actionDiskInfo, rigBuf>>2))
	if a.peek32(rigBuf+8) != 82 || a.peek32(rigBuf+20) != 512 || a.peek32(rigBuf+28) != r.d.volNode>>2 {
		t.Errorf("InfoData: state %d, block size %d", a.peek32(rigBuf+8), a.peek32(rigBuf+20))
	}
	if len(r.d.locks) != 0 || len(r.d.files) != 0 {
		t.Errorf("left %d locks and %d files", len(r.d.locks), len(r.d.files))
	}
}

func TestHostfsReadOnly(t *testing.T) {
	r := newHostfsRig(t, true)
	os.WriteFile(r.host("File"), []byte("data"), 0o644)
	r.fails("create", errDiskWriteProtected, r.pkt(actionFindOutput, rigFH>>2, 0, "New"))
	r.fails("delete", errDiskWriteProtected, r.pkt(actionDeleteObject, 0, "File"))
	fh := r.open(actionFindInput, 0, "File")
	r.a.pokeBytes(rigBuf, []byte("x"))
	r.fails("write", errDiskWriteProtected, r.pkt(actionWrite, fh, rigBuf, 1))
	r.ok("close", r.pkt(actionEnd, fh))
}

// A data folder beside a bootable hardfile: Workbench boots from the
// hardfile and the folder's volume is mounted.
func TestFolderBesideHardfile(t *testing.T) {
	for _, c := range []struct{ rom, adf string }{
		{"../ROMS/Kick13.rom", "../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf"},
		{"../ROMS/Kick32.rom", "../Workbench/Install3.2.adf"},
	} {
		rom, err := LoadROM(c.rom)
		if err != nil {
			t.Skip(err)
		}
		a, _ := New(rom, nil)
		a.ConfigureMemory(512, 1024)
		if err := a.AttachHardfile(hardfileFromADF(t, c.adf), false); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "ReadMe"), []byte("hi"), 0o644)
		if err := a.AttachDirectory(dir, "Data", false); err != nil {
			t.Fatal(err)
		}
		d := a.HardDisk.Dirs[0]
		if d.Bootable || d.Device != "" {
			t.Fatalf("bootable %v, device %q before reset", d.Bootable, d.Device)
		}
		a.Reset()
		if !waitWorkbench(t, a) {
			t.Fatalf("%s: Workbench did not start", c.rom)
		}
		for i := 0; i < 200 && d.port == 0; i++ {
			a.RunFrame()
		}
		if d.port == 0 {
			t.Errorf("%s: the folder's handler didn't start", c.rom)
		}
		if png := os.Getenv("HOSTFS_PNG"); png != "" {
			a.Video.DisplayActive = true
			for range 300 {
				a.RunFrame()
			}
			savePNG(t, a, strings.Replace(png, ".png", filepath.Base(c.rom)+".png", 1))
		}
		if d.Device != "DH1" {
			t.Errorf("device %s, want DH1", d.Device)
		}
	}
}

// screenModePrefs builds ENV:Sys/screenmode.prefs for a display mode.
func screenModePrefs(displayID uint32, w, h, depth uint16) []byte {
	be := binary.BigEndian
	var b []byte
	b = append(b, "FORM\x00\x00\x00\x00PREF"...)
	b = append(b, "PRHD\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00"...)
	b = append(b, "SCRM\x00\x00\x00\x1c"...)
	b = append(b, make([]byte, 16)...)
	b = be.AppendUint32(b, displayID)
	b = be.AppendUint16(b, w)
	b = be.AppendUint16(b, h)
	b = be.AppendUint16(b, depth)
	b = be.AppendUint16(b, 0)
	be.PutUint32(b[4:], uint32(len(b)-8))
	return b
}

// workbenchInMode boots the A1200 from a folder holding the Install 3.2
// disk, with IPrefs and screen mode preferences for displayID, and returns
// the machine some frames after Workbench starts.
func workbenchInMode(t *testing.T, displayID uint32, w, h, depth uint16) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "Install3.2")
	os.Mkdir(dir, 0o755)
	adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), dir)
	wb := readDisk(t, "../Workbench/Workbench3.2.adf")
	os.WriteFile(filepath.Join(dir, "C", "IPrefs"), adfRead(wb, "C/IPrefs"), 0o644)
	ss := filepath.Join(dir, "S", "Startup-Sequence")
	b, _ := os.ReadFile(ss)
	os.WriteFile(ss, []byte(strings.Replace(string(b), "LoadWB", "C:IPrefs\nLoadWB", 1)), 0o644)
	os.WriteFile(filepath.Join(dir, "Prefs", "Env-Archive", "Sys", "screenmode.prefs"),
		screenModePrefs(displayID, w, h, depth), 0o644)

	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 4096)
	if err := a.AttachDirectory(dir, "", false); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start")
	}
	a.Video.DisplayActive = true
	for range 400 {
		a.RunFrame()
	}
	return a
}

// Workbench on AGA in PAL Super-High Res, 16 colours: a frame of
// super-hires columns.
func TestWorkbenchSuperHiresAGA(t *testing.T) {
	a := workbenchInMode(t, 0x00029020, 1280, 256, 4)
	bplcon0 := a.Custom.ReadReg16(0x100)
	t.Logf("BPLCON0 $%04X, FMODE $%04X, BPLCON3 $%04X, %d columns", bplcon0,
		a.Custom.ReadReg16(0x1FC), a.Custom.ReadReg16(0x106), a.Video.Width)
	if png := os.Getenv("HOSTFS_PNG"); png != "" {
		savePNG(t, a, png)
	}
	if bplcon0&0x0040 == 0 || planesOf(bplcon0, true) != 4 {
		t.Fatalf("Workbench isn't in 16-colour super-hires: BPLCON0 $%04X", bplcon0)
	}
	if a.Video.Width != 2*a.Video.baseW {
		t.Errorf("frame is %d columns, want %d", a.Video.Width, 2*a.Video.baseW)
	}
}
