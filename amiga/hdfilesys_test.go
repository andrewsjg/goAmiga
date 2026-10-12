package amiga

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rdbFS is a filesystem for rdbImageFS to store in the RDB.
type rdbFS struct {
	dosType, version uint32
	code             []byte
}

// rdbImageFS builds a disk with an RDB whose one partition, DH0 (bootable),
// holds the filesystem image fs, with the filesystems given stored in the
// RDB. The geometry is 1 head, 32 sectors; the RDB takes as many
// cylinders as it needs.
func rdbImageFS(fs []byte, filesystems ...rdbFS) []byte {
	const spt = 32
	be := binary.BigEndian
	rdbBlocks := 2 // RDSK, PART
	for _, f := range filesystems {
		rdbBlocks += 1 + (len(f.code)+491)/492
	}
	rdbCyls := (rdbBlocks + spt - 1) / spt
	fsCyls := (len(fs)/512 + spt - 1) / spt
	cyls := rdbCyls + fsCyls
	img := make([]byte, cyls*spt*512)
	put := func(blk, off int, v uint32) { be.PutUint32(img[blk*512+off:], v) }
	sum := func(blk int) {
		n := int(be.Uint32(img[blk*512+4:]))
		put(blk, 8, 0)
		var s uint32
		for i := 0; i < n; i++ {
			s += be.Uint32(img[blk*512+4*i:])
		}
		put(blk, 8, -s)
	}
	header := func(blk int, id string, longs uint32) {
		copy(img[blk*512:], id)
		put(blk, 4, longs)
		put(blk, 12, 7) // HostID
	}

	header(0, "RDSK", 64)
	put(0, 16, 512)
	for _, off := range []int{24, 32, 36} { // no bad blocks, filesystems, drive init
		put(0, off, 0xFFFFFFFF)
	}
	put(0, 28, 1) // rdb_PartitionList
	for off := 40; off < 64; off += 4 {
		put(0, off, 0xFFFFFFFF)
	}
	put(0, 64, uint32(cyls))
	put(0, 68, spt)
	put(0, 72, 1) // heads
	put(0, 76, 1) // interleave
	put(0, 80, uint32(cyls))
	put(0, 96, uint32(cyls))
	put(0, 100, uint32(cyls))
	put(0, 104, 3)
	put(0, 128, 0)                           // rdb_RDBBlocksLo
	put(0, 132, uint32(rdbCyls*spt-1))       // rdb_RDBBlocksHi
	put(0, 136, uint32(rdbCyls))             // rdb_LoCylinder
	put(0, 140, uint32(cyls-1))              // rdb_HiCylinder
	put(0, 144, spt)                         // rdb_CylBlocks
	put(0, 152, uint32(max(rdbBlocks-1, 1))) // rdb_HighRDSKBlock
	copy(img[160:], "GOAMIGA ")
	copy(img[168:], "TEST DISK       ")

	const p = 1
	header(p, "PART", 64)
	put(p, 16, 0xFFFFFFFF) // no next partition
	put(p, 20, 1)          // PBFF_BOOTABLE
	img[p*512+36] = 3
	copy(img[p*512+37:], "DH0")
	env := []uint32{16, 128, 0, 1, 1, spt, 2, 0, 0, uint32(rdbCyls), uint32(cyls - 1), 30, 0, 0x1FE00, 0x7FFFFFFE, 0,
		be.Uint32(fs)} // ..., BootPri 0, DosType from the filesystem
	for i, v := range env {
		put(p, 128+4*i, v)
	}
	sum(p)

	blk := 2
	if len(filesystems) > 0 {
		put(0, 32, uint32(blk))
	}
	for i, f := range filesystems {
		fshd := blk
		blk++
		header(fshd, "FSHD", 64)
		put(fshd, 16, 0xFFFFFFFF)
		if i+1 < len(filesystems) {
			put(fshd, 16, uint32(blk+(len(f.code)+491)/492))
		}
		put(fshd, 32, f.dosType)
		put(fshd, 36, f.version)
		put(fshd, 40, 0x180) // PatchFlags: SegList, GlobalVec
		put(fshd, 72, uint32(blk))
		put(fshd, 76, 0xFFFFFFFF) // GlobalVec -1
		sum(fshd)
		for off := 0; off < len(f.code); off += 492 {
			n := copy(img[blk*512+20:blk*512+512], f.code[off:])
			header(blk, "LSEG", uint32(5+(n+3)/4))
			put(blk, 16, 0xFFFFFFFF)
			if off+492 < len(f.code) {
				put(blk, 16, uint32(blk+1))
			}
			sum(blk)
			blk++
		}
	}
	sum(0)
	copy(img[rdbCyls*spt*512:], fs)
	return img
}

// ---- A minimal OFS/FFS reader and an OFS to FFS converter ----

func adfLong(d []byte, blk, off int) uint32 { return binary.BigEndian.Uint32(d[blk*512+off:]) }

