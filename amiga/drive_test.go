package amiga

import (
	"bytes"
	"os"
	"testing"
)

// prb builds a CIA-B PRB value: motor on/off, the selected units, and the
// step/dir/side lines inactive (high).
func prb(motorOn bool, units ...int) byte {
	v := byte(0xFF)
	if motorOn {
		v &^= 0x80
	}
	for _, u := range units {
		v &^= 0x08 << u
	}
	return v
}

// readDriveID clocks a unit's 32-bit ID out of /RDY the way Kickstart's
// disk.resource does: motor on, then off (resetting the shift register),
// then 32 selects with the motor off, reading /RDY (low = 1) each time.
func readDriveID(d *DiskDrive, unit int) uint32 {
	last := prb(false)
	set := func(v byte) { d.HandleCIABWrite(last, v); last = v }
	set(prb(true, unit))
	set(prb(true))
	set(prb(false, unit))
	set(prb(false))
	var id uint32
	for i := 0; i < 32; i++ {
		set(prb(false, unit))
		id <<= 1
		if d.ReadStatus(0xFF)&0x20 == 0 {
			id |= 1
		}
		set(prb(false))
	}
	return id
}

func TestDriveIDs(t *testing.T) {
	a := createTestAmiga(t)
	a.Disk.ConnectDrive(1)
	if id := readDriveID(a.Disk, 1); id != 0xFFFFFFFF {
		t.Errorf("DF1: ID $%08X, want $FFFFFFFF (3.5\" DD drive)", id)
	}
	if id := readDriveID(a.Disk, 2); id != 0 {
		t.Errorf("DF2: (not connected) ID $%08X, want 0", id)
	}
}

// Each drive has its own motor latch and head; only selected drives step.
func TestDrivesIndependent(t *testing.T) {
	a := createTestAmiga(t)
	d := a.Disk
	df1 := d.ConnectDrive(1)
	last := prb(false)
	set := func(v byte) { d.HandleCIABWrite(last, v); last = v }

	set(prb(true, 1)) // DF1: motor on
	set(prb(true))
	if !df1.motor || d.motor {
		t.Fatalf("motors DF0=%v DF1=%v, want DF1 only", d.motor, df1.motor)
	}
	for i := 0; i < 5; i++ { // step DF1 inwards five times
		set(prb(true, 1) &^ 0x02) // select DF1, /DIR low (in)
		set(prb(true, 1) &^ 0x03) // /STEP low
		set(prb(true, 1) &^ 0x02)
	}
	set(prb(true))
	if df1.cylinder != 5 || d.cylinder != 0 {
		t.Errorf("cylinders DF0=%d DF1=%d, want 0 and 5", d.cylinder, df1.cylinder)
	}
}

// Disk DMA reads the selected drive's disk.
func TestDMAReadsSelectedDrive(t *testing.T) {
	a := createTestAmiga(t)
	d := a.Disk
	d.InsertDisk(make([]byte, ADFSize))
	df1 := d.ConnectDrive(1)
	df1.InsertDisk(patternADF())
	d.HandleCIABWrite(prb(false), prb(true, 1)) // DF1 selected, motor on
	d.dskpt = 0x20000
	d.WriteADKCON(0x0400) // no WORDSYNC: read from under the head
	d.WriteDSKLEN(0x8000 | 100)
	d.WriteDSKLEN(0x8000 | 100)

	want := df1.encodeMFMTrack(0)
	pos := d.headWord()
	got := make([]byte, 20)
	a.ChipRAM.Read(0x20000, got)
	if !bytes.Equal(got, want[2*pos:2*pos+20]) {
		t.Errorf("DMA data % X, want DF1's track % X", got, want[2*pos:2*pos+20])
	}
}

// Kickstart 1.3 finds DF1: at boot and mounts the disk in it.
func TestKS13MountsDF1(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	wb, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	second := append([]byte(nil), wb...)
	renameVolume(second, "SecondDisk")

	a, _ := New(rom, nil)
	a.Disk.InsertDisk(wb)
	a.Disk.ConnectDrive(1).InsertDisk(second)
	a.Reset()
	for f := 0; f < 3000; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatal(err)
		}
	}
	vols := dosVolumes(a)
	t.Logf("volumes: %v", vols)
	if !vols["Workbench1.3"] || !vols["SecondDisk"] {
		t.Errorf("volumes %v: want Workbench1.3 (DF0:) and SecondDisk (DF1:) both in drives", vols)
	}
}
