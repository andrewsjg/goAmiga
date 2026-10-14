// Command amixdisk builds a hard disk image with Amiga Unix (AMIX 2.1)
// installed, from the distribution's cpio archive, as the installer would
// have left it:
//
//   - A Rigid Disk Block (1 head, 64 sectors: 32 KB cylinders) with four
//     partitions, in the order AMIX numbers its slices: 1, the root
//     filesystem (/dev/dsk/c6d0s1); 2, swap; 3, the boot partition; 4, a
//     UFS filesystem.
//   - The root: an s5 filesystem holding the system, with /etc/vfstab
//     naming c6d0s1, the root the kernel is built for (SCSI ID 6, the
//     A3000's internal drive).
//   - With -net (the default), the network set up for goAmiga's A2065 and
//     its user-mode network: aen0 as 10.0.2.15 with a default route to
//     10.0.2.2 (/etc/inet/network-config, which only acts if the card is
//     there), and DNS through 10.0.2.3 (/etc/resolv.conf, resolv.so in
//     /etc/netconfig).
//   - UFS, mounted at /usr/ufs, holds the trees with names longer than
//     s5's 14 characters: /usr/src, /usr/X, /usr/x11r5 and /usr/public,
//     each left as a link to its place there (usr/src -> ufs/src). Nothing
//     in them is needed before rc2 mounts it: /etc/sysinit runs programs
//     from /usr, so the rest of /usr stays on the root. With -usr 0 there
//     is no UFS and every name is cut to 14 characters.
//   - The boot partition: AMIX's two-stage bootstrap and its kernel, made
//     as stand/Makefile's "bootpart" does (boot1, an IBLK info block for
//     boot2, boot2, an IBLK for the kernel, the kernel). Its DosEnvec sets
//     de_BootBlocks to 2, so Kickstart 2.0 and later boot it the way it
//     boots a floppy.
//
// The root and swap partitions are marked not to be mounted by AmigaOS.
//
//	amixdisk -cpio AMIX/amix_2.1.cpio -o amix.hdf
//	goamiga --cpu 68030 --fpu 68882 --rom ROMS/Kick32.rom --fastram 8192 \
//	        --hdf amix.hdf --scsi 6:amix.hdf
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	blockSize = 512
	cylBlocks = 64 // 1 head, 64 sectors
)

func main() {
	cpioPath := flag.String("cpio", "AMIX/amix_2.1.cpio", "AMIX 2.1 distribution archive (odc cpio)")
	out := flag.String("o", "amix.hdf", "hard disk image to write")
	rootMB := flag.Int("root", 150, "size of the root filesystem (s5) in MB")
	swapMB := flag.Int("swap", 32, "size of the swap partition in MB")
	usrMB := flag.Int("usr", 400, "size of the UFS filesystem for the trees with long names, in MB (whole 8 MB groups; 0 for none)")
	extraInodes := flag.Int("inodes", 8000, "free inodes to leave in the root filesystem")
	rootSlice := flag.String("rootdev", "c6d0s1", "root device for /etc/vfstab")
	flag.BoolVar(&netSetup, "net", true, "set the network up for goAmiga's A2065 (10.0.2.15, gateway 10.0.2.2, DNS 10.0.2.3)")
	flag.Parse()
	if err := build(*cpioPath, *out, *rootMB, *swapMB, *usrMB, *extraInodes, *rootSlice); err != nil {
		fmt.Fprintln(os.Stderr, "amixdisk:", err)
		os.Exit(1)
	}
}

