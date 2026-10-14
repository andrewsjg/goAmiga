package amiga

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// a2091Rig is a machine with an A2091 configured at $E90000 and a disk
// at SCSI ID 0, driven as AMIX's a2091.c drives it.
type a2091Rig struct {
	t    *testing.T
	a    *Amiga
	base uint32
	img  []byte
}

func newA2091Rig(t *testing.T) *a2091Rig {
	t.Helper()
	img := make([]byte, 1<<20)
	for i := range img {
		img[i] = byte(i / 512)
	}
	path := filepath.Join(t.TempDir(), "scsi0.hdf")
	os.WriteFile(path, img, 0o644)
	a := createTestAmiga(t)
	if err := a.AttachSCSI(0, path, false); err != nil {
		t.Fatal(err)
	}
	a.wireMemoryMap(false)
	// The chain: goamiga.device's board (which serves the disk to
	// AmigaOS) at $E90000, then the A2091 at $EA0000.
	for _, base := range []byte{0x90, 0xA0} {
		a.pokeBytes(0xE8004A, []byte{base}) // A19-A16 in the high nibble
		a.pokeBytes(0xE80048, []byte{0xE0})
	}
	if a.HardDisk.Configured() != 0xE90000 || a.SCSI.Configured() != 0xEA0000 {
		t.Fatalf("boards at $%06X and $%06X", a.HardDisk.Configured(), a.SCSI.Configured())
	}
	r := &a2091Rig{t: t, a: a, base: 0xEA0000, img: img}
	// initialize(): own ID 7 with advanced features, reset, wait.
	r.setreg(wdOwnID, 0x0F)
	r.setreg(wdCommand, 0x00)
	if r.reg(wdAux)&0x80 == 0 {
		t.Fatal("no interrupt after reset")
	}
	if st := r.reg(wdStatus); st != 0x01 {
		t.Fatalf("reset status $%02X, want $01 (advanced features)", st)
	}
	r.setreg(wdControl, 0x8C)
	r.setreg(wdSourceID, 0x80)
	r.setreg(0x02, 0xFF)
	return r
}

func (r *a2091Rig) setreg(n, v byte) {
	r.a.poke8(r.base+0x91, n)
	r.a.poke8(r.base+0x93, v)
}

func (r *a2091Rig) reg(n byte) byte {
	r.a.poke8(r.base+0x91, n)
	return r.a.peek8(r.base + 0x93)
}

// sat runs a select-and-transfer of cdb at target id, with DMA to or
// from addr, and returns the WD33C93 status, the target's status byte and
// the bytes left of count.
func (r *a2091Rig) sat(id byte, cdb []byte, addr uint32, count int, read bool) (wdStatus_, status byte, left int) {
	for i, b := range cdb {
		r.setreg(wdCDB1+byte(i), b)
	}
	dest := id
	cntr := uint16(0x38)
	if read {
		dest |= 0x40
		cntr = 0x30
	}
	r.setreg(wdDestID, dest)
	r.setreg(wdTargetLUN, 0)
	r.setreg(wdCount, byte(count>>16))
	r.setreg(wdCount+1, byte(count>>8))
	r.setreg(wdCount+2, byte(count))
	r.a.poke16(r.base+0x42, cntr)
	r.a.poke32(r.base+0x84, addr)
	r.a.poke16(r.base+0xE0, 1)
	r.a.Custom.WriteReg16(0x09C, 0) // clear INTREQ
	r.setreg(wdCommand, 0x08)
	if r.a.peek16(r.base+0x40)&0x10 == 0 {
		r.t.Fatal("ISTR: no interrupt pending after the command")
	}
	if r.a.Custom.ReadReg16(0x09C)&0x08 == 0 {
		r.t.Fatal("no INT2 request")
	}
	wdStatus_ = r.reg(wdStatus)
	status = r.reg(wdTargetLUN)
	left = int(r.reg(wdCount))<<16 | int(r.reg(wdCount+1))<<8 | int(r.reg(wdCount+2))
	// stopdma()
	r.a.poke16(r.base+0xE8, 1)
	if r.a.peek16(r.base+0x40)&1 == 0 {
		r.t.Fatal("FIFO not empty after flush")
	}
	r.a.poke16(r.base+0xE4, 1)
	r.a.poke16(r.base+0xE2, 1)
	return
}

