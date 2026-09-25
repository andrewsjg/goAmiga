package amiga

import (
	"fmt"
	"testing"
)

// TestAPenOrigin tracks every change to FgPen on the boot RastPort and
// records PC/stack when it first becomes 2 (and subsequent changes).
func TestAPenOrigin(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Reset()

	// We discover RP address dynamically: first time we see a plausible RP
	// with BitMap planes pointing at $A572, freeze that address.
	var rpAddr uint32
	var lastFg byte = 0xFF
	foundRP := false
	changes := 0
	buf1 := make([]byte, 1)
	bb := make([]byte, 4)

	readFg := func(rp uint32) byte {
		a.ChipRAM.Read(int(rp+25), buf1)
		return buf1[0]
	}
	readBM := func(rp uint32) uint32 {
		a.ChipRAM.Read(int(rp+4), bb)
		return uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3])
	}
	readPlanes := func(bm uint32) (p0, p1 uint32) {
		a.ChipRAM.Read(int(bm+8), bb)
		p0 = uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3])
		a.ChipRAM.Read(int(bm+12), bb)
		p1 = uint32(bb[0])<<24 | uint32(bb[1])<<16 | uint32(bb[2])<<8 | uint32(bb[3])
		return
	}

	// Also watch SetAPen entry FC5EAC
	setAPenHits := 0
	const setAPen = uint32(0xFC5EAC)
	const maxSteps = 5_000_000

	for i := 0; i < maxSteps; i++ {
		pc := a.CPU.PC

		// Detect SetAPen entry
		if pc == setAPen {
			setAPenHits++
			rp := a.CPU.A[1]
			pen := byte(a.CPU.D[0])
			if setAPenHits <= 30 || pen == 2 {
				bm := readBM(rp)
				p0, p1 := uint32(0), uint32(0)
				if bm > 0x1000 && bm < ChipRAMSize-16 {
					p0, p1 = readPlanes(bm)
				}
				// stack return address
				sp := a.CPU.A[7]
				sb := make([]byte, 16)
				a.ChipRAM.Read(int(sp), sb)
				ret := uint32(sb[0])<<24 | uint32(sb[1])<<16 | uint32(sb[2])<<8 | uint32(sb[3])
				// deeper stack for call chain
				var chain string
				for j := 0; j < 32; j += 4 {
					a.ChipRAM.Read(int(sp)+j, sb[:4])
					v := uint32(sb[0])<<24 | uint32(sb[1])<<16 | uint32(sb[2])<<8 | uint32(sb[3])
					if v >= 0xFC0000 || (v > 0x1000 && v < 0x80000) {
						chain += fmt.Sprintf(" $%08X", v)
					}
				}
				t.Logf("step=%d SetAPen(pen=%d) RP=$%05X BM=$%05X P0=$%05X P1=$%05X ret=$%08X A2=$%08X A3=$%08X A4=$%08X A5=$%08X A6=$%08X chain:%s",
					i, pen, rp, bm, p0, p1, ret, a.CPU.A[2], a.CPU.A[3], a.CPU.A[4], a.CPU.A[5], a.CPU.A[6], chain)

				// If this is pen 2 on a RP that will own our display bitmap
				if pen == 2 && (p0 == 0xA572 || p1 == 0xC4B2 || (bm > 0 && bm < 0x10000)) {
					if !foundRP && p0 == 0xA572 {
						foundRP = true
						rpAddr = rp
						t.Logf("  *** display RP discovered at $%05X via SetAPen(2)", rpAddr)
					}
				}
			}
		}

		// Discover RP by scanning known address once bitmap is set up
		if !foundRP && i > 2_000_000 && i%1000 == 0 {
			// Check $A4D2 which we previously observed
			bm := readBM(0xA4D2)
			if bm > 0x1000 && bm < ChipRAMSize-16 {
				p0, p1 := readPlanes(bm)
				if p0 == 0xA572 && p1 == 0xC4B2 {
					foundRP = true
					rpAddr = 0xA4D2
					lastFg = readFg(rpAddr)
					t.Logf("step=%d discovered RP $A4D2 Fg=%d BM=$%05X", i, lastFg, bm)
				}
			}
		}

		if foundRP {
			fg := readFg(rpAddr)
			if fg != lastFg {
				changes++
				sp := a.CPU.A[7]
				sb := make([]byte, 48)
				a.ChipRAM.Read(int(sp), sb)
				var stack string
				for j := 0; j < 48; j += 4 {
					v := uint32(sb[j])<<24 | uint32(sb[j+1])<<16 | uint32(sb[j+2])<<8 | uint32(sb[j+3])
					stack += fmt.Sprintf(" $%08X", v)
				}
				t.Logf("step=%d FgPen %d→%d PC=$%08X A0=$%08X A1=$%08X A2=$%08X stack:%s",
					i, lastFg, fg, pc, a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], stack)
				lastFg = fg
				if changes >= 15 {
					break
				}
			}
		}

		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	t.Logf("SetAPen hits=%d FgPen changes=%d final RP=$%05X Fg=%d", setAPenHits, changes, rpAddr, lastFg)
}