func build(cpioPath, out string, rootMB, swapMB, usrMB, extraInodes int, rootDev string) error {
	// First pass: count the inodes and pick out the boot files.
	f, err := os.Open(cpioPath)
	if err != nil {
		return err
	}
	defer f.Close()
	boot := map[string][]byte{"stand/boot1.boot": nil, "stand/boot2.boot": nil, "stand/unix": nil}
	entries := 0
	cr := newCPIOReader(f)
	for {
		e, err := cr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if _, ok := boot[cleanName(e.Name)]; ok && e.Size > 0 {
			b, err := io.ReadAll(e.data)
			if err != nil {
				return err
			}
			boot[cleanName(e.Name)] = b
		}
	}
	for n, b := range boot {
		if b == nil {
			return fmt.Errorf("%s isn't in the archive", n)
		}
	}
	bootImage := makeBootImage(boot["stand/boot1.boot"], boot["stand/boot2.boot"], boot["stand/unix"])

	// Layout, in cylinders.
	cyls := func(bytes int64) uint32 { return uint32((bytes + cylBlocks*blockSize - 1) / (cylBlocks * blockSize)) }
	rootCyls := cyls(int64(rootMB) << 20)
	swapCyls := cyls(int64(swapMB) << 20)
	bootCyls := cyls(int64(len(bootImage))) + 2
	usrCyls := cyls(int64(usrMB) << 20 / (ufsFPG * ufsFSize) * (ufsFPG * ufsFSize))
	parts := []partition{
		{name: "root", low: 1, high: rootCyls, dosType: 0x554E4901, noMount: true},
		{name: "swap", low: 1 + rootCyls, high: rootCyls + swapCyls, dosType: 0x554E4902, noMount: true},
		{name: "boot", low: 1 + rootCyls + swapCyls, high: rootCyls + swapCyls + bootCyls, dosType: 0x444F5300, bootable: true, bootBlocks: 2},
	}
	if usrCyls > 0 {
		lo := parts[2].high + 1
		parts = append(parts, partition{name: "usr", low: lo, high: lo + usrCyls - 1, dosType: 0x554E4903, noMount: true})
	}
	totalCyls := parts[len(parts)-1].high + 1

	img, err := os.Create(out)
	if err != nil {
		return err
	}
	defer img.Close()
	if err := img.Truncate(int64(totalCyls) * cylBlocks * blockSize); err != nil {
		return err
	}
	if err := writeRDB(img, totalCyls, parts); err != nil {
		return err
	}
	if _, err := img.WriteAt(bootImage, int64(parts[2].low)*cylBlocks*blockSize); err != nil {
		return err
	}

	// Second pass: the root filesystem.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rootBlocks := (parts[0].high - parts[0].low + 1) * cylBlocks * blockSize / s5BSize
	ninodes := entries + extraInodes
	if usrCyls > 0 {
		ninodes = 12000 + extraInodes // the system without the big trees
	}
	fs, err := newS5(img, int64(parts[0].low)*cylBlocks*blockSize, rootBlocks, min(ninodes, 65535))
	if err != nil {
		return err
	}
	var u *ufs
	usrSlice := ""
	if usrCyls > 0 {
		p := parts[3]
		if u, err = newUFS(img, int64(p.low)*cylBlocks*blockSize, int64(usrCyls)*cylBlocks*blockSize); err != nil {
			return err
		}
		usrSlice = rootDev[:len(rootDev)-1] + "4"
	}
	if err := fillFS(fs, u, newCPIOReader(f), rootDev, usrSlice); err != nil {
		return err
	}
	if err := fs.finish("root", "amix"); err != nil {
		return err
	}
	if u != nil {
		if err := u.finish("/usr/ufs"); err != nil {
			return err
		}
	}
	fmt.Printf("%s: %d MB, %d cylinders of %d blocks\n", out, int64(totalCyls)*cylBlocks*blockSize>>20, totalCyls, cylBlocks)
	for i, p := range parts {
		fmt.Printf("  slice %d: %-4s cylinders %d-%d\n", i+1, p.name, p.low, p.high)
	}
	fmt.Printf("  root (s5): %d inodes used of %d, %d of %d blocks used\n", len(fs.inodes)-1, fs.maxIno, fs.next, fs.fsize)
	if u != nil {
		fmt.Printf("  /usr/ufs (UFS): %d inodes used of %d, %d cylinder groups\n", len(u.inodes)-1, u.ncg*ufsIPG, u.ncg)
	}
	if len(fs.renamed) > 0 {
		fmt.Printf("  %d names cut to s5's 14 characters:\n", len(fs.renamed))
		for _, r := range fs.renamed {
			fmt.Println("    " + r)
		}
	}
	return nil
}

