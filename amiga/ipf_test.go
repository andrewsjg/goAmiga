package amiga

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"
)

// ipfSectors finds Amiga sectors in a decoded track: it looks for the
// $4489 $4489 sync at any bit position and decodes from there.
func ipfSectors(t *bitTrack) []mfmSector {
	var out []mfmSector
	seen := map[int]bool{}
	for i := 0; i < t.n; i++ {
		if t.word(i) != 0x4489 || t.word(i+16) != 0x4489 {
			continue
		}
		// Bytes from the sync on, word-aligned to it, enough for a sector.
		buf := make([]byte, 0, 1100)
		for k := 0; k < 544; k++ {
			w := t.word(i + 16*k)
			buf = append(buf, byte(w>>8), byte(w))
		}
		for _, s := range decodeMFMSectors(buf) {
			if s.ok && !seen[s.sector] {
				seen[s.sector] = true
				out = append(out, s)
			}
		}
		i += 16 * 500
	}
	return out
}

func TestParseIPFSupremacy(t *testing.T) {
	path := os.Getenv("GOAMIGA_IPF")
	if path == "" {
		t.Skip("set GOAMIGA_IPF to an IPF file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := ParseIPF(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("encoder %d platform %d", img.Encoder, img.Platform)
	for tr := 0; tr < 6; tr++ {
		tk := img.Tracks[tr]
		if tk == nil {
			t.Logf("track %d: unformatted", tr)
			continue
		}
		secs := ipfSectors(tk)
		t.Logf("track %d: %d cells, density %d, %d good AmigaDOS sectors, %d weak ranges", tr, tk.n, tk.density, len(secs), len(tk.weak))
		if tr == 0 && len(secs) > 0 {
			for _, s := range secs {
				if s.sector == 0 {
					t.Logf("  bootblock: %q", s.data[:4])
				}
			}
		}
	}
}

// ---- Building IPF images for tests ----

func be32s(vs ...uint32) []byte {
	b := make([]byte, 4*len(vs))
	for i, v := range vs {
		binary.BigEndian.PutUint32(b[4*i:], v)
	}
	return b
}

// ipfRecord frames body as an IPF record with its CRC.
func ipfRecord(id string, body []byte) []byte {
	r := append([]byte(id), be32s(uint32(12+len(body)), 0)...)
	r = append(r, body...)
	binary.BigEndian.PutUint32(r[8:], crc32.ChecksumIEEE(r))
	return r
}

type synthBlock struct {
	blockBits, gapBits uint32
	flags, gapValue    uint32
	data, gap          []byte // streams (gap: SPS gap streams)
}

// synthIPF builds a one-track IPF (cylinder 0, head 0).
func synthIPF(encoder uint32, startBit, trackBits uint32, blocks []synthBlock) []byte {
	var out []byte
	out = append(out, ipfRecord("CAPS", nil)...)
	info := be32s(1, encoder, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0)
	out = append(out, ipfRecord("INFO", info)...)
	var dataBits, gapBits uint32
	for _, b := range blocks {
		dataBits += b.blockBits
		gapBits += b.gapBits
	}
	imge := be32s(0, 0, 2, 1, (trackBits+7)/8, startBit/8, startBit, dataBits, gapBits, trackBits,
		uint32(len(blocks)), 0, 0, 1, 0, 0, 0)
	out = append(out, ipfRecord("IMGE", imge)...)
	// Extra area: descriptors, then gap streams, then data streams.
	descs := make([]byte, 32*len(blocks))
	var streams []byte
	base := uint32(len(descs))
	for i, b := range blocks {
		gapOff := uint32(0)
		if len(b.gap) > 0 {
			gapOff = base + uint32(len(streams))
			streams = append(streams, b.gap...)
		}
		dataOff := base + uint32(len(streams))
		streams = append(streams, b.data...)
		copy(descs[32*i:], be32s(b.blockBits, b.gapBits, gapOff, 1, 1, b.flags, b.gapValue, dataOff))
	}
	extra := append(descs, streams...)
	out = append(out, ipfRecord("DATA", be32s(uint32(len(extra)), uint32(8*len(extra)), crc32.ChecksumIEEE(extra), 1))...)
	return append(out, extra...)
}

// dataBitsAt returns n data bits (the odd cells of MFM) from cell i on.
func dataBitsAt(t *bitTrack, i, n int) []byte {
	out := make([]byte, n)
	for k := range out {
		out[k] = t.bit(i + 2*k + 1)
	}
	return out
}

func bitsOf(bs ...byte) []byte {
	var out []byte
	for _, b := range bs {
		for i := 7; i >= 0; i-- {
			out = append(out, b>>i&1)
		}
	}
	return out
}

// mfmValid checks n cells of MFM from i: data cells, with each clock cell
// 1 exactly when the data cells either side are both 0.
func mfmValid(t *bitTrack, i, n int) bool {
	for k := 2; k < n; k += 2 {
		prev, clock, d := t.bit(i+k-1), t.bit(i+k), t.bit(i+k+1)
		want := byte(0)
		if prev == 0 && d == 0 {
			want = 1
		}
		if clock != want {
			return false
		}
	}
	return true
}

// A CAPS-encoder track: a gap byte, the sync words as raw cells, two data
// bytes, then a gap of the gap value. Lengths are bytes; the track data
// starts startBit cells after the index.
func TestIPFCapsTrack(t *testing.T) {
	data := []byte{
		0x23, 0x01, 0x00, // gap: 1 byte $00
		0x21, 0x04, 0x44, 0x89, 0x44, 0x89, // sync: 4 bytes of cells
		0x22, 0x02, 0x12, 0x34, // data: 2 bytes
		0x00,
	}
	raw := synthIPF(1, 8, 112, []synthBlock{{blockBits: 16 + 32 + 32, gapBits: 32, gapValue: 0x4E, data: data}})
	img, err := ParseIPF(raw)
	if err != nil {
		t.Fatal(err)
	}
	tk := img.Tracks[0]
	if tk == nil || tk.n != 112 {
		t.Fatalf("track: %+v", tk)
	}
	if tk.word(8) != 0xAAAA {
		t.Errorf("gap byte $00 = $%04X, want $AAAA", tk.word(8))
	}
	if tk.word(24) != 0x4489 || tk.word(40) != 0x4489 {
		t.Errorf("sync = $%04X $%04X", tk.word(24), tk.word(40))
	}
	if got := dataBitsAt(tk, 56, 16); string(got) != string(bitsOf(0x12, 0x34)) {
		t.Errorf("data bits %v", got)
	}
	if got := dataBitsAt(tk, 88, 16); string(got) != string(bitsOf(0x4E, 0x4E)) {
		t.Errorf("gap value bits %v", got)
	}
	if !mfmValid(tk, 56, 64) {
		t.Error("data and gap are not valid MFM")
	}
	// The gap wraps round to the cells before the start position.
	if !mfmValid(tk, 88, 32) {
		t.Error("wrapped gap is not valid MFM")
	}
}

// SPS encoder: bit-length samples (flags bit 2) at odd alignments, and
// forward and backward gap streams.
func TestIPFSPSTrack(t *testing.T) {
	data := []byte{
		0x21, 0x0C, 0xA5, 0x60, // sync: 12 cells 1010 0101 0110
		0x22, 0x08, 0xC3, // data: 8 bits $C3
		0x00,
	}
	gap := []byte{
		0x21, 0x10, 0x22, 0x08, 0x4E, 0x00, // forward: 16 bits of $4E
		0x21, 0x08, 0x22, 0x08, 0x00, 0x00, // backward: 8 bits of $00
	}
	// gap of 48 cells = 24 data bits: 16 forward $4E, 8 backward $00.
	raw := synthIPF(2, 0, 12+16+48, []synthBlock{{blockBits: 12 + 16, gapBits: 48, flags: 4 | 3, data: data, gap: gap}})
	img, err := ParseIPF(raw)
	if err != nil {
		t.Fatal(err)
	}
	tk := img.Tracks[0]
	for i, want := range []byte{1, 0, 1, 0, 0, 1, 0, 1, 0, 1, 1, 0} {
		if tk.bit(i) != want {
			t.Fatalf("raw cell %d = %d", i, tk.bit(i))
		}
	}
	if got := dataBitsAt(tk, 12, 8); string(got) != string(bitsOf(0xC3)) {
		t.Errorf("data at cell 12: %v", got)
	}
	if got := dataBitsAt(tk, 28, 24); string(got) != string(bitsOf(0x4E, 0x4E, 0x00)) {
		t.Errorf("gap streams: %v, want $4E $4E $00", got)
	}
}

// Weak (flakey) cells are recorded and read differently each time.
func TestIPFWeakCells(t *testing.T) {
	data := []byte{0x21, 0x02, 0x44, 0x89, 0x25, 0x02, 0x00} // sync, 2 bytes (16 bits) weak
	raw := synthIPF(1, 0, 16+32+16, []synthBlock{{blockBits: 16 + 32, gapBits: 16, data: data}})
	img, err := ParseIPF(raw)
	if err != nil {
		t.Fatal(err)
	}
	tk := img.Tracks[0]
	if len(tk.weak) != 1 || tk.weak[0] != [2]int{16, 32} {
		t.Fatalf("weak ranges %v, want [[16 32]]", tk.weak)
	}
	seen := map[uint32]bool{}
	for i := 0; i < 20; i++ {
		tk.reroll()
		seen[uint32(tk.word(16))<<16|uint32(tk.word(32))] = true
	}
	if len(seen) < 2 {
		t.Error("weak cells read the same every time")
	}
	if tk.word(0) != 0x4489 {
		t.Error("reroll disturbed the sync word")
	}
}

func TestIPFCorrupt(t *testing.T) {
	raw := synthIPF(1, 0, 64, []synthBlock{{blockBits: 32, gapBits: 32, data: []byte{0x21, 0x04, 0x44, 0x89, 0x44, 0x89, 0}}})
	raw[len(raw)-3] ^= 0xFF // damage the data stream
	if _, err := ParseIPF(raw); err == nil {
		t.Error("damaged IPF parsed")
	}
}

// Read DMA from an IPF track finds the sync word at any bit position.
func TestIPFReadDMAUnaligned(t *testing.T) {
	data := []byte{
		0x21, 0x02, 0x44, 0x89, // sync (cells 5-20 after the start bit)
		0x22, 0x02, 0xBE, 0xEF, // data
		0x00,
	}
	raw := synthIPF(1, 5, 2000, []synthBlock{{blockBits: 16 + 32, gapBits: 2000 - 48, data: data}})
	img, err := ParseIPF(raw)
	if err != nil {
		t.Fatal(err)
	}
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	dr := a.Disk.DF(0)
	if err := dr.InsertImage(&DiskImage{IPF: img}); err != nil {
		t.Fatal(err)
	}
	dr.motor, dr.selected = true, true
	d := a.Disk
	d.dsksync, d.adkcon, d.dskpt = 0x4489, 0x0400, 0x5000
	d.dsklen = 0x8000 | 2
	d.startDMA()
	if !d.wordequal || d.completionCycles == 0 {
		t.Error("sync not seen / DMA not timed")
	}
	finishDiskDMA(d)
	w0, w1 := a.peek16(0x5000), a.peek16(0x5002)
	// The two words after the sync are the MFM of $BE $EF.
	var b [2]byte
	for k := 0; k < 8; k++ {
		b[0] |= byte(w0>>(14-2*k)&1) << (7 - k)
		b[1] |= byte(w1>>(14-2*k)&1) << (7 - k)
	}
	if b != [2]byte{0xBE, 0xEF} {
		t.Errorf("DMA read $%04X $%04X (data $%02X $%02X), want the MFM of $BE $EF", w0, w1, b[0], b[1])
	}
}
