package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// odc writes one cpio member.
func odc(b *bytes.Buffer, name string, ino, mode, nlink, rdev uint32, data string) {
	fmt.Fprintf(b, "070707%06o%06o%06o%06o%06o%06o%06o%011o%06o%011o", 1, ino, mode, 0, 0, nlink, rdev, 700000000, len(name)+1, len(data))
	b.WriteString(name + "\x00")
	b.WriteString(data)
}

// A small archive goes onto the disk and reads back from the s5
// filesystem with the right contents, links, devices and names.
func TestBuildDisk(t *testing.T) {
	var b bytes.Buffer
	odc(&b, ".", 1, modeDir|0o755, 2, 0, "")
	odc(&b, "stand", 2, modeDir|0o755, 2, 0, "")
	odc(&b, "stand/boot1.boot", 3, modeReg|0o644, 1, 0, strings.Repeat("1", 1024))
	odc(&b, "stand/boot2.boot", 4, modeReg|0o644, 1, 0, strings.Repeat("2", 8192))
	big := strings.Repeat("unix kernel ", 16000) // 192 KB: an indirect block
	odc(&b, "stand/unix", 5, modeReg|0o644, 2, 0, big)
	odc(&b, "stand/2.1_unix", 5, modeReg|0o644, 2, 0, big)
	odc(&b, "etc/vfstab", 6, modeReg|0o644, 1, 0, "/dev/dsk/c0d0s1\t/dev/rdsk/c0d0s1\t/\ts5\n")
	odc(&b, "dev/dsk/c6d0s1", 7, modeBlk|0o600, 1, 18<<8|22, "")
	odc(&b, "bin", 8, modeLnk|0o777, 1, 0, "/usr/bin")
	odc(&b, "usr/a_very_long_file_name.c", 9, modeReg|0o644, 1, 0, "long")
	odc(&b, "TRAILER!!!", 0, 0, 1, 0, "")
	dir := t.TempDir()
	cp := filepath.Join(dir, "a.cpio")
	os.WriteFile(cp, b.Bytes(), 0o644)
	out := filepath.Join(dir, "amix.hdf")
	if err := build(cp, out, 2, 1, 0, 100, "c6d0s1"); err != nil {
		t.Fatal(err)
	}
	img, _ := os.ReadFile(out)
	be := binary.BigEndian
	if string(img[:4]) != "RDSK" {
		t.Fatal("no RDB")
	}
	base := int(be.Uint32(img[512+128+36:])) * cylBlocks * blockSize // slice 1
	fs := func(blk uint32) []byte { return img[base+int(blk)*1024 : base+int(blk)*1024+1024] }
	inode := func(n int) []byte { o := base + 2048 + (n-1)*64; return img[o : o+64] }
	addr := func(in []byte, i int) uint32 {
		return uint32(in[12+3*i])<<16 | uint32(in[13+3*i])<<8 | uint32(in[14+3*i])
	}
	read := func(n int) []byte {
		in := inode(n)
		size := int(be.Uint32(in[8:]))
		var data []byte
		for i := 0; len(data) < size; i++ {
			var blk uint32
			if i < 10 {
				blk = addr(in, i)
			} else {
				blk = be.Uint32(fs(addr(in, 10))[4*(i-10):])
			}
			data = append(data, fs(blk)...)
		}
		return data[:size]
	}
	lookup := func(dir int, name string) int {
		d := read(dir)
		for i := 0; i < len(d); i += 16 {
			if strings.TrimRight(string(d[i+2:i+16]), "\x00") == name {
				return int(be.Uint16(d[i:]))
			}
		}
		t.Fatalf("%s not found", name)
		return 0
	}
	if be.Uint32(img[base+512+504:]) != s5Magic {
		t.Fatal("no s5 superblock")
	}
	stand := lookup(2, "stand")
	u := lookup(stand, "unix")
	if lookup(stand, "2.1_unix") != u || be.Uint16(inode(u)[2:]) != 2 {
		t.Error("the hard link should share an inode with 2 links")
	}
	if string(read(u)) != big {
		t.Error("the kernel reads back wrong")
	}
	if v := string(read(lookup(lookup(2, "etc"), "vfstab"))); !strings.Contains(v, "c6d0s1") || strings.Contains(v, "c0d0") {
		t.Errorf("vfstab: %q", v)
	}
	dev := inode(lookup(lookup(lookup(2, "dev"), "dsk"), "c6d0s1"))
	if addr(dev, 1) != 1 || addr(dev, 2) != 18 || addr(dev, 3) != 22 {
		t.Errorf("device numbers %d %d %d", addr(dev, 1), addr(dev, 2), addr(dev, 3))
	}
	if string(read(lookup(2, "bin"))) != "/usr/bin" {
		t.Error("symbolic link target")
	}
	lookup(lookup(2, "usr"), "a_very_long_fi")
	// The boot partition starts with boot1 and an IBLK for boot2.
	bootBase := int(be.Uint32(img[3*512+128+36:])) * cylBlocks * blockSize
	if img[bootBase] != '1' || string(img[bootBase+1024:bootBase+1028]) != "IBLK" ||
		be.Uint32(img[bootBase+1024+8:]) != 8192 {
		t.Error("boot partition layout")
	}
}

