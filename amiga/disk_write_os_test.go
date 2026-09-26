package amiga

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// ofsChecksumOK reports whether an OFS block's longs sum to zero (the
// checksum at offset 20 makes them).
func ofsChecksumOK(b []byte) bool {
	var sum uint32
	for i := 0; i < 512; i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	return sum == 0
}

// ofsFixChecksum recomputes an OFS block's checksum.
func ofsFixChecksum(b []byte) {
	binary.BigEndian.PutUint32(b[20:], 0)
	var sum uint32
	for i := 0; i < 512; i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	binary.BigEndian.PutUint32(b[20:], -sum)
}

// ofsReadFile finds a file by name in the root directory of an OFS image
// and returns its contents, checking the checksum of every block on the way.
func ofsReadFile(t *testing.T, adf []byte, name string) ([]byte, bool) {
	t.Helper()
	blk := func(n uint32) []byte { return adf[n*512 : n*512+512] }
	be := func(b []byte, off int) uint32 { return binary.BigEndian.Uint32(b[off:]) }
	root := blk(880)
	for i := 0; i < 72; i++ {
		for h := be(root, 24+4*i); h != 0; h = be(blk(h), 496) { // hash chain
			hdr := blk(h)
			if n := int(hdr[432]); !strings.EqualFold(string(hdr[433:433+n]), name) {
				continue
			}
			if !ofsChecksumOK(hdr) {
				t.Errorf("file header block %d has a bad checksum", h)
			}
			size := be(hdr, 324)
			var out []byte
			for d := be(hdr, 16); d != 0; d = be(blk(d), 16) {
				db := blk(d)
				if !ofsChecksumOK(db) {
					t.Errorf("data block %d has a bad checksum", d)
				}
				out = append(out, db[24:24+be(db, 12)]...)
			}
			if uint32(len(out)) != size {
				t.Errorf("file %q: %d bytes in data blocks, header says %d", name, len(out), size)
			}
			return out, true
		}
	}
	return nil, false
}

// AmigaDOS writes a file during boot and it lands, well-formed, in the
// disk image: the startup-sequence's first echo is patched to
// `echo >df0:written "goAmiga write test"`.
func TestAmigaDOSWritesFile(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	adf, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	// s/startup-sequence's first data block is block 1184 on this disk.
	const block = 1184
	b := adf[block*512 : block*512+512]
	old := []byte(`echo "Amiga Workbench Disk. Release 1.3.3 version 34.34"`)
	i := bytes.Index(b, old)
	if i < 0 {
		t.Skip("startup-sequence not where expected on this ADF")
	}
	cmd := []byte(`echo >df0:written "goAmiga write test"`)
	copy(b[i:], append(cmd, bytes.Repeat([]byte(" "), len(old)-len(cmd))...))
	ofsFixChecksum(b)

	a, _ := New(rom, nil)
	a.Disk.InsertDisk(adf)
	a.Reset()
	booted := false
	for i := 0; i < 25_000_000 && !booted; i++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			booted = readCStrMem(a, uint32(np), 24) == "workbench.task"
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	if !booted {
		t.Fatal("Workbench did not start")
	}
	// trackdisk flushes its dirty track buffer a couple of seconds after
	// the last write; give it five.
	for i := 0; i < 250; i++ {
		a.RunFrame()
	}
	t.Logf("sectors written %d, bad %d, track mismatches %d",
		a.Disk.SectorsWritten, a.Disk.BadSectorWrites, a.Disk.TrackMismatches)
	if a.Disk.BadSectorWrites != 0 || a.Disk.TrackMismatches != 0 {
		t.Error("write DMA produced bad or misplaced sectors")
	}
	got, ok := ofsReadFile(t, a.Disk.adf, "written")
	if !ok {
		t.Fatal("df0:written not found in the disk image")
	}
	if string(got) != "goAmiga write test\n" {
		t.Errorf("df0:written = %q, want %q", got, "goAmiga write test\n")
	}
}
