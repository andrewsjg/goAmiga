package amiga

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Filesystems stored in a Rigid Disk Block.
//
// An RDB can carry the code of the filesystems its partitions use: a
// FileSysHeaderBlock (FSHD) per filesystem names its DosType and version
// and points to a chain of LoadSegBlocks (LSEG) holding an AmigaDOS
// executable. This is how a disk partitioned with PFS3 or SFS, or with FFS
// for Kickstart 1.3 (whose ROM has only OFS), mounts at boot.
//
// goamiga.device does what a real controller's boot ROM does. For each
// partition with a filesystem in its RDB, it looks in FileSystem.resource
// for that DosType: an entry at least as new is used as it is (Kickstart's
// own FFS is newer than the copy most RDBs carry). Otherwise the
// executable is loaded (relocated by Go into memory AllocMem'd by the
// board's 68000 code) and added to FileSystem.resource for the partitions
// and Mount commands that follow. The partition's DeviceNode is patched
// from the entry: always its SegList, and the fields PatchFlags names.

// FileSystem is a filesystem carried by an RDB.
type FileSystem struct {
	DosType    uint32
	Version    uint32 // version<<16 | revision
	PatchFlags uint32
	// DeviceNode values from dn_Type to dn_GlobalVec, for PatchFlags
	// bits 0-8.
	Fields [9]uint32
	Code   []byte // the executable, from the LSEG blocks
}

// fsSegList and fsGlobalVec index FileSystem.Fields (and their PatchFlags
// bits).
const (
	fsStackSize = 4
	fsSegList   = 7
	fsGlobalVec = 8
)

// readFileSystems reads the FSHD chain starting at block next. A broken
// chain stops the reading but isn't an error: the partitions still mount
// if Kickstart knows their type.
func (h *Hardfile) readFileSystems(next uint32) error {
	be := binary.BigEndian
	blk := make([]byte, hdBlockSize)
	for n := 0; next != 0xFFFFFFFF && n < 32; n++ {
		if err := h.ReadAt(blk, int64(next)*hdBlockSize); err != nil {
			return fmt.Errorf("filesystem header at block %d: %v", next, err)
		}
		if string(blk[0:4]) != "FSHD" {
			return fmt.Errorf("block %d: expected FSHD, found %q", next, blk[0:4])
		}
		fs := FileSystem{DosType: be.Uint32(blk[32:]), Version: be.Uint32(blk[36:]), PatchFlags: be.Uint32(blk[40:])}
		for i := range fs.Fields {
			fs.Fields[i] = be.Uint32(blk[44+4*i:])
		}
		next = be.Uint32(blk[16:])
		code, err := h.readLoadSegs(fs.Fields[fsSegList])
		if err != nil {
			return fmt.Errorf("filesystem %s: %v", DosTypeString(fs.DosType), err)
		}
		fs.Code = code
		h.FileSystems = append(h.FileSystems, fs)
	}
	return nil
}

// readLoadSegs joins the data of the LSEG chain starting at block next.
func (h *Hardfile) readLoadSegs(next uint32) ([]byte, error) {
	be := binary.BigEndian
	blk := make([]byte, hdBlockSize)
	var code []byte
	for n := 0; next != 0xFFFFFFFF; n++ {
		if n > 16384 {
			return nil, errors.New("LSEG chain too long")
		}
		if err := h.ReadAt(blk, int64(next)*hdBlockSize); err != nil {
			return nil, fmt.Errorf("LSEG block %d: %v", next, err)
		}
		if string(blk[0:4]) != "LSEG" {
			return nil, fmt.Errorf("block %d: expected LSEG, found %q", next, blk[0:4])
		}
		longs := int(be.Uint32(blk[4:]))
		if longs < 5 || longs > hdBlockSize/4 {
			return nil, fmt.Errorf("LSEG block %d: bad size %d", next, longs)
		}
		code = append(code, blk[20:longs*4]...)
		next = be.Uint32(blk[16:])
	}
	if len(code) == 0 {
		return nil, errors.New("no LSEG blocks")
	}
	return code, nil
}

// FileSystem returns the RDB's filesystem for a DosType, or nil.
func (h *Hardfile) FileSystem(dosType uint32) *FileSystem {
	for i := range h.FileSystems {
		if h.FileSystems[i].DosType == dosType {
			return &h.FileSystems[i]
		}
	}
	return nil
}

