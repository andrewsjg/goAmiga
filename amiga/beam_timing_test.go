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
	if CPUClocksPerLine != 454 || CyclesPerFrame != 454*313 {
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
		0x30D1, 0xFFFE, // WAIT v=$30 h=$D0
		0x0180, 0x0F00, // MOVE COLOR00
		0xFFFF, 0xFFFE,
	}, 0x30)
	for a.vpos == 0x30 && a.Custom.ReadReg16(0x180) != 0x0F00 {
		a.advanceBeamPerCycle(1)
	}
	if a.vpos != 0x30 {
		t.Fatal("WAIT never released on its line")
	}
	// The WAIT releases at $D0; waking takes that slot and the MOVE runs
	// in the next one.
	if cc := a.hpos / CPUCyclesPerColorClock; cc < 0xD4 {
		t.Errorf("MOVE ran at colour clock $%02X, want >= $D4", cc)
	}
}

// Crossing line 255: WAIT $FFDE, then WAIT $2C for line $12C. Waking from
// the first WAIT pushes the second onto line 256, where the Copper's 8-bit
// line compare starts again from 0, so the MOVE happens at line 300 — not
// immediately on line 255 (which cut KS3.2's Workbench screen short).
func TestCopperLine255Wrap(t *testing.T) {
	a := startCopper(t, []uint16{
		0xFFDF, 0xFFFE, // WAIT v=$FF h=$DE
		0x2C01, 0xFFFE, // WAIT v=$2C (line $12C)
		0x0180, 0x0F00, // MOVE COLOR00
		0xFFFF, 0xFFFE,
	}, 0xF0)
	for a.Custom.ReadReg16(0x180) != 0x0F00 && a.vpos != 0 {
		a.advanceBeamPerCycle(1)
	}
	if a.vpos != 0x12C {
		t.Errorf("MOVE after the line-255 wrap ran on line %d, want 300", a.vpos)
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

// frameLines runs the beam from the start of a frame to the start of the
// next and returns how many lines it had and the LOF flag seen during it.
func frameLines(a *Amiga, fast bool) (lines int, lof bool) {
	lof = a.lof
	for {
		if fast {
			a.advanceBeamFast(CPUClocksPerLine)
		} else {
			a.advanceBeamPerCycle(CPUClocksPerLine)
		}
		lines++
		if a.vpos == 0 {
			return lines, lof
		}
	}
}

// Non-interlaced PAL frames are all long (313 lines, LOF=1); with LACE set
// they alternate long/short so the two fields interleave.
func TestFrameLengthAndLOF(t *testing.T) {
	for _, fast := range []bool{false, true} {
		a := createTestAmiga(t)
		a.vpos, a.hpos, a.lof = 0, 0, true
		for i := 0; i < 3; i++ {
			if n, lof := frameLines(a, fast); n != 313 || !lof {
				t.Fatalf("fast=%v non-interlaced frame %d: %d lines, LOF=%v; want 313, true", fast, i, n, lof)
			}
		}

		a.Custom.WriteReg16(0x100, 0x9204) // hires, 1 plane, LACE
		var got []int
		for i := 0; i < 4; i++ {
			n, lof := frameLines(a, fast)
			if (n == 313) != lof {
				t.Fatalf("fast=%v frame of %d lines had LOF=%v", fast, n, lof)
			}
			got = append(got, n)
		}
		if got[0] == got[1] || got[0] != got[2] || got[1] != got[3] {
			t.Errorf("fast=%v interlaced frame lengths %v, want alternating 313/312", fast, got)
		}
	}
}

func TestVPOSWSetsLOF(t *testing.T) {
	a := createTestAmiga(t)
	a.lof = true
	a.Custom.Write(0x02A, []byte{0x00, 0x00})
	if a.lof {
		t.Fatal("VPOSW $0000 left LOF set")
	}
	a.Custom.Write(0x02A, []byte{0x80, 0x00})
	if !a.lof {
		t.Fatal("VPOSW $8000 did not set LOF")
	}
}

// OCS Copper register protection: $000-$03E never, $040-$07E only with
// COPCON CDANG; an illegal MOVE halts the Copper until it is restarted.
func TestCopperIllegalMoveHalts(t *testing.T) {
	for _, c := range []struct {
		name  string
		reg   uint16
		cdang bool
		halts bool
	}{
		{"$000 (address 0's first word)", 0x000, false, true},
		{"$020 even with CDANG", 0x020, true, true},
		{"BLTCON0 without CDANG", 0x040, false, true},
		{"BLTCON0 with CDANG", 0x040, true, false},
		{"COLOR00", 0x180, false, false},
	} {
		a := startCopper(t, []uint16{
			c.reg, 0x1234,
			0x0182, 0x0ABC, // MOVE COLOR01: runs only if not halted
			0xFFFF, 0xFFFE,
		}, 0x40)
		if c.cdang {
			a.Custom.WriteReg16(0x02E, 0x0002)
		}
		a.advanceBeamPerCycle(CPUClocksPerLine)
		if halted := a.Custom.ReadReg16(0x182) != 0x0ABC; halted != c.halts {
			t.Errorf("%s: halted=%v, want %v", c.name, halted, c.halts)
		}
		if c.halts {
			a.Copper.Restart(1)
			if a.Copper.halted {
				t.Errorf("%s: still halted after restart", c.name)
			}
		}
	}
}