func cleanName(n string) string {
	n = strings.TrimPrefix(n, "./")
	n = strings.TrimPrefix(n, "/")
	if n == "" {
		return "."
	}
	return path.Clean(n)
}

// fsWriter is what fillFS needs of a filesystem (s5 or UFS).
type fsWriter interface {
	newInode(mode, uid, gid uint16, mtime uint32) int
	linkName(dir int, name string, ino int)
	writeData(ino int, r io.Reader, size int64) error
	setAttrs(ino int, mode, uid, gid uint16, mtime uint32)
	setDevice(ino int, rdev uint32)
	inodeFull() bool
}

// tree tracks a filesystem's paths and hard links while it fills.
type tree struct {
	fs    fsWriter
	now   uint32
	paths map[string]int
	links map[[2]uint32]int
}

func newTree(fs fsWriter, now uint32) *tree {
	return &tree{fs: fs, now: now, paths: map[string]int{".": 2}, links: map[[2]uint32]int{}}
}

// dir returns directory p's inode, making it (and its parents) if need be.
func (t *tree) dir(p string) int {
	if ino, ok := t.paths[p]; ok {
		return ino
	}
	parent := t.dir(path.Dir(p))
	ino := t.fs.newInode(0o040755, 0, 0, t.now)
	t.fs.linkName(parent, path.Base(p), ino)
	t.paths[p] = ino
	return ino
}

// ufsTrees are the archive's trees with names over 14 characters, which
// go on UFS when there is one.
var ufsTrees = []string{"usr/src", "usr/X", "usr/x11r5", "usr/public"}

// fillFS adds every archive member to the s5 root, or to u (if not nil)
// under ufsTrees.
func fillFS(s5 *s5fs, u *ufs, cr *cpioReader, rootDev, usrDev string) error {
	edits := fileEdits(rootDev, usrDev)
	root := newTree(s5, s5.now)
	var ut *tree
	if u != nil {
		ut = newTree(u, u.now)
		root.dir("usr/ufs") // the mount point
		for _, p := range ufsTrees {
			ut.dir(strings.TrimPrefix(p, "usr/"))
			link := "ufs/" + strings.TrimPrefix(p, "usr/")
			ino := s5.newInode(0o120777, 0, 0, s5.now)
			s5.linkName(root.dir("usr"), path.Base(p), ino)
			if err := s5.writeData(ino, strings.NewReader(link), int64(len(link))); err != nil {
				return err
			}
			root.paths[p] = ino
		}
	}
	for {
		e, err := cr.Next()
		if err == io.EOF {
			return addFiles(root)
		}
		if err != nil {
			return err
		}
		name := cleanName(e.Name)
		mode, uid, gid, mtime := uint16(e.Mode), uint16(e.UID), uint16(e.GID), uint32(e.Mtime)
		typ := e.Mode & modeType
		t := root
		if ut != nil {
			for _, p := range ufsTrees {
				if name == p || strings.HasPrefix(name, p+"/") {
					t, name = ut, strings.TrimPrefix(name, "usr/")
					break
				}
			}
		}
		if name == "." {
			t.fs.setAttrs(2, mode, uid, gid, mtime)
			continue
		}
		if typ == modeDir {
			ino, ok := t.paths[name]
			if !ok {
				ino = t.fs.newInode(mode, uid, gid, mtime)
				t.fs.linkName(t.dir(path.Dir(name)), path.Base(name), ino)
				t.paths[name] = ino
			}
			t.fs.setAttrs(ino, mode, uid, gid, mtime)
			continue
		}
		if _, dup := t.paths[name]; dup {
			continue
		}
		parent := t.dir(path.Dir(name))
		key := [2]uint32{e.Dev, e.Ino}
		if ino, ok := t.links[key]; ok && e.Nlink > 1 {
			t.fs.linkName(parent, path.Base(name), ino)
			t.paths[name] = ino
			continue
		}
		if t.fs.inodeFull() {
			return errors.New("out of inodes")
		}
		ino := t.fs.newInode(mode, uid, gid, mtime)
		t.fs.linkName(parent, path.Base(name), ino)
		t.paths[name] = ino
		if e.Nlink > 1 {
			t.links[key] = ino
		}
		switch typ {
		case modeReg, modeLnk:
			data, size := e.data, e.Size
			if edit := edits[name]; t == root && edit != nil {
				b, err := io.ReadAll(e.data)
				if err != nil {
					return err
				}
				v := edit(string(b))
				data, size = strings.NewReader(v), int64(len(v))
			}
			if err := t.fs.writeData(ino, data, size); err != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
		case modeChr, modeBlk:
			t.fs.setDevice(ino, e.Rdev)
		}
	}
}