// DosTypeString formats a DosType the usual way: DOS\1, PFS\3, SFS\0.
func DosTypeString(t uint32) string {
	b := []byte{byte(t >> 24), byte(t >> 16), byte(t >> 8)}
	for i, c := range b {
		if c < 0x20 || c > 0x7E {
			b[i] = '?'
		}
	}
	return fmt.Sprintf("%s\\%d", b, byte(t))
}

// VersionString formats a filesystem version as version.revision.
func VersionString(v uint32) string { return fmt.Sprintf("%d.%d", v>>16, v&0xFFFF) }

// ---- AmigaDOS executables (hunk format) ----

const (
	hunkCode         = 0x3E9
	hunkData         = 0x3EA
	hunkBSS          = 0x3EB
	hunkReloc32      = 0x3EC
	hunkSymbol       = 0x3F0
	hunkDebug        = 0x3F1
	hunkEnd          = 0x3F2
	hunkHeader       = 0x3F3
	hunkDRel32       = 0x3F7 // RELOC32SHORT in executables from old linkers
	hunkReloc32Short = 0x3FC
	hunkName         = 0x3E8

	memfChip  = 2
	memfClear = 0x10000
)

type hunk struct {
	size   int       // bytes, from the header
	chip   bool      // must be in chip RAM
	data   []byte    // code or data; shorter than size for BSS
	relocs []hunkRel // 32-bit addresses to relocate
}

type hunkRel struct {
	off    int // offset in this hunk
	target int // hunk the address points into
}

// parseHunks reads an executable's segments and relocations.
func parseHunks(b []byte) ([]hunk, error) {
	be := binary.BigEndian
	pos := 0
	long := func() (uint32, error) {
		if pos+4 > len(b) {
			return 0, errors.New("executable is truncated")
		}
		v := be.Uint32(b[pos:])
		pos += 4
		return v, nil
	}
	if v, err := long(); err != nil || v != hunkHeader {
		return nil, errors.New("not an AmigaDOS executable")
	}
	if v, err := long(); err != nil || v != 0 {
		return nil, errors.New("executable needs resident libraries")
	}
	tableSize, _ := long()
	first, _ := long()
	last, err := long()
	if err != nil || first != 0 || last < first || last >= tableSize || tableSize > 4096 {
		return nil, errors.New("bad hunk table")
	}
	hunks := make([]hunk, last-first+1)
	for i := range hunks {
		v, err := long()
		if err != nil {
			return nil, err
		}
		hunks[i].size = int(v&0x3FFFFFFF) * 4
		hunks[i].chip = v>>30 == 1
		if v>>30 == 3 {
			attrs, err := long()
			if err != nil {
				return nil, err
			}
			hunks[i].chip = attrs&memfChip != 0
		}
	}
	cur := 0
	for cur < len(hunks) {
		v, err := long()
		if err != nil {
			return nil, err
		}
		switch v & 0x3FFFFFFF {
		case hunkName:
			n, _ := long()
			pos += int(n) * 4
		case hunkCode, hunkData:
			n, err := long()
			if err != nil {
				return nil, err
			}
			size := int(n&0x3FFFFFFF) * 4
			if pos+size > len(b) || size > hunks[cur].size {
				return nil, fmt.Errorf("hunk %d: bad size", cur)
			}
			hunks[cur].data = b[pos : pos+size]
			pos += size
		case hunkBSS:
			long()
		case hunkReloc32:
			for {
				n, err := long()
				if err != nil {
					return nil, err
				}
				if n == 0 {
					break
				}
				target, _ := long()
				for range n {
					off, err := long()
					if err != nil {
						return nil, err
					}
					hunks[cur].relocs = append(hunks[cur].relocs, hunkRel{int(off), int(target)})
				}
			}
		case hunkReloc32Short, hunkDRel32:
			word := func() (int, error) {
				if pos+2 > len(b) {
					return 0, errors.New("executable is truncated")
				}
				v := be.Uint16(b[pos:])
				pos += 2
				return int(v), nil
			}
			for {
				n, err := word()
				if err != nil {
					return nil, err
				}
				if n == 0 {
					break
				}
				target, _ := word()
				for range n {
					off, err := word()
					if err != nil {
						return nil, err
					}
					hunks[cur].relocs = append(hunks[cur].relocs, hunkRel{off, target})
				}
			}
			pos = (pos + 3) &^ 3
		case hunkSymbol:
			for {
				n, err := long()
				if err != nil {
					return nil, err
				}
				if n == 0 {
					break
				}
				pos += int(n&0xFFFFFF)*4 + 4
			}
		case hunkDebug:
			n, _ := long()
			pos += int(n) * 4
		case hunkEnd:
			cur++
		default:
			return nil, fmt.Errorf("hunk %d: unsupported block type $%X", cur, v)
		}
	}
	for i, h := range hunks {
		for _, r := range h.relocs {
			if r.target >= len(hunks) || r.off < 0 || r.off+4 > len(h.data) {
				return nil, fmt.Errorf("hunk %d: bad relocation", i)
			}
		}
	}
	return hunks, nil
}

