package amiga

import (
	"encoding/binary"
	"testing"
)

// startCopper loads prog at $3000, enables Copper DMA and parks the beam at
// the start of line vpos.
func startCopper(t *testing.T, prog []uint16, vpos int) *Amiga {
	t.Helper()
	a := createTestAmiga(t)
	buf := make([]byte, 2*len(prog))
	for i, w := range prog {
		binary.BigEndian.PutUint16(buf[2*i:], w)
	}
	a.ChipRAM.Write(0x3000, buf)
	a.Copper.cop1lc = 0x3000
	a.Copper.Restart(1)
	binary.BigEndian.PutUint16(buf, 0x8280) // DMAEN + COPEN
	a.Custom.Write(0x096, buf[:2])
	a.vpos, a.hpos = vpos, 0
	return a
}

func TestBeamLineAndFrameLength(t *testing.T) {
	a := createTestAmiga(t)
	a.vpos, a.hpos = 10, 0
	a.advanceBeamPerCycle(CPUClocksPerLine - 1)
	if a.vpos != 10 {
		t.Fatalf("line ended early: vpos=%d hpos=%d", a.vpos, a.hpos)
	}
	a.advanceBeamPerCycle(1)
	if a.vpos != 11 || a.hpos != 0 {
		t.Fatalf("after 454 cycles: vpos=%d hpos=%d, want 11,0", a.vpos, a.hpos)
	}
	if CPUClocksPerLine != 454 || CyclesPerFrame != 454*312 {
		t.Fatalf("PAL timing: %d cycles/line, %d/frame", CPUClocksPerLine, CyclesPerFrame)
	}
}

func TestVHPOSRInColourClocks(t *testing.T) {
	a := createTestAmiga(t)
	a.vpos, a.hpos = 0x2C, 2*0xE0+1 // colour clock $E0, odd CPU cycle
	b := make([]byte, 4)
	a.Custom.Read(0x004, b) // MOVE.L $DFF004: VPOSR + VHPOSR
	if b[2] != 0x2C || b[3] != 0xE0 {
		t.Errorf("VHPOSR = $%02X%02X, want $2CE0", b[2], b[3])
	}
	a.Custom.Read(0x006, b[:2])
	if b[0] != 0x2C || b[1] != 0xE0 {
		t.Errorf("VHPOSR word = $%02X%02X, want $2CE0", b[0], b[1])
	}
}

// A WAIT's HP field holds beam bits H8-H2: WAIT $30E1 must hold until colour
// clock $E0 (near the end of the line), not halfway along it.
func TestCopperWaitHorizontalPosition(t *testing.T) {
	a := startCopper(t, []uint16{
		0x30E1, 0xFFFE, // WAIT v=$30 h=$E0
		0x0180, 0x0F00, // MOVE COLOR00
		0xFFFF, 0xFFFE,
	}, 0x30)
	for a.vpos == 0x30 && a.Custom.ReadReg16(0x180) != 0x0F00 {
		a.advanceBeamPerCycle(1)
	}
	if a.vpos != 0x30 {
		t.Fatal("WAIT never released on its line")
	}
	if cc := a.hpos / CPUCyclesPerColorClock; cc < 0xE0 {
		t.Errorf("MOVE ran at colour clock $%02X, want >= $E0", cc)
	}
}

// The Copper starts at most one instruction per CopperCyclesPerInstr CPU
// cycles, so a line holds ~56 MOVEs, not one per CPU cycle.
func TestCopperThroughputPerLine(t *testing.T) {
	var prog []uint16
	for i := 1; i <= 200; i++ {
		prog = append(prog, 0x0180, uint16(i)) // MOVE COLOR00 = i
	}
	prog = append(prog, 0xFFFF, 0xFFFE)
	a := startCopper(t, prog, 0x40)
	a.advanceBeamPerCycle(CPUClocksPerLine)
	got := int(a.Custom.ReadReg16(0x180))
	want := CPUClocksPerLine / CopperCyclesPerInstr
	if got < want-1 || got > want+1 {
		t.Errorf("Copper executed %d MOVEs in one line, want ~%d", got, want)
	}
}
