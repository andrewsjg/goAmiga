package amiga

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadDiskImage reads a floppy image: a plain ADF, a zip archive holding
// one (the first .adf in it is used, as game collections are distributed),
// or a gzip-compressed ADF (.adz, .adf.gz). saveable reports whether
// writes can go back to the file: only a plain ADF can; a compressed disk
// keeps what the Amiga writes in memory only.
//
// IPF (SPS/CAPS) images, which record a disk's raw magnetic layout to
// preserve copy protection, are recognised and refused with an explanation.
func ReadDiskImage(path string) (data []byte, saveable bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	switch {
	case bytes.HasPrefix(raw, []byte("PK\x03\x04")):
		data, err = adfFromZip(raw)
		return data, false, wrapImageErr(path, err)
	case bytes.HasPrefix(raw, []byte{0x1F, 0x8B}):
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, false, wrapImageErr(path, err)
		}
		data, err = io.ReadAll(io.LimitReader(zr, 4*ADFSize))
		if err == nil && len(data) != ADFSize {
			err = fmt.Errorf("uncompresses to %d bytes, not an ADF (%d)", len(data), ADFSize)
		}
		return data, false, wrapImageErr(path, err)
	case bytes.HasPrefix(raw, []byte("CAPS")):
		return nil, false, wrapImageErr(path, errIPF)
	}
	if len(raw) != ADFSize {
		return nil, false, fmt.Errorf("%s: %d bytes, not an ADF (%d)", filepath.Base(path), len(raw), ADFSize)
	}
	return raw, true, nil
}

var errIPF = errors.New("an IPF (SPS/CAPS) image, a raw copy-protected disk format goAmiga can't read yet; use an ADF version")

func wrapImageErr(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), err)
}

// adfFromZip returns the first ADF in a zip archive.
func adfFromZip(raw []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var names []string
	ipf := false
	for _, f := range zr.File {
		names = append(names, f.Name)
		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".ipf") {
			ipf = true
		}
		if !strings.HasSuffix(lower, ".adf") || f.UncompressedSize64 != ADFSize {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, ADFSize))
	}
	if ipf {
		return nil, errIPF
	}
	return nil, fmt.Errorf("no ADF in the archive (it holds %s)", strings.Join(names, ", "))
}
