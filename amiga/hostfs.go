package amiga

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Host folders as Amiga drives.
//
// A folder on the host is mounted as a DOS device with its own volume, as
// UAE's "filesystem" does. goamiga.device mounts it like a partition, but
// its DeviceNode's SegList points to a handler in the board's ROM: a loop
// that waits for DOS packets on its process's message port and hands each
// one to Go (a trap), which does the work on the host folder and fills in
// the result for the loop to reply.
//
// Locks (FileLock structures, which DOS reads) live in a pool the handler
// allocates when it starts; file handles need only an ID in fh_Arg1. All
// other state is Go's.
//
// Amiga-only file attributes, protection bits and comments, are kept in
// a NAME.uaem file next to the file, the format WinUAE and FS-UAE use:
//
//	----rwed 2026-10-12 16:48:00.00 a comment
//
// It exists only while they differ from the defaults (all access allowed,
// no comment). The host's own write permission shows as W and D cleared.

// HostDir is a host folder mounted as an Amiga drive.
type HostDir struct {
	Path     string // the folder
	Volume   string // the volume name
	Device   string // the DOS device name, e.g. "DH1"; set when the ROM is built
	ReadOnly bool
	Bootable bool // it has S/Startup-Sequence

	devNode uint32 // the DeviceNode, from MakeDosNode
	port    uint32 // the handler's message port, once started
	volNode uint32
	pool    uint32 // lock memory
	poolEnd uint32
	poolTop uint32   // first never-used byte
	free    []uint32 // freed lock slots
	locks   map[uint32]*hostLock
	root    *hostLock // what a zero lock means: the root
	files   map[uint32]*hostFile
	nextKey uint32
}

type hostLock struct {
	path      string // relative to the root, '/'-separated host names; "" for the root
	exclusive bool
	key       uint32
	list      []string // ExNext's snapshot of a directory (host names)
}

type hostFile struct {
	path      string
	f         *os.File
	write     bool
	exclusive bool
}

const (
	hostPoolSize = 16 << 10
	hostLockSize = 20
	hostVolSize  = 44 + 32 // DeviceList and its name

	hostDirUnit = 0x100 // unit numbers in the parameter packets (unused)
)

// AttachDirectory mounts a host folder as an Amiga drive, named volume (the
// folder's name if empty). It fits the hard disk controller if needed.
// Call it before Reset.
func (a *Amiga) AttachDirectory(dir, volume string, readOnly bool) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if volume == "" {
		volume = filepath.Base(dir)
	}
	volume = amigaName(volume)
	if volume == "" || len(volume) > 30 {
		return fmt.Errorf("%q can't be a volume name (1 to 30 characters, no : or /)", volume)
	}
	if a.HardDisk == nil {
		a.HardDisk = &hdBoard{amiga: a}
	}
	d := &HostDir{Path: dir, Volume: volume, ReadOnly: readOnly}
	if _, err := d.find("S/Startup-Sequence"); err == nil {
		d.Bootable = true
	}
	a.HardDisk.Dirs = append(a.HardDisk.Dirs, d)
	a.wireMemoryMap(a.overlay)
	return nil
}

// partition is the DosEnvec MakeDosNode gets for the folder.
func (d *HostDir) partition() *Partition {
	env := make([]uint32, deDosType+1)
	env[deTableSize] = deDosType
	env[deSizeBlock] = hdBlockSize / 4
	env[deSurfaces] = 1
	env[deSectorPerBlock] = 1
	env[deBlocksPerTrack] = 1
	env[deNumBuffers] = 1
	env[deBufMemType] = 1
	env[deMaxTransfer] = 0x7FFFFFFF
	env[deMask] = 0xFFFFFFFE
	env[deBootPri] = uint32(0xFFFFFFF6) // -10: after hardfiles
	env[deDosType] = 0x444F5300
	return &Partition{Name: d.Device, Env: env, Bootable: d.Bootable}
}

func (d *HostDir) reset() {
	for _, f := range d.files {
		f.f.Close()
	}
	d.devNode, d.port, d.volNode, d.pool, d.poolEnd, d.poolTop = 0, 0, 0, 0, 0, 0
	d.free, d.locks, d.files, d.nextKey = nil, map[uint32]*hostLock{}, map[uint32]*hostFile{}, 0
	d.root = &hostLock{}
}

// ---- The handler's traps ----

// dirStart is the handler process starting: D0 = the lock pool's size.
func (h *hdBoard) dirStart() {
	h.amiga.CPU.D[0] = hostPoolSize
}

// dirPool records the handler's port (A3) and pool (D0). Which folder it
// serves is learnt from the startup packet.
func (h *hdBoard) dirPool() {
	cpu := h.amiga.CPU
	h.newPorts[cpu.A[3]] = cpu.D[0]
}

