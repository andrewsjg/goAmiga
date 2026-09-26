package amiga

import (
	"os"
	"testing"
)

// Inspect ViewPorts and how DIW $0581/$40C1 was derived.
func TestWorkbenchVP(t *testing.T) {
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

	// Dump known VPs more completely - VP layout KS1.3:
	// +0 Next, +4 ColorMap, +8 DspIns, +12 SprIns, +16 ClrIns, +20 UCopIns
	// +24 Modes (UWORD), +26 reserved, +28 DxOffset, +30 DyOffset, +32 DWidth, +34 DHeight
	// +36 RasInfo
	// Try each 640-width hit as DWidth at +32
	t.Log("ViewPorts with DWidth=640:")
	for i := 0; i+40 < len(mem); i += 2 {
		if rd16(i) != 640 {
			continue
		}
		// assume i is DWidth at offset +32
		base := i - 32
		if base < 0 {
			continue
		}
		dh := rd16(i + 2)
		if dh < 20 || dh > 400 {
			continue
		}
		dx := int16(rd16(base + 28))
		dy := int16(rd16(base + 30))
		modes := rd16(base + 24)
		next := rd32(base + 0)
		cm := rd32(base + 4)
		dsp := rd32(base + 8)
		ri := rd32(base + 36)
		t.Logf("VP@$%06X Next=$%08X CM=$%08X DspIns=$%08X Modes=$%04X Dx=%d Dy=%d %dx%d RasInfo=$%08X",
			base, next, cm, dsp, modes, dx, dy, 640, dh, ri)
		if ri > 0 && ri+12 < uint32(len(mem)) {
			// RasInfo: +0 Next, +4 BitMap, +8 RxOffset, +10 RyOffset
			bm := rd32(int(ri) + 4)
			rx := int16(rd16(int(ri) + 8))
			ry := int16(rd16(int(ri) + 10))
			t.Logf("  RasInfo@$%08X BitMap=$%08X Rx=%d Ry=%d", ri, bm, rx, ry)
			if bm > 0 && bm+8 < uint32(len(mem)) {
				// BitMap: +0 BytesPerRow, +2 Rows, +4 Flags, +5 Depth, +8 Planes[8]
				bpr := rd16(int(bm))
				rows := rd16(int(bm) + 2)
				depth := mem[int(bm)+5]
				t.Logf("  BitMap@$%08X BPR=%d Rows=%d Depth=%d", bm, bpr, rows, depth)
				for p := 0; p < int(depth) && p < 6; p++ {
					pl := rd32(int(bm) + 8 + p*4)
					t.Logf("    Plane[%d]=$%08X", p, pl)
				}
			}
		}
		// DspIns CopList: +0 Next, +4 _CopList intermediate...
		if dsp > 0 && dsp+16 < uint32(len(mem)) {
			t.Logf("  DspIns@$%08X: %08X %08X %08X %08X", dsp,
				rd32(int(dsp)), rd32(int(dsp)+4), rd32(int(dsp)+8), rd32(int(dsp)+12))
		}
	}

	// Compute expected DIW from first VP
	// Also log current copper DIW
	t.Logf("Copper DIWSTRT=$%04X DIWSTOP=$%04X", a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090))

	// GfxBase ActiView - find GfxBase via library list
	// ExecBase + $15A is DeviceList, + $14E LibList in some docs
	// Open graphics and read ActiView at GfxBase+34
	// Manually: scan for LOFCprList pointing to cop1
	cop1 := a.Copper.cop1lc
	for i := 0; i+8 < len(mem); i += 2 {
		if rd32(i) == cop1 {
			// possible View.LOFCprList at View+4? View: +0 ViewPort, +4 LOFCprList, +8 SHFCprList
			t.Logf("cop1 ref@$%06X; -4=$%08X +4=$%08X +8=$%08X +12=$%08X",
				i, rd32(i-4), rd32(i+4), rd32(i+8), rd32(i+12))
		}
	}
}