// segListSize is the memory a seglist of hunks takes: each segment is a
// size longword, the BPTR to the next segment, then the hunk.
func segListSize(hunks []hunk) (size uint32, chip bool) {
	for _, h := range hunks {
		size += 8 + uint32(h.size)
		chip = chip || h.chip
	}
	return size, chip
}

// buildSegList lays out hunks as a seglist at addr (longword aligned) and
// returns the memory image and the seglist's BPTR.
func buildSegList(hunks []hunk, addr uint32) ([]byte, uint32) {
	be := binary.BigEndian
	size, _ := segListSize(hunks)
	img := make([]byte, size)
	starts := make([]uint32, len(hunks)) // offset of each segment in img
	off := uint32(0)
	for i, h := range hunks {
		starts[i] = off
		off += 8 + uint32(h.size)
	}
	for i, h := range hunks {
		s := starts[i]
		be.PutUint32(img[s:], 8+uint32(h.size))
		if i+1 < len(hunks) {
			be.PutUint32(img[s+4:], (addr+starts[i+1]+4)>>2)
		}
		data := img[s+8 : s+8+uint32(h.size)]
		copy(data, h.data)
		for _, r := range h.relocs {
			v := be.Uint32(data[r.off:])
			be.PutUint32(data[r.off:], v+addr+starts[r.target]+8)
		}
	}
	return img, (addr + 4) >> 2
}

// ---- Mounting ----

// FileSysEntry (FileSystem.resource) offsets.
const (
	fseDosType  = 14
	fseVersion  = 18
	fsePatch    = 22
	fseFields   = 26 // fse_Type ... fse_GlobalVec, as FileSystem.Fields
	fseSize     = 62
	fseReserved = 64 // memory set aside for one in a loaded filesystem's block

	execResourceList = 336
)

// fsMount is a filesystem ready for DeviceNodes: what they are patched
// with.
type fsMount struct {
	patch  uint32
	fields [9]uint32
}

// fsPending is a filesystem waiting for its memory.
type fsPending struct {
	fs    *FileSystem
	hunks []hunk
}

// fsPrepare is called for a new DeviceNode (D7) before it goes on the
// MountList. It patches the node if its filesystem is known already, and
// otherwise returns the memory needed to load it (D0, with the AllocMem
// requirements in D1), or 0 if there is nothing to load.
func (h *hdBoard) fsPrepare() {
	a := h.amiga
	cpu := a.CPU
	cpu.D[0] = 0
	h.pending = nil
	p := h.parts[h.nextPart]
	dt := p.p.Env[deDosType]
	fs := h.Files[p.unit].FileSystem(dt)
	if fs == nil {
		return
	}
	node := cpu.D[7]
	if m, ok := h.mounts[dt]; ok {
		h.patchNode(node, m)
		return
	}
	if e := a.fileSysEntry(dt); e != 0 && a.peek32(e+fseVersion) >= fs.Version {
		m := fsMount{patch: a.peek32(e + fsePatch)}
		for i := range m.fields {
			m.fields[i] = a.peek32(e + fseFields + uint32(4*i))
		}
		h.mounts[dt] = m
		h.tracef("%s: %s %s from FileSystem.resource (the RDB has %s)", p.p.Name, DosTypeString(dt),
			VersionString(a.peek32(e+fseVersion)), VersionString(fs.Version))
		h.patchNode(node, m)
		return
	}
	hunks, err := parseHunks(fs.Code)
	if err != nil {
		h.tracef("%s: filesystem %s in the RDB: %v", p.p.Name, DosTypeString(dt), err)
		h.mounts[dt] = fsMount{} // don't try again for the next partition
		return
	}
	size, chip := segListSize(hunks)
	cpu.D[0] = fseReserved + size
	cpu.D[1] = memfClear | 1 // MEMF_PUBLIC
	if chip {
		cpu.D[1] = memfClear | memfChip
	}
	h.pending = &fsPending{fs, hunks}
}