// DOS packet offsets.
const (
	dpLink = 0
	dpPort = 4
	dpType = 8
	dpRes1 = 12
	dpRes2 = 16
	dpArg1 = 20

	dosTrue = 0xFFFFFFFF
)

// dirPacket handles the packet in the message at A1 for the handler whose
// port is A3, and sets A0/A1 for PutMsg to reply it.
func (h *hdBoard) dirPacket() {
	a := h.amiga
	cpu := a.CPU
	msg, port := cpu.A[1], cpu.A[3]
	dp := a.peek32(msg + 10)
	typ := a.peek32(dp + dpType)
	var args [7]uint32
	for i := range args {
		args[i] = a.peek32(dp + dpArg1 + uint32(4*i))
	}
	var res1, res2 uint32
	d := h.dirByPort(port)
	if d == nil {
		res1, res2 = h.dirStartup(port, args)
	} else {
		res1, res2 = d.packet(a, typ, args)
		if h.Trace != nil {
			name := ""
			switch typ {
			case actionLocateObject, actionDeleteObject, actionRenameObject, actionCreateDir:
				name = a.peekBString(args[1] << 2)
			case actionFindInput, actionFindOutput, actionFindUpdate, actionSetProtect, actionSetComment, actionSetDate:
				name = a.peekBString(args[2] << 2)
			}
			h.tracef("%s: packet %d %q (%X %X %X) = %X, %d", d.Device, int32(typ), name, args[0], args[1], args[2], res1, int32(res2))
		}
	}
	a.poke32(dp+dpRes1, res1)
	a.poke32(dp+dpRes2, res2)
	cpu.A[0] = a.peek32(dp + dpPort)
	a.poke32(dp+dpPort, port)
	a.poke32(msg+10, dp)
	cpu.A[1] = msg
}

func (h *hdBoard) dirByPort(port uint32) *HostDir {
	for _, d := range h.Dirs {
		if d.port == port && port != 0 {
			return d
		}
	}
	return nil
}

// dirStartup handles a handler's first packet: Arg2 is its dn_Startup,
// and Arg3 its DeviceNode (from Kickstart 2.0; 1.3 leaves it 0).
func (h *hdBoard) dirStartup(port uint32, args [7]uint32) (uint32, uint32) {
	a := h.amiga
	for _, d := range h.Dirs {
		node := d.devNode
		if node == 0 || (args[2]<<2 != node && (args[2] != 0 || args[1] != a.peek32(node+28))) {
			continue
		}
		d.reset()
		d.devNode = node
		d.port = port
		d.pool = h.newPorts[port]
		delete(h.newPorts, port)
		d.poolTop, d.poolEnd = d.pool, d.pool+hostPoolSize
		if d.pool == 0 {
			h.tracef("%s: no memory for the handler", d.Device)
			return 0, errNoFreeStore
		}
		a.poke32(node+8, port) // dn_Task
		d.volNode = d.pool
		d.poolTop += hostVolSize
		d.mountVolume(a)
		h.tracef("%s: handler started for %s (%s), port $%06X", d.Device, d.Volume, d.Path, port)
		return dosTrue, 0
	}
	h.tracef("startup packet for no folder (args % X)", args)
	return 0, errObjectNotFound
}

// mountVolume builds the volume's DeviceList node and links it into the
// DOS list.
func (d *HostDir) mountVolume(a *Amiga) {
	v := d.volNode
	for i := uint32(0); i < hostVolSize; i += 4 {
		a.poke32(v+i, 0)
	}
	a.poke32(v+4, 2) // DLT_VOLUME
	a.poke32(v+8, d.port)
	putDateStamp(a, v+16, time.Now())
	a.poke32(v+32, 0x444F5300) // DOS\0
	d.setVolumeName(a, d.Volume)
	dos := a.findNode(a.peek32(4)+378, "dos.library")
	if dos == 0 {
		return
	}
	info := a.peek32(a.peek32(dos+34)+24) << 2 // dl_Root -> rn_Info
	if info == 0 {
		return
	}
	a.poke32(v, a.peek32(info+4)) // dol_Next = di_DevInfo
	a.poke32(info+4, v>>2)
}

func (d *HostDir) setVolumeName(a *Amiga, name string) {
	name = name[:min(len(name), 30)]
	d.Volume = name
	a.poke8(d.volNode+44, byte(len(name)))
	a.pokeBytes(d.volNode+45, []byte(name))
	a.poke32(d.volNode+40, (d.volNode+44)>>2)
}

// ---- Packets ----

