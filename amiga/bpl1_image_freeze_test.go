package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

// TestBPL1ImageFreeze boots Kickstart 1.3 (no disk) and freezes BPL1 when:
//  1. first non-solid ("IMAGE") content appears
//  2. peak non-solid word count
//  3. first solid FILL after image was present
//  4. final state
//
// Logs every blit that writes into the display BPL1 region with full channel
// parameters for comparison against graphics.library SetRast / BltTemplate.
func TestBPL1ImageFreeze(t *testing.T) {
	romData, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip("Kick13.rom not found:", err)
	}
	a, err := New(romData, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}

	const (
		bpl1Start = 0xA572
		bpl1Bytes = 200 * 40 // 8000
		bpl1End   = bpl1Start + bpl1Bytes
		bpl2Start = 0xC4B2
		maxSteps  = 12_000_000
	)

	type blitEvent struct {
		step               int
		con0, con1         uint16
		apt, bpt, cpt, dpt uint32
		h, w               int
		pc                 uint32
		kind               string
		sample             uint16
		aW, bW, cW         uint16 // first word channels
	}

	var events []blitEvent
	step := 0

	// Active blit classification
	var (
		active                    bool
		curCon0                   uint16
		curCon1                   uint16
		curApt                    uint32
		curBpt                    uint32
		curCpt                    uint32
		curDpt                    uint32
		curH, curW                int
		curPC                     uint32
		curStep                   int
		nFF, n00, nOther          int
		sample                    uint16
		firstAW, firstBW, firstCW uint16
		gotChannelSample          bool
	)

	classify := func() string {
		if nOther > 0 {
			return "IMAGE"
		}
		if nFF > 0 && n00 == 0 {
			return "FILL"
		}
		if n00 > 0 && nFF == 0 {
			return "CLEAR"
		}
		if nFF > 0 && n00 > 0 {
			return "IMAGE" // mixed solid extremes still interesting
		}
		return "EMPTY"
	}

	flush := func() {
		if !active {
			return
		}
		events = append(events, blitEvent{
			step: curStep, con0: curCon0, con1: curCon1,
			apt: curApt, bpt: curBpt, cpt: curCpt, dpt: curDpt,
			h: curH, w: curW, pc: curPC, kind: classify(), sample: sample,
			aW: firstAW, bW: firstBW, cW: firstCW,
		})
		active = false
	}

	a.Blitter.DebugWriteHook = func(addr int, val, aW, bW, cW uint16, mt byte) {
		if addr < bpl1Start || addr >= bpl1End {
			return
		}
		if !active || step != curStep {
			flush()
			active = true
			curStep = step
			curCon0 = a.Blitter.con0
			curCon1 = a.Blitter.con1
			curApt = a.Blitter.preApt
			curBpt = a.Blitter.preBpt
			curCpt = a.Blitter.preCpt
			curDpt = a.Blitter.preDpt
			sz := a.Blitter.lastSize
			curH = int(sz >> 6)
			curW = int(sz & 0x3F)
			if curH == 0 {
				curH = 1024
			}
			if curW == 0 {
				curW = 64
			}
			curPC = a.CPU.PC
			nFF, n00, nOther = 0, 0, 0
			sample = val
			firstAW, firstBW, firstCW = aW, bW, cW
			gotChannelSample = true
		}
		switch val {
		case 0xFFFF:
			nFF++
		case 0x0000:
			n00++
		default:
			nOther++
			sample = val
		}
		_ = gotChannelSample
	}

	bplWordStats := func() (ff, zero, other int) {
		buf := make([]byte, 2)
		for off := bpl1Start; off < bpl1End; off += 2 {
			a.ChipRAM.Read(off, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			switch v {
			case 0xFFFF:
				ff++
			case 0x0000:
				zero++
			default:
				other++
			}
		}
		return
	}

	saveSnap := func(path, label string) {
		ff, zero, other := bplWordStats()
		t.Logf("%s [step %d]: BPL1 words FF=%d zero=%d other=%d", label, step, ff, zero, other)
		if err := saveBPL12PNG(a, path, bpl1Start, bpl2Start); err != nil {
			t.Logf("  save %s failed: %v", path, err)
		} else {
			t.Logf("  saved %s", path)
		}
	}

	var (
		imageSeen      bool
		firstImageStep int
		peakOther      int
		savedFirst     bool
		savedPeak      bool
		savedAfterOW   bool
		prevBusy       bool
		prevHadImage   bool
	)

	for i := 0; i < maxSteps; i++ {
		step = i
		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v PC=$%08X", i, err, a.CPU.PC)
		}

		// Blit just completed
		if prevBusy && !a.Blitter.busy {
			flush()
			ff, zero, other := bplWordStats()
			_ = zero
			if other > 0 {
				if !imageSeen {
					imageSeen = true
					firstImageStep = i
					t.Logf("FIRST IMAGE content at step %d (other=%d FF=%d)", i, other, ff)
					if len(events) > 0 {
						e := events[len(events)-1]
						t.Logf("  last blit: kind=%s con0=$%04X apt=$%05X cpt=$%05X dpt=$%05X sz=%dx%d A=$%04X B=$%04X C=$%04X PC=$%08X",
							e.kind, e.con0, e.apt, e.cpt, e.dpt, e.h, e.w, e.aW, e.bW, e.cW, e.pc)
					}
				}
				if !savedFirst {
					saveSnap("/tmp/bpl1_first_image.png", "first IMAGE")
					savedFirst = true
				}
				if other > peakOther {
					peakOther = other
					if other > 20 {
						saveSnap("/tmp/bpl1_peak_image.png", fmt.Sprintf("peak IMAGE other=%d", other))
						savedPeak = true
					}
				}
				prevHadImage = true
			} else if prevHadImage && ff > 3000 && !savedAfterOW {
				// Solid fill overwrote prior image
				if len(events) > 0 {
					e := events[len(events)-1]
					t.Logf("OVERWRITE to solid at step %d by blit kind=%s con0=$%04X apt=$%05X dpt=$%05X sz=%dx%d PC=$%08X",
						i, e.kind, e.con0, e.apt, e.dpt, e.h, e.w, e.pc)
					// Dump call stack
					sp := a.CPU.A[7]
					stackBuf := make([]byte, 64)
					a.ChipRAM.Read(int(sp), stackBuf)
					var stack string
					for j := 0; j < 64; j += 4 {
						addr := uint32(stackBuf[j])<<24 | uint32(stackBuf[j+1])<<16 |
							uint32(stackBuf[j+2])<<8 | uint32(stackBuf[j+3])
						stack += fmt.Sprintf(" $%08X", addr)
					}
					t.Logf("  PC=$%08X SP=$%08X stack:%s", a.CPU.PC, sp, stack)
				}
				saveSnap("/tmp/bpl1_after_overwrite.png", "after overwrite")
				savedAfterOW = true
			}
		}
		prevBusy = a.Blitter.busy
	}
	flush()

	saveSnap("/tmp/bpl1_final.png", "final")

	t.Logf("=== BPL1-targeting blit log (%d events) ===", len(events))
	for _, e := range events {
		use := ""
		if e.con0&0x0800 != 0 {
			use += "A"
		} else {
			use += "-"
		}
		if e.con0&0x0400 != 0 {
			use += "B"
		} else {
			use += "-"
		}
		if e.con0&0x0200 != 0 {
			use += "C"
		} else {
			use += "-"
		}
		if e.con0&0x0100 != 0 {
			use += "D"
		} else {
			use += "-"
		}
		t.Logf("  step=%7d %-5s con0=$%04X con1=$%04X min=$%02X USE=%s apt=$%05X bpt=$%05X cpt=$%05X dpt=$%05X sz=%3dx%2d sample=$%04X A=$%04X B=$%04X C=$%04X PC=$%08X",
			e.step, e.kind, e.con0, e.con1, e.con0&0xFF, use,
			e.apt, e.bpt, e.cpt, e.dpt, e.h, e.w, e.sample, e.aW, e.bW, e.cW, e.pc)
	}

	if !imageSeen {
		t.Error("no IMAGE content ever appeared in BPL1")
	} else {
		t.Logf("Summary: first IMAGE step=%d peakOther=%d savedFirst=%v savedPeak=%v savedAfterOW=%v",
			firstImageStep, peakOther, savedFirst, savedPeak, savedAfterOW)
	}
}

