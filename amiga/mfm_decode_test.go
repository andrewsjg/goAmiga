package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

func decodePair(odd, even uint32) uint32 {
	return ((odd & 0x55555555) << 1) | (even & 0x55555555)
}

func TestMFMTrackdiskDecode(t *testing.T) {
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a := createTestAmiga(t)
	a.Disk.InsertDisk(data)
	a.Disk.motor = true
	a.Disk.selected = true
	a.Disk.cylinder = 0
	a.Disk.side = 0

	mfm := a.Disk.encodeMFMTrack(0)
	t.Logf("MFM track0 len=%d", len(mfm))

	// Find first sector after double sync
	off := -1
	for i := 0; i < len(mfm)-4; i += 2 {
		w0 := uint16(mfm[i])<<8 | uint16(mfm[i+1])
		w1 := uint16(mfm[i+2])<<8 | uint16(mfm[i+3])
		if w0 == 0x4489 && w1 == 0x4489 {
			off = i + 4 // after both syncs
			break
		}
	}
	if off < 0 {
		t.Fatal("no double sync")
	}
	t.Logf("sector0 data starts at MFM+$%04X", off)

	// Header: 5 longs interleaved = 10 MFM longs = 40 bytes
	// Then header checksum: 2 MFM longs = 8 bytes
	// Then data checksum: 2 MFM longs = 8 bytes  
	// Then data: 256 MFM longs = 1024 bytes

	readLong := func(o int) uint32 {
		return binary.BigEndian.Uint32(mfm[o : o+4])
	}

	// Decode header info (first interleaved pair)
	info := decodePair(readLong(off), readLong(off+4))
	t.Logf("info=$%08X format=%02X track=%d sec=%d togap=%d",
		info, info>>24, (info>>16)&0xFF, (info>>8)&0xFF, info&0xFF)

	// Header checksum over 10 longs (5 pairs)
	var hdrXOR uint32
	for i := 0; i < 10; i++ {
		hdrXOR ^= readLong(off+i*4) & 0x55555555
	}
	hdrChkStored := decodePair(readLong(off+40), readLong(off+44))
	t.Logf("hdr XOR=$%08X stored=$%08X match=%v", hdrXOR, hdrChkStored, hdrXOR == hdrChkStored)

	// Data checksum
	dataStart := off + 48 // after header(40)+hdrchk(8)
	var dataXOR uint32
	for i := 0; i < 256; i++ {
		dataXOR ^= readLong(dataStart+8+i*4) & 0x55555555 // skip data chk first 8 bytes
	}
	// Actually data checksum is BEFORE data block
	dataChkStored := decodePair(readLong(dataStart), readLong(dataStart+4))
	dataBlockStart := dataStart + 8
	dataXOR = 0
	for i := 0; i < 256; i++ {
		dataXOR ^= readLong(dataBlockStart+i*4) & 0x55555555
	}
	t.Logf("data XOR=$%08X stored=$%08X match=%v", dataXOR, dataChkStored, dataXOR == dataChkStored)

	// Decode first 16 bytes of sector
	sec := make([]byte, 16)
	for i := 0; i < 4; i++ {
		odd := readLong(dataBlockStart + i*4)
		even := readLong(dataBlockStart + 128*4 + i*4)
		v := decodePair(odd, even)
		sec[i*4] = byte(v >> 24)
		sec[i*4+1] = byte(v >> 16)
		sec[i*4+2] = byte(v >> 8)
		sec[i*4+3] = byte(v)
	}
	t.Logf("decoded sector0 start: %q %02X", sec[:4], sec[:16])
	t.Logf("ADF sector0 start: %q", data[:4])

	// Compare our DMA path (WORDSYNC skips first sync only)
	a.Disk.dskpt = 0x20000
	a.Disk.dsksync = 0x4489
	a.Disk.adkcon = 0x0400
	a.Disk.WriteDSKLEN(0x9A00)
	a.Disk.WriteDSKLEN(0x9A00)
	// Buffer starts after first 4489, so first word is second 4489
	buf := make([]byte, 8)
	a.ChipRAM.Read(0x20000, buf)
	t.Logf("DMA buffer starts: %02X (expect 4489 then header)", buf)

	// If trackdisk expects data after BOTH syncs, our offset is wrong by 2 bytes
	// Simulate: start at buffer+2
	// Header at DMA+2
	hdr0 := binary.BigEndian.Uint32(mustRead(a, 0x20002, 4))
	hdr1 := binary.BigEndian.Uint32(mustRead(a, 0x20006, 4))
	t.Logf("DMA+2 info pair: $%08X $%08X -> $%08X", hdr0, hdr1, decodePair(hdr0, hdr1))
}

func mustRead(a *Amiga, addr, n int) []byte {
	b := make([]byte, n)
	a.ChipRAM.Read(addr, b)
	return b
}