const (
	actionDie           = 5
	actionCurrentVolume = 7
	actionLocateObject  = 8
	actionRenameDisk    = 9
	actionFreeLock      = 15
	actionDeleteObject  = 16
	actionRenameObject  = 17
	actionMoreCache     = 18
	actionCopyDir       = 19
	actionSetProtect    = 21
	actionCreateDir     = 22
	actionExamineObject = 23
	actionExamineNext   = 24
	actionDiskInfo      = 25
	actionInfo          = 26
	actionFlush         = 27
	actionSetComment    = 28
	actionParent        = 29
	actionInhibit       = 31
	actionSetDate       = 34
	actionSameLock      = 40
	actionRead          = 'R'
	actionWrite         = 'W'
	actionFindUpdate    = 1004
	actionFindInput     = 1005
	actionFindOutput    = 1006
	actionEnd           = 1007
	actionSeek          = 1008
	actionSetFileSize   = 1022
	actionWriteProtect  = 1023
	actionFHFromLock    = 1026
	actionIsFilesystem  = 1027
	actionChangeMode    = 1028
	actionCopyDirFH     = 1030
	actionParentFH      = 1031
	actionExamineFH     = 1034
	actionSetOwner      = 1036

	errNoFreeStore        = 103
	errObjectInUse        = 202
	errObjectExists       = 203
	errDirNotFound        = 204
	errObjectNotFound     = 205
	errActionNotKnown     = 209
	errInvalidName        = 210
	errInvalidLock        = 211
	errObjectWrongType    = 212
	errDiskWriteProtected = 214
	errRenameAcross       = 215
	errDirNotEmpty        = 216
	errSeek               = 219
	errDiskFull           = 221
	errDeleteProtected    = 222
	errWriteProtected     = 223
	errReadProtected      = 224
	errNoMoreEntries      = 232
)

// dosErr is a packet failure: Res1 0 (or -1 for Seek and Read), Res2 the
// code.
type dosErr uint32

func (e dosErr) Error() string { return fmt.Sprintf("DOS error %d", uint32(e)) }

func (d *HostDir) packet(a *Amiga, typ uint32, arg [7]uint32) (uint32, uint32) {
	r, err := d.do(a, typ, arg)
	if err != nil {
		code := uint32(errObjectNotFound)
		var de dosErr
		switch {
		case errors.As(err, &de):
			code = uint32(de)
		case errors.Is(err, fs.ErrNotExist):
		case errors.Is(err, fs.ErrPermission):
			code = errWriteProtected
		case errors.Is(err, fs.ErrExist):
			code = errObjectExists
		default:
			code = errDiskFull
		}
		if typ == actionRead || typ == actionWrite || typ == actionSeek || typ == actionSetFileSize {
			return dosTrue, code
		}
		return 0, code
	}
	return r, 0
}