// netSetup is -net.
var netSetup = true

// fileEdits are the changes to the archive's files, by path.
func fileEdits(rootDev, usrDev string) map[string]func(string) string {
	e := map[string]func(string) string{
		"etc/vfstab": func(v string) string {
			v = strings.ReplaceAll(v, "c0d0s1", rootDev)
			if usrDev != "" {
				v += fmt.Sprintf("/dev/dsk/%s\t/dev/rdsk/%s\t/usr/ufs\tufs\t2\tyes\t-\n", usrDev, usrDev)
			}
			return v
		},
	}
	if netSetup {
		e["etc/inet/network-config"] = func(v string) string {
			return strings.Replace(v, "/usr/sbin/ifconfig aen0 `uname -n` up -trailers",
				"/usr/sbin/ifconfig aen0 10.0.2.15 netmask 255.255.255.0 up -trailers &&\n"+
					"\t    /usr/sbin/route add default 10.0.2.2 1", 1)
		}
		e["etc/netconfig"] = func(v string) string {
			return strings.ReplaceAll(v, "/usr/lib/tcpip.so\n", "/usr/lib/tcpip.so,/usr/lib/resolv.so\n")
		}
	}
	return e
}

// addFiles adds files the archive doesn't have.
func addFiles(root *tree) error {
	if !netSetup {
		return nil
	}
	files := map[string]string{"etc/resolv.conf": "nameserver 10.0.2.3\n"}
	for name, v := range files {
		if _, ok := root.paths[name]; ok {
			continue
		}
		ino := root.fs.newInode(0o100644, 0, 3, root.now) // root:sys
		root.fs.linkName(root.dir(path.Dir(name)), path.Base(name), ino)
		root.paths[name] = ino
		if err := root.fs.writeData(ino, strings.NewReader(v), int64(len(v))); err != nil {
			return err
		}
	}
	return nil
}

// ---- The boot partition ----

// makeBootImage lays out boot1, boot2 and the kernel with their IBLK
// info blocks, as stand/Makefile does.
func makeBootImage(boot1, boot2, kernel []byte) []byte {
	var b []byte
	b = append(b, boot1...)
	b = append(b, iblk(boot2)...)
	b = append(b, boot2...)
	b = append(b, iblk(kernel)...)
	b = append(b, kernel...)
	return b
}

// iblk is makeiblk's 512-byte info block: "IBLK", the size at 8, the
// uncompressed size at 12 (0: not compressed), the SVR4 sum checksum at
// 16 and the preferred load address at 20 ($FFFFFFFF: the boot code
// picks the largest block of non-chip memory).
func iblk(data []byte) []byte {
	b := make([]byte, 512)
	copy(b, "IBLK")
	be := binary.BigEndian
	be.PutUint32(b[8:], uint32(len(data)))
	be.PutUint32(b[16:], svr4Sum(data))
	be.PutUint32(b[20:], 0xFFFFFFFF)
	return b
}

