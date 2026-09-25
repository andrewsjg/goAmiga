package amiga

import (
	"encoding/binary"
	"testing"
)

// spinningDisk returns an Amiga with a blank formatted-looking disk in DF0:,
// motor on, WORDSYNC set, and the beam cycle counter at cycles.
func spinningDisk(t *testing.T, cycles uint64) *Amiga {
	t.Helper()
	a := createTestAmiga(t)
	a.Disk.InsertDisk(make([]byte, ADFSize))
	a.Disk.selected, a.Disk.motor = true, true
	a.Disk.WriteADKCON(0x8400) // WORDSYNC
	a.Disk.dskpt = 0x20000
	a.cycles = cycles
	return a
}

func TestMFMTrackIsOneRevolution(t *testing.T) {
	a := spinningDisk(t, 0)
	if n := len(a.Disk.encodeMFMTrack(0)); n != MFMTrackWords*2 {
		t.Fatalf("MFM track is %d bytes, want %d", n, MFMTrackWords*2)
	}
	// 300 RPM: one revolution is 200 ms of 7.09379 MHz CPU clock.
	if ms := float64(DiskRevolutionCycles) / 7093790 * 1000; ms < 199.9 || ms > 200.1 {
		t.Fatalf("revolution = %.2f ms, want 200", ms)
	}
}

// A WORDSYNC read waits for the next sync word under the head, then takes
// one word time per word; DSKSYN fires at the sync, DSKBLK at the end.
func TestDiskDMARotationalTiming(t *testing.T) {
	mfm := spinningDisk(t, 0).Disk.encodeMFMTrack(0)
	syncs := []int{}
	for i := 0; i < MFMTrackWords; i++ {
		if binary.BigEndian.Uint16(mfm[2*i:]) == 0x4489 {
			syncs = append(syncs, i)
		}
	}
	if len(syncs) != 2*SectorsPerTrack {
		t.Fatalf("%d sync words on the track, want %d", len(syncs), 2*SectorsPerTrack)
	}

	// Put the head 10 words before the 4th sector's first sync.
	head := syncs[6] - 10
	a := spinningDisk(t, uint64(head)*DiskCyclesPerWord)
	a.Disk.WriteDSKLEN(0x8000 | 100)
	a.Disk.WriteDSKLEN(0x8000 | 100)

	wait := 11 // up to and including the sync word
	if got := a.Disk.syncCycles; got != wait*DiskCyclesPerWord {
		t.Errorf("DSKSYN after %d cycles, want %d", got, wait*DiskCyclesPerWord)
	}
	if got := a.Disk.completionCycles; got != (wait+100)*DiskCyclesPerWord {
		t.Errorf("DSKBLK after %d cycles, want %d", got, (wait+100)*DiskCyclesPerWord)
	}
	// Data starts with the word after the sync (the second $4489).
	got := make([]byte, 4)
	a.ChipRAM.Read(0x20000, got)
	if string(got) != string(mfm[2*(syncs[6]+1):2*(syncs[6]+3)]) {
		t.Errorf("DMA data starts % X, want % X", got, mfm[2*(syncs[6]+1):2*(syncs[6]+3)])
	}

	a.Disk.TickCycles(wait*DiskCyclesPerWord - 1)
	if a.Custom.ReadReg16(0x09C)&0x1000 != 0 {
		t.Error("DSKSYN before the sync word passed the head")
	}
	a.Disk.TickCycles(1)
	if a.Custom.ReadReg16(0x09C)&0x1000 == 0 {
		t.Error("no DSKSYN when the sync word passed the head")
	}
	if a.Custom.ReadReg16(0x09C)&0x0002 != 0 {
		t.Error("DSKBLK before the transfer finished")
	}
	a.Disk.TickCycles(100 * DiskCyclesPerWord)
	if a.Custom.ReadReg16(0x09C)&0x0002 == 0 {
		t.Error("no DSKBLK after the transfer")
	}
}

// A read longer than the gap-to-end wraps around to the start of the track.
func TestDiskDMAWrapsAroundTrack(t *testing.T) {
	a := spinningDisk(t, uint64(MFMTrackWords-5)*DiskCyclesPerWord)
	a.Disk.WriteADKCON(0x0400) // no WORDSYNC: start right under the head
	a.Disk.WriteDSKLEN(0x8000 | 20)
	a.Disk.WriteDSKLEN(0x8000 | 20)
	mfm := a.Disk.encodeMFMTrack(0)
	got := make([]byte, 40)
	a.ChipRAM.Read(0x20000, got)
	want := append(append([]byte{}, mfm[2*(MFMTrackWords-5):]...), mfm[:30]...)
	if string(got) != string(want) {
		t.Errorf("wrapped read\n got % X\nwant % X", got, want)
	}
	if a.Disk.syncCycles != 0 || a.Disk.completionCycles != 20*DiskCyclesPerWord {
		t.Errorf("no-sync read: sync %d, done %d cycles", a.Disk.syncCycles, a.Disk.completionCycles)
	}
}

// With no sync word on the track a WORDSYNC read never starts.
func TestDiskDMANoSyncNeverStarts(t *testing.T) {
	a := spinningDisk(t, 0)
	a.Disk.dsksync = 0x1234
	a.Disk.WriteDSKLEN(0x8000 | 20)
	a.Disk.WriteDSKLEN(0x8000 | 20)
	if a.Disk.completionCycles != 0 || a.Disk.syncCycles != 0 {
		t.Error("DMA scheduled without a sync word")
	}
}

// The selected, spinning drive pulses CIA-B /FLAG once per revolution.
func TestDiskIndexPulse(t *testing.T) {
	a := spinningDisk(t, DiskRevolutionCycles-100)
	a.CIAB.Write(CIARegICR<<8, []byte{0x90}) // enable FLG
	if c := a.Disk.CyclesToNextEvent(); c != 100 {
		t.Errorf("next index in %d cycles, want 100", c)
	}
	a.Disk.TickCycles(99)
	if a.CIAB.icrData&0x10 != 0 {
		t.Fatal("index pulse early")
	}
	a.cycles += 99
	a.Disk.TickCycles(2)
	if a.CIAB.icrData&0x10 == 0 {
		t.Fatal("no index pulse at the revolution boundary")
	}

	a.Disk.selected = false
	a.CIAB.icrData = 0
	a.cycles = DiskRevolutionCycles - 1
	a.Disk.TickCycles(2)
	if a.CIAB.icrData&0x10 != 0 {
		t.Error("deselected drive produced an index pulse")
	}
}