func (d *HostDir) do(a *Amiga, typ uint32, arg [7]uint32) (uint32, error) {
	b := func(i int) string { return a.peekBString(arg[i] << 2) }
	switch typ {
	case actionLocateObject:
		p, err := d.resolve(a, arg[0], b(1), false)
		if err != nil {
			return 0, err
		}
		return d.lock(a, p, int32(arg[2]) == -1)
	case actionFreeLock:
		return dosTrue, d.unlock(a, arg[0])
	case actionCopyDir:
		l, err := d.lockAt(arg[0])
		if err != nil {
			return 0, err
		}
		if arg[0] == 0 {
			return 0, nil
		}
		return d.lock(a, l.path, false)
	case actionParent:
		l, err := d.lockAt(arg[0])
		if err != nil {
			return 0, err
		}
		if l.path == "" {
			return 0, nil
		}
		return d.lock(a, parentOf(l.path), false)
	case actionSameLock:
		l1, err1 := d.lockAt(arg[0])
		l2, err2 := d.lockAt(arg[1])
		if err1 != nil || err2 != nil || l1.path != l2.path {
			return 0, nil
		}
		return dosTrue, nil
	case actionExamineObject:
		l, err := d.lockAt(arg[0])
		if err != nil {
			return 0, err
		}
		l.list = nil
		return dosTrue, d.examine(a, l.path, arg[1]<<2, 0)
	case actionExamineNext:
		return d.examineNext(a, arg[0], arg[1]<<2)
	case actionFindInput, actionFindOutput, actionFindUpdate:
		return d.open(a, typ, arg[0]<<2, arg[1], b(2))
	case actionRead:
		f, err := d.file(arg[0])
		if err != nil {
			return 0, err
		}
		buf := make([]byte, arg[2])
		n, err := io.ReadFull(f.f, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return 0, err
		}
		a.pokeBytes(arg[1], buf[:n])
		a.HardDisk.lastIO, a.HardDisk.lastWrite = a.cycles|1, false
		return uint32(n), nil
	case actionWrite:
		f, err := d.file(arg[0])
		if err != nil {
			return 0, err
		}
		if !f.write {
			return 0, d.protectedErr()
		}
		buf := make([]byte, arg[2])
		a.peekBytes(arg[1], buf)
		n, err := f.f.Write(buf)
		a.HardDisk.lastIO, a.HardDisk.lastWrite = a.cycles|1, true
		if err != nil {
			return 0, dosErr(errDiskFull)
		}
		return uint32(n), nil
	case actionSeek:
		return d.seek(arg[0], int32(arg[1]), int32(arg[2]))
	case actionEnd:
		f, err := d.file(arg[0])
		if err != nil {
			return 0, err
		}
		f.f.Close()
		delete(d.files, arg[0])
		return dosTrue, nil
	case actionSetFileSize:
		return d.setFileSize(arg[0], int32(arg[1]), int32(arg[2]))
	case actionFHFromLock:
		l, err := d.lockAt(arg[1])
		if err != nil {
			return 0, err
		}
		if err := d.openAt(a, arg[0]<<2, l.path, !d.ReadOnly, l.exclusive, false); err != nil {
			return 0, err
		}
		d.dropLock(a, arg[1])
		return dosTrue, nil
	case actionCopyDirFH, actionParentFH:
		f, err := d.file(arg[0])
		if err != nil {
			return 0, err
		}
		if typ == actionParentFH {
			return d.lock(a, parentOf(f.path), false)
		}
		return d.lock(a, f.path, false)
	case actionExamineFH:
		f, err := d.file(arg[0])
		if err != nil {
			return 0, err
		}
		return dosTrue, d.examine(a, f.path, arg[1]<<2, 0)
	case actionDeleteObject:
		p, err := d.resolve(a, arg[0], b(1), false)
		if err != nil {
			return 0, err
		}
		return dosTrue, d.delete(p)
	case actionRenameObject:
		from, err := d.resolve(a, arg[0], b(1), false)
		if err != nil {
			return 0, err
		}
		to, err := d.resolve(a, arg[2], b(3), true)
		if err != nil {
			return 0, err
		}
		return dosTrue, d.rename(from, to)
	case actionCreateDir:
		if d.ReadOnly {
			return 0, dosErr(errDiskWriteProtected)
		}
		p, err := d.resolve(a, arg[0], b(1), true)
		if err != nil {
			return 0, err
		}
		if err := os.Mkdir(d.host(p), 0o755); err != nil {
			return 0, err
		}
		return d.lock(a, p, true)
	case actionSetProtect, actionSetComment:
		if d.ReadOnly {
			return 0, dosErr(errDiskWriteProtected)
		}
		p, err := d.resolve(a, arg[1], b(2), false)
		if err != nil {
			return 0, err
		}
		m := d.meta(p)
		if typ == actionSetProtect {
			m.prot = arg[3]
		} else {
			m.comment = a.peekBString(arg[3] << 2)
		}
		return dosTrue, d.setMeta(p, m)
	case actionSetDate:
		if d.ReadOnly {
			return 0, dosErr(errDiskWriteProtected)
		}
		p, err := d.resolve(a, arg[1], b(2), false)
		if err != nil {
			return 0, err
		}
		t := getDateStamp(a, arg[3])
		return dosTrue, os.Chtimes(d.host(p), t, t)
	case actionDiskInfo:
		d.info(a, arg[0]<<2)
		return dosTrue, nil
	case actionInfo:
		if _, err := d.lockAt(arg[0]); err != nil {
			return 0, err
		}
		d.info(a, arg[1]<<2)
		return dosTrue, nil
	case actionCurrentVolume:
		return d.volNode >> 2, nil
	case actionRenameDisk:
		name := amigaName(b(0))
		if name == "" {
			return 0, dosErr(errInvalidName)
		}
		d.setVolumeName(a, name)
		return dosTrue, nil
	case actionIsFilesystem, actionFlush, actionInhibit, actionDie, actionWriteProtect,
		actionChangeMode, actionSetOwner:
		return dosTrue, nil
	case actionMoreCache:
		return 1, nil
	}
	return 0, dosErr(errActionNotKnown)
}

// ---- Paths ----

// host is the host path of a relative path.
func (d *HostDir) host(p string) string {
	if p == "" {
		return d.Path
	}
	return filepath.Join(d.Path, filepath.FromSlash(p))
}

func parentOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

