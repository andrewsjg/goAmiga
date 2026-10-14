package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"time"
)

// An s5 (System V) filesystem writer, laid out as Amiga Unix's kernel
// reads it (its headers, sys/fs/s5*.h, with gcc's 4-byte alignment, which
// the kernel's s5statvfs confirms):
//
//   - Logical blocks of 1 KB (s_type Fs2b). Block 0 holds the superblock
//     at byte 512; the inode list starts at block 2 and runs to s_isize.
//   - Inodes are 64 bytes: mode, links, uid, gid (16 bits each), size,
//     13 three-byte block addresses (10 direct, then single, double and
//     triple indirect) and a generation byte, then three times. Inode 2 is
//     the root directory.
//   - A device file keeps its number in the addresses: address 1 = 1
//     marks the expanded form, address 2 the major and 3 the minor (iread).
//   - Directories are 16-byte entries: a 16-bit inode number and a
//     14-character name.
//   - Free blocks are a chain of lists of 50: the superblock holds the
//     first, and entry 0 of each list is the block holding the next.
//   - The superblock's s_state is FsOKAY - s_time when the filesystem is
//     clean.
type s5fs struct {
	w       io.WriterAt
	base    int64  // byte offset of the filesystem in w
	fsize   uint32 // blocks in the filesystem
	isize   uint32 // first data block
	next    uint32 // next free data block
	inodes  []*s5inode
	maxIno  int
	now     uint32
	renamed []string
}

const (
	s5BSize    = 1024
	s5InoSize  = 64
	s5PerInd   = s5BSize / 4
	s5NICFREE  = 50
	s5DirEnt   = 16
	s5RootIno  = 2
	s5Magic    = 0xfd187e20
	s5OK       = 0x7c269d38
	s5TypeFs2b = 2
)

type s5inode struct {
	mode         uint16
	nlink        int
	uid, gid     uint16
	size         uint32
	addr         [13]uint32
	atime, mtime uint32
	isDir        bool
	entries      []s5dirent
	names        map[string]bool // in a directory, the names used
	parent       int
}

type s5dirent struct {
	name string
	ino  int
}

// newS5 starts a filesystem of blocks 1 KB blocks with room for ninodes
// inodes (at most 65535), at byte offset base of w.
func newS5(w io.WriterAt, base int64, blocks uint32, ninodes int) (*s5fs, error) {
	if ninodes > 65535 {
		return nil, fmt.Errorf("s5: %d inodes is over the 65535 limit", ninodes)
	}
	ilist := uint32((ninodes*s5InoSize + s5BSize - 1) / s5BSize)
	fs := &s5fs{w: w, base: base, fsize: blocks, isize: 2 + ilist, maxIno: int(ilist) * s5BSize / s5InoSize,
		now: uint32(time.Now().Unix())}
	fs.next = fs.isize
	if fs.isize+16 > blocks {
		return nil, fmt.Errorf("s5: %d blocks is too small", blocks)
	}
	fs.inodes = make([]*s5inode, 2) // 0 unused, 1 the bad block inode
	fs.inodes[1] = &s5inode{}
	root := fs.newInode(0o040755, 0, 0, fs.now)
	if root != s5RootIno {
		panic("root inode isn't 2")
	}
	fs.inodes[root].parent = root
	return fs, nil
}

func (fs *s5fs) newInode(mode uint16, uid, gid uint16, mtime uint32) int {
	ino := len(fs.inodes)
	in := &s5inode{mode: mode, uid: uid, gid: gid, atime: mtime, mtime: mtime}
	if mode&0o170000 == 0o040000 {
		in.isDir = true
		in.names = map[string]bool{".": true, "..": true}
	}
	fs.inodes = append(fs.inodes, in)
	return ino
}

func (fs *s5fs) inodesFull() bool { return len(fs.inodes) > fs.maxIno }

