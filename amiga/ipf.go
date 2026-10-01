package amiga

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand/v2"
)

// IPF (Interchangeable Preservation Format, from the Software Preservation
// Society) images record each track as the stream of MFM cells the disk
// holds, so copy protection survives: odd sync words, long tracks, weak
// (randomly-reading) bits. See docs/ipf-format.md for the format and how it
// is decoded here.
//
// An IPF is a sequence of records, each a 12-byte header (4-character ID,
// big-endian length including the header, CRC-32 of the record with the CRC
// field zeroed) and a body of big-endian longwords: CAPS (identifies the
// file), INFO (the image), an IMGE per track (its geometry) and a DATA per
// track (block descriptors and the streams the cells are built from, in an
// extra area after the record).

// IPFImage is a decoded IPF disk.
type IPFImage struct {
	Encoder  int // 1 CAPS (byte-oriented), 2 SPS (bit-oriented)
	Platform int // INFO platform[0]: 1 = Amiga
	Tracks   [2 * 84]*bitTrack
	raw      []byte // the file, kept for save states
}

// bitTrack is one track's MFM cells, one bit each (most significant bit
// first), as many as the track holds.
type bitTrack struct {
	cells []byte // packed
	n     int    // number of cells
	weak  [][2]int
	// density is the IMGE density type: 1 noise (unformatted), 2 auto
	// (uniform), 3-9 protection schemes with varying cell timing (Copylock,
	// Speedlock, ...), which this decoder does not model.
	density int
}

func (t *bitTrack) bit(i int) byte { i %= t.n; return t.cells[i>>3] >> (7 - i&7) & 1 }

func (t *bitTrack) setBit(i int, v byte) {
	i %= t.n
	if v != 0 {
		t.cells[i>>3] |= 0x80 >> (i & 7)
	} else {
		t.cells[i>>3] &^= 0x80 >> (i & 7)
	}
}

// word returns the 16 cells from cell i on (wrapping round the track).
func (t *bitTrack) word(i int) uint16 {
	var w uint16
	for k := 0; k < 16; k++ {
		w = w<<1 | uint16(t.bit(i+k))
	}
	return w
}

// reroll gives the track's weak cells new random values, as each pass of
// the head reads them differently.
func (t *bitTrack) reroll() {
	for _, r := range t.weak {
		for i := r[0]; i < r[0]+r[1]; i++ {
			t.setBit(i, byte(rand.IntN(2)))
		}
	}
}

var (
	errNotIPF   = errors.New("not an IPF image")
	errIPFShort = errors.New("IPF: truncated")
)

func be32(b []byte, i int) uint32 { return binary.BigEndian.Uint32(b[4*i:]) }

// ParseIPF decodes an IPF image.
func ParseIPF(raw []byte) (*IPFImage, error) {
	if len(raw) < 12 || string(raw[:4]) != "CAPS" {
		return nil, errNotIPF
	}
	img := &IPFImage{raw: raw}
	type imge struct {
		cyl, head, density, startBit, trackBits, blocks int
		flaky                                           bool
	}
	images := map[uint32]imge{}
	for off := 0; off+12 <= len(raw); {
		id := string(raw[off : off+4])
		n := int(binary.BigEndian.Uint32(raw[off+4:]))
		if n < 12 || off+n > len(raw) {
			return nil, fmt.Errorf("IPF: record %q at %d has bad length %d", id, off, n)
		}
		rec := raw[off : off+n]
		var hdr [12]byte
		copy(hdr[:], rec[:12])
		binary.BigEndian.PutUint32(hdr[8:], 0)
		crc := crc32.Update(crc32.ChecksumIEEE(hdr[:]), crc32.IEEETable, rec[12:])
		if crc != binary.BigEndian.Uint32(rec[8:]) {
			return nil, fmt.Errorf("IPF: record %q at %d fails its CRC", id, off)
		}
		body := rec[12:]
		off += n
		switch id {
		case "INFO":
			if len(body) < 84 {
				return nil, errIPFShort
			}
			img.Encoder = int(be32(body, 1))
			img.Platform = int(be32(body, 12))
			if img.Encoder != 1 && img.Encoder != 2 {
				return nil, fmt.Errorf("IPF: unknown encoder %d", img.Encoder)
			}
		case "IMGE":
			if len(body) < 68 {
				return nil, errIPFShort
			}
			images[be32(body, 13)] = imge{
				cyl: int(be32(body, 0)), head: int(be32(body, 1)), density: int(be32(body, 2)),
				startBit: int(be32(body, 6)), trackBits: int(be32(body, 9)), blocks: int(be32(body, 10)),
				flaky: be32(body, 12)&1 != 0,
			}
		case "DATA":
			if len(body) < 16 {
				return nil, errIPFShort
			}
			size := int(be32(body, 0))
			if off+size > len(raw) {
				return nil, errIPFShort
			}
			extra := raw[off : off+size]
			off += size
			if crc32.ChecksumIEEE(extra) != be32(body, 2) {
				return nil, fmt.Errorf("IPF: data for chunk %d fails its CRC", be32(body, 3))
			}
			im, ok := images[be32(body, 3)]
			if !ok {
				return nil, fmt.Errorf("IPF: DATA for unknown chunk %d", be32(body, 3))
			}
			t := im.cyl*2 + im.head
			if t < 0 || t >= len(img.Tracks) {
				continue
			}
			tr, err := decodeIPFTrack(img.Encoder, extra, im.blocks, im.startBit, im.trackBits)
			if err != nil {
				return nil, fmt.Errorf("IPF: cylinder %d head %d: %w", im.cyl, im.head, err)
			}
			if tr != nil {
				tr.density = im.density
			}
			img.Tracks[t] = tr
		}
	}
	if img.Encoder == 0 {
		return nil, errors.New("IPF: no INFO record")
	}
	return img, nil
}