// resolve turns an Amiga name relative to a lock into a relative host
// path. With create, the last part need not exist (it is named as given).
func (d *HostDir) resolve(a *Amiga, lock uint32, name string, create bool) (string, error) {
	l, err := d.lockAt(lock)
	if err != nil {
		return "", err
	}
	// DOS sends the name as given, prefix and all, with the lock the prefix
	// stands for: "devs:x" comes with DEVS:'s lock. Only ":x" is the root.
	cur := l.path
	if i := strings.IndexByte(name, ':'); i >= 0 {
		if i == 0 {
			cur = ""
		}
		name = name[i+1:]
	}
	if name == "" {
		return cur, nil
	}
	parts := strings.Split(name, "/")
	if n := len(parts); n > 1 && parts[n-1] == "" && parts[n-2] != "" {
		parts = parts[:n-1] // "dir/" is "dir"
	}
	for i, part := range parts {
		if part == "" { // a '/' on its own: the parent
			if cur == "" {
				return "", dosErr(errObjectNotFound)
			}
			cur = parentOf(cur)
			continue
		}
		hostName, err := d.lookup(cur, part)
		if err != nil {
			if i == len(parts)-1 && create && errors.Is(err, fs.ErrNotExist) {
				hostName = hostFileName(part)
			} else if i < len(parts)-1 {
				return "", dosErr(errDirNotFound)
			} else {
				return "", err
			}
		}
		cur = path.Join(cur, hostName)
	}
	return cur, nil
}

// lookup finds the host name in the directory dir matching an Amiga name,
// ignoring case as AmigaDOS does.
func (d *HostDir) lookup(dir, name string) (string, error) {
	ents, err := os.ReadDir(d.host(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isNotDir(err) {
			return "", dosErr(errDirNotFound)
		}
		return "", err
	}
	for _, e := range ents {
		if !hiddenName(e.Name()) && strings.EqualFold(amigaName(e.Name()), name) {
			return e.Name(), nil
		}
	}
	return "", fs.ErrNotExist
}

// find resolves an Amiga path from the root (for Go's own use).
func (d *HostDir) find(name string) (string, error) {
	cur := ""
	for _, part := range strings.Split(name, "/") {
		n, err := d.lookup(cur, part)
		if err != nil {
			return "", err
		}
		cur = path.Join(cur, n)
	}
	return cur, nil
}

func isNotDir(err error) bool {
	var pe *fs.PathError
	return errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "not a directory")
}

// hiddenName reports host files the Amiga doesn't see: the .uaem files
// and the Finder's.
func hiddenName(n string) bool {
	return strings.HasSuffix(n, ".uaem") || n == ".DS_Store" || strings.HasPrefix(n, "._")
}

// amigaName converts a host name to the Amiga's Latin-1, with characters
// it can't have replaced. "" if the name can't be shown at all.
func amigaName(n string) string {
	var b []byte
	for _, r := range n {
		switch {
		case r == ':' || r == '/' || r < 0x20:
			r = '_'
		case r > 0xFF || r == utf8.RuneError:
			r = '?'
		}
		b = append(b, byte(r))
	}
	if len(b) > 107 {
		return ""
	}
	return string(b)
}

// hostFileName converts a new Amiga name (Latin-1) to UTF-8.
func hostFileName(n string) string {
	var b strings.Builder
	for i := 0; i < len(n); i++ {
		b.WriteRune(rune(n[i]))
	}
	return b.String()
}

// ---- Locks ----

func (d *HostDir) lockAt(bptr uint32) (*hostLock, error) {
	if bptr == 0 {
		return d.root, nil
	}
	l, ok := d.locks[bptr<<2]
	if !ok {
		return nil, dosErr(errInvalidLock)
	}
	return l, nil
}

// inUse reports whether p is locked or open: any use, or with exclusive
// only an exclusive one.
func (d *HostDir) inUse(p string, exclusive bool) bool {
	for _, l := range d.locks {
		if l.path == p && (!exclusive || l.exclusive) {
			return true
		}
	}
	for _, f := range d.files {
		if f.path == p && (!exclusive || f.exclusive) {
			return true
		}
	}
	return false
}

// lock makes a FileLock on p and returns its BPTR.
func (d *HostDir) lock(a *Amiga, p string, exclusive bool) (uint32, error) {
	if _, err := os.Stat(d.host(p)); err != nil {
		return 0, err
	}
	if d.inUse(p, !exclusive) {
		return 0, dosErr(errObjectInUse)
	}
	var addr uint32
	if n := len(d.free); n > 0 {
		addr, d.free = d.free[n-1], d.free[:n-1]
	} else if d.poolTop+hostLockSize <= d.poolEnd {
		addr = d.poolTop
		d.poolTop += hostLockSize
	} else {
		return 0, dosErr(errNoFreeStore)
	}
	d.nextKey++
	d.locks[addr] = &hostLock{path: p, exclusive: exclusive, key: d.nextKey}
	access := uint32(0xFFFFFFFE) // SHARED_LOCK
	if exclusive {
		access = 0xFFFFFFFF
	}
	a.poke32(addr, 0)
	a.poke32(addr+4, d.nextKey)
	a.poke32(addr+8, access)
	a.poke32(addr+12, d.port)
	a.poke32(addr+16, d.volNode>>2)
	return addr >> 2, nil
}

