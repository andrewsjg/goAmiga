package amiga

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// patternADF returns an image whose every byte depends on its offset.
func patternADF() []byte {
	adf := make([]byte, ADFSize)
	for i := range adf {
		adf[i] = byte(i*7 + i>>9)
	}
	return adf
}

// writeDMA writes mfm to the disk through the DMA registers, with the head
// on the given track, and returns the image track after the write.
func writeDMA(t *testing.T, a *Amiga, track int, mfm []byte) []byte {
	t.Helper()
	a.ChipRAM.Write(0x20000, mfm)
	d := a.Disk
	d.selected, d.motor = true, true
	d.cylinder, d.side = track/2, track%2
	d.dskpt = 0x20000
	words := uint16(len(mfm) / 2)
	d.WriteDSKLEN(0xC000 | words) // DMAEN + WRITE
	d.WriteDSKLEN(0xC000 | words)
	lastWriteCycles = d.completionCycles
	finishDiskDMA(d) // the buffer is decoded when the transfer ends
	return d.adf[track*TrackSize : (track+1)*TrackSize]
}

// lastWriteCycles is how long writeDMA's transfer took.
var lastWriteCycles int

// A track encoded the way trackdisk writes it decodes back to the same
// sectors, and the DMA takes one word time per word.
func TestDiskWriteRoundTrip(t *testing.T) {
	src := patternADF()
	a := createTestAmiga(t)
	a.Disk.InsertDisk(append([]byte(nil), src...))
	const track = 37
	mfm := a.Disk.encodeMFMTrack(track)
	clear(a.Disk.adf[track*TrackSize : (track+1)*TrackSize])

	got := writeDMA(t, a, track, mfm)
	if !bytes.Equal(got, src[track*TrackSize:(track+1)*TrackSize]) {
		t.Fatal("written track does not match the source data")
	}
	if a.Disk.SectorsWritten != SectorsPerTrack || a.Disk.BadSectorWrites != 0 || a.Disk.TrackMismatches != 0 {
		t.Errorf("stats: %d written, %d bad, %d mismatched", a.Disk.SectorsWritten, a.Disk.BadSectorWrites, a.Disk.TrackMismatches)
	}
	if lastWriteCycles != MFMTrackWords*DiskCyclesPerWord {
		t.Errorf("write DMA takes %d cycles, want %d", lastWriteCycles, MFMTrackWords*DiskCyclesPerWord)
	}
	if a.Custom.ReadReg16(0x09C)&0x0002 == 0 {
		t.Error("no DSKBLK after write DMA")
	}
}

// A sector whose data checksum fails is not written.
func TestDiskWriteRejectsBadChecksum(t *testing.T) {
	a := createTestAmiga(t)
	a.Disk.InsertDisk(patternADF())
	mfm := a.Disk.encodeMFMTrack(10)
	// Sector 3's data starts 3*1088 + 8 (gap, sync) + 56 bytes in.
	mfm[3*1088+8+mfmSectorData+100] ^= 0x10
	clear(a.Disk.adf[10*TrackSize : 11*TrackSize])
	got := writeDMA(t, a, 10, mfm)
	if a.Disk.SectorsWritten != 10 || a.Disk.BadSectorWrites != 1 {
		t.Errorf("stats: %d written, %d bad; want 10, 1", a.Disk.SectorsWritten, a.Disk.BadSectorWrites)
	}
	if !bytes.Equal(got[3*BytesPerSector:4*BytesPerSector], make([]byte, BytesPerSector)) {
		t.Error("corrupt sector 3 was written")
	}
}

func TestDiskWriteProtect(t *testing.T) {
	a := createTestAmiga(t)
	a.Disk.InsertDisk(patternADF())
	a.Disk.selected, a.Disk.motor = true, true
	if a.Disk.ReadStatus(0xFF)&0x08 == 0 {
		t.Error("writable disk reports /DSKPROT low (protected)")
	}
	a.Disk.WriteProtected = true
	if a.Disk.ReadStatus(0xFF)&0x08 != 0 {
		t.Error("write-protected disk reports /DSKPROT high")
	}
	mfm := a.Disk.encodeMFMTrack(5)
	clear(a.Disk.adf[5*TrackSize : 6*TrackSize])
	got := writeDMA(t, a, 5, mfm)
	if !bytes.Equal(got, make([]byte, TrackSize)) || a.Disk.SectorsWritten != 0 {
		t.Error("write-protected disk was written")
	}
	if lastWriteCycles == 0 || a.Custom.ReadReg16(0x09C)&0x0002 == 0 {
		t.Error("write DMA to a protected disk should still run and complete")
	}
}

// A disk loaded from a file saves written tracks back to it.
func TestDiskWriteSavesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.adf")
	src := patternADF()
	if err := os.WriteFile(path, make([]byte, ADFSize), 0o644); err != nil {
		t.Fatal(err)
	}
	a := createTestAmiga(t)
	if err := a.LoadDisk(path); err != nil {
		t.Fatal(err)
	}
	// Encode track 80 from the pattern, then write it to the blank disk.
	tmp := createTestAmiga(t)
	tmp.Disk.InsertDisk(src)
	writeDMA(t, a, 80, tmp.Disk.encodeMFMTrack(80))
	if a.Disk.SaveErr != nil {
		t.Fatal(a.Disk.SaveErr)
	}
	file, _ := os.ReadFile(path)
	if !bytes.Equal(file[80*TrackSize:81*TrackSize], src[80*TrackSize:81*TrackSize]) {
		t.Error("track 80 not saved to the file")
	}
	if !bytes.Equal(file[:80*TrackSize], make([]byte, 80*TrackSize)) {
		t.Error("other tracks in the file changed")
	}
}
