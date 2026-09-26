package amiga

import (
	"os"
	"testing"
)

// findLibrary returns the base of the named library from exec's LibList.
func findLibrary(a *Amiga, name string) uint32 {
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	execBase := rd(4)
	for n := rd(execBase + 0x17A); n != 0 && rd(n) != 0; n = rd(n) {
		if readCStrMem(a, rd(n+10), 32) == name {
			return n
		}
	}
	return 0
}

// dosVolumes returns AmigaDOS's volume list: name → whether the volume is
// in a drive (dl_Task set). DosLibrary.dl_Root (+34) → RootNode.rn_Info
// (+24, BPTR) → DosInfo.di_DevInfo (+4, BPTR) → DeviceList nodes: dl_Next
// (+0 BPTR), dl_Type (+4, 2 = volume), dl_Task (+8), dl_Name (+40 BSTR).
func dosVolumes(a *Amiga) map[string]bool {
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	dos := findLibrary(a, "dos.library")
	if dos == 0 {
		return nil
	}
	info := rd(rd(dos+34)+24) << 2
	vols := map[string]bool{}
	for n := rd(info+4) << 2; n != 0; n = rd(n) << 2 {
		if rd(n+4) != 2 {
			continue
		}
		bstr := rd(n+40) << 2
		l, _ := a.CPU.M.Byte(int(bstr))
		name := make([]byte, l)
		a.CPU.M.Read(int(bstr)+1, name)
		vols[string(name)] = rd(n+8) != 0
	}
	return vols
}

// renameVolume sets the volume name in an OFS image's root block and fixes
// its checksum.
func renameVolume(adf []byte, name string) {
	root := adf[880*512 : 881*512]
	root[432] = byte(len(name))
	copy(root[433:], name)
	ofsFixChecksum(root)
}

// Kickstart notices a disk swap: ejecting takes the volume out of the
// drive, and inserting another disk mounts its volume.
func TestDiskSwap(t *testing.T) {
	a := bootWB(t)
	vols := dosVolumes(a)
	if !vols["Workbench1.3"] {
		t.Fatalf("after boot, volumes %v: want Workbench1.3 in the drive", vols)
	}

	a.Disk.Eject()
	for i := 0; i < 250; i++ { // 5 s: trackdisk polls for changes every few seconds
		a.RunFrame()
	}
	if vols = dosVolumes(a); vols["Workbench1.3"] {
		t.Fatalf("after eject, volumes %v: Workbench1.3 still in the drive", vols)
	}

	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	renameVolume(data, "SwapTest")
	a.Disk.InsertDisk(data)
	for i := 0; i < 500; i++ {
		a.RunFrame()
	}
	vols = dosVolumes(a)
	t.Logf("volumes after insert: %v", vols)
	if !vols["SwapTest"] {
		t.Errorf("inserted disk's volume SwapTest not mounted: %v", vols)
	}

	// SwapDisk (what the F11 key does) ejects, waits for trackdisk to see
	// the empty drive, then inserts.
	data2 := append([]byte(nil), data...)
	renameVolume(data2, "SwapTwo")
	if err := a.Disk.SwapDisk(data2, ""); err != nil {
		t.Fatal(err)
	}
	frames := 0
	for ; frames < 200 && a.Disk.Swapping(); frames++ {
		a.RunFrame()
	}
	t.Logf("SwapDisk inserted after %d frames", frames)
	if a.Disk.Swapping() {
		t.Fatal("SwapDisk never inserted the disk")
	}
	for i := 0; i < 500; i++ {
		a.RunFrame()
	}
	vols = dosVolumes(a)
	if !vols["SwapTwo"] || vols["SwapTest"] {
		t.Errorf("after SwapDisk, volumes %v: want SwapTwo in the drive, SwapTest out", vols)
	}
}

func TestSwapDiskTiming(t *testing.T) {
	a := createTestAmiga(t)
	a.Disk.InsertDisk(patternADF())
	a.Disk.selected = true

	// Nobody polls: the disk goes in after swapMaxWait.
	a.Disk.SwapDisk(make([]byte, ADFSize), "")
	if a.Disk.Inserted() || a.Disk.ReadStatus(0xFF)&0x04 != 0 {
		t.Fatal("drive not empty (with /DSKCHANGE low) right after SwapDisk")
	}
	a.Disk.TickCycles(swapMaxWait - 1)
	if a.Disk.Inserted() {
		t.Fatal("inserted before swapMaxWait")
	}
	a.Disk.TickCycles(1)
	if !a.Disk.Inserted() || a.Disk.Swapping() {
		t.Fatal("not inserted after swapMaxWait")
	}

	// A head step with the drive empty (trackdisk's poll) brings the
	// insert forward to swapAfterPoll.
	a.Disk.SwapDisk(make([]byte, ADFSize), "")
	a.Disk.HandleCIABWrite(0x01, 0x00) // /STEP falling edge
	a.Disk.TickCycles(swapAfterPoll)
	if !a.Disk.Inserted() {
		t.Fatal("not inserted swapAfterPoll after the empty-drive poll")
	}
	if a.Disk.ReadStatus(0xFF)&0x04 != 0 {
		t.Error("/DSKCHANGE should stay low until the head steps with the new disk in")
	}
}