// link enters ino in directory dir under name, cut to 14 characters (and
// made unique) if longer. It returns the name used.
func (fs *s5fs) link(dir int, name string, ino int) string {
	d := fs.inodes[dir]
	used := name
	if len(name) > 14 {
		used = name[:14]
		for i := 1; d.names[used]; i++ {
			suffix := fmt.Sprintf("~%d", i)
			used = name[:14-len(suffix)] + suffix
		}
		fs.renamed = append(fs.renamed, name+" -> "+used)
	}
	d.names[used] = true
	d.entries = append(d.entries, s5dirent{used, ino})
	in := fs.inodes[ino]
	in.nlink++
	if in.isDir {
		in.parent = dir
		d.nlink++ // its ".."
	}
	return used
}

func (fs *s5fs) writeBlock(b uint32, data []byte) error {
	if b >= fs.fsize {
		return fmt.Errorf("s5: block %d is past the end (%d)", b, fs.fsize)
	}
	_, err := fs.w.WriteAt(data, fs.base+int64(b)*s5BSize)
	return err
}

func (fs *s5fs) alloc() (uint32, error) {
	if fs.next >= fs.fsize {
		return 0, fmt.Errorf("s5: filesystem full (%d blocks)", fs.fsize)
	}
	b := fs.next
	fs.next++
	return b, nil
}

// writeData stores data for ino from r (size bytes).
func (fs *s5fs) writeData(ino int, r io.Reader, size int64) error {
	var blocks []uint32
	buf := make([]byte, s5BSize)
	for left := size; left > 0; {
		clear(buf)
		n := min(left, s5BSize)
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return err
		}
		b, err := fs.alloc()
		if err != nil {
			return err
		}
		if err := fs.writeBlock(b, buf); err != nil {
			return err
		}
		blocks = append(blocks, b)
		left -= n
	}
	in := fs.inodes[ino]
	in.size = uint32(size)
	return fs.setAddrs(in, blocks)
}

// setAddrs fills in the inode's block addresses, writing indirect blocks.
func (fs *s5fs) setAddrs(in *s5inode, blocks []uint32) error {
	i := 0
	for ; i < 10 && i < len(blocks); i++ {
		in.addr[i] = blocks[i]
	}
	rest := blocks[i:]
	for level := 1; level <= 3 && len(rest) > 0; level++ {
		b, used, err := fs.indirect(level, rest)
		if err != nil {
			return err
		}
		in.addr[9+level] = b
		rest = rest[used:]
	}
	if len(rest) > 0 {
		return fmt.Errorf("s5: file too big")
	}
	return nil
}

// indirect writes an indirect block of the given level for as many of
// blocks as it holds, returning its address and how many it took.
func (fs *s5fs) indirect(level int, blocks []uint32) (uint32, int, error) {
	b, err := fs.alloc()
	if err != nil {
		return 0, 0, err
	}
	buf := make([]byte, s5BSize)
	used := 0
	for i := 0; i < s5PerInd && used < len(blocks); i++ {
		var e uint32
		if level == 1 {
			e = blocks[used]
			used++
		} else {
			sub, n, err := fs.indirect(level-1, blocks[used:])
			if err != nil {
				return 0, 0, err
			}
			e = sub
			used += n
		}
		binary.BigEndian.PutUint32(buf[4*i:], e)
	}
	return b, used, fs.writeBlock(b, buf)
}

