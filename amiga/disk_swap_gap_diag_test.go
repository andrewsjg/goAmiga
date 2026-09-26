package amiga

import (
	"os"
	"testing"
)

// TestDiskSwapGapDiag measures how soon after an eject AmigaDOS takes the
// volume out of the drive:
//
//	GOAMIGA_DIAG=1 go test -run TestDiskSwapGapDiag -v ./amiga/
func TestDiskSwapGapDiag(t *testing.T) {
	requireDiag(t)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	_ = data
	for trial := 0; trial < 3; trial++ {
		a := bootWB(t)
		for i := 0; i < 37*trial; i++ { // vary the phase against trackdisk's poll
			a.RunFrame()
		}
		a.Disk.Eject()
		for f := 1; f <= 400; f++ {
			a.RunFrame()
			if !dosVolumes(a)["Workbench1.3"] {
				t.Logf("trial %d: volume out of the drive %d frames (%.1f s) after eject", trial, f, float64(f)/50)
				break
			}
		}
	}
}
