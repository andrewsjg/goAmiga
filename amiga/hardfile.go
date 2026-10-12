package amiga

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
)

// A Hardfile is a hard disk image (.hdf): either a single partition with
// no partition table (WinUAE's plain "hardfile": 32 sectors per track, one
// head, two reserved blocks), or a whole disk with a Rigid Disk Block
// describing its geometry and partitions.
type Hardfile struct {
	Path     string
	ReadOnly bool
	f        *os.File
	blocks   int64 // 512-byte blocks

	// Geometry reported by TD_GETGEOMETRY.
	Cylinders, Heads, Sectors int

	Partitions []Partition
	rdb        bool
}

// HasRDB reports whether the image has a Rigid Disk Block (else it is a
// plain hardfile, one partition with no partition table).
func (h *Hardfile) HasRDB() bool { return h.rdb }

// Partition is one DOS volume on a hardfile.
type Partition struct {
	Name     string   // DOS device name without the colon, e.g. "DH0"
	Env      []uint32 // DosEnvec, from de_TableSize on
	Bootable bool
}

// DosEnvec indices.
const (
	deTableSize = iota
	deSizeBlock
	deSecOrg
	deSurfaces
	deSectorPerBlock
	deBlocksPerTrack
	deReserved
	dePreAlloc
	deInterleave
	deLowCyl
	deHighCyl
	deNumBuffers
	deBufMemType
	deMaxTransfer
	deMask
	deBootPri
	deDosType
)

const hdBlockSize = 512

// OpenHardfile opens an image. unit numbers the plain-hardfile device name
// (DH<unit>); dosType is the filesystem to mount an unformatted plain
// hardfile with. RDB partitions carry their own names and types.
func OpenHardfile(path string, readOnly bool, unit int, dosType uint32) (*Hardfile, error) {
	flag := os.O_RDWR
	if readOnly {
		flag = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	h := &Hardfile{Path: path, ReadOnly: readOnly, f: f, blocks: st.Size() / hdBlockSize}
	if h.blocks < 64 {
		f.Close()
		return nil, fmt.Errorf("%s: too small for a hard disk (%d bytes)", path, st.Size())
	}
	ok, err := h.readRDB()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	h.rdb = ok
	if !ok {
		h.plain(unit, dosType)
	}
	return h, nil
}

// plain sets up a hardfile without an RDB as one partition covering it.
func (h *Hardfile) plain(unit int, dosType uint32) {
	h.Heads, h.Sectors = 1, 32
	h.Cylinders = int(h.blocks / int64(h.Heads*h.Sectors))
	var b [4]byte
	h.f.ReadAt(b[:], 0)
	if b[0] == 'D' && b[1] == 'O' && b[2] == 'S' {
		dosType = binary.BigEndian.Uint32(b[:])
	}
	env := make([]uint32, deDosType+1)
	env[deTableSize] = deDosType
	env[deSizeBlock] = hdBlockSize / 4
	env[deSurfaces] = uint32(h.Heads)
	env[deSectorPerBlock] = 1
	env[deBlocksPerTrack] = uint32(h.Sectors)
	env[deReserved] = 2
	env[deLowCyl] = 0
	env[deHighCyl] = uint32(h.Cylinders - 1)
	env[deNumBuffers] = 30
	env[deBufMemType] = 1 // MEMF_PUBLIC: no DMA restrictions
	env[deMaxTransfer] = 0x7FFFFFFF
	env[deMask] = 0xFFFFFFFE
	env[deBootPri] = uint32(int32(-unit))
	env[deDosType] = dosType
	h.Partitions = []Partition{{Name: fmt.Sprintf("DH%d", unit), Env: env, Bootable: true}}
}

// readRDB looks for a Rigid Disk Block in the first 16 blocks and reads
// the partitions it lists. Filesystems stored in the RDB are not loaded:
// partitions must use a type the Kickstart ROM filesystem handles.
func (h *Hardfile) readRDB() (bool, error) {
	blk := make([]byte, hdBlockSize)
	rdb := -1
	for i := 0; i < 16; i++ {
		if _, err := h.f.ReadAt(blk, int64(i)*hdBlockSize); err != nil {
			return false, err
		}
		if string(blk[0:4]) == "RDSK" {
			rdb = i
			break
		}
	}
	if rdb < 0 {
		return false, nil
	}
	be := binary.BigEndian
	h.Cylinders = int(be.Uint32(blk[64:]))
	h.Sectors = int(be.Uint32(blk[68:]))
	h.Heads = int(be.Uint32(blk[72:]))
	next := be.Uint32(blk[28:]) // rdb_PartitionList
	for n := 0; next != 0xFFFFFFFF && n < 64; n++ {
		if _, err := h.f.ReadAt(blk, int64(next)*hdBlockSize); err != nil {
			return false, err
		}
		if string(blk[0:4]) != "PART" {
			return false, fmt.Errorf("block %d: expected PART, found %q", next, blk[0:4])
		}
		flags := be.Uint32(blk[20:])
		next = be.Uint32(blk[16:])
		if flags&2 != 0 { // PBFF_NOMOUNT
			continue
		}
		nameLen := min(int(blk[36]), 30)
		size := min(int(be.Uint32(blk[128:])), 19) + 1
		env := make([]uint32, max(size, deDosType+1))
		for i := 0; i < size; i++ {
			env[i] = be.Uint32(blk[128+4*i:])
		}
		if size <= deDosType {
			env[deTableSize] = deDosType
			env[deDosType] = 0x444F5300 // DOS\0
		}
		h.Partitions = append(h.Partitions, Partition{
			Name:     strings.TrimSuffix(string(blk[37:37+nameLen]), ":"),
			Env:      env,
			Bootable: flags&1 != 0, // PBFF_BOOTABLE
		})
	}
	if len(h.Partitions) == 0 {
		return false, errors.New("RDB lists no mountable partitions")
	}
	return true, nil
}

// Size is the image size in bytes.
func (h *Hardfile) Size() int64 { return h.blocks * hdBlockSize }

func (h *Hardfile) ReadAt(p []byte, off int64) error {
	if off < 0 || off+int64(len(p)) > h.Size() {
		return errHDRange
	}
	_, err := h.f.ReadAt(p, off)
	return err
}

func (h *Hardfile) WriteAt(p []byte, off int64) error {
	if h.ReadOnly {
		return errHDProtected
	}
	if off < 0 || off+int64(len(p)) > h.Size() {
		return errHDRange
	}
	_, err := h.f.WriteAt(p, off)
	return err
}

// Close flushes and closes the image.
func (h *Hardfile) Close() error {
	if h.f == nil {
		return nil
	}
	err := h.f.Close()
	h.f = nil
	return err
}

var (
	errHDRange     = errors.New("access beyond end of hardfile")
	errHDProtected = errors.New("hardfile is read-only")
)