func TestA2091(t *testing.T) {
	r := newA2091Rig(t)
	a := r.a

	st, ts, left := r.sat(0, []byte{0x12, 0, 0, 0, 36, 0}, 0x10000, 36, true)
	if st != 0x16 || ts != 0 || left != 0 {
		t.Fatalf("INQUIRY: WD status $%02X, target status $%02X, %d left", st, ts, left)
	}
	var inq [36]byte
	a.peekBytes(0x10000, inq[:])
	if inq[0] != 0 || !bytes.Contains(inq[:], []byte("GOAMIGA")) {
		t.Errorf("INQUIRY data % X", inq)
	}
	if ph := r.reg(wdPhase); ph != 0x60 {
		t.Errorf("command phase $%02X, want $60", ph)
	}

	r.sat(0, []byte{0x25, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 0x10000, 8, true)
	if last := a.peek32(0x10000); last != 2047 || a.peek32(0x10004) != 512 {
		t.Errorf("READ CAPACITY: last block %d, size %d", last, a.peek32(0x10004))
	}

	// READ(10) blocks 5-6.
	r.sat(0, []byte{0x28, 0, 0, 0, 0, 5, 0, 0, 2, 0}, 0x20000, 1024, true)
	got := make([]byte, 1024)
	a.peekBytes(0x20000, got)
	if !bytes.Equal(got, r.img[5*512:7*512]) {
		t.Error("READ(10) returned the wrong data")
	}

	// WRITE(10) block 9 from memory.
	a.pokeBytes(0x30000, bytes.Repeat([]byte{0xA5}, 512))
	if st, ts, _ := r.sat(0, []byte{0x2A, 0, 0, 0, 0, 9, 0, 0, 1, 0}, 0x30000, 512, false); st != 0x16 || ts != 0 {
		t.Fatalf("WRITE(10): $%02X/$%02X", st, ts)
	}
	f, _ := os.ReadFile(a.SCSI.files[0].Path)
	if !bytes.Equal(f[9*512:10*512], bytes.Repeat([]byte{0xA5}, 512)) {
		t.Error("WRITE(10) didn't reach the image")
	}

	// Past the end: CHECK CONDITION, then REQUEST SENSE says why.
	if _, ts, _ := r.sat(0, []byte{0x28, 0, 0, 0, 0x10, 0, 0, 0, 1, 0}, 0x20000, 512, true); ts != scsiCheckCondition {
		t.Errorf("READ past the end: target status $%02X, want CHECK CONDITION", ts)
	}
	r.sat(0, []byte{0x03, 0, 0, 0, 18, 0}, 0x10000, 18, true)
	if key := a.peek8(0x10002) & 0xF; key != senseIllegalRequest {
		t.Errorf("sense key %d, want ILLEGAL REQUEST", key)
	}

	// Nobody at ID 3.
	if st, _, _ := r.sat(3, []byte{0, 0, 0, 0, 0, 0}, 0x10000, 0, true); st != 0x42 {
		t.Errorf("select of an empty ID: status $%02X, want $42 (timeout)", st)
	}
}

// A tape kept as a directory: reads stop at filemarks with CHECK
// CONDITION, FILEMARK and the residue; SPACE and REWIND move it.
func TestSCSITape(t *testing.T) {
	r := newA2091Rig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.tape"), []byte("00\r\n01\r\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "00"), bytes.Repeat([]byte{0xAA}, 1024), 0o644)
	os.WriteFile(filepath.Join(dir, "01"), bytes.Repeat([]byte{0xBB}, 3*512), 0o644)
	if err := r.a.AttachTape(4, dir); err != nil {
		t.Fatal(err)
	}
	a := r.a
	r.sat(4, []byte{0x12, 0, 0, 0, 36, 0}, 0x10000, 36, true)
	if a.peek8(0x10000) != 1 {
		t.Errorf("INQUIRY peripheral type %d, want 1 (sequential)", a.peek8(0x10000))
	}
	// READ 4 fixed blocks from file 00 (2 blocks long): 2 come back, then
	// the filemark.
	st, ts, _ := r.sat(4, []byte{0x08, 1, 0, 0, 4, 0}, 0x20000, 2048, true)
	if st != 0x16 || ts != scsiCheckCondition {
		t.Fatalf("read over the filemark: $%02X/$%02X, want CHECK CONDITION", st, ts)
	}
	if a.peek8(0x20000) != 0xAA || a.peek8(0x20000+1023) != 0xAA {
		t.Error("the file's two blocks weren't transferred")
	}
	r.sat(4, []byte{0x03, 0, 0, 0, 18, 0}, 0x10000, 18, true)
	if s := a.peek8(0x10002); s&0x80 == 0 || a.peek32(0x10003) != 2 {
		t.Errorf("sense byte 2 $%02X, information %d: want FILEMARK and residue 2", s, a.peek32(0x10003))
	}
	// Now at file 01.
	r.sat(4, []byte{0x08, 1, 0, 0, 1, 0}, 0x20000, 512, true)
	if a.peek8(0x20000) != 0xBB {
		t.Error("after the filemark, the next read should be file 01")
	}
	// REWIND, SPACE one filemark, read file 01 again.
	r.sat(4, []byte{0x01, 0, 0, 0, 0, 0}, 0, 0, true)
	r.sat(4, []byte{0x11, 1, 0, 0, 1, 0}, 0, 0, true)
	r.sat(4, []byte{0x08, 1, 0, 0, 1, 0}, 0x20000, 512, true)
	if a.peek8(0x20000) != 0xBB {
		t.Error("SPACE 1 filemark should reach file 01")
	}
	// Past the end: BLANK CHECK.
	r.sat(4, []byte{0x11, 1, 0, 0, 1, 0}, 0, 0, true)
	if _, ts, _ := r.sat(4, []byte{0x08, 1, 0, 0, 1, 0}, 0x20000, 512, true); ts != scsiCheckCondition {
		t.Error("reading past the last file should fail")
	}
	r.sat(4, []byte{0x03, 0, 0, 0, 18, 0}, 0x10000, 18, true)
	if k := a.peek8(0x10002) & 0xF; k != senseBlankCheck {
		t.Errorf("sense key %d past the end, want BLANK CHECK", k)
	}
	// Writing is refused.
	if _, ts, _ := r.sat(4, []byte{0x10, 0, 0, 0, 1, 0}, 0, 0, false); ts != scsiCheckCondition {
		t.Error("WRITE FILEMARKS should be refused on a read-only tape")
	}
}
