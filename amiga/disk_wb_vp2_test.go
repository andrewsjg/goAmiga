package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchVP2(t *testing.T) {
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
	for i := 0; i < 14_000_000; i++ {
		a.Step()
	}

	mem := make([]byte, ChipRAMSize)
	a.ChipRAM.Read(0, mem)
	rd16 := func(i int) uint16 { return uint16(mem[i])<<8 | uint16(mem[i+1]) }
	rd32 := func(i int) uint32 {
		return uint32(mem[i])<<24 | uint32(mem[i+1])<<16 | uint32(mem[i+2])<<8 | uint32(mem[i+3])
	}

	// KS1.3 ViewPort: DWidth@+28 DHeight@+30 RasInfo@+32
	t.Log("ViewPorts DWidth@+28:")
	for i := 0; i+40 < len(mem); i += 2 {
		if rd16(i) != 640 {
			continue
		}
		base := i - 28
		if base < 0 {
			continue
		}
		dh := rd16(i + 2)
		if dh < 50 || dh > 400 {
			continue
		}
		// sanity: Next should be 0 or chip ptr, ColorMap chip/rom ptr
		next := rd32(base)
		cm := rd32(base + 4)
		dsp := rd32(base + 8)
		if next > 0x80000 && next < 0xFC0000 {
			continue
		}
		if cm == 0 || (cm < 0x1000 && cm != 0) {
			// allow briefly
		}
		dx := int16(rd16(base + 24))
		dy := int16(rd16(base + 26))
		modes := rd16(base + 20)
		ri := rd32(base + 32)
		t.Logf("VP@$%06X Next=$%08X CM=$%08X DspIns=$%08X SprIns=$%08X ClrIns=$%08X Modes=$%04X Dx=%d Dy=%d %dx%d RasInfo=$%08X",
			base, next, cm, dsp, rd32(base+12), rd32(base+16), modes, dx, dy, 640, dh, ri)

		// Expected DIW
		vstart := 0x2C + int(dy)
		vstop := vstart + int(dh)
		t.Logf("  expected VSTART=%d($%02X) VSTOP=%d($%02X) DIWSTRT=$%04X DIWSTOP=$%04X",
			vstart, vstart&0xFF, vstop, vstop&0xFF,
			(vstart&0xFF)<<8|0x81, (vstop&0xFF)<<8|0xC1)

		if ri >= 0x1000 && ri+12 < uint32(len(mem)) {
			bm := rd32(int(ri) + 4)
			t.Logf("  RasInfo@$%08X Next=$%08X BM=$%08X Rx=%d Ry=%d",
				ri, rd32(int(ri)), bm, int16(rd16(int(ri)+8)), int16(rd16(int(ri)+10)))
			if bm >= 0x1000 && bm+20 < uint32(len(mem)) {
				bpr := rd16(int(bm))
				rows := rd16(int(bm) + 2)
				depth := mem[int(bm)+5]
				t.Logf("  BitMap@$%08X BPR=%d Rows=%d Depth=%d pad=%d Flags=%02X",
					bm, bpr, rows, depth, mem[int(bm)+4], mem[int(bm)+4])
				for p := 0; p < int(depth) && p < 6; p++ {
					t.Logf("    P[%d]=$%08X", p, rd32(int(bm)+8+p*4))
				}
			}
		}
		if dsp >= 0x1000 && dsp+20 < uint32(len(mem)) {
			// CopList: Next, _CopList, _CopIns, Count, MaxCount, DyOffset?
			t.Logf("  DspIns CopList: Next=$%08X L=$%08X Ins=$%08X counts=$%04X $%04X",
				rd32(int(dsp)), rd32(int(dsp)+4), rd32(int(dsp)+8), rd16(int(dsp)+12), rd16(int(dsp)+14))
		}
	}

	// View at cop1-4: ViewPort*, LOFCprList, SHFCprList
	// cop1 ref at 2244: -4 was ViewPort?
	t.Logf("View-ish @$2240: VP=$%08X LOF=$%08X SHF=$%08X DyOffset?=$%04X DxOffset?=$%04X",
		rd32(0x2240), rd32(0x2244), rd32(0x2248), rd16(0x224C), rd16(0x224E))
	// Actually from earlier: 2240: 49A6, 2244: 2368=cop1, 2248: 1B18, 224C: 0, 2250: BDB0=cop2
	// That might be CopList not View: CopList has cop1 ptr inside
	t.Logf("struct@$2240 longs: %08X %08X %08X %08X %08X %08X",
		rd32(0x2240), rd32(0x2244), rd32(0x2248), rd32(0x224C), rd32(0x2250), rd32(0x2254))

	// GfxBase: search ActiView non-null near known patterns
	// After LoadView, GfxBase->ActiView points to View
	// View+0 = ViewPort list
}