func saveBPL12PNG(a *Amiga, path string, bpl1, bpl2 uint32) error {
	const w, h = 320, 200
	colors := [4][3]byte{
		{255, 255, 255},
		{0, 0, 0},
		{0x77, 0x77, 0xCC},
		{0xBB, 0xBB, 0xBB},
	}
	for i := 0; i < 4; i++ {
		rgb := a.Custom.ReadReg16(0x180 + i*2)
		colors[i] = [3]byte{
			byte(((rgb >> 8) & 0xF) * 17),
			byte(((rgb >> 4) & 0xF) * 17),
			byte((rgb & 0xF) * 17),
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	buf := make([]byte, 40)
	buf2 := make([]byte, 40)
	for y := 0; y < h; y++ {
		a.ChipRAM.Read(int(bpl1)+y*40, buf)
		a.ChipRAM.Read(int(bpl2)+y*40, buf2)
		for x := 0; x < w; x++ {
			byteIdx := x / 8
			bitIdx := uint(7 - (x % 8))
			var idx byte
			if buf[byteIdx]&(1<<bitIdx) != 0 {
				idx |= 1
			}
			if buf2[byteIdx]&(1<<bitIdx) != 0 {
				idx |= 2
			}
			c := colors[idx]
			off := (y*w + x) * 4
			img.Pix[off] = c[0]
			img.Pix[off+1] = c[1]
			img.Pix[off+2] = c[2]
			img.Pix[off+3] = 255
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