// trackWriter lays cells into a track from a start position, wrapping, and
// MFM-encodes data bits with the clock bit the previous cell implies.
type trackWriter struct {
	t    *bitTrack
	pos  int
	last byte // the last cell written
}

func (w *trackWriter) cell(v byte) {
	w.t.setBit(w.pos, v)
	w.pos++
	w.last = v
}

// data MFM-encodes one data bit: a clock cell (1 only between two zeros),
// then the data cell.
func (w *trackWriter) data(d byte) {
	clock := byte(0)
	if w.last == 0 && d == 0 {
		clock = 1
	}
	w.cell(clock)
	w.cell(d)
}

// decodeIPFTrack builds a track's cells from its DATA area: block
// descriptors (32 bytes each) followed by the streams they point into.
func decodeIPFTrack(encoder int, area []byte, blocks, startBit, trackBits int) (*bitTrack, error) {
	if blocks == 0 || trackBits == 0 {
		return nil, nil // unformatted: noise, no sync words to find
	}
	if len(area) < 32*blocks {
		return nil, errIPFShort
	}
	t := &bitTrack{cells: make([]byte, (trackBits+7)/8), n: trackBits}
	w := &trackWriter{t: t, pos: startBit}
	for b := 0; b < blocks; b++ {
		d := area[32*b : 32*b+32]
		blockBits, gapBits := int(be32(d, 0)), int(be32(d, 1))
		gapOffset := int(be32(d, 2))
		flags, gapValue, dataOffset := be32(d, 5), byte(be32(d, 6)), int(be32(d, 7))
		if encoder == 1 {
			flags = 0 // CAPS encoder: lengths in bytes, no gap streams
		}
		start := w.pos
		if blockBits > 0 {
			if err := w.dataStream(area, dataOffset, flags&4 != 0); err != nil {
				return nil, fmt.Errorf("block %d: %w", b, err)
			}
		}
		// The descriptor's length is authoritative.
		w.pos = start + blockBits
		if gapBits > 0 {
			if err := w.gap(area, gapOffset, flags, gapValue, gapBits); err != nil {
				return nil, fmt.Errorf("block %d gap: %w", b, err)
			}
		}
		w.pos = start + blockBits + gapBits
	}
	return t, nil
}

// element reads a stream element at off: its type (low 5 bits of the head
// byte), a count whose width in bytes is the top 3 bits, and returns the
// offset of the sample that follows.
func element(area []byte, off int) (kind int, count int, next int, err error) {
	if off >= len(area) {
		return 0, 0, 0, errIPFShort
	}
	h := area[off]
	kind, width := int(h&0x1F), int(h>>5)
	off++
	if off+width > len(area) {
		return 0, 0, 0, errIPFShort
	}
	for i := 0; i < width; i++ {
		count = count<<8 | int(area[off+i])
	}
	return kind, count, off + width, nil
}

// dataStream writes a block's data stream: samples of sync or raw cells
// (copied as they are), data or gap bytes (MFM-encoded) and weak cells.
// Counts are in bytes, or bits when bitCounts is set.
func (w *trackWriter) dataStream(area []byte, off int, bitCounts bool) error {
	for {
		kind, count, next, err := element(area, off)
		if err != nil {
			return err
		}
		if kind == 0 {
			return nil
		}
		bits := count
		if !bitCounts {
			bits = count * 8
		}
		nbytes := (bits + 7) / 8
		if kind == 5 { // flakey: no sample, 2 cells per bit, random each read
			w.t.weak = append(w.t.weak, [2]int{w.pos % w.t.n, 2 * bits})
			for i := 0; i < 2*bits; i++ {
				w.cell(byte(rand.IntN(2)))
			}
			off = next
			continue
		}
		if next+nbytes > len(area) {
			return errIPFShort
		}
		sample := area[next : next+nbytes]
		for i := 0; i < bits; i++ {
			v := sample[i>>3] >> (7 - i&7) & 1
			switch kind {
			case 1, 4: // sync, raw: cells
				w.cell(v)
			case 2, 3: // data, gap: MFM-encoded
				w.data(v)
			default:
				return fmt.Errorf("unknown stream element type %d", kind)
			}
		}
		off = next + nbytes
	}
}

