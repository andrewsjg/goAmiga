package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// cpioEntry is one member of an "odc" (portable ASCII, magic 070707)
// cpio archive, the format AMIX's distribution uses.
type cpioEntry struct {
	Name     string
	Dev, Ino uint32
	Mode     uint32
	UID, GID uint32
	Nlink    uint32
	Rdev     uint32 // major<<8 | minor
	Mtime    int64
	Size     int64
	data     io.Reader // the file's contents, Size bytes
}

const (
	modeType = 0o170000
	modeDir  = 0o040000
	modeReg  = 0o100000
	modeLnk  = 0o120000
	modeChr  = 0o020000
	modeBlk  = 0o060000
	modeFifo = 0o010000
)

// cpioReader reads an odc archive member by member.
type cpioReader struct {
	r    *bufio.Reader
	left int64 // unread data of the current member
}

func newCPIOReader(r io.Reader) *cpioReader {
	return &cpioReader{r: bufio.NewReaderSize(r, 1<<20)}
}

// Next returns the next member, or io.EOF after the trailer.
func (c *cpioReader) Next() (*cpioEntry, error) {
	if c.left > 0 {
		if _, err := io.CopyN(io.Discard, c.r, c.left); err != nil {
			return nil, err
		}
		c.left = 0
	}
	var h [76]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return nil, err
	}
	if string(h[:6]) != "070707" {
		return nil, fmt.Errorf("not an odc cpio header: %q", h[:6])
	}
	oct := func(b []byte) (uint64, error) { return strconv.ParseUint(string(b), 8, 64) }
	var f [10]uint64
	for i, span := range [][2]int{{6, 12}, {12, 18}, {18, 24}, {24, 30}, {30, 36}, {36, 42}, {42, 48}, {48, 59}, {59, 65}, {65, 76}} {
		v, err := oct(h[span[0]:span[1]])
		if err != nil {
			return nil, fmt.Errorf("bad cpio header field: %v", err)
		}
		f[i] = v
	}
	name := make([]byte, f[8])
	if _, err := io.ReadFull(c.r, name); err != nil {
		return nil, err
	}
	e := &cpioEntry{
		Name: strings.TrimRight(string(name), "\x00"),
		Dev:  uint32(f[0]), Ino: uint32(f[1]), Mode: uint32(f[2]),
		UID: uint32(f[3]), GID: uint32(f[4]), Nlink: uint32(f[5]), Rdev: uint32(f[6]),
		Mtime: int64(f[7]), Size: int64(f[9]),
	}
	if e.Name == "TRAILER!!!" {
		return nil, io.EOF
	}
	c.left = e.Size
	e.data = &cpioData{c}
	return e, nil
}

type cpioData struct{ c *cpioReader }

func (d *cpioData) Read(p []byte) (int, error) {
	if d.c.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > d.c.left {
		p = p[:d.c.left]
	}
	n, err := d.c.r.Read(p)
	d.c.left -= int64(n)
	return n, err
}
