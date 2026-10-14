package amiga

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A SCSI tape drive, read-only, playing a tape kept as a directory of
// files: the tape's files in order, with a filemark after each. The
// order comes from index.tape (one name per line) if the directory has
// one, else the names sorted. Amiga Unix's distribution tape is kept this
// way, and AMIX's ct driver and amixpkg read it as from a Commodore A3070
// (an Archive Viper QIC drive, at SCSI ID 4).
//
// The drive works in fixed 512-byte blocks, as QIC drives do. A READ that
// meets a filemark returns the blocks before it with CHECK CONDITION, the
// sense data's FILEMARK bit and the residue, leaving the tape after the
// filemark; past the last file it reports BLANK CHECK at end of medium.
//
// The sense data carries the Archive drive's QIC-02 status bytes at 8 and
// 9, which AMIX's ctopen reads: write protected (byte 8 bit 4), end of
// medium (bit 3), filemark (bit 0); beginning of medium (byte 9 bit 3)
// and power-on reset (bit 0), with bit 7 of each set when any other is.
// ctopen refuses the drive unless the tape is at its beginning or the
// driver knows where it is.
type scsiTape struct {
	dir   string
	files []string // paths, in tape order
	sizes []int64
	file  int   // current file
	pos   int64 // byte offset in it
	sense [18]byte
	reset bool // power-on reset not yet reported
	onIO  func(write bool)
	open  *os.File
	openN int
}

const tapeBlock = 512

// newSCSITape opens the tape kept in dir.
func newSCSITape(dir string) (*scsiTape, error) {
	t := &scsiTape{dir: dir, openN: -1, reset: true}
	var names []string
	if f, err := os.Open(filepath.Join(dir, "index.tape")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if n := strings.TrimSpace(sc.Text()); n != "" {
				names = append(names, n)
			}
		}
		f.Close()
	} else {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names = append(names, e.Name())
			}
		}
	}
	for _, n := range names {
		p := filepath.Join(dir, n)
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("tape file %s: %v", n, err)
		}
		t.files = append(t.files, p)
		t.sizes = append(t.sizes, st.Size())
	}
	if len(t.files) == 0 {
		return nil, fmt.Errorf("%s: no tape files", dir)
	}
	return t, nil
}

// qicStatus is the Archive drive's two QIC-02 status bytes, given the
// sense flags of the last command.
func (t *scsiTape) qicStatus(flags byte) (byte, byte) {
	s0 := byte(0x10) // write protected
	if t.file >= len(t.files) || flags&senseEOM != 0 {
		s0 |= 0x08
	}
	if flags&senseFilemark != 0 {
		s0 |= 0x01
	}
	s1 := byte(0)
	if t.file == 0 && t.pos == 0 {
		s1 |= 0x08 // beginning of medium
	}
	if t.reset {
		s1 |= 0x01
	}
	if s0 != 0 {
		s0 |= 0x80
	}
	if s1 != 0 {
		s1 |= 0x80
	}
	return s0, s1
}

func (t *scsiTape) close() {
	if t.open != nil {
		t.open.Close()
		t.open = nil
	}
}

func (t *scsiTape) setSense(key byte, flags byte, info uint32, asc byte) {
	t.sense = [18]byte{}
	t.sense[0] = 0x70
	if info != 0 || flags != 0 {
		t.sense[0] |= 0x80 // VALID: the information field holds the residue
	}
	t.sense[2] = key | flags
	binary.BigEndian.PutUint32(t.sense[3:], info)
	t.sense[7] = 10
	t.sense[12] = asc
}

func (t *scsiTape) check(key, flags byte, info uint32, asc byte) ([]byte, byte) {
	t.setSense(key, flags, info, asc)
	return nil, scsiCheckCondition
}

func (t *scsiTape) dataOut(cdb []byte) int {
	switch cdb[0] {
	case 0x0A: // WRITE(6)
		n := int(cdb[2])<<16 | int(cdb[3])<<8 | int(cdb[4])
		if cdb[1]&1 != 0 {
			n *= tapeBlock
		}
		return n
	case 0x15:
		return int(cdb[4])
	}
	return 0
}

const (
	senseFilemark = 0x80
	senseEOM      = 0x40
	senseILI      = 0x20
)