// adfEntries lists a directory: name, header block, secondary type.
func adfEntries(d []byte, dir int) (names []string, blocks []int, types []int32) {
	for i := 0; i < 72; i++ {
		for h := int(adfLong(d, dir, 24+4*i)); h != 0; h = int(adfLong(d, h, 496)) {
			names = append(names, string(d[h*512+433:h*512+433+int(d[h*512+432])]))
			blocks = append(blocks, h)
			types = append(types, int32(adfLong(d, h, 508)))
		}
	}
	return
}

// adfDataBlocks lists a file's data blocks in order.
func adfDataBlocks(d []byte, h int) []int {
	var out []int
	for b := h; b != 0; b = int(adfLong(d, b, 504)) {
		n := int(adfLong(d, b, 8))
		for i := 0; i < n; i++ {
			out = append(out, int(adfLong(d, b, 308-4*i)))
		}
	}
	return out
}

// adfRead reads a file from an OFS or FFS image.
func adfRead(d []byte, path string) []byte {
	cur := 880
	for _, part := range strings.Split(path, "/") {
		names, blocks, _ := adfEntries(d, cur)
		cur = 0
		for i, n := range names {
			if strings.EqualFold(n, part) {
				cur = blocks[i]
			}
		}
		if cur == 0 {
			return nil
		}
	}
	size := int(adfLong(d, cur, 324))
	var data []byte
	for _, b := range adfDataBlocks(d, cur) {
		if d[3]&1 != 0 {
			data = append(data, d[b*512:b*512+512]...)
		} else {
			data = append(data, d[b*512+24:b*512+24+int(adfLong(d, b, 12))]...)
		}
	}
	return data[:size]
}

// ofsToFFS converts an OFS image to FFS in place: each file's data is
// rewritten into the first of its blocks, 512 bytes a block, and its block
// tables shortened. The blocks left over stay allocated.
func ofsToFFS(d []byte) {
	be := binary.BigEndian
	checksum := func(b int) {
		be.PutUint32(d[b*512+20:], 0)
		var s uint32
		for i := 0; i < 128; i++ {
			s += be.Uint32(d[b*512+4*i:])
		}
		be.PutUint32(d[b*512+20:], -s)
	}
	var convert func(dir int)
	convert = func(dir int) {
		_, blocks, types := adfEntries(d, dir)
		for i, h := range blocks {
			if types[i] == 2 {
				convert(h)
				continue
			}
			size := int(adfLong(d, h, 324))
			var data []byte
			for _, b := range adfDataBlocks(d, h) {
				data = append(data, d[b*512+24:b*512+24+int(adfLong(d, b, 12))]...)
			}
			data = data[:size]
			old := adfDataBlocks(d, h)
			used := old[:(size+511)/512]
			for j, b := range used {
				clear(d[b*512 : b*512+512])
				copy(d[b*512:b*512+512], data[j*512:])
			}
			first := uint32(0)
			if len(used) > 0 {
				first = uint32(used[0])
			}
			be.PutUint32(d[h*512+16:], first)
			prev := -1
			for b, rest := h, used; b != 0; b = int(adfLong(d, b, 504)) {
				if b != h && len(rest) == 0 {
					be.PutUint32(d[prev*512+504:], 0)
					checksum(prev)
					break
				}
				n := min(72, len(rest))
				for k := 0; k < 72; k++ {
					v := uint32(0)
					if k < n {
						v = uint32(rest[k])
					}
					be.PutUint32(d[b*512+308-4*k:], v)
				}
				be.PutUint32(d[b*512+8:], uint32(n))
				rest = rest[n:]
				checksum(b)
				prev = b
			}
		}
	}
	convert(880)
	d[3] = 1 // DOS\1
}

// ---- Tests ----

