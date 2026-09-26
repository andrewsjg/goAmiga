package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestBPL1Writes(t *testing.T) {
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

	const bpl1Start = 0xA572
	const bpl1End = 0xC4B2
	const maxSteps = 10_000_000

	step := 0
	imageSeenOnce := false
	firstOverwriteCapture := false

	type blitInfo struct {
		step    int
		con0    uint16
		isImage bool
		isFF    bool
	}
	var timeline []blitInfo
	curStep := -1
	curCon0 := uint16(0)
	curFF := true
	curImg := false

	flush := func() {
		if curStep >= 0 {
			timeline = append(timeline, blitInfo{curStep, curCon0, curImg, curFF})
		}
	}

	a.Blitter.DebugWriteHook = func(addr int, val, aW, bW, cW uint16, mt byte) {
		if addr >= bpl1Start && addr < bpl1End {
			if step != curStep {
				flush()
				curStep = step
				curCon0 = a.Blitter.con0
				curFF = true
				curImg = false
			}
			if val != 0xFFFF {
				curFF = false
			}
			if val != 0xFFFF && val != 0x0000 {
				curImg = true
			}
		}
	}

	var savedImagePNG bool

	for i := 0; i < maxSteps; i++ {
		step = i
		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		// After a known IMAGE blit, save BPL1 as a diagnostic image
		if !savedImagePNG && i > 4300000 && i%1000 == 0 {
			// Check if BPL1 line 96 has image data
			buf := make([]byte, 2)
			a.ChipRAM.Read(0xB482, buf)
			v := uint16(buf[0])<<8 | uint16(buf[1])
			if v != 0xFFFF && v != 0x0000 {
				// BPL1 has image data! Save a snapshot
				saveBPL1Snapshot(t, a, "/tmp/bpl1_with_image.png")
				savedImagePNG = true
				t.Logf("Saved BPL1 snapshot at step %d (val=$%04X)", i, v)
			}
		}

		// Detect first overwrite: image was seen, then FILL-$FF happens
		if !firstOverwriteCapture && imageSeenOnce && len(timeline) > 0 {
			last := timeline[len(timeline)-1]
			if last.isFF && !last.isImage {
				// First FILL-$FF after image was drawn
				t.Logf("First overwrite at step %d", last.step)
				sp := a.CPU.A[7]
				stackBuf := make([]byte, 80)
				a.ChipRAM.Read(int(sp), stackBuf)
				var stack string
				for j := 0; j < 80; j += 4 {
					addr := uint32(stackBuf[j])<<24 | uint32(stackBuf[j+1])<<16 | uint32(stackBuf[j+2])<<8 | uint32(stackBuf[j+3])
					stack += fmt.Sprintf(" $%08X", addr)
				}
				t.Logf("  PC=$%08X A7=$%08X", a.CPU.PC, sp)
				t.Logf("  Stack:%s", stack)
				firstOverwriteCapture = true
			}
		}
		if len(timeline) > 0 && timeline[len(timeline)-1].isImage {
			imageSeenOnce = true
		}
	}
	flush()

	t.Logf("BPL1 blit timeline (%d events):", len(timeline))
	for _, e := range timeline {
		label := "FILL"
		if e.isImage {
			label = "IMAGE"
		} else if !e.isFF {
			label = "CLEAR"
		}
		t.Logf("  step=%d con0=$%04X %s", e.step, e.con0, label)
	}
	_ = fmt.Sprintf("")
}

func saveBPL1Snapshot(t *testing.T, a *Amiga, path string) {
	t.Helper()
	// Render BPL1+BPL2 as a 320x200 2bpp image
	const w, h = 320, 200
	bpl1 := uint32(0xA572)
	bpl2 := uint32(0xC4B2)
	colors := [4][3]byte{
		{255, 255, 255},    // COLOR00 white
		{0, 0, 0},          // COLOR01 black
		{0x77, 0x77, 0xCC}, // COLOR02
		{0xBB, 0xBB, 0xBB}, // COLOR03
	}
	// Read actual palette
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
		a.ChipRAM.Read(int(bpl1+uint32(y*40)), buf)
		a.ChipRAM.Read(int(bpl2+uint32(y*40)), buf2)
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
		t.Log("Failed to save:", err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
}
