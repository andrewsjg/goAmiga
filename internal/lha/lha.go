// Package lha reads LhA archives, the Amiga's usual archive format (WHDLoad
// games come in them): header levels 0, 1 and 2, and the methods -lh0-
// (stored), -lh5-, -lh6- and -lh7- (LZSS with static Huffman coding, an
// 8, 32 or 64 KB window) and -lhd- (a directory).
package lha

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// File is one entry of an archive.
type File struct {
	Name     string // slash-separated path
	Method   string // "-lh5-" ...
	Size     int64  // unpacked
	Packed   int64
	Modified time.Time
	CRC      uint16
	Dir      bool

	data []byte // packed data
}

// Read reads every entry of an archive.
func Read(r io.Reader) ([]*File, error) {
	br := bufio.NewReader(r)
	var files []*File
	for {
		f, err := readHeader(br)
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		f.data = make([]byte, f.Packed)
		if _, err := io.ReadFull(br, f.data); err != nil {
			return files, fmt.Errorf("lha: %s: %w", f.Name, err)
		}
		files = append(files, f)
	}
}

// readHeader reads an entry's header; io.EOF at the end of the archive
// (end of file, or a zero byte).
func readHeader(r *bufio.Reader) (*File, error) {
	first, err := r.Peek(1)
	if err != nil || first[0] == 0 {
		return nil, io.EOF
	}
	base := make([]byte, 22)
	if _, err := io.ReadFull(r, base); err != nil {
		return nil, fmt.Errorf("lha: short header: %w", err)
	}
	le16 := binary.LittleEndian.Uint16
	le32 := binary.LittleEndian.Uint32
	f := &File{Method: string(base[2:7])}
	if f.Method[0] != '-' || f.Method[4] != '-' {
		return nil, fmt.Errorf("lha: not an LhA archive (method %q)", f.Method)
	}
	f.Packed = int64(le32(base[7:]))
	f.Size = int64(le32(base[11:]))
	level := base[20]
	var dir string
	switch level {
	case 0, 1:
		size := int(base[0]) + 2 // the whole base header
		if size < 24 {
			return nil, errors.New("lha: bad header")
		}
		rest := make([]byte, size-22)
		if _, err := io.ReadFull(r, rest); err != nil {
			return nil, err
		}
		f.Modified = dosTime(le32(base[15:]))
		n := int(base[21])
		if n > len(rest) {
			return nil, errors.New("lha: bad header")
		}
		f.Name = string(rest[:n])
		if len(rest) >= n+2 {
			f.CRC = le16(rest[n:])
		}
		if level == 1 {
			// Extended headers follow, counted in the packed size.
			next := int(le16(rest[len(rest)-2:]))
			for next > 0 {
				if next < 3 {
					return nil, errors.New("lha: bad extended header")
				}
				ext := make([]byte, next)
				if _, err := io.ReadFull(r, ext); err != nil {
					return nil, err
				}
				f.Packed -= int64(next)
				dir = extHeader(f, ext, dir)
				next = int(le16(ext[len(ext)-2:]))
			}
		}
	case 2:
		total := int(le16(base[0:]))
		if total < 26 {
			return nil, errors.New("lha: bad header")
		}
		rest := make([]byte, total-22)
		if _, err := io.ReadFull(r, rest); err != nil {
			return nil, err
		}
		f.Modified = time.Unix(int64(le32(base[15:])), 0)
		f.CRC = uint16(base[21]) | uint16(rest[0])<<8 // bytes 21-22; the OS id at 23
		// Extended headers from offset 24: each is [type, data..., next size].
		p := 24 - 22
		next := int(le16(rest[p:]))
		p += 2
		for next > 0 && p+next <= len(rest) {
			ext := rest[p : p+next]
			dir = extHeader(f, ext, dir)
			p += next
			next = int(le16(ext[len(ext)-2:]))
		}
	default:
		return nil, fmt.Errorf("lha: header level %d", level)
	}
	f.Name = cleanName(dir, f.Name)
	f.Dir = f.Method == "-lhd-"
	return f, nil
}

// extHeader applies an extended header (type, data, next size) and
// returns the directory it names, if any.
func extHeader(f *File, ext []byte, dir string) string {
	if len(ext) < 3 {
		return dir
	}
	data := ext[1 : len(ext)-2]
	switch ext[0] {
	case 0x00: // header CRC: not checked
	case 0x01:
		f.Name = string(data)
	case 0x02:
		dir = strings.ReplaceAll(string(data), "\xff", "/")
	}
	return dir
}

// cleanName makes an entry's path: Amiga archives may put a comment after
// a NUL, separate directories with \, / or $FF, and name files in
// ISO-8859-1, which becomes UTF-8.
func cleanName(dir, name string) string {
	if i := strings.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	p := strings.NewReplacer("\\", "/", "\xff", "/").Replace(path.Join(dir, name))
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		b.WriteRune(rune(p[i]))
	}
	return strings.TrimPrefix(path.Clean("/"+b.String()), "/")
}

// dosTime decodes an MS-DOS date and time.
func dosTime(v uint32) time.Time {
	return time.Date(int(v>>25)+1980, time.Month(v>>21&15), int(v>>16&31),
		int(v>>11&31), int(v>>5&63), int(v&31)*2, 0, time.Local)
}

// Data unpacks an entry.
func (f *File) Data() ([]byte, error) {
	var out []byte
	switch f.Method {
	case "-lh0-", "-lhd-":
		out = f.data
	case "-lh5-":
		out = decode(f.data, int(f.Size), 13, 4)
	case "-lh6-":
		out = decode(f.data, int(f.Size), 15, 5)
	case "-lh7-":
		out = decode(f.data, int(f.Size), 16, 5)
	default:
		return nil, fmt.Errorf("lha: %s: method %s isn't supported", f.Name, f.Method)
	}
	if len(out) != int(f.Size) {
		return nil, fmt.Errorf("lha: %s: unpacked %d bytes, want %d", f.Name, len(out), f.Size)
	}
	if !f.Dir && crc16(out) != f.CRC {
		return nil, fmt.Errorf("lha: %s: CRC mismatch", f.Name)
	}
	return out, nil
}

// ExtractAll unpacks an archive file into dir, refusing paths that would
// leave it.
func ExtractAll(archive, dir string) error {
	fh, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer fh.Close()
	files, err := Read(fh)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Name == "" {
			continue // nameless entries (some Amiga archives have them): lha skips them too
		}
		dst := filepath.Join(dir, filepath.FromSlash(f.Name))
		if rel, err := filepath.Rel(dir, dst); err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("lha: %s: outside the destination", f.Name)
		}
		if f.Dir {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		data, err := f.Data()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		os.Chtimes(dst, f.Modified, f.Modified)
	}
	return nil
}

// crc16 is the CRC LhA uses (CRC-16/ARC).
func crc16(b []byte) uint16 {
	var c uint16
	for _, v := range b {
		c ^= uint16(v)
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0xA001
			} else {
				c >>= 1
			}
		}
	}
	return c
}
