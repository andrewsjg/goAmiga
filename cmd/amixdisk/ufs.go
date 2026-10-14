package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"time"
)

// A UFS writer: the 4.3BSD-Tahoe fast file system in Amiga Unix's form
// (its sys/fs/ufs_*.h; the kernel's mountfs checks the magic at $55C, a
// block size of at most 8 KB, at most 8 fragments, and fs_state +
// fs_time == FSOKAY for a clean filesystem).
//
//   - 8 KB blocks of 1 KB fragments. A cylinder group is 16 cylinders of
//     1024 sectors (8 MB), with a superblock copy at fragment 16, the
//     group's header at 24, its 2048 inodes from 32, and data from 288.
//     The primary superblock is at byte 8192.
//   - Inodes are 128 bytes, with both the old 16-bit and the expanded
//     (EFT, flagged $90909090) mode, uid and gid. A file's size is the
//     second long of the quad on 68k. Devices keep the old number in
//     address 0 and the expanded one in address 1.
//   - Small files end in a fragment: the last block of a file under 12
//     blocks takes only the fragments it needs, packed into partly used
//     blocks.
//   - Directories are 4.3BSD records (inode, record length, name length,
//     name) in 512-byte chunks.
//   - The free map, free counts and per-cylinder free block counts are
//     computed as mkfs and fsck compute them, and every group has a copy
//     of the superblock.
type ufs struct {
	w      io.WriterAt
	base   int64
	ncg    int
	free   [][]byte // per group, the free fragment map (1 = free)
	inodes []*ufsInode
	now    uint32
	cursor int // next fragment to try for a block
	tail   int // fragment where the current tail block's free part starts, or -1
	tailN  int // free fragments left in it
}

const (
	ufsBSize   = 8192
	ufsFSize   = 1024
	ufsFrag    = 8
	ufsNSect   = 64
	ufsNTrak   = 16
	ufsSPC     = ufsNSect * ufsNTrak // sectors per cylinder
	ufsCPG     = 16
	ufsFPG     = ufsCPG * ufsSPC / 2 // fragments per group
	ufsIPG     = 2048
	ufsINOPB   = ufsBSize / 128
	ufsNIndir  = ufsBSize / 4
	ufsSBlkno  = 16
	ufsCBlkno  = 24
	ufsIBlkno  = 32
	ufsDBlkno  = ufsIBlkno + ufsIPG/(ufsINOPB/ufsFrag)
	ufsMagic   = 0x011954
	ufsCGMagic = 0x090255
	ufsFSOkay  = 0x7c269d38
	ufsEFT     = 0x90909090
	ufsNRPOS   = 8
)

type ufsInode struct {
	mode         uint16
	nlink        int
	uid, gid     uint16
	size         uint32
	db           [12]uint32
	ib           [3]uint32
	blocks       uint32 // 512-byte sectors held
	atime, mtime uint32
	isDir        bool
	entries      []s5dirent
	parent       int
}

// newUFS starts a filesystem of size bytes (rounded down to whole
// cylinder groups) at byte base of w.
func newUFS(w io.WriterAt, base int64, size int64) (*ufs, error) {
	ncg := int(size / (ufsFPG * ufsFSize))
	if ncg < 1 {
		return nil, fmt.Errorf("ufs: %d bytes is too small", size)
	}
	fs := &ufs{w: w, base: base, ncg: ncg, now: uint32(time.Now().Unix()), tail: -1}
	cs := ufsCSSize(ncg)
	for c := 0; c < ncg; c++ {
		// Free: the data area, and in groups after the first the
		// fragments before the superblock copy (group 0 has the boot
		// block and the superblock there).
		m := make([]byte, ufsFPG/8)
		dmin := ufsDBlkno
		if c == 0 {
			dmin += cs // the summary area
		}
		for f := 0; f < ufsFPG; f++ {
			if c > 0 && f < ufsSBlkno || f >= dmin {
				m[f/8] |= 1 << (f % 8)
			}
		}
		fs.free = append(fs.free, m)
	}
	fs.inodes = make([]*ufsInode, 2) // 0 and 1 are reserved
	root := fs.newInode(0o040755, 0, 0, fs.now)
	fs.inodes[root].parent = root
	lf := fs.newInode(0o040700, 0, 0, fs.now)
	fs.link(root, "lost+found", lf)
	return fs, nil
}

