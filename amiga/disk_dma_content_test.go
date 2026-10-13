package amiga

import (
	"os"
	"testing"
)

// After first disk DMA of track 0, check if bootblock signature DOS\0 is present
// when decoded, or at least MFM sync pattern in buffer.
func TestDiskDMAContent(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	// Force a track 0 DMA like the ROM does
	a.Disk.motor = true
	a.Disk.selected = true
	a.Disk.cylinder = 0
	a.Disk.side = 0
	a.Disk.dskpt = 0x6B14
	a.Disk.dsksync = 0x4489
	a.Disk.adkcon = 0x0400              // WORDSYNC
	a.Disk.WriteDSKLEN(0x8000 | 0x1CBE) // first write
	a.Disk.WriteDSKLEN(0x8000 | 0x1CBE) // second write triggers (word count from log $9CBE)

	// Check buffer for 0x4489 sync
	buf := make([]byte, 64)
	a.ChipRAM.Read(0x6B14, buf)
	t.Logf("DMA buffer first 32 words:")
	for i := 0; i < 64; i += 2 {
		w := uint16(buf[i])<<8 | uint16(buf[i+1])
		if i%16 == 0 {
			t.Logf("  $%04X:", 0x6B14+i)
		}
		t.Logf("    %04X", w)
	}
	// Search for 4489
	found := false
	raw := make([]byte, 0x4000)
	a.ChipRAM.Read(0x6B14, raw)
	for i := 0; i < len(raw)-1; i += 2 {
		w := uint16(raw[i])<<8 | uint16(raw[i+1])
		if w == 0x4489 {
			t.Logf("found sync $4489 at buffer+$%04X", i)
			found = true
			break
		}
	}
	if !found {
		t.Log("NO sync word in DMA buffer — check WORDSYNC skip")
	}
	// ADF bootblock raw
	t.Logf("ADF bootblock ID: %q", string(data[0:4]))
}
