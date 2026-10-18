package amiga

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ideTest fits Gayle with a 1 MB patterned hardfile as the master.
func ideTest(t *testing.T) (*Amiga, *gayle, string) {
	t.Helper()
	img := make([]byte, 1<<20)
	for i := range img {
		img[i] = byte(i/512) ^ byte(i*7)
	}
	path := filepath.Join(t.TempDir(), "ide.hdf")
	os.WriteFile(path, img, 0o644)
	a := createTestAmiga(t)
	if err := a.AttachIDE(path, false); err != nil {
		t.Fatal(err)
	}
	return a, a.Gayle, path
}

// regAddr is a task file register's address on Gayle or the A4000.
func (g *gayle) regAddr(reg int) int {
	switch {
	case g.a4000 && reg == 0:
		return a4000IDEData
	case g.a4000 && reg == 0x406:
		return a4000IDECtrl + 0x1A
	case g.a4000:
		return a4000IDEData + 4*reg + 2
	case reg == 0x406:
		return gayleIDEBase + 0x1018
	}
	return gayleIDEBase + reg*4
}

func (g *gayle) w8(reg int, v byte) { g.Write(g.regAddr(reg), []byte{v}) }

func (g *gayle) r8(reg int) byte {
	var b [1]byte
	g.Read(g.regAddr(reg), b[:])
	return b[0]
}

func (g *gayle) rData() uint16 {
	var b [2]byte
	g.Read(g.regAddr(0), b[:])
	return binary.BigEndian.Uint16(b[:])
}

func (g *gayle) wData(v uint16) { g.Write(g.regAddr(0), binary.BigEndian.AppendUint16(nil, v)) }

// waitDRQ lets the drive finish being busy and checks it wants data moved.
func (g *gayle) waitReady(t *testing.T, wantDRQ bool) {
	t.Helper()
	g.tick(ideCommandDelay)
	st := g.r8(0x406)
	if st&ataBSY != 0 || (st&ataDRQ != 0) != wantDRQ || st&ataERR != 0 {
		t.Fatalf("status $%02X (error $%02X), want DRQ %v", st, g.r8(1), wantDRQ)
	}
}

func TestIDEIdentify(t *testing.T) {
	_, g, _ := ideTest(t)
	g.w8(6, 0xA0) // master
	g.w8(7, 0xEC)
	if g.r8(0x406)&ataBSY == 0 {
		t.Error("not busy after the command")
	}
	g.waitReady(t, true)
	// The drive's words arrive byte-swapped, as on a real Amiga.
	var id [256]uint16
	for i := range id {
		w := g.rData()
		id[i] = w>>8 | w<<8
	}
	if st := g.r8(7); st&ataDRQ != 0 {
		t.Errorf("DRQ still set after 256 words (status $%02X)", st)
	}
	// 2048 blocks; a plain hardfile's geometry: 1 head, 32 sectors.
	if id[1] != 64 || id[3] != 1 || id[6] != 32 {
		t.Errorf("geometry %d/%d/%d, want 64/1/32", id[1], id[3], id[6])
	}
	if lba := uint32(id[61])<<16 | uint32(id[60]); lba != 2048 || id[49]&0x200 == 0 {
		t.Errorf("LBA capacity %d (capabilities $%04X), want 2048 with LBA", lba, id[49])
	}
	var model []byte
	for _, w := range id[27:47] {
		model = append(model, byte(w>>8), byte(w))
	}
	if !strings.HasPrefix(string(model), "goAmiga hardfile") {
		t.Errorf("model %q", model)
	}
}

