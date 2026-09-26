package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

// TestRenderDiag boots without disk, renders the insert disk screen,
// saves a PNG, and dumps orientation diagnostics.
func TestRenderDiag(t *testing.T) {
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

	const maxSteps = 55_000_000
	checkBuf := make([]byte, 2)
	templateBuf := make([]byte, 2)
	lastBPL1Val := byte(0)
	lastTemplateVal := byte(0)
	prevBusy := false
	currentStep := 0
	blitCount := 0

	// Track ALL blitter operations in the critical window
	for i := 0; i < maxSteps; i++ {
		currentStep = i
		_ = currentStep

		// Detect ALL blitter starts
		if a.Blitter.busy && !prevBusy {
			blitCount++
			// Use pre-blit pointers (before pointer write-back)
			dpt := a.Blitter.preDpt
			apt := a.Blitter.preApt
			con0 := a.Blitter.con0
			sz := a.Blitter.lastSize

			// Log ALL blits from step 4M to 5M
			if i >= 4_000_000 && i <= 5_000_000 {
				h := sz >> 6
				w := sz & 0x3F
				t.Logf("Step %d: BLIT#%d con0=$%04X con1=$%04X apt=$%05X dpt=$%05X sz=%dx%d PC=$%08X",
					i, blitCount, con0, a.Blitter.con1, apt, dpt, h, w, a.CPU.PC)
			}

			// Always log blits to/from template or BPL1
			if dpt == 0x104B8 || (dpt >= 0x104B8 && dpt < 0x104D0) || apt == 0x104B8 {
				t.Logf("Step %d: TEMPLATE-BLIT#%d con0=$%04X dpt=$%05X apt=$%05X sz=%dx%d PC=$%08X",
					i, blitCount, con0, dpt, apt, sz>>6, sz&0x3F, a.CPU.PC)
			}
			if dpt == 0xA572 && byte(con0&0xFF) == 0xEA {
				// Read template to check if it's valid
				a.ChipRAM.Read(0x104B8, templateBuf)
				// Dump call stack and BDAT
				sp := a.CPU.A[7]
				stackBuf := make([]byte, 160)
				a.ChipRAM.Read(int(sp), stackBuf)
				var callStack string
				for j := 0; j < 160; j += 4 {
					addr := uint32(stackBuf[j])<<24 | uint32(stackBuf[j+1])<<16 | uint32(stackBuf[j+2])<<8 | uint32(stackBuf[j+3])
					callStack += fmt.Sprintf(" $%08X", addr)
				}
				t.Logf("Step %d: BPL1-EA-FILL#%d template=$%02X%02X BDAT=$%04X apt=$%05X PC=$%08X stack:%s",
					i, blitCount, templateBuf[0], templateBuf[1], a.Blitter.bdat, apt, a.CPU.PC, callStack)
				// Dump D/A registers
				t.Logf("  Regs: D0=$%08X D1=$%08X D2=$%08X D3=$%08X D4=$%08X D5=$%08X D6=$%08X D7=$%08X",
					a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.D[4], a.CPU.D[5], a.CPU.D[6], a.CPU.D[7])
				t.Logf("  Regs: A0=$%08X A1=$%08X A2=$%08X A3=$%08X A4=$%08X A5=$%08X A6=$%08X A7=$%08X",
					a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3], a.CPU.A[4], a.CPU.A[5], a.CPU.A[6], a.CPU.A[7])
				// Dump code at $5C00-$5C80 (caller)
				codeBuf := make([]byte, 128)
				a.ChipRAM.Read(0x5C00, codeBuf)
				var codeStr string
				for j := 0; j < 128; j += 2 {
					if j%32 == 0 {
						codeStr += fmt.Sprintf("\n    $%04X:", 0x5C00+j)
					}
					codeStr += fmt.Sprintf(" %02X%02X", codeBuf[j], codeBuf[j+1])
				}
				t.Logf("  Code@$5C00:%s", codeStr)
			}
		}
		prevBusy = a.Blitter.busy

		if err := a.Step(); err != nil {
			t.Fatalf("Failed at step %d: %v", i, err)
		}

		// Monitor template value at $104B8 — every step in critical range
		if i >= 4_030_000 && i <= 4_110_000 {
			a.ChipRAM.Read(0x104B8, templateBuf)
			if templateBuf[0] != lastTemplateVal {
				t.Logf("Step %d: TEMPLATE $%02X→$%02X (PC=$%08X busy=%v)",
					i, lastTemplateVal, templateBuf[0], a.CPU.PC, a.Blitter.busy)
				lastTemplateVal = templateBuf[0]
			}
		} else if i%100 == 0 && i >= 3_400_000 && i <= 5_100_000 {
			a.ChipRAM.Read(0x104B8, templateBuf)
			if templateBuf[0] != lastTemplateVal {
				t.Logf("Step %d: TEMPLATE $%02X→$%02X (PC=$%08X busy=%v)",
					i, lastTemplateVal, templateBuf[0], a.CPU.PC, a.Blitter.busy)
				lastTemplateVal = templateBuf[0]
			}
		}

		// Monitor BPL1 value at $A572
		if i%500 == 0 {
			a.ChipRAM.Read(0xA572, checkBuf)
			if checkBuf[0] != lastBPL1Val {
				t.Logf("Step %d: BPL1 transition $%02X→$%02X (PC=$%08X)",
					i, lastBPL1Val, checkBuf[0], a.CPU.PC)
				lastBPL1Val = checkBuf[0]
			}
		}
	}

	// Render frames and save PNG
	for f := 0; f < 5; f++ {
		if err := a.RunFrame(); err != nil {
			t.Fatalf("RunFrame failed: %v", err)
		}
	}
	v := a.Video
	if v == nil {
		t.Fatal("Video is nil")
	}
	img := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	copy(img.Pix, v.Pixels)
	f, err := os.Create("/tmp/insertdisk_diag.png")
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
	t.Logf("Saved /tmp/insertdisk_diag.png (%dx%d)", v.Width, v.Height)

	// Check BPL1 final state
	bpl1Buf := make([]byte, 40)
	allFF := true
	for line := 0; line < 200; line++ {
		a.ChipRAM.Read(int(0xA572+uint32(line*40)), bpl1Buf)
		for _, b := range bpl1Buf {
			if b != 0xFF {
				allFF = false
				break
			}
		}
		if !allFF {
			break
		}
	}
	if allFF {
		t.Log("FAIL: BPL1 is entirely $FF — image data overwritten")
	} else {
		t.Log("OK: BPL1 has image data")
	}
	_ = fmt.Sprintf("") // keep fmt import
}