// With a UFS partition, the long-named trees go there and keep their
// names, and the root has links to them.
func TestBuildDiskUFS(t *testing.T) {
	var b bytes.Buffer
	odc(&b, ".", 1, modeDir|0o755, 2, 0, "")
	odc(&b, "stand/boot1.boot", 3, modeReg|0o644, 1, 0, strings.Repeat("1", 1024))
	odc(&b, "stand/boot2.boot", 4, modeReg|0o644, 1, 0, strings.Repeat("2", 8192))
	odc(&b, "stand/unix", 5, modeReg|0o644, 1, 0, "kernel")
	odc(&b, "etc/vfstab", 6, modeReg|0o644, 1, 0, "/dev/dsk/c0d0s1\t/dev/rdsk/c0d0s1\t/\ts5\n")
	long := strings.Repeat("long file contents ", 1000) // 19 KB: 2 blocks and a fragment
	odc(&b, "usr/src/X/contrib/games/dragon/ic/rdragon_28x32.icon", 7, modeReg|0o644, 1, 0, long)
	odc(&b, "usr/src/short", 8, modeReg|0o644, 1, 0, "hi")
	odc(&b, "TRAILER!!!", 0, 0, 1, 0, "")
	dir := t.TempDir()
	cp := filepath.Join(dir, "a.cpio")
	os.WriteFile(cp, b.Bytes(), 0o644)
	out := filepath.Join(dir, "amix.hdf")
	if err := build(cp, out, 2, 1, 8, 100, "c6d0s1"); err != nil {
		t.Fatal(err)
	}
	img, _ := os.ReadFile(out)
	be := binary.BigEndian
	base := int(be.Uint32(img[4*512+128+36:])) * cylBlocks * blockSize // slice 4
	sb := img[base+8192:]
	if be.Uint32(sb[1372:]) != ufsMagic || be.Uint32(sb[132:])+be.Uint32(sb[32:]) != ufsFSOkay {
		t.Fatal("no clean UFS superblock")
	}
	frag := func(f uint32) []byte { return img[base+int(f)*ufsFSize:] }
	inode := func(ino int) []byte {
		cg := ino / ufsIPG
		return frag(uint32(cg*ufsFPG + ufsIBlkno))[(ino%ufsIPG)*128:]
	}
	read := func(ino int) []byte {
		in := inode(ino)
		size := int(be.Uint32(in[12:]))
		var data []byte
		for i := 0; len(data) < size; i++ {
			data = append(data, frag(be.Uint32(in[40+4*i:]))[:ufsBSize]...)
		}
		return data[:size]
	}
	lookup := func(dir int, name string) int {
		d := read(dir)
		for off := 0; off < len(d); {
			ino, rl, nl := be.Uint32(d[off:]), int(be.Uint16(d[off+4:])), int(be.Uint16(d[off+6:]))
			if ino != 0 && string(d[off+8:off+8+nl]) == name {
				return int(ino)
			}
			off += rl
		}
		t.Fatalf("%s not found", name)
		return 0
	}
	ic := lookup(lookup(lookup(lookup(lookup(lookup(2, "src"), "X"), "contrib"), "games"), "dragon"), "ic")
	f := lookup(ic, "rdragon_28x32.icon")
	if string(read(f)) != long {
		t.Error("the long-named file reads back wrong")
	}
	if blocks := be.Uint32(inode(f)[104:]); blocks != (2*8+3)*2 {
		t.Errorf("di_blocks %d, want %d (two blocks and three fragments)", blocks, (2*8+3)*2)
	}
	lookup(2, "lost+found")
	// The root's links and vfstab.
	rbase := int(be.Uint32(img[512+128+36:])) * cylBlocks * blockSize
	_ = rbase
	vfstab := string(img) // crude: the text is somewhere in the image
	if !strings.Contains(vfstab, "/dev/dsk/c6d0s4\t/dev/rdsk/c6d0s4\t/usr/ufs\tufs") {
		t.Error("vfstab has no /usr/ufs entry")
	}
	if !strings.Contains(vfstab, "ufs/src") {
		t.Error("no usr/src -> ufs/src link")
	}
}
