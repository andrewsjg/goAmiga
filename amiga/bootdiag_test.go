package amiga

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

// TestBootDiagFast runs the emulator in headless (fast) mode and monitors for
// exception vectors being entered, copper list changes, and bitplane state.
func TestBootDiagFast(t *testing.T) {
	requireDiag(t)
	romData, err := os.ReadFile("../Kick13.rom")
	if err != nil {
		t.Skipf("Cannot load ROM: %v", err)
	}

	a, err := New(romData, nil)
	if err != nil {
		t.Fatalf("Error creating Amiga: %v", err)
	}

	diskData, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skipf("Cannot load disk: %v", err)
	}
	if err := a.Disk.InsertDisk(diskData); err != nil {
		t.Fatalf("Error inserting disk: %v", err)
	}

	if err := a.Reset(); err != nil {
		t.Fatalf("Error resetting: %v", err)
	}

	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])

	// Exception tracking: count entries to known exception vector addresses
	type exInfo struct {
		count     int
		firstPC   uint32 // PC that branched to exception handler
		firstD7   uint32 // D7 at time of exception
		firstSP   uint32
		firstSR   uint32
		firstStep int
	}
	excMap := make(map[int]*exInfo) // vector number → info

	lastPC := a.CPU.PC
	lastCop1lc := uint32(0)
	copperDumps := 0
	lastBPLCON0 := uint16(0)
	lastDMACON := uint16(0)

	maxSteps := 200_000_000

	for step := 0; step < maxSteps; step++ {
		prevPC := a.CPU.PC

		if err := a.Step(); err != nil {
			fmt.Printf("\n=== CPU ERROR at step %d, PC=$%08X ===\n", step, prevPC)
			fmt.Printf("Error: %v\n", err)
			dumpCPUState(a)
			break
		}

		newPC := a.CPU.PC

		// Detect exception handler entry by checking if PC jumped to a known
		// exception vector handler. Check vectors 2-11 (fault exceptions).
		if newPC != lastPC+2 && newPC != lastPC+4 && newPC != lastPC+6 &&
			newPC != lastPC+8 && newPC != lastPC && newPC >= 0xFC0000 {
			// Only check fault-type exceptions (2-11)
			for vecNum := 2; vecNum <= 11; vecNum++ {
				buf := make([]byte, 4)
				a.ChipRAM.Read(vecNum*4, buf)
				vecAddr := binary.BigEndian.Uint32(buf)
				if newPC == vecAddr && vecAddr != 0 {
					info, ok := excMap[vecNum]
					if !ok {
						info = &exInfo{}
						excMap[vecNum] = info
					}
					info.count++
					if info.count == 1 {
						info.firstPC = prevPC
						info.firstD7 = a.CPU.D[7]
						info.firstSP = a.CPU.A[7]
						info.firstSR = a.CPU.SR
						info.firstStep = step
						vecNames := []string{"", "", "Bus Error", "Address Error",
							"Illegal Instruction", "Divide by Zero", "CHK", "TRAPV",
							"Privilege Violation", "Trace", "Line-A", "Line-F"}
						fmt.Printf("\n*** EXCEPTION Vec %d (%s) at step %d ***\n",
							vecNum, vecNames[vecNum], step)
						fmt.Printf("  Handler: $%08X\n", vecAddr)
						fmt.Printf("  From PC: $%08X\n", prevPC)
						dumpCPUState(a)

						// Dump stack to see the exception frame
						fmt.Printf("  Stack:\n")
						sp := a.CPU.A[7]
						sbuf := make([]byte, 4)
						for i := 0; i < 16; i++ {
							addr := sp + uint32(i*4)
							if addr < ChipRAMSize {
								a.ChipRAM.Read(int(addr), sbuf)
							} else {
								a.CPU.M.Read(int(addr), sbuf)
							}
							val := binary.BigEndian.Uint32(sbuf)
							fmt.Printf("    SP+$%02X: $%08X\n", i*4, val)
						}
					}
					break
				}
			}
		}
		lastPC = newPC

		// Track copper list changes (first 10 changes only)
		if a.Copper.cop1lc != lastCop1lc && a.Copper.cop1lc != 0 && copperDumps < 10 {
			copperDumps++
			fmt.Printf("\nStep %d: COP1LC $%08X → $%08X\n", step, lastCop1lc, a.Copper.cop1lc)
			dumpCopperListInternal(a)
			lastCop1lc = a.Copper.cop1lc
		} else if a.Copper.cop1lc != lastCop1lc {
			lastCop1lc = a.Copper.cop1lc
		}

		// Track BPLCON0 transitions (planes on/off)
		bplcon0 := a.Custom.ReadReg16(0x100)
		if bplcon0 != lastBPLCON0 {
			oldPlanes := (lastBPLCON0 >> 12) & 0x7
			newPlanes := (bplcon0 >> 12) & 0x7
			if oldPlanes != newPlanes {
				fmt.Printf("Step %d: planes %d→%d (BPLCON0 $%04X→$%04X) PC=$%08X\n",
					step, oldPlanes, newPlanes, lastBPLCON0, bplcon0, a.CPU.PC)
				if newPlanes > 0 {
					dumpVideoRegs(a)
				}
			}
			lastBPLCON0 = bplcon0
		}

		// Track DMACON BPL DMA changes
		dmacon := a.Custom.ReadReg16(0x096)
		if dmacon != lastDMACON {
			prevBPL := lastDMACON&0x0300 == 0x0300
			newBPL := dmacon&0x0300 == 0x0300
			if prevBPL != newBPL {
				fmt.Printf("Step %d: BPL DMA %v→%v (DMACON $%04X→$%04X) PC=$%08X\n",
					step, prevBPL, newBPL, lastDMACON, dmacon, a.CPU.PC)
			}
			lastDMACON = dmacon
		}

		// Progress
		if step > 0 && step%50_000_000 == 0 {
			fmt.Printf("--- Step %dM: PC=$%08X BPLCON0=$%04X DMACON=$%04X ---\n",
				step/1_000_000, a.CPU.PC, bplcon0, dmacon)
		}
	}

	// Summary
	fmt.Printf("\n=== Exception Summary ===\n")
	vecNames := []string{"", "", "Bus Error", "Address Error",
		"Illegal Instruction", "Divide by Zero", "CHK", "TRAPV",
		"Privilege Violation", "Trace", "Line-A", "Line-F"}
	for vecNum := 2; vecNum <= 11; vecNum++ {
		if info, ok := excMap[vecNum]; ok {
			fmt.Printf("  Vec %d (%s): %d hits, first at step %d from PC=$%08X, D7=$%08X\n",
				vecNum, vecNames[vecNum], info.count, info.firstStep, info.firstPC, info.firstD7)
		}
	}

	fmt.Printf("\n=== Final State ===\n")
	dumpVideoRegs(a)
	dumpCPUState(a)
}

