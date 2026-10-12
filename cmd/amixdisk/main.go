// Command amixdisk builds a hard disk image with Amiga Unix (AMIX 2.1)
// installed, from the distribution's cpio archive, as the installer would
// have left it:
//
//   - A Rigid Disk Block (1 head, 64 sectors: 32 KB cylinders) with three
//     partitions, in the order AMIX numbers its slices: 1, the root
//     filesystem (/dev/dsk/c6d0s1); 2, swap; 3, the boot partition.
//   - The root: an s5 filesystem holding every file in the archive, with
//     /etc/vfstab naming c6d0s1, the root the kernel is built for (SCSI ID
//     6, the A3000's internal drive).
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
	rootMB := flag.Int("root", 400, "size of the root filesystem in MB")
	swapMB := flag.Int("swap", 32, "size of the swap partition in MB")
	extraInodes := flag.Int("inodes", 8000, "free inodes to leave in the root filesystem")
	rootSlice := flag.String("rootdev", "c6d0s1", "root device for /etc/vfstab")
	flag.Parse()
	if err := build(*cpioPath, *out, *rootMB, *swapMB, *extraInodes, *rootSlice); err != nil {
		fmt.Fprintln(os.Stderr, "amixdisk:", err)
		os.Exit(1)
	}
}

func build(cpioPath, out string, rootMB, swapMB, extraInodes int, rootDev string) error {
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
	parts := []partition{
		{name: "root", low: 1, high: rootCyls, dosType: 0x554E4901, noMount: true},
		{name: "swap", low: 1 + rootCyls, high: rootCyls + swapCyls, dosType: 0x554E4902, noMount: true},
		{name: "boot", low: 1 + rootCyls + swapCyls, high: rootCyls + swapCyls + bootCyls, dosType: 0x444F5300, bootable: true, bootBlocks: 2},
	}
	totalCyls := parts[2].high + 1

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
	fs, err := newS5(img, int64(parts[0].low)*cylBlocks*blockSize, rootBlocks, min(entries+extraInodes, 65535))
	if err != nil {
		return err
	}
	if err := fillFS(fs, newCPIOReader(f), rootDev); err != nil {
		return err
	}
	if err := fs.finish("root", "amix"); err != nil {
		return err
	}
	fmt.Printf("%s: %d MB, %d cylinders of %d blocks\n", out, int64(totalCyls)*cylBlocks*blockSize>>20, totalCyls, cylBlocks)
	for i, p := range parts {
		fmt.Printf("  slice %d: %-4s cylinders %d-%d\n", i+1, p.name, p.low, p.high)
	}
	fmt.Printf("  root: %d inodes used of %d, %d of %d blocks used\n", len(fs.inodes)-1, fs.maxIno, fs.next, fs.fsize)
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

// fillFS adds every archive member to the filesystem.
func fillFS(fs *s5fs, cr *cpioReader, rootDev string) error {
	links := map[[2]uint32]int{} // (dev, ino) -> inode, for hard links
	paths := map[string]int{".": s5RootIno}
	var dirOf func(p string) (int, error)
	dirOf = func(p string) (int, error) {
		if ino, ok := paths[p]; ok {
			return ino, nil
		}
		parent, err := dirOf(path.Dir(p))
		if err != nil {
			return 0, err
		}
		ino := fs.newInode(0o040755, 0, 0, fs.now)
		fs.link(parent, path.Base(p), ino)
		paths[p] = ino
		return ino, nil
	}
	for {
		e, err := cr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := cleanName(e.Name)
		mode := uint16(e.Mode)
		typ := e.Mode & modeType
		if name == "." {
			r := fs.inodes[s5RootIno]
			r.mode, r.uid, r.gid, r.mtime, r.atime = mode, uint16(e.UID), uint16(e.GID), uint32(e.Mtime), uint32(e.Mtime)
			continue
		}
		if _, dup := paths[name]; dup && typ != modeDir {
			continue
		}
		parent, err := dirOf(path.Dir(name))
		if err != nil {
			return err
		}
		if typ == modeDir {
			ino, ok := paths[name]
			if !ok {
				ino = fs.newInode(mode, uint16(e.UID), uint16(e.GID), uint32(e.Mtime))
				fs.link(parent, path.Base(name), ino)
				paths[name] = ino
			}
			in := fs.inodes[ino]
			in.mode, in.uid, in.gid, in.mtime, in.atime = mode, uint16(e.UID), uint16(e.GID), uint32(e.Mtime), uint32(e.Mtime)
			continue
		}
		key := [2]uint32{e.Dev, e.Ino}
		if ino, ok := links[key]; ok && e.Nlink > 1 {
			fs.link(parent, path.Base(name), ino)
			paths[name] = ino
			continue
		}
		if fs.inodesFull() {
			return errors.New("out of inodes")
		}
		ino := fs.newInode(mode, uint16(e.UID), uint16(e.GID), uint32(e.Mtime))
		fs.link(parent, path.Base(name), ino)
		paths[name] = ino
		if e.Nlink > 1 {
			links[key] = ino
		}
		in := fs.inodes[ino]
		switch typ {
		case modeReg, modeLnk:
			data := e.data
			size := e.Size
			if name == "etc/vfstab" {
				b, err := io.ReadAll(e.data)
				if err != nil {
					return err
				}
				s := strings.ReplaceAll(string(b), "c0d0s1", rootDev)
				data, size = strings.NewReader(s), int64(len(s))
			}
			if err := fs.writeData(ino, data, size); err != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
		case modeChr, modeBlk:
			in.addr[1], in.addr[2], in.addr[3] = 1, e.Rdev>>8, e.Rdev&0xFF
		}
	}
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