func (d *HostDir) unlock(a *Amiga, bptr uint32) error {
	if bptr == 0 {
		return nil
	}
	if _, ok := d.locks[bptr<<2]; !ok {
		return dosErr(errInvalidLock)
	}
	d.dropLock(a, bptr)
	return nil
}

func (d *HostDir) dropLock(a *Amiga, bptr uint32) {
	if bptr == 0 {
		return
	}
	addr := bptr << 2
	if _, ok := d.locks[addr]; ok {
		delete(d.locks, addr)
		a.poke32(addr+12, 0)
		d.free = append(d.free, addr)
	}
}

// ---- Files ----

func (d *HostDir) file(id uint32) (*hostFile, error) {
	f, ok := d.files[id]
	if !ok {
		return nil, dosErr(errInvalidLock)
	}
	return f, nil
}

func (d *HostDir) protectedErr() error {
	if d.ReadOnly {
		return dosErr(errDiskWriteProtected)
	}
	return dosErr(errWriteProtected)
}

func (d *HostDir) open(a *Amiga, typ, fh, lock uint32, name string) (uint32, error) {
	newFile := typ == actionFindOutput
	p, err := d.resolve(a, lock, name, newFile || typ == actionFindUpdate)
	if err != nil {
		return 0, err
	}
	if p == "" {
		return 0, dosErr(errObjectWrongType)
	}
	if (newFile || typ == actionFindUpdate) && d.ReadOnly {
		return 0, dosErr(errDiskWriteProtected)
	}
	return dosTrue, d.openAt(a, fh, p, !d.ReadOnly, newFile, newFile || typ == actionFindUpdate)
}

// openAt opens p for the FileHandle at fh: for writing too if write and
// the host allows it, emptied if exclusive (MODE_NEWFILE), created if
// create.
func (d *HostDir) openAt(a *Amiga, fh uint32, p string, write, exclusive, create bool) error {
	hp := d.host(p)
	if st, err := os.Stat(hp); err == nil && st.IsDir() {
		return dosErr(errObjectWrongType)
	} else if err != nil && !create {
		return err
	}
	if d.inUse(p, !exclusive) {
		return dosErr(errObjectInUse)
	}
	if exclusive && d.meta(p).prot&protWrite != 0 {
		return dosErr(errWriteProtected)
	}
	flag := os.O_RDONLY
	if write {
		flag = os.O_RDWR
		if create {
			flag |= os.O_CREATE
		}
		if exclusive {
			flag |= os.O_TRUNC
		}
	}
	f, err := os.OpenFile(hp, flag, 0o644)
	if err != nil && write && !exclusive && errors.Is(err, fs.ErrPermission) {
		write = false // a file the host won't let us write: read it
		f, err = os.Open(hp)
	}
	if err != nil {
		return err
	}
	d.nextKey++
	d.files[d.nextKey] = &hostFile{path: p, f: f, write: write, exclusive: exclusive}
	a.poke32(fh+36, d.nextKey) // fh_Arg1
	return nil
}

func (d *HostDir) seek(id uint32, off, mode int32) (uint32, error) {
	f, err := d.file(id)
	if err != nil {
		return 0, err
	}
	old, err := f.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, dosErr(errSeek)
	}
	st, err := f.f.Stat()
	if err != nil {
		return 0, dosErr(errSeek)
	}
	pos := int64(off)
	switch mode {
	case 0: // OFFSET_CURRENT
		pos += old
	case 1: // OFFSET_END
		pos += st.Size()
	case -1: // OFFSET_BEGINNING
	default:
		return 0, dosErr(errSeek)
	}
	if pos < 0 || pos > st.Size() {
		return 0, dosErr(errSeek)
	}
	f.f.Seek(pos, io.SeekStart)
	return uint32(old), nil
}

func (d *HostDir) setFileSize(id uint32, off, mode int32) (uint32, error) {
	f, err := d.file(id)
	if err != nil {
		return 0, err
	}
	if !f.write {
		return 0, d.protectedErr()
	}
	cur, _ := f.f.Seek(0, io.SeekCurrent)
	st, err := f.f.Stat()
	if err != nil {
		return 0, err
	}
	size := int64(off)
	switch mode {
	case 0:
		size += cur
	case 1:
		size += st.Size()
	}
	if size < 0 {
		return 0, dosErr(errSeek)
	}
	if err := f.f.Truncate(size); err != nil {
		return 0, err
	}
	if cur > size {
		f.f.Seek(size, io.SeekStart)
	}
	return uint32(size), nil
}

// ---- Directories ----

