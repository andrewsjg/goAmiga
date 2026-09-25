package amiga

import (
	"os"
	"testing"
)

// Probe trackdisk decode completion: success returns sector 0-10 in D0;
// errors are $15-$1B (see Kick 1.3 FEAE68-FEAE80).
func TestDiskDecodeStatus(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	errNames := map[byte]string{
		0x15: "nosync",
		0x16: "bad_layout",
		0x17: "bad_fmt",
		0x18: "hdrchk2",
		0x19: "datachk",
		0x1A: "nosync2",
		0x1B: "hdr/trk",
	}

	type evt struct {
		step int
		d0   uint32
		cyl  int
		side int
		pc   uint32
	}
	var results []evt
	var dataChkFails, layoutFails, success []evt
	bootMagicHits := 0
	bootExecHits := 0
	tdRet := 0

	// After FEAE94 (sector copy) returns
	copyRet := 0
	// Second-phase sector walk hits
	walkHits := 0
	walkFail := 0

	for i := 0; i < 8_000_000; i++ {
		pc := a.CPU.PC
		switch pc {
		case 0xFEA652: // after decode returns
			d0 := a.CPU.D[0] & 0xFF
			e := evt{step: i, d0: d0, cyl: a.Disk.cylinder, side: a.Disk.side, pc: pc}
			results = append(results, e)
			if d0 < 11 {
				success = append(success, e)
			} else if d0 == 0x19 {
				dataChkFails = append(dataChkFails, e)
			} else if d0 == 0x16 {
				layoutFails = append(layoutFails, e)
			}
			if len(results) <= 24 {
				name := errNames[byte(d0)]
				if d0 < 11 {
					name = "OK_sec"
				}
				t.Logf("step=%d DECODE D0=$%02X (%s) cyl=%d side=%d", i, d0, name, e.cyl, e.side)
			}

		case 0xFEAD36: // after first FEAE94 copy
			copyRet++
			if copyRet <= 6 {
				t.Logf("step=%d after FEAE94#1 D0=$%08X D2=$%08X D3=$%08X D4=$%08X A2=$%08X",
					i, a.CPU.D[0], a.CPU.D[2], a.CPU.D[3], a.CPU.D[4], a.CPU.A[2])
			}

		case 0xFEAD9A: // multi-sector walk
			walkHits++
			if walkHits <= 4 || (walkHits <= 20 && a.CPU.D[4]&0xFFFF == 0) {
				// dump long at a2+d4
				off := a.CPU.A[2] + (a.CPU.D[4] & 0xFFFF)
				b := make([]byte, 8)
				a.ChipRAM.Read(int(off), b)
				t.Logf("step=%d walk d4=$%04X d2=%d d5=%d @%06X: %02X",
					i, a.CPU.D[4]&0xFFFF, a.CPU.D[2]&0xFF, a.CPU.D[5]&0xFF, off, b)
			}

		case 0xFEAE70, 0xFEAE74, 0xFEAE78, 0xFEAE7C, 0xFEAE80:
			walkFail++
			if walkFail <= 12 {
				t.Logf("step=%d WALK FAIL pc=$%08X d4=$%04X d2=%d d5=%d d6=$%08X d0=$%08X",
					i, pc, a.CPU.D[4]&0xFFFF, a.CPU.D[2]&0xFF, a.CPU.D[5]&0xFF, a.CPU.D[6], a.CPU.D[0])
			}

		case 0xFE85A4:
			bootMagicHits++
			if bootMagicHits <= 4 {
				a4 := a.CPU.A[4]
				var mag uint32
				if a4 > 0 && a4 < 0x80000 {
					b := make([]byte, 4)
					a.ChipRAM.Read(int(a4), b)
					mag = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
				}
				t.Logf("step=%d DOS MAGIC A4=$%08X mag=$%08X", i, a4, mag)
			}

		case 0xFE85C6:
			bootExecHits++
			t.Logf("step=%d *** JSR BOOTBLOCK *** A4=$%08X", i, a.CPU.A[4])

		case 0xFEA366:
			tdRet++
			if tdRet <= 10 {
				t.Logf("step=%d td_return D0/D2=$%08X", i, a.CPU.D[2])
			}
		}
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}

	// Summarize decode D0 histogram
	hist := map[uint32]int{}
	for _, e := range results {
		hist[e.d0]++
	}
	t.Logf("decode results total=%d hist=%v", len(results), hist)
	t.Logf("success=%d datachk=%d layout=%d walkHits=%d walkFail=%d copyRet=%d",
		len(success), len(dataChkFails), len(layoutFails), walkHits, walkFail, copyRet)
	t.Logf("bootMagic=%d bootExec=%d tdRet=%d", bootMagicHits, bootExecHits, tdRet)
}
