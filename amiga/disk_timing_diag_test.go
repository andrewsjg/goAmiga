package amiga

import (
	"os"
	"testing"
)

// TestDiskTimingDiag logs every disk DMA during a Workbench boot: when it
// started (frame), where the head was, how long it waited for sync and how
// long the transfer took, plus motor changes. Use it to see where boot time
// goes on the floppy:
//
//	GOAMIGA_DIAG=1 go test -run TestDiskTimingDiag -v ./amiga/
func TestDiskTimingDiag(t *testing.T) {
	requireDiag(t)
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Disk.InsertDisk(data)
	a.Reset()

	frame := func() float64 { return float64(a.cycles) / CyclesPerFrame }
	dmas, logged := 0, 0
	perTrack := map[int]int{}
	lastWrites, lastMotor, lastCyl := 0, false, 0
	dumped := false
	var lastDMA uint64
	for steps := 0; steps < 100_000_000; steps++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(np), 24) == "workbench.task" {
				break
			}
		}
		pendingBefore := a.Disk.completionCycles
		writesBefore := a.Disk.dsklenWrites
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		d := a.Disk
		if d.motor != lastMotor && logged < 300 {
			t.Logf("frame %8.1f motor %v cyl %d", frame(), d.motor, d.cylinder)
			logged++
			lastMotor = d.motor
		}
		if d.cylinder != lastCyl {
			lastCyl = d.cylinder
		}
		if writesBefore > 0 && d.dsklenWrites == 0 && d.completionCycles > pendingBefore {
			dmas++
			perTrack[d.cylinder*2+d.side]++
			if logged < 300 && (a.cycles-lastDMA) > 60*CyclesPerFrame {
				t.Logf("frame %8.1f DMA #%d cyl %d side %d words %d wait %d words (+%.1f frames since last)",
					frame(), dmas, d.cylinder, d.side, d.dsklen&0x3FFF,
					d.syncCycles/DiskCyclesPerWord, float64(a.cycles-lastDMA)/CyclesPerFrame)
				logged++
			}
			lastDMA = a.cycles
		}
		_ = lastWrites
		if dmas > 100 && !dumped && a.cycles-lastDMA > 200*CyclesPerFrame {
			dumped = true
			for _, c := range []*CIA{a.CIAA, a.CIAB} {
				t.Logf("STALL %s: TOD $%06X alarm $%06X stopped=%v ICR mask $%02X data $%02X CRA $%02X CRB $%02X TA $%04X/%04X TB $%04X/%04X",
					c.Name, c.todCounter, c.todAlarm, c.todStopped, c.icrMask, c.icrData,
					c.regs[CIARegCRA], c.regs[CIARegCRB], c.timerACounter, c.timerALatch, c.timerBCounter, c.timerBLatch)
			}
			t.Logf("STALL INTENA $%04X INTREQ $%04X DMACON $%04X SR $%04X PC $%06X stopped=%v disk sel=%v motor=%v",
				a.Custom.ReadReg16(0x09A), a.Custom.ReadReg16(0x09C), a.Custom.ReadReg16(0x096),
				a.CPU.SR, a.CPU.PC, a.cpuStopped, d.selected, d.motor)
		}
	}
	t.Logf("boot done at frame %.0f, %d disk DMAs of %d distinct tracks", frame(), dmas, len(perTrack))
	max, maxTrack := 0, 0
	for tr, n := range perTrack {
		if n > max {
			max, maxTrack = n, tr
		}
	}
	t.Logf("most-read track %d (cyl %d side %d): %d times", maxTrack, maxTrack/2, maxTrack%2, max)
}