func TestParseHunks(t *testing.T) {
	be := binary.BigEndian
	var b []byte
	l := func(vs ...uint32) {
		for _, v := range vs {
			b = be.AppendUint32(b, v)
		}
	}
	l(hunkHeader, 0, 3, 0, 2, 2, 1|1<<30, 4) // code 8 bytes, data 4 (chip), BSS 16
	l(hunkCode, 2, 0x11111111, 0x00000002)
	l(hunkReloc32, 1, 2, 4, 0) // code+4 -> BSS
	l(hunkEnd)
	l(hunkData, 1, 0x00000000)
	l(hunkReloc32Short)
	for _, w := range []uint16{1, 0, 0, 0} { // data+0 -> code; terminator
		b = be.AppendUint16(b, w)
	}
	l(hunkEnd)
	l(hunkBSS, 4, hunkSymbol, 1, 0x41424344, 0, 0, hunkEnd)

	hunks, err := parseHunks(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 3 || hunks[0].size != 8 || hunks[1].size != 4 || !hunks[1].chip || hunks[2].size != 16 {
		t.Fatalf("hunks %+v", hunks)
	}
	size, chip := segListSize(hunks)
	if size != 8+8+8+4+8+16 || !chip {
		t.Errorf("seglist size %d chip %v", size, chip)
	}
	const at = 0x20000
	img, seg := buildSegList(hunks, at)
	if seg != (at+4)>>2 {
		t.Errorf("seglist BPTR $%X", seg)
	}
	codeAt, dataAt, bssAt := uint32(at+8), uint32(at+16+8), uint32(at+28+8)
	if be.Uint32(img[0:]) != 16 || be.Uint32(img[4:]) != (dataAt-4)>>2 || be.Uint32(img[20:]) != (bssAt-4)>>2 {
		t.Errorf("segment links: % X", img[:32])
	}
	if v := be.Uint32(img[12:]); v != bssAt+2 {
		t.Errorf("RELOC32 gave $%X, want $%X", v, bssAt+2)
	}
	if v := be.Uint32(img[24:]); v != codeAt {
		t.Errorf("RELOC32SHORT gave $%X, want $%X", v, codeAt)
	}
	if _, err := parseHunks([]byte("not a program")); err == nil {
		t.Error("parseHunks accepted a non-executable")
	}
}

// bootRDBFS boots rom from an RDB disk holding image, with the filesystems
// given in the RDB, and returns the machine and its driver trace.
func bootRDBFS(t *testing.T, romPath string, image []byte, filesystems ...rdbFS) (*Amiga, string) {
	t.Helper()
	rom, err := LoadROM(romPath)
	if err != nil {
		t.Skip(err)
	}
	path := filepath.Join(t.TempDir(), "rdb.hdf")
	if err := os.WriteFile(path, rdbImageFS(image, filesystems...), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := New(rom, nil)
	if err := a.ConfigureMemory(512, 1024); err != nil {
		t.Fatal(err)
	}
	if err := a.AttachHardfile(path, false); err != nil {
		t.Fatal(err)
	}
	if f := a.HardDisk.Files[0]; f.FSError != nil || len(f.FileSystems) != len(filesystems) {
		t.Fatalf("filesystems in the RDB: %d, %v", len(f.FileSystems), f.FSError)
	}
	var trace strings.Builder
	a.HardDisk.Trace = &trace
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Log(trace.String())
		t.Fatal("Workbench did not start")
	}
	t.Log(trace.String())
	return a, trace.String()
}

func readDisk(t *testing.T, path string) []byte {
	t.Helper()
	d, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	return d
}

// Kickstart 1.3's ROM has only OFS: Workbench 1.3 on an FFS partition
// boots with the FastFileSystem from the RDB.
func TestRDBFileSystemKS13(t *testing.T) {
	wb := readDisk(t, "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	ffs := adfRead(wb, "l/FastFileSystem")
	if ffs == nil {
		t.Fatal("no l/FastFileSystem on the Workbench disk")
	}
	ofsToFFS(wb)
	if got := adfRead(wb, "s/startup-sequence"); !strings.Contains(string(got), "LoadWB") {
		t.Fatalf("the converted disk reads back wrong: %q", got)
	}
	_, trace := bootRDBFS(t, "../ROMS/Kick13.rom", wb, rdbFS{0x444F5301, 34 << 16, ffs})
	if !strings.Contains(trace, "loaded filesystem DOS\\1 34.0") {
		t.Error("the filesystem was not loaded from the RDB")
	}
}

// Kickstart 3.2's FFS is newer than an RDB's 1.3 copy: the ROM's is used.
func TestRDBFileSystemOlderThanROM(t *testing.T) {
	inst := readDisk(t, "../disks/Install3.2.adf")
	wb := readDisk(t, "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	_, trace := bootRDBFS(t, "../ROMS/Kick32.rom", inst, rdbFS{0x444F5301, 34 << 16, adfRead(wb, "l/FastFileSystem")})
	if !strings.Contains(trace, "from FileSystem.resource") || strings.Contains(trace, "loaded filesystem") {
		t.Error("expected the ROM's FFS to be used")
	}
}

// A newer filesystem in the RDB is loaded on Kickstart 3.2 too, and added
// to FileSystem.resource.
func TestRDBFileSystemNewerThanROM(t *testing.T) {
	inst := readDisk(t, "../disks/Install3.2.adf")
	ffs := adfRead(inst, "L/FastFileSystem")
	a, trace := bootRDBFS(t, "../ROMS/Kick32.rom", inst, rdbFS{0x444F5301, 99 << 16, ffs})
	if !strings.Contains(trace, "loaded filesystem DOS\\1 99.0") {
		t.Error("the filesystem was not loaded from the RDB")
	}
	if e := a.fileSysEntry(0x444F5301); e == 0 || a.peek32(e+fseVersion) != 99<<16 {
		t.Error("FileSystem.resource has no entry for the loaded filesystem")
	}
}