// ufsCSSize is the fragments the cylinder group summary takes.
func ufsCSSize(ncg int) int { return (ncg*16 + ufsFSize - 1) / ufsFSize }

func (fs *ufs) isFree(f int) bool {
	return fs.free[f/ufsFPG][(f%ufsFPG)/8]&(1<<(f%8)) != 0
}

func (fs *ufs) take(f int) { fs.free[f/ufsFPG][(f%ufsFPG)/8] &^= 1 << (f % 8) }

func (fs *ufs) newInode(mode uint16, uid, gid uint16, mtime uint32) int {
	ino := len(fs.inodes)
	in := &ufsInode{mode: mode, uid: uid, gid: gid, atime: mtime, mtime: mtime, isDir: mode&0o170000 == 0o040000}
	fs.inodes = append(fs.inodes, in)
	return ino
}

func (fs *ufs) full() bool { return len(fs.inodes) >= fs.ncg*ufsIPG }

func (fs *ufs) link(dir int, name string, ino int) {
	d := fs.inodes[dir]
	d.entries = append(d.entries, s5dirent{name, ino})
	in := fs.inodes[ino]
	in.nlink++
	if in.isDir {
		in.parent = dir
		d.nlink++
	}
}

// allocBlock takes the next free whole block.
func (fs *ufs) allocBlock() (int, error) {
	total := fs.ncg * ufsFPG
	for f := fs.cursor; f+ufsFrag <= total; f += ufsFrag {
		ok := true
		for i := 0; i < ufsFrag; i++ {
			if !fs.isFree(f + i) {
				ok = false
				break
			}
		}
		if ok {
			for i := 0; i < ufsFrag; i++ {
				fs.take(f + i)
			}
			fs.cursor = f + ufsFrag
			return f, nil
		}
	}
	return 0, fmt.Errorf("ufs: filesystem full")
}

// allocFrags takes n (< 8) contiguous fragments in a partly used block.
func (fs *ufs) allocFrags(n int) (int, error) {
	if fs.tail < 0 || fs.tailN < n {
		b, err := fs.allocBlock()
		if err != nil {
			return 0, err
		}
		for i := 0; i < ufsFrag; i++ { // give it back, to hand out in pieces
			fs.free[(b+i)/ufsFPG][((b+i)%ufsFPG)/8] |= 1 << ((b + i) % 8)
		}
		fs.tail, fs.tailN = b, ufsFrag
	}
	f := fs.tail
	for i := 0; i < n; i++ {
		fs.take(f + i)
	}
	fs.tail += n
	fs.tailN -= n
	return f, nil
}

func (fs *ufs) write(frag int, data []byte) error {
	_, err := fs.w.WriteAt(data, fs.base+int64(frag)*ufsFSize)
	return err
}

// writeData stores size bytes from r as ino's contents.
func (fs *ufs) writeData(ino int, r io.Reader, size int64) error {
	in := fs.inodes[ino]
	in.size = uint32(size)
	nblk := int((size + ufsBSize - 1) / ufsBSize)
	var blocks []uint32
	buf := make([]byte, ufsBSize)
	for i := 0; i < nblk; i++ {
		n := min(size-int64(i)*ufsBSize, ufsBSize)
		clear(buf)
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return err
		}
		var f int
		var err error
		frags := ufsFrag
		if i == nblk-1 && nblk <= 12 && n < ufsBSize {
			frags = int((n + ufsFSize - 1) / ufsFSize)
		}
		if frags < ufsFrag {
			f, err = fs.allocFrags(frags)
		} else {
			f, err = fs.allocBlock()
		}
		if err != nil {
			return err
		}
		if err := fs.write(f, buf[:frags*ufsFSize]); err != nil {
			return err
		}
		blocks = append(blocks, uint32(f))
		in.blocks += uint32(frags * 2)
	}
	return fs.setAddrs(in, blocks)
}

