package amiga

import (
	"os"
	"testing"
)

func TestDiskADKCONAtDMA(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	n := 0
	prev := false
	for i := 0; i < 5_000_000 && n < 6; i++ {
		busy := a.Disk.completionCycles > 0
		if busy && !prev {
			n++
			t.Logf("DMA#%d ADKCON=$%04X WORDSYNC=%v FAST=%v MSBSYNC=%v dsksync=$%04X cyl=%d",
				n, a.Disk.adkcon,
				a.Disk.adkcon&0x0400 != 0,
				a.Disk.adkcon&0x0100 != 0,
				a.Disk.adkcon&0x0200 != 0,
				a.Disk.dsksync, a.Disk.cylinder)
			// dump first 16 bytes of target
			buf := make([]byte, 16)
			a.ChipRAM.Read(int(a.Disk.dskpt), buf)
			t.Logf("  buf@%06X: %02X", a.Disk.dskpt, buf)
		}
		prev = busy
		a.Step()
	}
}