// fsLoad loads the pending filesystem into the memory in D0 (0 if
// AllocMem failed), adds it to FileSystem.resource and patches the
// DeviceNode in D7.
func (h *hdBoard) fsLoad() {
	a := h.amiga
	cpu := a.CPU
	pend, mem := h.pending, cpu.D[0]
	h.pending = nil
	if pend == nil {
		return
	}
	fs := pend.fs
	if mem == 0 {
		h.tracef("no memory to load filesystem %s", DosTypeString(fs.DosType))
		return
	}
	img, seg := buildSegList(pend.hunks, mem+fseReserved)
	a.pokeBytes(mem+fseReserved, img)
	m := fsMount{patch: fs.PatchFlags | 1<<fsSegList, fields: fs.Fields}
	m.fields[fsSegList] = seg
	if fs.PatchFlags&(1<<fsGlobalVec) == 0 {
		// Every filesystem stored in an RDB is C or assembler, not BCPL.
		m.patch |= 1 << fsGlobalVec
		m.fields[fsGlobalVec] = 0xFFFFFFFF
	}
	h.mounts[fs.DosType] = m

	// The FileSysEntry, at the start of the block, named after the device.
	if res := a.findResource("FileSystem.resource"); res != 0 {
		e := mem
		a.poke32(e+10, h.devNameAddr)
		a.poke32(e+fseDosType, fs.DosType)
		a.poke32(e+fseVersion, fs.Version)
		a.poke32(e+fsePatch, m.patch)
		for i, v := range m.fields {
			a.poke32(e+fseFields+uint32(4*i), v)
		}
		a.addHead(res+18, e)
	}
	h.tracef("loaded filesystem %s %s from the RDB: %d hunks at $%06X", DosTypeString(fs.DosType),
		VersionString(fs.Version), len(pend.hunks), mem+fseReserved)
	h.patchNode(cpu.D[7], m)
}

// patchNode sets the DeviceNode fields m's PatchFlags name.
func (h *hdBoard) patchNode(node uint32, m fsMount) {
	for i, v := range m.fields {
		if m.patch&(1<<i) != 0 {
			h.amiga.poke32(node+4+uint32(4*i), v)
		}
	}
}

// findResource looks up a resource by name in ExecBase's ResourceList.
func (a *Amiga) findResource(name string) uint32 {
	return a.findNode(a.peek32(4)+execResourceList, name)
}

// findNode looks up a node by name in the exec List at list.
func (a *Amiga) findNode(list uint32, name string) uint32 {
	for n, i := a.peek32(list), 0; n != 0 && i < 1000; n, i = a.peek32(n), i+1 {
		if a.peek32(n) == 0 { // the list's tail
			break
		}
		if a.peekString(a.peek32(n+10), 64) == name {
			return n
		}
	}
	return 0
}

// fileSysEntry finds FileSystem.resource's entry for a DosType, or 0.
func (a *Amiga) fileSysEntry(dosType uint32) uint32 {
	res := a.findResource("FileSystem.resource")
	if res == 0 {
		return 0
	}
	for n, i := a.peek32(res+18), 0; n != 0 && i < 1000; n, i = a.peek32(n), i+1 {
		if a.peek32(n) == 0 {
			break
		}
		if a.peek32(n+fseDosType) == dosType {
			return n
		}
	}
	return 0
}

// addHead puts node at the head of the exec List at list.
func (a *Amiga) addHead(list, node uint32) {
	head := a.peek32(list)
	a.poke32(node, head)
	a.poke32(node+4, list)
	a.poke32(head+4, node)
	a.poke32(list, node)
}

func (a *Amiga) peekString(addr uint32, max int) string {
	var b []byte
	for i := 0; i < max && addr != 0; i++ {
		c := a.peek8(addr + uint32(i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}
