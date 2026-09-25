package amiga

import (
	"os"
	"testing"
)

func TestDiskDecodeResult(t *testing.T) {
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

	ok, fail, nosync, decoded := 0, 0, 0, 0
	for i := 0; i < 8_000_000; i++ {
		pc := a.CPU.PC
		switch pc {
		case 0xFEACFA: // after cmp.l d0,d6 — flags set, before branch
			// Z set if equal. Check d0 vs d6
			if a.CPU.D[0] == a.CPU.D[6] {
				ok++
			} else {
				fail++
				if fail <= 6 {
					t.Logf("step=%d HDRCHK FAIL d6=$%08X d0=$%08X", i, a.CPU.D[6], a.CPU.D[0])
				}
			}
		case 0xFEAD08: // after move.l d0,d3 — info in d3
			decoded++
			if decoded <= 8 {
				info := a.CPU.D[3]
				t.Logf("step=%d info=$%08X fmt=%02X trk=%d sec=%d A5/d5 buffer?",
					i, info, info>>24, (info>>16)&0xFF, (info>>8)&0xFF)
			}
		case 0xFEAD1C: // after track compare, before branch
			if decoded <= 8 {
				t.Logf("  track cmp: header_trk vs unit+4b — D0=$%02X unit[4b]=?", a.CPU.D[0]&0xFF)
				// unit in a3
				b := make([]byte, 1)
				a.ChipRAM.Read(int(a.CPU.A[3]+0x4b), b)
				t.Logf("  unit+4b (expected track)=%d side-ish, D0=%d match=%v",
					b[0], a.CPU.D[0]&0xFF, b[0] == byte(a.CPU.D[0]))
			}
		case 0xFEAE68:
			nosync++
			if nosync <= 4 {
				t.Logf("step=%d NO SYNC", i)
			}
		case 0xFEAE6C:
			fail++
			if fail <= 8 {
				t.Logf("step=%d FAIL FEAE6C", i)
			}
		case 0xFEAC80: // right after sync search returns
			if decoded < 3 {
				t.Logf("step=%d after search A0=$%08X D0=$%08X", i, a.CPU.A[0], a.CPU.D[0])
			}
		}
		a.Step()
	}
	t.Logf("summary hdr_ok≈%d fail=%d nosync=%d headers=%d", ok, fail, nosync, decoded)
}