// FileInfoBlock offsets.
const (
	fibDiskKey    = 0
	fibType       = 4
	fibName       = 8
	fibProtection = 116
	fibEntryType  = 120
	fibSize       = 124
	fibNumBlocks  = 128
	fibDate       = 132
	fibComment    = 144
)

// examine fills the FileInfoBlock at fib for p, with key as fib_DiskKey.
func (d *HostDir) examine(a *Amiga, p string, fib, key uint32) error {
	st, err := os.Stat(d.host(p))
	if err != nil {
		return err
	}
	name := amigaName(path.Base(p))
	typ := int32(-3) // ST_FILE
	switch {
	case p == "":
		typ, name = 1, d.Volume // ST_ROOT
	case st.IsDir():
		typ = 2 // ST_USERDIR
	}
	m := d.meta(p)
	prot := m.prot
	if d.ReadOnly || st.Mode().Perm()&0o200 == 0 {
		prot |= protWrite | protDelete
	}
	size := uint32(0)
	if !st.IsDir() {
		size = uint32(min(st.Size(), 0x7FFFFFFF))
	}
	a.poke32(fib+fibDiskKey, key)
	a.poke32(fib+fibType, uint32(typ))
	a.pokeBString(fib+fibName, name, 107)
	a.poke32(fib+fibProtection, prot)
	a.poke32(fib+fibEntryType, uint32(typ))
	a.poke32(fib+fibSize, size)
	a.poke32(fib+fibNumBlocks, (size+511)/512)
	putDateStamp(a, fib+fibDate, st.ModTime())
	a.pokeBString(fib+fibComment, m.comment, 79)
	return nil
}

// examineNext fills the FileInfoBlock with the next entry of the locked
// directory; fib_DiskKey counts the entries returned.
func (d *HostDir) examineNext(a *Amiga, lock, fib uint32) (uint32, error) {
	l, err := d.lockAt(lock)
	if err != nil {
		return 0, err
	}
	key := a.peek32(fib + fibDiskKey)
	if l.list == nil || key == 0 {
		ents, err := os.ReadDir(d.host(l.path))
		if err != nil {
			return 0, dosErr(errObjectWrongType)
		}
		l.list = []string{}
		for _, e := range ents {
			if !hiddenName(e.Name()) && amigaName(e.Name()) != "" {
				l.list = append(l.list, e.Name())
			}
		}
	}
	for int(key) < len(l.list) {
		n := l.list[key]
		key++
		if err := d.examine(a, path.Join(l.path, n), fib, key); err == nil {
			return dosTrue, nil
		}
	}
	l.list = nil
	return 0, dosErr(errNoMoreEntries)
}

func (d *HostDir) delete(p string) error {
	if d.ReadOnly {
		return dosErr(errDiskWriteProtected)
	}
	if p == "" {
		return dosErr(errObjectWrongType)
	}
	if d.inUse(p, false) {
		return dosErr(errObjectInUse)
	}
	if d.meta(p).prot&protDelete != 0 {
		return dosErr(errDeleteProtected)
	}
	hp := d.host(p)
	if st, err := os.Stat(hp); err == nil && st.IsDir() {
		// The Amiga can't see the Finder's files, so they don't count.
		ents, _ := os.ReadDir(hp)
		for _, e := range ents {
			if !hiddenName(e.Name()) {
				return dosErr(errDirNotEmpty)
			}
		}
		for _, e := range ents {
			os.Remove(filepath.Join(hp, e.Name()))
		}
	}
	if err := os.Remove(hp); err != nil {
		return err
	}
	os.Remove(hp + ".uaem")
	return nil
}

func (d *HostDir) rename(from, to string) error {
	if d.ReadOnly {
		return dosErr(errDiskWriteProtected)
	}
	if from == "" || to == "" {
		return dosErr(errObjectWrongType)
	}
	if to == from+"/"+path.Base(to) || strings.HasPrefix(to, from+"/") {
		return dosErr(errObjectInUse) // into itself
	}
	if _, err := os.Stat(d.host(to)); err == nil && !strings.EqualFold(from, to) {
		return dosErr(errObjectExists)
	}
	if err := os.Rename(d.host(from), d.host(to)); err != nil {
		return err
	}
	if _, err := os.Stat(d.host(from) + ".uaem"); err == nil {
		os.Rename(d.host(from)+".uaem", d.host(to)+".uaem")
	}
	// Locks and open files follow what they refer to.
	move := func(p string) string {
		if p == from {
			return to
		}
		if strings.HasPrefix(p, from+"/") {
			return to + p[len(from):]
		}
		return p
	}
	for _, l := range d.locks {
		l.path = move(l.path)
	}
	for _, f := range d.files {
		f.path = move(f.path)
	}
	return nil
}

