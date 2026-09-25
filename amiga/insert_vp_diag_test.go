package amiga

import (
	"testing"
)

func TestInsertVPDiag(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()
	for i := 0; i < 4_000_000; i++ {
		a.Step()
	}
	for i := 0; i < 3; i++ {
		a.RunFrame()
	}

	// COP2 at known location - reverse: find ViewPort used
	// Dump GfxBase ActiView
	// GfxBase $221E from script - may still be valid
	gb := uint32(0x221E)
	// ActiView at GfxBase+34 ($22)
	av, _ := a.CPU.M.Long(int(gb) + 0x22)
	t.Logf("GfxBase=$%08X ActiView=$%08X", gb, av)
	if av > 0 && av < 0x80000 {
		// View: +0 ViewPort*, +4 LOFCprList, +8 SHFCprList, +12 DyOffset, +14 DxOffset
		vp, _ := a.CPU.M.Long(int(av))
		lof, _ := a.CPU.M.Long(int(av) + 4)
		shf, _ := a.CPU.M.Long(int(av) + 8)
		dy, _ := a.CPU.M.Word(int(av) + 12)
		dx, _ := a.CPU.M.Word(int(av) + 14)
		t.Logf("View: VP=$%08X LOF=$%08X SHF=$%08X Dy=%d Dx=%d", vp, lof, shf, int16(dy), int16(dx))
		if vp > 0 && vp < 0x80000 {
			// ViewPort layout
			modes, _ := a.CPU.M.Word(int(vp) + 20)
			dxo, _ := a.CPU.M.Word(int(vp) + 24)
			dyo, _ := a.CPU.M.Word(int(vp) + 26)
			dw, _ := a.CPU.M.Word(int(vp) + 28)
			dh, _ := a.CPU.M.Word(int(vp) + 30)
			ri, _ := a.CPU.M.Long(int(vp) + 32)
			dsp, _ := a.CPU.M.Long(int(vp) + 8)
			cm, _ := a.CPU.M.Long(int(vp) + 4)
			t.Logf("VP: Modes=$%04X Dx=%d Dy=%d %dx%d RasInfo=$%08X DspIns=$%08X CM=$%08X",
				modes, int16(dxo), int16(dyo), dw, dh, ri, dsp, cm)
			// Expected DIW
			vs := 0x2C + int(int16(dy)) + int(int16(dyo))
			ve := vs + int(dh)
			t.Logf("expected DIWSTRT=$%04X DIWSTOP=$%04X (vs=%d ve=%d)",
				(vs&0xFF)<<8|0x81, (ve&0xFF)<<8|0xC1, vs, ve)
			if ri > 0x1000 && ri < 0x80000 {
				bm, _ := a.CPU.M.Long(int(ri) + 4)
				t.Logf("RasInfo BM=$%08X", bm)
				if bm > 0x1000 {
					bpr, _ := a.CPU.M.Word(int(bm))
					rows, _ := a.CPU.M.Word(int(bm) + 2)
					depth, _ := a.CPU.M.Byte(int(bm) + 5)
					p0, _ := a.CPU.M.Long(int(bm) + 8)
					t.Logf("BitMap BPR=%d Rows=%d Depth=%d P0=$%08X", bpr, rows, depth, p0)
				}
			}
		}
	}
	t.Logf("actual copper DIW $%04X/$%04X", a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090))
}