// finish writes the directories, the inode list, the free list and the
// superblock.
func (fs *s5fs) finish(fname, fpack string) error {
	for ino, in := range fs.inodes {
		if in == nil || !in.isDir {
			continue
		}
		ents := append([]s5dirent{{".", ino}, {"..", in.parent}}, in.entries...)
		sort.SliceStable(ents[2:], func(i, j int) bool { return ents[2+i].name < ents[2+j].name })
		data := make([]byte, len(ents)*s5DirEnt)
		for i, e := range ents {
			binary.BigEndian.PutUint16(data[i*s5DirEnt:], uint16(e.ino))
			copy(data[i*s5DirEnt+2:i*s5DirEnt+16], e.name)
		}
		if err := fs.writeData(ino, bytesReader(data), int64(len(data))); err != nil {
			return err
		}
	}
	// Each directory's "." links it once more; the root's ".." too.
	for _, in := range fs.inodes {
		if in != nil && in.isDir {
			in.nlink++
		}
	}
	fs.inodes[s5RootIno].nlink++

	// The inode list.
	ilist := make([]byte, (fs.isize-2)*s5BSize)
	for ino := 1; ino < len(fs.inodes); ino++ {
		in := fs.inodes[ino]
		if ino == 1 {
			continue
		}
		o := (ino - 1) * s5InoSize
		d := ilist[o : o+s5InoSize]
		be := binary.BigEndian
		be.PutUint16(d[0:], in.mode)
		be.PutUint16(d[2:], uint16(in.nlink))
		be.PutUint16(d[4:], in.uid)
		be.PutUint16(d[6:], in.gid)
		be.PutUint32(d[8:], in.size)
		for i, a := range in.addr {
			d[12+3*i], d[13+3*i], d[14+3*i] = byte(a>>16), byte(a>>8), byte(a)
		}
		be.PutUint32(d[52:], in.atime)
		be.PutUint32(d[56:], in.mtime)
		be.PutUint32(d[60:], in.mtime)
	}
	if _, err := fs.w.WriteAt(ilist, fs.base+2*s5BSize); err != nil {
		return err
	}

	// The free list, freed from the top down so blocks are handed out
	// in ascending order.
	nfree, free := 1, make([]uint32, s5NICFREE)
	tfree := uint32(0)
	for b := fs.fsize - 1; b >= fs.next; b-- {
		if nfree == s5NICFREE {
			blk := make([]byte, s5BSize)
			binary.BigEndian.PutUint32(blk, uint32(nfree))
			for i := 0; i < nfree; i++ {
				binary.BigEndian.PutUint32(blk[4+4*i:], free[i])
			}
			if err := fs.writeBlock(b, blk); err != nil {
				return err
			}
			nfree = 0
		}
		free[nfree] = b
		nfree++
		tfree++
	}

	sb := make([]byte, 512)
	be := binary.BigEndian
	be.PutUint16(sb[0:], uint16(fs.isize))
	be.PutUint32(sb[4:], fs.fsize)
	be.PutUint16(sb[8:], uint16(nfree))
	for i := 0; i < nfree; i++ {
		be.PutUint32(sb[12+4*i:], free[i])
	}
	be.PutUint16(sb[212:], 0) // s_ninode: the kernel scans for free inodes
	be.PutUint32(sb[420:], fs.now)
	be.PutUint32(sb[432:], tfree)
	be.PutUint16(sb[436:], uint16(fs.maxIno-(len(fs.inodes)-1)))
	copy(sb[438:444], fname)
	copy(sb[444:450], fpack)
	be.PutUint32(sb[500:], s5OK-fs.now)
	be.PutUint32(sb[504:], s5Magic)
	be.PutUint32(sb[508:], s5TypeFs2b)
	_, err := fs.w.WriteAt(sb, fs.base+512)
	return err
}

type byteReader struct {
	b []byte
}

func bytesReader(b []byte) io.Reader { return &byteReader{b} }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

// fsWriter adaptors.

func (fs *s5fs) inodeFull() bool                        { return fs.inodesFull() }
func (fs *s5fs) linkName(dir int, name string, ino int) { fs.link(dir, name, ino) }
func (fs *s5fs) setAttrs(ino int, mode, uid, gid uint16, mtime uint32) {
	in := fs.inodes[ino]
	in.mode, in.uid, in.gid, in.mtime, in.atime = mode, uid, gid, mtime, mtime
}
func (fs *s5fs) setDevice(ino int, rdev uint32) {
	in := fs.inodes[ino]
	in.addr[1], in.addr[2], in.addr[3] = 1, rdev>>8, rdev&0xFF
}