func (fs *ufs) setAddrs(in *ufsInode, blocks []uint32) error {
	i := 0
	for ; i < 12 && i < len(blocks); i++ {
		in.db[i] = blocks[i]
	}
	rest := blocks[i:]
	for level := 1; level <= 3 && len(rest) > 0; level++ {
		b, used, err := fs.indirect(in, level, rest)
		if err != nil {
			return err
		}
		in.ib[level-1] = b
		rest = rest[used:]
	}
	if len(rest) > 0 {
		return fmt.Errorf("ufs: file too big")
	}
	return nil
}

func (fs *ufs) indirect(in *ufsInode, level int, blocks []uint32) (uint32, int, error) {
	b, err := fs.allocBlock()
	if err != nil {
		return 0, 0, err
	}
	in.blocks += ufsBSize / 512
	buf := make([]byte, ufsBSize)
	used := 0
	for i := 0; i < ufsNIndir && used < len(blocks); i++ {
		var e uint32
		if level == 1 {
			e = blocks[used]
			used++
		} else {
			sub, n, err := fs.indirect(in, level-1, blocks[used:])
			if err != nil {
				return 0, 0, err
			}
			e, used = sub, used+n
		}
		binary.BigEndian.PutUint32(buf[4*i:], e)
	}
	return uint32(b), used, fs.write(b, buf)
}

// dirData lays out directory entries in 512-byte chunks, padded to at
// least minSize.
func dirData(ents []s5dirent, minSize int) []byte {
	var out []byte
	chunk := make([]byte, 0, 512)
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		// The last record takes the rest of the chunk.
		last := lastRecord(chunk)
		binary.BigEndian.PutUint16(chunk[last+4:], uint16(512-last))
		out = append(out, chunk...)
		out = append(out, make([]byte, 512-len(chunk))...)
		chunk = chunk[:0]
	}
	for _, e := range ents {
		rec := 8 + (len(e.name)+1+3)&^3
		if len(chunk)+rec > 512 {
			flush()
		}
		r := make([]byte, rec)
		binary.BigEndian.PutUint32(r, uint32(e.ino))
		binary.BigEndian.PutUint16(r[4:], uint16(rec))
		binary.BigEndian.PutUint16(r[6:], uint16(len(e.name)))
		copy(r[8:], e.name)
		chunk = append(chunk, r...)
	}
	flush()
	for len(out) < minSize { // empty chunks: one record of inode 0
		c := make([]byte, 512)
		binary.BigEndian.PutUint16(c[4:], 512)
		out = append(out, c...)
	}
	return out
}

func lastRecord(chunk []byte) int {
	off := 0
	for {
		rl := int(binary.BigEndian.Uint16(chunk[off+4:]))
		if off+rl >= len(chunk) {
			return off
		}
		off += rl
	}
}