func TestIDEReadWrite(t *testing.T) {
	a, g, path := ideTest(t)
	img, _ := os.ReadFile(path)

	// READ SECTORS, two sectors from LBA 100.
	g.w8(6, 0xE0) // LBA, master
	g.w8(2, 2)
	g.w8(3, 100)
	g.w8(4, 0)
	g.w8(5, 0)
	g.w8(7, 0x20)
	got := make([]byte, 0, 1024)
	for s := 0; s < 2; s++ {
		g.waitReady(t, true)
		for i := 0; i < 256; i++ {
			got = binary.BigEndian.AppendUint16(got, g.rData())
		}
	}
	if !bytes.Equal(got, img[100*512:102*512]) {
		t.Fatal("READ SECTORS data differs from the image")
	}
	if g.r8(7)&ataDRQ != 0 || g.r8(3) != 101 {
		t.Errorf("after the read: status $%02X, sector register %d (want 101)", g.r8(7), g.r8(3))
	}

	// The same through CHS: cylinder 3, head 0, sector 5 = LBA 3*32+4.
	g.w8(6, 0xA0)
	g.w8(2, 1)
	g.w8(3, 5)
	g.w8(4, 3)
	g.w8(7, 0x20)
	g.waitReady(t, true)
	got = got[:0]
	for i := 0; i < 256; i++ {
		got = binary.BigEndian.AppendUint16(got, g.rData())
	}
	if !bytes.Equal(got, img[100*512:101*512]) {
		t.Fatal("CHS read differs from LBA 100")
	}

	// SET MULTIPLE 4, then WRITE MULTIPLE 8 sectors at LBA 500.
	g.w8(2, 4)
	g.w8(7, 0xC6)
	g.waitReady(t, false)
	g.r8(7)
	pattern := make([]byte, 8*512)
	for i := range pattern {
		pattern[i] = byte(i * 13)
	}
	g.w8(6, 0xE0)
	g.w8(2, 8)
	g.w8(3, 0xF4) // 500 = $1F4
	g.w8(4, 0x01)
	g.w8(5, 0)
	g.w8(7, 0xC5)
	for blk := 0; blk < 2; blk++ {
		if st := g.r8(0x406); st&ataDRQ == 0 {
			t.Fatalf("block %d: no DRQ (status $%02X)", blk, st)
		}
		for i := 0; i < 4*256; i++ {
			g.wData(binary.BigEndian.Uint16(pattern[blk*2048+2*i:]))
		}
		g.tick(ideCommandDelay)
		if !g.line() {
			t.Errorf("block %d: no interrupt after writing it", blk)
		}
		g.r8(7) // acknowledge
	}
	disk, _ := os.ReadFile(path)
	if !bytes.Equal(disk[500*512:508*512], pattern) {
		t.Error("WRITE MULTIPLE didn't reach the image")
	}

	// Gayle latches the interrupt, and passes it on when enabled.
	g.Write(0xDA9000, []byte{0}) // clear
	g.Write(0xDAA000, []byte{0x80})
	a.Custom.WriteReg16(0x09C, 0)
	g.w8(7, 0xE7) // FLUSH CACHE
	g.tick(ideCommandDelay)
	var b [1]byte
	if g.Read(0xDA9000, b[:]); b[0]&0x80 == 0 {
		t.Error("Gayle didn't latch the IDE interrupt")
	}
	if a.Custom.ReadReg16(0x09C)&0x08 == 0 {
		t.Error("no INT2 (PORTS) request")
	}
	g.r8(7)
	g.Write(0xDA9000, []byte{0x7F})
	a.Custom.WriteReg16(0x09C, 0)
	g.tick(10)
	if a.Custom.ReadReg16(0x09C)&0x08 != 0 {
		t.Error("INT2 still requested after the driver cleared it")
	}
}

// Bad requests fail the way a drive fails them.
func TestIDEErrors(t *testing.T) {
	_, g, _ := ideTest(t)
	g.w8(6, 0xE0)
	g.w8(2, 1)
	g.w8(3, 0)
	g.w8(4, 0)
	g.w8(5, 0x10) // LBA 1M: past the end
	g.w8(7, 0x20)
	g.tick(ideCommandDelay)
	if st := g.r8(7); st&ataERR == 0 || g.r8(1)&ataIDNF == 0 {
		t.Errorf("read past the end: status $%02X error $%02X, want IDNF", st, g.r8(1))
	}
	g.w8(7, 0xA1) // IDENTIFY PACKET DEVICE: not an ATAPI drive
	g.tick(ideCommandDelay)
	if st := g.r8(7); st&ataERR == 0 || g.r8(1)&ataABRT == 0 {
		t.Errorf("IDENTIFY PACKET: status $%02X error $%02X, want ABRT", st, g.r8(1))
	}
	g.w8(6, 0xB0) // the slave: not fitted
	if st := g.r8(7); st != 0 {
		t.Errorf("absent slave status $%02X, want 0", st)
	}
}

// Gayle's ID comes out a bit at a time in bit 7: $D0.
func TestGayleID(t *testing.T) {
	_, g, _ := ideTest(t)
	g.Write(0xDE1000, []byte{0})
	id := byte(0)
	for i := 0; i < 8; i++ {
		var b [1]byte
		g.Read(0xDE1000, b[:])
		id = id<<1 | b[0]>>7
	}
	if id != 0xD0 {
		t.Errorf("Gayle ID $%02X, want $D0", id)
	}
}

// Kickstart 3.2 for the A1200 finds an IDE drive through Gayle and its
// scsi.device boots Workbench from the RDB partition on it.
func TestGayleIDEBootKS32A1200(t *testing.T) {
	bootIDE(t, readDisk(t, "../Workbench/Install3.2.adf"))
}

// Kickstart's scsi.device loads a filesystem from the RDB itself.
func TestGayleIDEFileSystemKS32A1200(t *testing.T) {
	inst := readDisk(t, "../Workbench/Install3.2.adf")
	a := bootIDE(t, inst, rdbFS{0x444F5301, 99 << 16, adfRead(inst, "L/FastFileSystem")})
	if e := a.fileSysEntry(0x444F5301); e == 0 || a.peek32(e+fseVersion) != 99<<16 {
		t.Error("scsi.device didn't load the filesystem from the RDB")
	}
}

// bootIDE boots an A1200 from an RDB disk holding image on the IDE
// interface, with the filesystems given in the RDB.
func bootIDE(t *testing.T, image []byte, filesystems ...rdbFS) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/kick32a1200.rom")
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(t.TempDir(), "ide-rdb.hdf")
	if err := os.WriteFile(path, rdbImageFS(image, filesystems...), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := New(rom, nil)
	a.SetCPU("68020", 0)
	a.SetChipset("aga")
	a.SetChipRAM(2048)
	a.ConfigureMemory(0, 0)
	if err := a.AttachIDE(path, true); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GAYLE_TRACE") != "" {
		a.Gayle.Trace = logWriter{t}
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench didn't start from the IDE drive")
	}
	return a
}