// InfoData offsets.
func (d *HostDir) info(a *Amiga, id uint32) {
	total, free := diskSpace(d.Path)
	const bpb = 512
	// Old programs multiply blocks by the block size in 32 bits.
	limit := uint64(0x7FFFFFFF) / bpb
	blocks := min(total/bpb, limit)
	used := blocks - min(free/bpb, blocks)
	state := uint32(82) // ID_VALIDATED
	if d.ReadOnly {
		state = 80 // ID_WRITE_PROTECTED
	}
	inUse := uint32(0)
	if len(d.locks) > 0 || len(d.files) > 0 {
		inUse = dosTrue
	}
	for i, v := range []uint32{0, 0, state, uint32(blocks), uint32(used), bpb, 0x444F5300, d.volNode >> 2, inUse} {
		a.poke32(id+uint32(4*i), v)
	}
}

// ---- Protection bits and comments (.uaem files) ----

const (
	protDelete = 1 << 0
	protWrite  = 1 << 2
)

type hostMeta struct {
	prot    uint32
	comment string
}

// meta reads p's .uaem file, if it has one.
func (d *HostDir) meta(p string) hostMeta {
	var m hostMeta
	if p == "" {
		return m
	}
	b, err := os.ReadFile(d.host(p) + ".uaem")
	if err != nil {
		return m
	}
	s := strings.TrimRight(string(b), "\r\n")
	if len(s) < 8 {
		return m
	}
	// hsparwed: h s p a set when the letter is there; r w e d set when not.
	for i, c := range s[:8] {
		bit := uint32(1) << (7 - i)
		if (i < 4) == (c != '-') {
			m.prot |= bit
		}
	}
	if f := strings.SplitN(s, " ", 4); len(f) == 4 {
		m.comment = amigaText(f[3])
	}
	return m
}

func (d *HostDir) setMeta(p string, m hostMeta) error {
	if p == "" {
		return nil
	}
	hp := d.host(p)
	if m.prot&0xFF == 0 && m.comment == "" {
		if err := os.Remove(hp + ".uaem"); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	flags := []byte("hsparwed")
	for i := range flags {
		bit := uint32(1) << (7 - i)
		if (i < 4) != (m.prot&bit != 0) {
			flags[i] = '-'
		}
	}
	t := time.Now()
	if st, err := os.Stat(hp); err == nil {
		t = st.ModTime()
	}
	line := fmt.Sprintf("%s %s %s\n", flags, t.Format("2006-01-02 15:04:05.00"), hostFileName(m.comment))
	return os.WriteFile(hp+".uaem", []byte(line), 0o644)
}

// amigaText converts UTF-8 to Latin-1.
func amigaText(s string) string {
	var b []byte
	for _, r := range s {
		if r > 0xFF {
			r = '?'
		}
		b = append(b, byte(r))
	}
	return string(b)
}

// ---- Dates and strings ----

var amigaEpoch = time.Date(1978, 1, 1, 0, 0, 0, 0, time.UTC)

// putDateStamp writes t as a DateStamp (local time): days since 1978,
// minutes, ticks.
func putDateStamp(a *Amiga, addr uint32, t time.Time) {
	t = t.Local()
	y, mo, dd := t.Date()
	days := int64(time.Date(y, mo, dd, 0, 0, 0, 0, time.UTC).Sub(amigaEpoch).Hours() / 24)
	if days < 0 {
		days = 0
	}
	a.poke32(addr, uint32(days))
	a.poke32(addr+4, uint32(t.Hour()*60+t.Minute()))
	a.poke32(addr+8, uint32(t.Second()*50+t.Nanosecond()/20_000_000))
}

func getDateStamp(a *Amiga, addr uint32) time.Time {
	days, mins, ticks := a.peek32(addr), a.peek32(addr+4), a.peek32(addr+8)
	day := amigaEpoch.AddDate(0, 0, int(days))
	return time.Date(day.Year(), day.Month(), day.Day(), int(mins/60), int(mins%60), int(ticks/50),
		int(ticks%50)*20_000_000, time.Local)
}

// peekBString reads a BCPL string (length byte first).
func (a *Amiga) peekBString(addr uint32) string {
	if addr == 0 {
		return ""
	}
	b := make([]byte, a.peek8(addr))
	a.peekBytes(addr+1, b)
	return string(b)
}

// pokeBString writes a BCPL string of at most max characters.
func (a *Amiga) pokeBString(addr uint32, s string, max int) {
	s = s[:min(len(s), max)]
	a.poke8(addr, byte(len(s)))
	a.pokeBytes(addr+1, append([]byte(s), 0))
}

// sortedKeys lists a map's keys in order, for save states.
func sortedKeys[V any](m map[uint32]V) []uint32 {
	ks := make([]uint32, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
