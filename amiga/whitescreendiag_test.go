package amiga

import (
	"fmt"
	"os"
	"testing"
)

// TestWhiteScreenDiag runs boot and dumps display state after several frames.
func TestWhiteScreenDiag(t *testing.T) {
	romData, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip("Kick13.rom not found:", err)
	}
	a, err := New(romData, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Try loading disk (optional)
	for _, p := range []string{
		"../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf",
		"../wb13.adf",
	} {
		if data, err2 := os.ReadFile(p); err2 == nil {
			if err3 := a.Disk.InsertDisk(data); err3 == nil {
				t.Logf("Inserted disk: %s", p)
				break
			}
		}
	}

	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}

	// Run 100 frames (~2 seconds of PAL)
	for frame := 0; frame < 100; frame++ {
		if err := a.RunFrame(); err != nil {
			t.Logf("Error at frame %d: %v", frame, err)
			break
		}

		// Dump display state at frames 10, 50, 99
		if frame == 10 || frame == 50 || frame == 99 {
			dumpDisplayState(t, a, frame)
		}
	}
}

func dumpDisplayState(t *testing.T, a *Amiga, frame int) {
	t.Helper()

	// BPLCON0: tells us about display mode
	bplcon0 := a.Custom.ReadReg16(0x100)
	numPlanes := (bplcon0 >> 12) & 0x7
	hires := bplcon0&0x8000 != 0

	// DMACON
	dmacon := a.Custom.ReadReg16(0x096)
	dmaen := dmacon&0x0200 != 0
	bplen := dmacon&0x0100 != 0
	copen := dmacon&0x0080 != 0
	blten := dmacon&0x0040 != 0

	// Colors
	color00 := a.Custom.ReadReg16(0x180)
	color01 := a.Custom.ReadReg16(0x182)
	color02 := a.Custom.ReadReg16(0x184)
	color03 := a.Custom.ReadReg16(0x186)

	// Display window
	diwstrt := a.Custom.ReadReg16(0x08E)
	diwstop := a.Custom.ReadReg16(0x090)
	ddfstrt := a.Custom.ReadReg16(0x092)
	ddfstop := a.Custom.ReadReg16(0x094)

	// Bitplane pointers
	bpl1ptH := a.Custom.ReadReg16(0x0E0)
	bpl1ptL := a.Custom.ReadReg16(0x0E2)
	bpl1pt := uint32(bpl1ptH)<<16 | uint32(bpl1ptL)
	bpl2ptH := a.Custom.ReadReg16(0x0E4)
	bpl2ptL := a.Custom.ReadReg16(0x0E6)
	bpl2pt := uint32(bpl2ptH)<<16 | uint32(bpl2ptL)

	// Copper list
	cop1lc := a.Copper.cop1lc
	cop2lc := a.Copper.cop2lc
	copPC := a.Copper.pc

	// INTENA/INTREQ
	intena := a.Custom.ReadReg16(0x09A)
	intreq := a.Custom.ReadReg16(0x09C)

	// Disk state
	diskStatus := "no disk"
	if a.Disk.adf != nil {
		diskStatus = "disk present"
	}

	fmt.Printf("\n=== DISPLAY STATE at frame %d ===\n", frame)
	fmt.Printf("  BPLCON0=$%04X (planes=%d hires=%v)\n", bplcon0, numPlanes, hires)
	fmt.Printf("  DMACON=$%04X (DMAEN=%v BPLEN=%v COPEN=%v BLTEN=%v)\n",
		dmacon, dmaen, bplen, copen, blten)
	fmt.Printf("  DIWSTRT=$%04X DIWSTOP=$%04X DDFSTRT=$%04X DDFSTOP=$%04X\n",
		diwstrt, diwstop, ddfstrt, ddfstop)
	fmt.Printf("  COLOR00=$%04X COLOR01=$%04X COLOR02=$%04X COLOR03=$%04X\n",
		color00, color01, color02, color03)
	fmt.Printf("  BPL1PT=$%08X BPL2PT=$%08X\n", bpl1pt, bpl2pt)
	fmt.Printf("  COP1LC=$%08X COP2LC=$%08X CopperPC=$%08X\n", cop1lc, cop2lc, copPC)
	fmt.Printf("  INTENA=$%04X INTREQ=$%04X\n", intena, intreq)
	fmt.Printf("  Disk: %s motor=%v selected=%v cylinder=%d diskChange=%v\n",
		diskStatus, a.Disk.motor, a.Disk.selected, a.Disk.cylinder, a.Disk.diskChange)

	// Dump first 20 words of Copper list if COP1LC is set
	if cop1lc != 0 && cop1lc < ChipRAMSize {
		fmt.Printf("  Copper list at $%08X:\n", cop1lc)
		buf := make([]byte, 2)
		for i := 0; i < 40; i += 2 {
			addr := int(cop1lc) + i*2
			if addr+1 < ChipRAMSize {
				a.ChipRAM.Read(addr, buf)
				w := uint16(buf[0])<<8 | uint16(buf[1])
				if i%4 == 0 {
					fmt.Printf("    $%08X:", addr)
				}
				fmt.Printf(" %04X", w)
				if i%4 == 2 {
					// Decode
					ir1 := uint16(0)
					a.ChipRAM.Read(addr-2, buf)
					ir1 = uint16(buf[0])<<8 | uint16(buf[1])
					if ir1&1 == 0 {
						// MOVE: reg=(ir1&0x1FE), val=w
						reg := ir1 & 0x1FE
						if name, ok := customRegNames[int(reg)]; ok {
							fmt.Printf("  ; %s = $%04X", name, w)
						} else {
							fmt.Printf("  ; reg $%03X = $%04X", reg, w)
						}
					} else {
						// WAIT: vpos=(ir1>>8), hpos=ir1&0xFE
						vp := ir1 >> 8
						hp := ir1 & 0xFE
						if ir1 == 0xFFFF && w == 0xFFFE {
							fmt.Printf("  ; END")
						} else {
							fmt.Printf("  ; WAIT vpos>=%d hpos>=%d", vp, hp)
						}
					}
					fmt.Println()
				}
			}
		}
	}

	// Check what pixels look like
	if a.Video != nil && len(a.Video.Pixels) > 0 {
		// Sample pixels at center of screen
		centerY := a.Video.Height / 2
		centerX := a.Video.Width / 2
		pixOff := (centerY*a.Video.Width + centerX) * 4
		if pixOff+3 < len(a.Video.Pixels) {
			r := a.Video.Pixels[pixOff]
			g := a.Video.Pixels[pixOff+1]
			b := a.Video.Pixels[pixOff+2]
			fmt.Printf("  Center pixel (%d,%d): R=%d G=%d B=%d\n", centerX, centerY, r, g, b)
		}
		// Check if all pixels are the same
		sameCount := 0
		firstR := a.Video.Pixels[0]
		firstG := a.Video.Pixels[1]
		firstB := a.Video.Pixels[2]
		for y := 0; y < a.Video.Height; y++ {
			for x := 0; x < a.Video.Width; x++ {
				off := (y*a.Video.Width + x) * 4
				if a.Video.Pixels[off] == firstR &&
					a.Video.Pixels[off+1] == firstG &&
					a.Video.Pixels[off+2] == firstB {
					sameCount++
				}
			}
		}
		total := a.Video.Width * a.Video.Height
		fmt.Printf("  Pixels: %d/%d same as (0,0) color=(%d,%d,%d) → %s\n",
			sameCount, total, firstR, firstG, firstB,
			func() string {
				if sameCount == total {
					return "SOLID COLOR"
				}
				return "MIXED"
			}())
	}
}
