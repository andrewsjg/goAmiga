package amiga

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"
)

// TestCaptureFrame runs the emulator with DisplayActive=true for a limited
// number of RunFrame calls to capture what the display looks like at various
// boot stages. Saves PNG screenshots.
func TestCaptureFrame(t *testing.T) {
	requireDiag(t)
	romData, err := os.ReadFile("../ROMS/Kick13.rom")
	if err != nil {
		t.Skipf("Cannot load ROM: %v", err)
	}
	a, err := New(romData, nil)
	if err != nil {
		t.Fatalf("Error creating Amiga: %v", err)
	}
	diskData, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skipf("Cannot load disk: %v", err)
	}
	if err := a.Disk.InsertDisk(diskData); err != nil {
		t.Fatalf("Error inserting disk: %v", err)
	}
	if err := a.Reset(); err != nil {
		t.Fatalf("Error resetting: %v", err)
	}

	a.Video.DisplayActive = true

	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])

	// Run frame by frame, capturing screenshots at interesting points
	screenshots := []int{10, 50, 100, 200, 500, 1000, 2000, 5000}
	nextShot := 0

	for frame := 0; frame < 6000; frame++ {
		if err := a.RunFrame(); err != nil {
			fmt.Printf("Frame %d: error %v\n", frame, err)
			break
		}

		bplcon0 := a.Custom.ReadReg16(0x100)
		dmacon := a.Custom.ReadReg16(0x096)
		numPlanes := (bplcon0 >> 12) & 0x7

		if frame%500 == 0 || (nextShot < len(screenshots) && frame == screenshots[nextShot]) {
			fmt.Printf("Frame %d: BPLCON0=$%04X planes=%d DMACON=$%04X COP1LC=$%08X PC=$%08X\n",
				frame, bplcon0, numPlanes, dmacon, a.Copper.cop1lc, a.CPU.PC)

			// Print copper list if it has real content
			if a.Copper.cop1lc != 0 && a.Copper.cop1lc < ChipRAMSize {
				buf := make([]byte, 4)
				a.ChipRAM.Read(int(a.Copper.cop1lc), buf)
				ir1 := binary.BigEndian.Uint16(buf[0:2])
				if ir1 != 0 { // Non-empty copper list
					fmt.Printf("  Copper list:\n")
					addr := a.Copper.cop1lc
					for i := 0; i < 30; i++ {
						if addr >= ChipRAMSize {
							break
						}
						a.ChipRAM.Read(int(addr), buf)
						ir1 = binary.BigEndian.Uint16(buf[0:2])
						ir2 := binary.BigEndian.Uint16(buf[2:4])
						if ir1 == 0xFFFF && ir2 == 0xFFFE {
							fmt.Printf("    $%06X: WAIT END\n", addr)
							break
						}
						if ir1&1 == 0 {
							regOff := int(ir1 & 0x1FE)
							name := customRegNames[regOff]
							if name == "" {
								name = fmt.Sprintf("$%03X", regOff)
							}
							fmt.Printf("    $%06X: MOVE %s = $%04X\n", addr, name, ir2)
						} else if ir2&1 == 0 {
							vp := (ir1 >> 8) & 0xFF
							hp := (ir1 & 0xFE) >> 1
							fmt.Printf("    $%06X: WAIT V>=%d H>=%d\n", addr, vp, hp)
						}
						addr += 4
					}
				}
			}

			// Print video regs and bitplane pointers
			for p := 0; p < 6; p++ {
				if a.Video.bplpt[p] != 0 {
					fmt.Printf("  BPL%dPT=$%08X\n", p+1, a.Video.bplpt[p])
				}
			}
			fmt.Printf("  BPLCON1=$%04X BPL1MOD=$%04X BPL2MOD=$%04X\n",
				a.Custom.ReadReg16(0x102),
				a.Custom.ReadReg16(0x108), a.Custom.ReadReg16(0x10A))
		}

		if nextShot < len(screenshots) && frame == screenshots[nextShot] {
			nextShot++

			// Save screenshot if there's visible content
			if numPlanes > 0 || frame > 100 {
				filename := fmt.Sprintf("/tmp/amiga_frame_%04d.png", frame)
				saveScreenshot(a.Video, filename)
				fmt.Printf("  Saved %s\n", filename)
			}
		}
	}

	// Final screenshot
	saveScreenshot(a.Video, "/tmp/amiga_frame_final.png")
	fmt.Println("Saved /tmp/amiga_frame_final.png")

	// Final state dump
	fmt.Printf("\nFinal: BPLCON0=$%04X DMACON=$%04X COP1LC=$%08X PC=$%08X\n",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x096),
		a.Copper.cop1lc, a.CPU.PC)
}

func saveScreenshot(v *Video, filename string) {
	img := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	copy(img.Pix, v.Pixels)
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("  Error creating %s: %v\n", filename, err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
}
