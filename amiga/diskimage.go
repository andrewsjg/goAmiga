package amiga

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// holding either (the first .adf, else the first .ipf, in it is used, as
// game collections are distributed); or a gzip-compressed ADF (.adz,
// .adf.gz). A compressed ADF keeps what the Amiga writes in memory only;
// an IPF is read-only.
func ReadDiskImage(path string) (*DiskImage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, err := diskImageFrom(raw)
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

// imageFromZip returns the first ADF in a zip archive, or failing that
// the first IPF.
func imageFromZip(raw []byte) (*DiskImage, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	read := func(f *zip.File, limit int64) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, limit))
	}
	var names []string
	var ipf, adf *zip.File
	for _, f := range zr.File {
		names = append(names, f.Name)
		lower := strings.ToLower(f.Name)
		switch {
		case strings.HasSuffix(lower, ".ipf") && (ipf == nil || bootDiskName(f.Name) > bootDiskName(ipf.Name)):
			ipf = f
		case strings.HasSuffix(lower, ".adf") && f.UncompressedSize64 == ADFSize &&
			(adf == nil || bootDiskName(f.Name) > bootDiskName(adf.Name)):
			adf = f
		}
	}
	if adf != nil {
		data, err := read(adf, ADFSize)
		if err != nil {
			return nil, err
		}
		return &DiskImage{ADF: data}, nil
	}
	if ipf != nil {
		data, err := read(ipf, 16<<20)
		if err != nil {
			return nil, err
		}
		p, err := ParseIPF(data)
		if err != nil {
			return nil, err
		}
		return &DiskImage{IPF: p}, nil
	}
	return nil, fmt.Errorf("no ADF or IPF in the archive (it holds %s)", strings.Join(names, ", "))
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
