package amiga

import (
	"os"
	"testing"
)

// Booted from a hard disk with DF0: empty, a disk inserted later is found.
func testInsertAfterEmptyBoot(t *testing.T, rom, hdf, floppy, volume string) {
	a := bootHardfileMem(t, rom, hdf, 1024, 0, 2048)
	a.HardDisk.Trace = nil
	data, err := os.ReadFile(floppy)
	if err != nil {
		t.Skip(err)
	}
	if volume == "Floppy" {
		renameVolume(data, volume)
	}
	for i := 0; i < 200; i++ {
		a.RunFrame()
	}
	if err := a.Disk.DF(0).SwapDisk(data, ""); err != nil {
		t.Fatal(err)
	}
	inserted := -1
	for f := 0; f < 1500; f++ {
		a.RunFrame()
		if inserted < 0 && !a.Disk.DF(0).Swapping() {
			inserted = f
			t.Logf("disk in the drive %d frames after the swap", f)
		}
		if dosVolumes(a)[volume] {
			t.Logf("%s mounted %d frames after the swap", volume, f)
			return
		}
	}
	t.Errorf("volume %s never appeared; volumes %v; drive inserted=%v swapping=%v",
		volume, dosVolumes(a), a.Disk.DF(0).Inserted(), a.Disk.DF(0).Swapping())
}

func TestInsertAfterEmptyBootKS13(t *testing.T) {
	testInsertAfterEmptyBoot(t, "../ROMS/Kick13.rom", "../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf",
		"../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf", "Floppy")
}

func TestInsertAfterEmptyBootKS32(t *testing.T) {
	testInsertAfterEmptyBoot(t, "../ROMS/Kick32.rom", "../disks/Install3.2.adf", "../disks/Extras3.2.adf", "Extras3.2")
}
