package amiga

import (
	"encoding/binary"
	"testing"
)

func createTestAmigaWithDisk(t *testing.T) (*Amiga, *DiskDrive) {
	t.Helper()
	a := createTestAmiga(t)

	// Create a minimal ADF with known data
	adf := make([]byte, ADFSize)
	// Write recognizable data in first sector
	for i := 0; i < BytesPerSector; i++ {
		adf[i] = byte(i)
	}
	if err := a.Disk.InsertDisk(adf); err != nil {
		t.Fatalf("failed to insert disk: %v", err)
	}
	return a, a.Disk
}

func TestDiskInsert(t *testing.T) {
	a := createTestAmiga(t)

	// Wrong size should fail
	if err := a.Disk.InsertDisk(make([]byte, 100)); err == nil {
		t.Error("expected error for wrong ADF size")
	}

	// Correct size should succeed
	if err := a.Disk.InsertDisk(make([]byte, ADFSize)); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiskCIABMotorAndSelect(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	// Initially not selected, motor off
	if d.selected {
		t.Error("drive should not be selected initially")
	}
	if d.motor {
		t.Error("motor should be off initially")
	}

	// Select drive (bit 3 low) with motor on (bit 7 low)
	d.HandleCIABWrite(0xFF, 0x77) // bits 7,3 = 0
	if !d.selected {
		t.Error("drive should be selected")
	}
	if !d.motor {
		t.Error("motor should be on")
	}

	// Deselect drive
	d.HandleCIABWrite(0x77, 0x7F) // bit 3 = 1
	if d.selected {
		t.Error("drive should be deselected")
	}
}

func TestDiskStepping(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	if d.cylinder != 0 {
		t.Fatalf("expected cylinder 0, got %d", d.cylinder)
	}

	// Step toward center (DIR=0): select drive, set DIR=0, pulse STEP
	d.HandleCIABWrite(0xFF, 0x77) // select, motor on
	d.HandleCIABWrite(0x77, 0x75) // DIR=0, STEP high→low (bit 0: 1→0, bit 1: 0)
	// Actually need step falling edge: old bit 0 = 1, new bit 0 = 0
	// old=0x77 has bit 0 = 1, new=0x76 has bit 0 = 0
	d.cylinder = 0                // reset
	d.HandleCIABWrite(0x77, 0x76) // step falling edge, DIR=1 (toward track 0)

	// DIR=1 means toward track 0, already at 0, so no change
	if d.cylinder != 0 {
		t.Errorf("cylinder should stay at 0, got %d", d.cylinder)
	}

	// Step toward center: DIR=0 (bit 1 = 0), step pulse
	d.HandleCIABWrite(0x76, 0x77) // raise STEP
	d.HandleCIABWrite(0x77, 0x74) // DIR=0 (bit1=0), STEP falling edge (bit0: 1→0)
	if d.cylinder != 1 {
		t.Errorf("expected cylinder 1, got %d", d.cylinder)
	}
}

func TestDiskTrack0Detection(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	d.selected = true
	d.motor = true

	// At cylinder 0: /DSKTRACK0 should be low (bit 4 clear)
	status := d.ReadStatus(0xFF)
	if status&0x10 != 0 {
		t.Error("/DSKTRACK0 should be low at cylinder 0")
	}

	// Move to cylinder 1
	d.cylinder = 1
	status = d.ReadStatus(0xFF)
	if status&0x10 == 0 {
		t.Error("/DSKTRACK0 should be high at cylinder 1")
	}
}

func TestDiskChangeDetection(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	d.selected = true
	d.motor = true

	// After insert, diskChange is true → /DSKCHANGE should be low (bit 2 clear)
	status := d.ReadStatus(0xFF)
	if status&0x04 != 0 {
		t.Error("/DSKCHANGE should be low after insert (diskChange=true)")
	}

	// Step clears diskChange → /DSKCHANGE goes HIGH (inactive = no change)
	d.HandleCIABWrite(0xFF, 0x77) // select + motor
	d.HandleCIABWrite(0x77, 0x74) // step
	d.selected = true

	status = d.ReadStatus(0xFF)
	if status&0x04 == 0 {
		t.Error("/DSKCHANGE should be high after step (disk present, change cleared)")
	}
}

func TestDSKLENDoubleWrite(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	// Single write should not trigger DMA
	d.WriteDSKLEN(0x8000 | 100) // DMA enable + 100 words
	if d.dsklenWrites != 1 {
		t.Errorf("expected 1 write count, got %d", d.dsklenWrites)
	}

	// Clear DMA should reset counter
	d.WriteDSKLEN(0x0000)
	if d.dsklenWrites != 0 {
		t.Errorf("expected 0 write count after clear, got %d", d.dsklenWrites)
	}
}

func TestDiskDMATransfer(t *testing.T) {
	a, d := createTestAmigaWithDisk(t)

	// Setup drive state
	d.selected = true
	d.motor = true
	d.cylinder = 0
	d.side = 0

	// Set WORDSYNC mode
	d.WriteADKCON(0x8400) // set bit 10

	// Set destination in ChipRAM
	d.dskpt = 0x20000
	d.dsksync = 0x4489

	// Double-write DSKLEN to trigger DMA (read 100 words)
	d.WriteDSKLEN(0x8064) // DMA enable + 100 words
	d.WriteDSKLEN(0x8064) // second write triggers DMA

	// Check that data was written to ChipRAM immediately
	buf := make([]byte, 4)
	a.ChipRAM.Read(0x20000, buf)

	// Data should not be all zeros (MFM encoded data was written)
	allZero := true
	for _, b := range buf {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("expected non-zero data in ChipRAM after DMA transfer")
	}

	// Completion interrupts are deferred — advance the timer
	d.TickCycles(100*2 + 64)

	// Check DSKBLK interrupt was fired (INTREQ bit 1)
	intreq := a.Custom.ReadReg16(0x09C)
	if intreq&0x02 == 0 {
		t.Error("DSKBLK interrupt (bit 1) should be set after DMA")
	}

	// Check DSKSYN interrupt (bit 12)
	if intreq&0x1000 == 0 {
		t.Error("DSKSYN interrupt (bit 12) should be set after DMA")
	}
}

func TestMFMEncoding(t *testing.T) {
	a := createTestAmiga(t)
	d := a.Disk

	adf := make([]byte, ADFSize)
	// Put known pattern in first sector
	for i := 0; i < BytesPerSector; i++ {
		adf[i] = byte(i & 0xFF)
	}
	d.InsertDisk(adf)

	mfm := d.encodeMFMTrack(0)
	if len(mfm) == 0 {
		t.Fatal("MFM track should not be empty")
	}

	// Check for sync words ($4489)
	foundSync := false
	for i := 0; i < len(mfm)-1; i += 2 {
		w := binary.BigEndian.Uint16(mfm[i : i+2])
		if w == 0x4489 {
			foundSync = true
			break
		}
	}
	if !foundSync {
		t.Error("MFM track should contain sync word $4489")
	}
}

func TestMFMOddEven(t *testing.T) {
	// Test MFM odd/even bit extraction and reconstruction
	val := uint32(0xDEADBEEF)
	odd := mfmOdd(val)
	even := mfmEven(val)

	// Verify only data bits are set (0x55555555 mask)
	if odd&^uint32(0x55555555) != 0 {
		t.Errorf("mfmOdd should only have bits in 0x55555555 positions, got $%08X", odd)
	}
	if even&^uint32(0x55555555) != 0 {
		t.Errorf("mfmEven should only have bits in 0x55555555 positions, got $%08X", even)
	}

	// Reconstruct: odd bits are (val>>1)&0x55555555, even bits are val&0x55555555
	// Original = (odd << 1) | even
	reconstructed := (odd << 1) | even
	if reconstructed != val {
		t.Errorf("MFM odd/even reconstruction failed: got $%08X, expected $%08X", reconstructed, val)
	}
}

func TestDiskSideSelect(t *testing.T) {
	_, d := createTestAmigaWithDisk(t)

	// /SIDE bit 2: 0 = upper head (side 1), 1 = lower head (side 0)
	d.HandleCIABWrite(0xFF, 0x73) // bit 2 = 0 → upper
	if d.side != 1 {
		t.Errorf("expected side 1 (upper), got %d", d.side)
	}

	d.HandleCIABWrite(0x73, 0x77) // bit 2 = 1 → lower
	if d.side != 0 {
		t.Errorf("expected side 0 (lower), got %d", d.side)
	}
}