func (t *scsiTape) command(lun int, cdb, out []byte) ([]byte, byte) {
	if lun != 0 && cdb[0] != 0x12 && cdb[0] != 0x03 {
		return t.check(senseIllegalRequest, 0, 0, 0x25)
	}
	switch cdb[0] {
	case 0x00, 0x15, 0x16, 0x17, 0x1B, 0x1E, 0x19:
		// TEST UNIT READY, MODE SELECT (density), RESERVE, RELEASE,
		// LOAD/UNLOAD, PREVENT ALLOW, ERASE (refused below for writes).
		if cdb[0] == 0x1B && cdb[4]&1 == 0 { // unload rewinds
			t.file, t.pos = 0, 0
		}
		if cdb[0] == 0x19 {
			return t.check(senseDataProtect, 0, 0, 0x27)
		}
	case 0x01: // REWIND
		t.file, t.pos = 0, 0
	case 0x03: // REQUEST SENSE
		s := t.sense
		if s[0] == 0 {
			s = [18]byte{0x70, 0, 0, 0, 0, 0, 0, 10}
		}
		s[8], s[9] = t.qicStatus(s[2])
		t.sense = [18]byte{0x70, 0, 0, 0, 0, 0, 0, 10}
		t.reset = false
		if lun != 0 {
			s[2], s[12] = senseIllegalRequest, 0x25
		}
		return clip(s[:], int(cdb[4])), scsiGood
	case 0x05: // READ BLOCK LIMITS: fixed 512
		return []byte{0, 0, 2, 0, 2, 0}, scsiGood
	case 0x12: // INQUIRY
		r := make([]byte, 36)
		r[0] = 1    // sequential access
		r[1] = 0x80 // removable
		if lun != 0 {
			r[0] = 0x7F
		}
		r[2], r[3], r[4] = 2, 2, 31
		copy(r[8:], fmt.Sprintf("%-8s%-16s%-4s", "ARCHIVE", "VIPER 150  21247", "-005"))
		return clip(r, int(cdb[4])), scsiGood
	case 0x1A: // MODE SENSE: write protected, QIC-150, 512-byte blocks
		r := []byte{11, 0, 0x80 | 0x10, 8, 0x10, 0, 0, 0, 0, 0, 2, 0}
		return clip(r, int(cdb[4])), scsiGood
	case 0x08: // READ(6)
		n := int64(cdb[2])<<16 | int64(cdb[3])<<8 | int64(cdb[4])
		if cdb[1]&1 == 0 {
			// Variable-length reads: one block of up to n bytes.
			return t.read(1, n)
		}
		return t.read(n, tapeBlock)
	case 0x0A, 0x10: // WRITE(6), WRITE FILEMARKS
		return t.check(senseDataProtect, 0, 0, 0x27)
	case 0x11: // SPACE
		count := int32(uint32(cdb[2])<<24|uint32(cdb[3])<<16|uint32(cdb[4])<<8) >> 8
		return t.space(cdb[1]&7, int(count))
	default:
		return t.check(senseIllegalRequest, 0, 0, 0x20)
	}
	return nil, scsiGood
}

// read reads n blocks of size bytes (or, for a variable read, one block
// of up to size bytes) from the current file.
func (t *scsiTape) read(n, size int64) ([]byte, byte) {
	if t.file >= len(t.files) {
		return t.check(senseBlankCheck, senseEOM, uint32(n), 0x00)
	}
	left := t.sizes[t.file] - t.pos
	want := n * size
	got := min(want, left)
	if size == tapeBlock {
		got -= got % tapeBlock
	}
	buf := make([]byte, got)
	if got > 0 {
		if t.openN != t.file {
			t.close()
			f, err := os.Open(t.files[t.file])
			if err != nil {
				return t.check(senseMediumError, 0, uint32(n), 0x11)
			}
			t.open, t.openN = f, t.file
		}
		if _, err := t.open.ReadAt(buf, t.pos); err != nil {
			return t.check(senseMediumError, 0, uint32(n), 0x11)
		}
		t.pos += got
		if t.onIO != nil {
			t.onIO(false)
		}
	}
	if got == want {
		return buf, scsiGood
	}
	// The filemark: the tape moves past it.
	t.file, t.pos = t.file+1, 0
	residue := uint32(n - got/size)
	t.setSense(senseNone, senseFilemark, residue, 0x00)
	return buf, scsiCheckCondition
}

// space moves over filemarks (code 1), blocks (0) or to the end of data
// (3).
func (t *scsiTape) space(code byte, count int) ([]byte, byte) {
	switch code {
	case 1: // filemarks
		if count >= 0 {
			for ; count > 0; count-- {
				if t.file >= len(t.files) {
					return t.check(senseBlankCheck, senseEOM, uint32(count), 0x00)
				}
				t.file, t.pos = t.file+1, 0
			}
			return nil, scsiGood
		}
		// Backwards: to just before the filemark ending an earlier file.
		for ; count < 0; count++ {
			if t.file == 0 {
				t.pos = 0
				return t.check(senseNone, senseEOM, uint32(-count), 0x00) // beginning of medium
			}
			t.file--
			t.pos = t.sizes[t.file]
		}
		return nil, scsiGood
	case 0: // blocks, within the current file
		if t.file >= len(t.files) {
			return t.check(senseBlankCheck, senseEOM, uint32(max(count, -count)), 0x00)
		}
		np := t.pos + int64(count)*tapeBlock
		switch {
		case np < 0:
			t.pos = 0
			return t.check(senseNone, senseFilemark, uint32(-np/tapeBlock), 0x00)
		case np > t.sizes[t.file]:
			over := (np - t.sizes[t.file]) / tapeBlock
			t.file, t.pos = t.file+1, 0
			return t.check(senseNone, senseFilemark, uint32(over), 0x00)
		}
		t.pos = np
		return nil, scsiGood
	case 3: // end of data
		t.file, t.pos = len(t.files), 0
		return nil, scsiGood
	}
	return t.check(senseIllegalRequest, 0, 0, 0x24)
}

// AttachTape connects the tape kept in dir as a read-only SCSI tape
// drive at target id on the A2091 (fitting the board if needed). Amiga
// Unix expects its tape drive at ID 4.
func (a *Amiga) AttachTape(id int, dir string) error {
	if id < 0 || id > 6 {
		return fmt.Errorf("SCSI ID %d: must be 0-6", id)
	}
	if a.SCSI == nil {
		a.SCSI = &a2091{amiga: a}
	}
	c := a.SCSI
	if c.targets[id] != nil {
		return fmt.Errorf("SCSI ID %d is taken", id)
	}
	t, err := newSCSITape(dir)
	if err != nil {
		return err
	}
	t.onIO = func(write bool) { c.lastIO, c.lastWrite = a.cycles|1, write }
	c.targets[id] = t
	c.tapes = append(c.tapes, t)
	a.wireMemoryMap(a.overlay)
	return nil
}