// svr4Sum is the checksum of "sum" (and makeiblk): the byte sum folded
// twice to 16 bits.
func svr4Sum(data []byte) uint32 {
	var s uint32
	for _, c := range data {
		s += uint32(c)
	}
	s = s>>16 + s&0xFFFF
	return s>>16 + s&0xFFFF
}

// ---- The Rigid Disk Block ----

type partition struct {
	name              string
	low, high         uint32
	dosType           uint32
	bootable, noMount bool
	bootBlocks        uint32
}

func writeRDB(w io.WriterAt, cyls uint32, parts []partition) error {
	be := binary.BigEndian
	blk := func() []byte { return make([]byte, blockSize) }
	sum := func(b []byte) {
		be.PutUint32(b[8:], 0)
		var s uint32
		n := int(be.Uint32(b[4:]))
		for i := 0; i < n; i++ {
			s += be.Uint32(b[4*i:])
		}
		be.PutUint32(b[8:], -s)
	}
	r := blk()
	copy(r, "RDSK")
	be.PutUint32(r[4:], 64)
	be.PutUint32(r[12:], 7) // host ID
	be.PutUint32(r[16:], blockSize)
	for _, off := range []int{24, 32, 36} { // bad blocks, filesystems, drive init: none
		be.PutUint32(r[off:], 0xFFFFFFFF)
	}
	be.PutUint32(r[28:], 1) // first PART
	for off := 40; off < 64; off += 4 {
		be.PutUint32(r[off:], 0xFFFFFFFF)
	}
	be.PutUint32(r[64:], cyls)
	be.PutUint32(r[68:], cylBlocks) // sectors
	be.PutUint32(r[72:], 1)         // heads
	be.PutUint32(r[76:], 1)         // interleave
	be.PutUint32(r[80:], cyls)      // park
	be.PutUint32(r[96:], cyls)      // write precompensation
	be.PutUint32(r[100:], cyls)     // reduced write
	be.PutUint32(r[104:], 3)        // step rate
	be.PutUint32(r[128:], 0)
	be.PutUint32(r[132:], cylBlocks-1) // RDB blocks
	be.PutUint32(r[136:], 1)           // first cylinder for partitions
	be.PutUint32(r[140:], cyls-1)
	be.PutUint32(r[144:], cylBlocks) // rdb_CylBlocks: AMIX's slices use it
	be.PutUint32(r[152:], uint32(len(parts)))
	copy(r[160:], "GOAMIGA ")
	copy(r[168:], "AMIX DISK       ")
	copy(r[184:], "2.1 ")
	sum(r)
	if _, err := w.WriteAt(r, 0); err != nil {
		return err
	}
	for i, p := range parts {
		b := blk()
		copy(b, "PART")
		be.PutUint32(b[4:], 64)
		be.PutUint32(b[12:], 7)
		next := uint32(0xFFFFFFFF)
		if i+1 < len(parts) {
			next = uint32(2 + i)
		}
		be.PutUint32(b[16:], next)
		flags := uint32(0)
		if p.bootable {
			flags |= 1
		}
		if p.noMount {
			flags |= 2
		}
		be.PutUint32(b[20:], flags)
		b[36] = byte(len(p.name))
		copy(b[37:], p.name)
		env := []uint32{19, 128, 0, 1, 1, cylBlocks, 2, 0, 0, p.low, p.high, 30, 0, 0x7FFFFFFF, 0xFFFFFFFE,
			0, p.dosType, 0, 0, p.bootBlocks}
		for j, v := range env {
			be.PutUint32(b[128+4*j:], v)
		}
		sum(b)
		if _, err := w.WriteAt(b, int64(1+i)*blockSize); err != nil {
			return err
		}
	}
	return nil
}