// gapSample is one gap stream sample: a bit pattern repeated to fill
// length data bits (length < 0: a loop sample, repeated as needed).
type gapSample struct {
	pattern []byte // bits, one per byte
	length  int
}

// gapStream reads a gap stream (lengths always in bits): stream_length
// elements (type 1) each followed by a data element (type 2); a data
// element with no length before it is an explicit loop sample.
func gapStream(area []byte, off int) ([]gapSample, int, error) {
	var out []gapSample
	length := -1
	for {
		kind, count, next, err := element(area, off)
		if err != nil {
			return nil, 0, err
		}
		switch kind {
		case 0:
			return out, next, nil
		case 1:
			length = count
			off = next
		case 2:
			nbytes := (count + 7) / 8
			if next+nbytes > len(area) {
				return nil, 0, errIPFShort
			}
			p := make([]byte, count)
			for i := range p {
				p[i] = area[next+i>>3] >> (7 - i&7) & 1
			}
			out = append(out, gapSample{p, length})
			length = -1
			off = next + nbytes
		default:
			return nil, 0, fmt.Errorf("unknown gap element type %d", kind)
		}
	}
}

// expand turns gap samples into data bits: each fixed sample repeated to
// its length. loop is the explicit loop sample, or (with none) the last
// sample, for filling any remainder.
func expand(s []gapSample) (fixed, loop []byte) {
	for _, g := range s {
		if g.length < 0 {
			loop = g.pattern
			continue
		}
		for i := 0; i < g.length && len(g.pattern) > 0; i++ {
			fixed = append(fixed, g.pattern[i%len(g.pattern)])
		}
	}
	if loop == nil && len(s) > 0 {
		loop = s[len(s)-1].pattern
	}
	return
}

func hasLoop(s []gapSample) bool {
	for _, g := range s {
		if g.length < 0 {
			return true
		}
	}
	return false
}

func repeat(p []byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		if len(p) > 0 {
			out[i] = p[i%len(p)]
		}
	}
	return out
}

// gap writes gapBits cells of gap: the gap value byte repeated, or the
// block's forward and/or backward gap streams (SPS encoder).
func (w *trackWriter) gap(area []byte, off int, flags uint32, value byte, gapBits int) error {
	total := gapBits / 2 // data bits; MFM doubles them
	var bitsOut []byte
	if flags&3 == 0 {
		var p [8]byte
		for i := range p {
			p[i] = value >> (7 - i) & 1
		}
		bitsOut = repeat(p[:], total)
	} else {
		var fwd, bwd []gapSample
		var err error
		if flags&1 != 0 {
			if fwd, off, err = gapStream(area, off); err != nil {
				return err
			}
		}
		if flags&2 != 0 {
			if bwd, _, err = gapStream(area, off); err != nil {
				return err
			}
		}
		ff, fl := expand(fwd)
		bf, bl := expand(bwd)
		// A backward stream is applied in reverse, so without an explicit
		// loop sample its automatic loop is its first sample, not its last.
		if len(bwd) > 0 && !hasLoop(bwd) {
			bl = bwd[0].pattern
		}
		switch {
		case flags&3 == 1: // forward only
			bitsOut = append(ff, repeat(fl, max(0, total-len(ff)))...)
		case flags&3 == 2: // backward only: ends at the next block
			bitsOut = append(repeat(bl, max(0, total-len(bf))), bf...)
		default: // both: fixed parts at each end, loops meet in the middle
			for len(ff)+len(bf) > total {
				if len(ff) >= len(bf) && len(ff) > 0 {
					ff = ff[:len(ff)-1]
				} else {
					bf = bf[1:]
				}
			}
			mid := total - len(ff) - len(bf)
			bitsOut = append(append(append(ff, repeat(fl, mid/2)...), repeat(bl, mid-mid/2)...), bf...)
		}
		if len(bitsOut) > total {
			bitsOut = bitsOut[len(bitsOut)-total:]
		}
	}
	for _, b := range bitsOut[:min(total, len(bitsOut))] {
		w.data(b)
	}
	return nil
}
