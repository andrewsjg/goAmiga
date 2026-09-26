package amiga

import (
	"fmt"
	"os"
	"testing"
)

// Focused disk-boot diagnostic: IRQ enable, DMA completions, PC hotspots.
func TestDiskBootDiag(t *testing.T) {
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

	type dmaEvt struct {
		step   int
		cyl    int
		side   int
		len    uint16
		pt     uint32
		intena uint16
		pc     uint32
	}
	var dmas []dmaEvt
	var lastLen uint16
	prevBusyDMA := false

	// After completion interrupt fires
	prevIntreq := uint16(0)
	dskblkIRQs := 0

	hotPC := map[uint32]int{}
	// Sample every 100k after 2M
	for i := 0; i < 15_000_000; i++ {
		// Detect DMA start (dsklen double-write just triggered startDMA)
		if a.Disk.completionCycles > 0 && !prevBusyDMA {
			dmas = append(dmas, dmaEvt{
				step: i, cyl: a.Disk.cylinder, side: a.Disk.side,
				len: a.Disk.dsklen, pt: a.Disk.dskpt,
				intena: a.Custom.ReadReg16(0x09A), pc: a.CPU.PC,
			})
		}
		prevBusyDMA = a.Disk.completionCycles > 0

		intreq := a.Custom.ReadReg16(0x09C)
		if intreq&0x0002 != 0 && prevIntreq&0x0002 == 0 {
			dskblkIRQs++
			if dskblkIRQs <= 8 {
				t.Logf("step=%d DSKBLK IRQ#%d INTENA=$%04X IPL=%d PC=$%08X cyl=%d",
					i, dskblkIRQs, a.Custom.ReadReg16(0x09A), a.CPU.IPL(), a.CPU.PC, a.Disk.cylinder)
			}
		}
		prevIntreq = intreq

		if i > 2_000_000 && i%50 == 0 {
			hotPC[a.CPU.PC]++
		}

		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		if i > 0 && i%3_000_000 == 0 {
			t.Logf("--- %dM PC=$%08X motor=%v cyl=%d dmas=%d dskblkIRQs=%d INTENA=$%04X ---",
				i/1_000_000, a.CPU.PC, a.Disk.motor, a.Disk.cylinder, len(dmas), dskblkIRQs,
				a.Custom.ReadReg16(0x09A))
		}
		_ = lastLen
	}

	t.Logf("DMA events (%d):", len(dmas))
	for i, e := range dmas {
		if i < 20 {
			t.Logf("  #%d step=%d cyl=%d side=%d len=$%04X pt=$%06X INTENA=$%04X PC=$%08X",
				i, e.step, e.cyl, e.side, e.len, e.pt, e.intena, e.pc)
		}
	}
	t.Logf("DSKBLK IRQs total=%d", dskblkIRQs)

	// Top hot PCs
	type kv struct {
		pc uint32
		n  int
	}
	var top []kv
	for pc, n := range hotPC {
		top = append(top, kv{pc, n})
	}
	// simple sort
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].n > top[i].n {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	t.Log("hot PCs:")
	for i := 0; i < 15 && i < len(top); i++ {
		t.Logf("  $%08X x%d", top[i].pc, top[i].n)
	}

	// Decode first sector header from a fresh DMA of track 0
	a2, _ := New(rom, nil)
	a2.Disk.InsertDisk(data)
	a2.Disk.motor = true
	a2.Disk.selected = true
	a2.Disk.cylinder = 0
	a2.Disk.side = 0
	a2.Disk.dskpt = 0x10000
	a2.Disk.dsksync = 0x4489
	a2.Disk.adkcon = 0x0400
	a2.Disk.WriteDSKLEN(0x9A00)
	a2.Disk.WriteDSKLEN(0x9A00)
	// dump MFM after first sync and try decode header info
	raw := make([]byte, 64)
	a2.ChipRAM.Read(0x10000, raw)
	t.Logf("fresh DMA head: %02X", raw[:32])
	_ = fmt.Sprintf
}