// finish writes the directories, the inodes, the cylinder groups, the
// summary and the superblocks.
func (fs *ufs) finish(mountedOn string) error {
	for ino, in := range fs.inodes {
		if in == nil || !in.isDir {
			continue
		}
		in.nlink++ // "."
		ents := append([]s5dirent{{".", ino}, {"..", in.parent}}, in.entries...)
		sort.SliceStable(ents[2:], func(i, j int) bool { return ents[2+i].name < ents[2+j].name })
		min := 0
		if ino == 3 { // lost+found: room for fsck to reconnect files
			min = ufsBSize
		}
		data := dirData(ents, min)
		if err := fs.writeData(ino, bytesReader(data), int64(len(data))); err != nil {
			return err
		}
	}
	fs.inodes[2].nlink++ // the root's ".."

	be := binary.BigEndian
	cssize := ufsCSSize(fs.ncg)
	type csum struct{ ndir, nbfree, nifree, nffree int32 }
	var total csum
	sums := make([]csum, fs.ncg)
	dsize := int32(0)
	for c := 0; c < fs.ncg; c++ {
		cg := make([]byte, ufsBSize)
		var cs csum
		// Inodes.
		ib := make([]byte, ufsIPG*128)
		iused := make([]byte, ufsIPG/8)
		for i := 0; i < ufsIPG; i++ {
			ino := c*ufsIPG + i
			if ino < 2 {
				iused[i/8] |= 1 << (i % 8)
				continue
			}
			if ino >= len(fs.inodes) {
				cs.nifree++
				continue
			}
			iused[i/8] |= 1 << (i % 8)
			in := fs.inodes[ino]
			if in.isDir {
				cs.ndir++
			}
			d := ib[i*128 : i*128+128]
			be.PutUint16(d[0:], in.mode)
			be.PutUint16(d[2:], uint16(in.nlink))
			be.PutUint16(d[4:], in.uid)
			be.PutUint16(d[6:], in.gid)
			be.PutUint32(d[12:], in.size)
			be.PutUint32(d[16:], in.atime)
			be.PutUint32(d[24:], in.mtime)
			be.PutUint32(d[32:], in.mtime)
			for k, a := range in.db {
				be.PutUint32(d[40+4*k:], a)
			}
			for k, a := range in.ib {
				be.PutUint32(d[88+4*k:], a)
			}
			be.PutUint32(d[104:], in.blocks)
			be.PutUint32(d[108:], uint32(ino))
			be.PutUint32(d[112:], uint32(in.mode))
			be.PutUint32(d[116:], uint32(in.uid))
			be.PutUint32(d[120:], uint32(in.gid))
			be.PutUint32(d[124:], ufsEFT)
		}
		if err := fs.write(c*ufsFPG+ufsIBlkno, ib); err != nil {
			return err
		}
		// Free space, as fsck counts it.
		m := fs.free[c]
		var frsum [8]int32
		var btot [ufsCPG]int32
		var bpos [ufsCPG][ufsNRPOS]int16
		for b := 0; b < ufsFPG; b += ufsFrag {
			n := 0
			for i := 0; i < ufsFrag; i++ {
				if m[(b+i)/8]&(1<<((b+i)%8)) != 0 {
					n++
				}
			}
			if n == ufsFrag {
				cs.nbfree++
				cyl := b * 2 / ufsSPC
				rpos := b * 2 % ufsSPC % ufsNSect * ufsNRPOS / ufsNSect
				btot[cyl]++
				bpos[cyl][rpos]++
				continue
			}
			cs.nffree += int32(n)
			run := 0
			for i := 0; i <= ufsFrag; i++ {
				if i < ufsFrag && m[(b+i)/8]&(1<<((b+i)%8)) != 0 {
					run++
					continue
				}
				if run > 0 {
					frsum[run]++
				}
				run = 0
			}
		}
		dupper := ufsDBlkno
		if c == 0 {
			dupper += cssize
		} else {
			dsize += ufsSBlkno
		}
		dsize += int32(ufsFPG - dupper)
		// fsck rebuilds the header and compares: it takes the earlier of
		// the group's time and the clock's (0 matches whatever AMIX's
		// clock says), and gives the last group fs_ncyl % fs_cpg
		// cylinders, as mkfs does: 0 when the groups are all whole.
		be.PutUint32(cg[8:], 0)
		be.PutUint32(cg[12:], uint32(c))
		ncyl := uint16(ufsCPG)
		if c == fs.ncg-1 {
			ncyl = uint16(fs.ncg * ufsCPG % ufsCPG)
		}
		be.PutUint16(cg[16:], ncyl)
		be.PutUint16(cg[18:], ufsIPG)
		be.PutUint32(cg[20:], ufsFPG)
		be.PutUint32(cg[24:], uint32(cs.ndir))
		be.PutUint32(cg[28:], uint32(cs.nbfree))
		be.PutUint32(cg[32:], uint32(cs.nifree))
		be.PutUint32(cg[36:], uint32(cs.nffree))
		for k := 1; k < 8; k++ {
			be.PutUint32(cg[52+4*k:], uint32(frsum[k]))
		}
		for k := 0; k < ufsCPG; k++ {
			be.PutUint32(cg[84+4*k:], uint32(btot[k]))
			for r := 0; r < ufsNRPOS; r++ {
				be.PutUint16(cg[212+2*(k*ufsNRPOS+r):], uint16(bpos[k][r]))
			}
		}
		copy(cg[724:], iused)
		be.PutUint32(cg[980:], ufsCGMagic)
		copy(cg[984:], m)
		if err := fs.write(c*ufsFPG+ufsCBlkno, cg[:ufsCGSize()]); err != nil {
			return err
		}
		sums[c] = cs
		total.ndir += cs.ndir
		total.nbfree += cs.nbfree
		total.nifree += cs.nifree
		total.nffree += cs.nffree
	}
	// The summary area.
	sa := make([]byte, cssize*ufsFSize)
	for c, s := range sums {
		be.PutUint32(sa[16*c:], uint32(s.ndir))
		be.PutUint32(sa[16*c+4:], uint32(s.nbfree))
		be.PutUint32(sa[16*c+8:], uint32(s.nifree))
		be.PutUint32(sa[16*c+12:], uint32(s.nffree))
	}
	if err := fs.write(ufsDBlkno, sa); err != nil {
		return err
	}

	// The superblock.
	sb := make([]byte, 2048)
	put := func(off int, v uint32) { be.PutUint32(sb[off:], v) }
	put(8, ufsSBlkno)
	put(12, ufsCBlkno)
	put(16, ufsIBlkno)
	put(20, ufsDBlkno)
	put(24, 0)          // cgoffset
	put(28, 0xFFFFFFFF) // cgmask
	put(32, fs.now)
	put(36, uint32(fs.ncg*ufsFPG))
	put(40, uint32(dsize))
	put(44, uint32(fs.ncg))
	put(48, ufsBSize)
	put(52, ufsFSize)
	put(56, ufsFrag)
	put(60, 10) // minfree
	put(64, 0)  // rotdelay
	put(68, 60) // rps
	put(72, ^uint32(ufsBSize-1))
	put(76, ^uint32(ufsFSize-1))
	put(80, 13)
	put(84, 10)
	put(88, 1)    // maxcontig
	put(92, 2048) // maxbpg
	put(96, 3)    // fragshift
	put(100, 1)   // fsbtodb
	put(104, 2048)
	put(108, ^uint32(ufsBSize/16-1)) // csmask
	put(112, 9)                      // csshift
	put(116, ufsNIndir)
	put(120, ufsINOPB)
	put(124, 2) // nspf
	put(128, 0) // optim: time
	put(132, ufsFSOkay-fs.now)
	put(144, fs.now)
	put(148, uint32(fs.ncg))
	put(152, ufsDBlkno) // csaddr
	put(156, uint32(cssize*ufsFSize))
	put(160, uint32(ufsCGSize()))
	put(164, ufsNTrak)
	put(168, ufsNSect)
	put(172, ufsSPC)
	put(176, uint32(fs.ncg*ufsCPG))
	put(180, ufsCPG)
	put(184, ufsIPG)
	put(188, ufsFPG)
	put(192, uint32(total.ndir))
	put(196, uint32(total.nbfree))
	put(200, uint32(total.nifree))
	put(204, uint32(total.nffree))
	copy(sb[212:], mountedOn)
	put(856, 0) // cpc: no rotational layout
	for i := 0; i < 32*ufsNRPOS; i++ {
		be.PutUint16(sb[860+2*i:], 0xFFFF)
	}
	put(1372, ufsMagic)
	if _, err := fs.w.WriteAt(sb, fs.base+8192); err != nil {
		return err
	}
	for c := 0; c < fs.ncg; c++ {
		if err := fs.write(c*ufsFPG+ufsSBlkno, sb); err != nil {
			return err
		}
	}
	return nil
}

// ufsCGSize is the cylinder group header's size, in whole fragments.
func ufsCGSize() int { return (984 + ufsFPG/8 + ufsFSize - 1) / ufsFSize * ufsFSize }

// fsWriter adaptors: what fillFS needs of a filesystem.

func (fs *ufs) inodeFull() bool                        { return fs.full() }
func (fs *ufs) linkName(dir int, name string, ino int) { fs.link(dir, name, ino) }
func (fs *ufs) setAttrs(ino int, mode, uid, gid uint16, mtime uint32) {
	in := fs.inodes[ino]
	in.mode, in.uid, in.gid, in.mtime, in.atime = mode, uid, gid, mtime, mtime
}
func (fs *ufs) setDevice(ino int, rdev uint32) {
	in := fs.inodes[ino]
	in.db[0] = rdev                      // the old 16-bit number
	in.db[1] = (rdev>>8)<<18 | rdev&0xFF // the expanded one
}