func dumpCPUState(a *Amiga) {
	fmt.Printf("D0=$%08X D1=$%08X D2=$%08X D3=$%08X\n", a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3])
	fmt.Printf("D4=$%08X D5=$%08X D6=$%08X D7=$%08X\n", a.CPU.D[4], a.CPU.D[5], a.CPU.D[6], a.CPU.D[7])
	fmt.Printf("A0=$%08X A1=$%08X A2=$%08X A3=$%08X\n", a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3])
	fmt.Printf("A4=$%08X A5=$%08X A6=$%08X SP=$%08X\n", a.CPU.A[4], a.CPU.A[5], a.CPU.A[6], a.CPU.A[7])
	fmt.Printf("SR=$%04X PC=$%08X\n", a.CPU.SR, a.CPU.PC)
}

func dumpVideoRegs(a *Amiga) {
	fmt.Printf("BPLCON0=$%04X BPLCON1=$%04X BPLCON2=$%04X\n",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x102), a.Custom.ReadReg16(0x104))
	fmt.Printf("DIWSTRT=$%04X DIWSTOP=$%04X DDFSTRT=$%04X DDFSTOP=$%04X\n",
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094))
	fmt.Printf("DMACON=$%04X INTENA=$%04X INTREQ=$%04X\n",
		a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x09A), a.Custom.ReadReg16(0x09C))
	fmt.Printf("BPL1MOD=$%04X BPL2MOD=$%04X\n",
		a.Custom.ReadReg16(0x108), a.Custom.ReadReg16(0x10A))
	for p := 0; p < 6; p++ {
		fmt.Printf("BPL%dPT=$%08X ", p+1, a.Video.bplpt[p])
	}
	fmt.Println()
	for i := 0; i < 8; i++ {
		fmt.Printf("COLOR%02d=$%04X ", i, a.Custom.ReadReg16(0x180+i*2))
	}
	fmt.Println()
}

func dumpCopperListInternal(a *Amiga) {
	addr := a.Copper.cop1lc
	if addr == 0 || addr >= ChipRAMSize {
		fmt.Println("  (invalid)")
		return
	}
	buf := make([]byte, 4)
	for i := 0; i < 200; i++ {
		if addr >= ChipRAMSize {
			break
		}
		a.ChipRAM.Read(int(addr), buf)
		ir1 := binary.BigEndian.Uint16(buf[0:2])
		ir2 := binary.BigEndian.Uint16(buf[2:4])

		if ir1 == 0xFFFF && ir2 == 0xFFFE {
			fmt.Printf("  $%06X: WAIT END\n", addr)
			break
		}

		if ir1&1 == 0 {
			regOff := int(ir1 & 0x1FE)
			name := customRegNames[regOff]
			if name == "" {
				name = fmt.Sprintf("$%03X", regOff)
			}
			fmt.Printf("  $%06X: MOVE %s = $%04X\n", addr, name, ir2)
		} else if ir2&1 == 0 {
			vp := (ir1 >> 8) & 0xFF
			hp := (ir1 & 0xFE) >> 1
			fmt.Printf("  $%06X: WAIT V>=%d H>=%d\n", addr, vp, hp)
		} else {
			fmt.Printf("  $%06X: SKIP $%04X $%04X\n", addr, ir1, ir2)
		}
		addr += 4
	}
}
