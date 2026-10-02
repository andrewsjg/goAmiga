package amiga

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DiskImage is a floppy image: an ADF (sector data) or an IPF (raw cells,
// copy protection included).
type DiskImage struct {
	ADF []byte
	IPF *IPFImage
	// Saveable: writes can go back to the file (only a plain ADF).
	Saveable bool
}

// ReadDiskImage reads a floppy image: a plain ADF; an IPF; a zip archive
// holding either, as game collections are distributed; or a
// gzip-compressed ADF (.adz, .adf.gz). A compressed ADF keeps what the
// Amiga writes in memory only; an IPF is read-only.
//
// From a zip with several disks it reads the boot disk (see bootDiskName),
// an ADF before an IPF; "game.zip#Disk2.adf" names a particular one (see
// DiskImagesIn).
func ReadDiskImage(path string) (*DiskImage, error) {
	file, member := splitZipMember(path)
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var img *DiskImage
	if member != "" {
		img, err = imageFromZipMember(raw, member)
	} else {
		img, err = diskImageFrom(raw)
	}
	if err != nil {
		return nil, wrapImageErr(path, err)
	}
	if img.ADF != nil && bytes.Equal(img.ADF, raw) {
		img.Saveable = true
	}
	return img, nil
}

func diskImageFrom(raw []byte) (*DiskImage, error) {
	switch {
	case bytes.HasPrefix(raw, []byte("PK\x03\x04")):
		return imageFromZip(raw)
	case bytes.HasPrefix(raw, []byte{0x1F, 0x8B}):
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(zr, 4*ADFSize))
		if err != nil {
			return nil, err
		}
		if len(data) != ADFSize {
			return nil, fmt.Errorf("uncompresses to %d bytes, not an ADF (%d)", len(data), ADFSize)
		}
		return &DiskImage{ADF: data}, nil
	case bytes.HasPrefix(raw, []byte("CAPS")):
		ipf, err := ParseIPF(raw)
		if err != nil {
			return nil, err
		}
		return &DiskImage{IPF: ipf}, nil
	}
	if len(raw) != ADFSize {
		return nil, fmt.Errorf("%d bytes, not an ADF (%d) or IPF", len(raw), ADFSize)
	}
	return &DiskImage{ADF: raw}, nil
}

func wrapImageErr(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), err)
}

// zipDisks lists the disk images in a zip archive, the boot disk first and
// the rest in name order: the ADFs, or if there are none the IPFs.
func zipDisks(zr *zip.Reader) []*zip.File {
	var adfs, ipfs []*zip.File
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		switch {
		case strings.HasSuffix(lower, ".ipf"):
			ipfs = append(ipfs, f)
		case strings.HasSuffix(lower, ".adf") && f.UncompressedSize64 == ADFSize:
			adfs = append(adfs, f)
		}
	}
	disks := adfs
	if len(disks) == 0 {
		disks = ipfs
	}
	slices.SortStableFunc(disks, func(a, b *zip.File) int {
		if c := bootDiskName(b.Name) - bootDiskName(a.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return disks
}

// imageFromZip returns the boot disk in a zip archive (see zipDisks).
func imageFromZip(raw []byte) (*DiskImage, error) {
	return imageFromZipMember(raw, "")
}

// imageFromZipMember returns the disk image called name in a zip archive,
// or with name "" its boot disk.
func imageFromZipMember(raw []byte, name string) (*DiskImage, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var f *zip.File
	if name == "" {
		if disks := zipDisks(zr); len(disks) > 0 {
			f = disks[0]
		}
	} else {
		for _, zf := range zr.File {
			if zf.Name == name {
				f = zf
			}
		}
	}
	if f == nil {
		var names []string
		for _, zf := range zr.File {
			names = append(names, zf.Name)
		}
		want := "no ADF or IPF"
		if name != "" {
			want = "no " + name
		}
		return nil, fmt.Errorf("%s in the archive (it holds %s)", want, strings.Join(names, ", "))
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, 16<<20))
	if err != nil {
		return nil, err
	}
	return diskImageFrom(data)
}

// splitZipMember splits "game.zip#Disk2.adf" into the archive and the
// member; other paths come back whole.
func splitZipMember(path string) (file, member string) {
	i := strings.LastIndex(path, "#")
	if i < 0 || !strings.HasSuffix(strings.ToLower(path[:i]), ".zip") {
		return path, ""
	}
	if _, err := os.Stat(path); err == nil {
		return path, "" // a file really called that
	}
	return path[:i], path[i+1:]
}

// DiskImagesIn expands a zip archive holding several disks into a path for
// each ("game.zip#Disk1.adf", ...), the boot disk first; any other path
// comes back alone. Drives cycle through the list to change disks.
func DiskImagesIn(path string) ([]string, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".zip") {
		return []string{path}, nil
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	disks := zipDisks(&zr.Reader)
	if len(disks) < 2 {
		return []string{path}, nil
	}
	paths := make([]string, len(disks))
	for i, f := range disks {
		paths[i] = path + "#" + f.Name
	}
	return paths, nil
}

// bootDiskName scores how much a file name in a multi-disk archive looks
// like the disk to boot: "Disk1", "Disk 1 of 3", "DiskOne", "Program",
// "Boot" score above the rest.
func bootDiskName(name string) int {
	n := strings.ToLower(filepath.Base(name))
	n = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(n)
	switch {
	case strings.Contains(n, "boot"), strings.Contains(n, "program"):
		return 3
	case strings.Contains(n, "disk1"), strings.Contains(n, "diskone"), strings.Contains(n, "disk01"), strings.Contains(n, "disk1of"):
		return 2
	case strings.Contains(n, "disk2"), strings.Contains(n, "disk3"), strings.Contains(n, "disk4"), strings.Contains(n, "disktwo"):
		return 0
	}
	return 1
}
