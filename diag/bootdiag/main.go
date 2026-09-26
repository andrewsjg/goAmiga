package main

import (
	"encoding/binary"
	"fmt"
	"os"

	m68k "github.com/cavaliercoder/go-m68k"
	"github.com/cavaliercoder/goAmiga/amiga"
)

// boot_diag runs the emulator headless and monitors for:
// 1. Guru Meditation (Alert call) - traps into exec Alert vector
// 2. Video register state at various boot stages
// 3. Copper list contents at the point workbench is displayed
func main() {
	romData, err := amiga.LoadROM("ROMS/Kick13.rom")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	a, err := amiga.New(romData, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := a.LoadDisk("disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := a.Reset(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])

	// Track Alert calls. In Kickstart 1.3, Alert is at the exec library
	// Alert vector. We'll look for the distinctive pattern of writing
	// $7FF (red) to COLOR00 which is what the Guru Meditation does.

	var alertPC uint32
	var alertD7 uint32
	guruDetected := false
	lastBPLCON0 := uint16(0)
	dumpCount := 0

	for step := 0; step < 200_000_000; step++ {
		pc := a.CPU.PC

		// Before each step, check if we're at a known Alert entry point
		// Guru Meditation writes alternating red/black border colors
		// Also detect exception to vectors 2/3 (Bus/Address error)
		if err := a.Step(); err != nil {
			fmt.Printf("\n=== CPU ERROR at step %d, PC=$%08X ===\n", step, pc)
			fmt.Printf("Error: %v\n", err)
			fmt.Printf("D0=$%08X D1=$%08X D2=$%08X D3=$%08X\n", a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3])
			fmt.Printf("D4=$%08X D5=$%08X D6=$%08X D7=$%08X\n", a.CPU.D[4], a.CPU.D[5], a.CPU.D[6], a.CPU.D[7])
			fmt.Printf("A0=$%08X A1=$%08X A2=$%08X A3=$%08X\n", a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3])
			fmt.Printf("A4=$%08X A5=$%08X A6=$%08X SP=$%08X\n", a.CPU.A[4], a.CPU.A[5], a.CPU.A[6], a.CPU.A[7])
			fmt.Printf("SR=$%04X\n", a.CPU.SR)
			break
		}

		// Detect Guru Meditation: Alert writes $0F00 (red) to COLOR00 ($DFF180)
		// The Kickstart 1.3 Alert routine writes red/black to COLOR00 alternately
		// We detect writes to COLOR00 with value 0x0F00 during the alert

		newPC := a.CPU.PC

		// Check if we're in ROM space and writing red to COLOR00
		// The Alert function in KS 1.3 is at approximately $FC0BA0-$FC0C60
		// Also detect via the pattern: MOVE.W #$0F00,$DFF180
		if newPC >= 0xFC0000 && newPC < 0xFFFFFF {
			// Read the current instruction at PC to see if it's a MOVE to COLOR00
			buf := make([]byte, 2)
			a.ChipRAM.Read(0, buf) // dummy read to check ChipRAM accessibility
		}

		// Monitor COLOR00 for red (Guru indicator)
		color00 := a.Custom.ReadReg16(0x180)
		if color00 == 0x0F00 && !guruDetected {
			guruDetected = true
			alertPC = pc
			alertD7 = a.CPU.D[7]
			fmt.Printf("\n=== GURU MEDITATION DETECTED at step %d ===\n", step)
			fmt.Printf("PC=$%08X (instruction before COLOR00 turned red)\n", alertPC)
			fmt.Printf("Current PC=$%08X\n", newPC)
			fmt.Printf("D0=$%08X D1=$%08X D2=$%08X D3=$%08X\n", a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3])
			fmt.Printf("D4=$%08X D5=$%08X D6=$%08X D7=$%08X\n", a.CPU.D[4], a.CPU.D[5], a.CPU.D[6], a.CPU.D[7])
			fmt.Printf("A0=$%08X A1=$%08X A2=$%08X A3=$%08X\n", a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3])
			fmt.Printf("A4=$%08X A5=$%08X A6=$%08X SP=$%08X\n", a.CPU.A[4], a.CPU.A[5], a.CPU.A[6], a.CPU.A[7])
			fmt.Printf("SR=$%04X\n", a.CPU.SR)

			// Dump the stack to find the return address
			fmt.Printf("\nStack dump:\n")
			sp := a.CPU.A[7]
			for i := 0; i < 32; i++ {
				buf := make([]byte, 4)
				a.ChipRAM.Read(int(sp+uint32(i*4)), buf)
				val := binary.BigEndian.Uint32(buf)
				fmt.Printf("  SP+%02X: $%08X\n", i*4, val)
			}

			// Read the alert number from D7 (standard Kickstart convention)
			fmt.Printf("\nAlert number (D7): $%08X\n", alertD7)
			if alertD7&0x80000000 != 0 {
				fmt.Printf("  DEADEND ALERT (unrecoverable)\n")
			} else {
				fmt.Printf("  Recoverable alert\n")
			}
			fmt.Printf("  Subsystem: $%04X\n", (alertD7>>16)&0x7FFF)
			fmt.Printf("  General error: $%02X\n", (alertD7>>8)&0xFF)
			fmt.Printf("  Specific error: $%02X\n", alertD7&0xFF)
		}

		// Monitor BPLCON0 changes
		bplcon0 := a.Custom.ReadReg16(0x100)
		if bplcon0 != lastBPLCON0 {
			lastBPLCON0 = bplcon0
			numPlanes := (bplcon0 >> 12) & 0x7
			hires := bplcon0 & 0x8000
			if numPlanes > 0 && dumpCount < 20 {
				dumpCount++
				fmt.Printf("\nStep %d: BPLCON0=$%04X (planes=%d, hires=%v)\n", step, bplcon0, numPlanes, hires != 0)
				fmt.Printf("  BPLCON1=$%04X BPLCON2=$%04X\n",
					a.Custom.ReadReg16(0x102), a.Custom.ReadReg16(0x104))
				fmt.Printf("  DIWSTRT=$%04X DIWSTOP=$%04X\n",
					a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090))
				fmt.Printf("  DDFSTRT=$%04X DDFSTOP=$%04X\n",
					a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094))
				fmt.Printf("  BPL1MOD=$%04X BPL2MOD=$%04X\n",
					a.Custom.ReadReg16(0x108), a.Custom.ReadReg16(0x10A))
				for p := 0; p < int(numPlanes); p++ {
					fmt.Printf("  BPL%dPT=$%08X\n", p+1, a.Video.GetBplPt(p))
				}
				fmt.Printf("  DMACON=$%04X\n", a.Custom.ReadReg16(0x096))
			}
		}

		// After guru detected, run 1M more steps then dump copper list & video state
		if guruDetected && step > 0 && step%50_000_000 == 0 {
			dumpVideoState(a, step)
		}

		_ = alertPC
		_ = alertD7
	}

	if !guruDetected {
		fmt.Println("\nNo Guru Meditation detected in 200M steps")
	}

	// Final copper list dump
	fmt.Printf("\n=== Final Video State ===\n")
	dumpVideoState(a, 200_000_000)
	dumpCopperList(a)

	// Dump the exception vectors to see what's installed
	fmt.Printf("\n=== Exception Vectors ===\n")
	for v := 2; v <= 11; v++ {
		buf := make([]byte, 4)
		a.ChipRAM.Read(v*4, buf)
		vec := binary.BigEndian.Uint32(buf)
		vecNames := map[int]string{
			2: "Bus Error", 3: "Address Error", 4: "Illegal Instruction",
			5: "Divide by Zero", 6: "CHK", 7: "TRAPV",
			8: "Privilege Violation", 9: "Trace", 10: "Line-A", 11: "Line-F",
		}
		fmt.Printf("  Vector %d (%s): $%08X\n", v, vecNames[v], vec)
	}

	// Check if CPU is stopped
	fmt.Printf("\nCPU state: PC=$%08X RunState=%d\n", a.CPU.PC, a.CPU.RunState())
	if a.CPU.RunState() == m68k.RunStateStopped {
		fmt.Println("CPU is STOPPED (waiting for interrupt)")
	}
}

func dumpVideoState(a *amiga.Amiga, step int) {
	fmt.Printf("\n--- Video State at step %d ---\n", step)
	fmt.Printf("BPLCON0=$%04X BPLCON1=$%04X BPLCON2=$%04X\n",
		a.Custom.ReadReg16(0x100), a.Custom.ReadReg16(0x102), a.Custom.ReadReg16(0x104))
	fmt.Printf("DIWSTRT=$%04X DIWSTOP=$%04X\n",
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090))
	fmt.Printf("DDFSTRT=$%04X DDFSTOP=$%04X\n",
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094))
	fmt.Printf("DMACON=$%04X INTENA=$%04X INTREQ=$%04X\n",
		a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x09A), a.Custom.ReadReg16(0x09C))
	fmt.Printf("BPL1MOD=$%04X BPL2MOD=$%04X\n",
		a.Custom.ReadReg16(0x108), a.Custom.ReadReg16(0x10A))

	for p := 0; p < 6; p++ {
		fmt.Printf("BPL%dPT=$%08X\n", p+1, a.Video.GetBplPt(p))
	}

	// Dump COLOR00-COLOR07
	for i := 0; i < 8; i++ {
		fmt.Printf("COLOR%02d=$%04X ", i, a.Custom.ReadReg16(0x180+i*2))
	}
	fmt.Println()
}

func dumpCopperList(a *amiga.Amiga) {
	cop1lc := a.Copper.GetCop1LC()
	fmt.Printf("\n=== Copper List 1 at $%08X ===\n", cop1lc)
	if cop1lc == 0 || cop1lc >= 0x80000 {
		fmt.Println("  (invalid or empty)")
		return
	}
	for i := 0; i < 100; i++ {
		buf := make([]byte, 4)
		addr := cop1lc + uint32(i*4)
		if addr >= 0x80000 {
			break
		}
		a.ChipRAM.Read(int(addr), buf)
		ir1 := binary.BigEndian.Uint16(buf[0:2])
		ir2 := binary.BigEndian.Uint16(buf[2:4])

		if ir1 == 0xFFFF && ir2 == 0xFFFE {
			fmt.Printf("  $%06X: WAIT END-OF-LIST\n", addr)
			break
		}

		if ir1&1 == 0 {
			regOff := int(ir1 & 0x1FE)
			name := ""
			switch {
			case regOff >= 0x180 && regOff <= 0x1BE:
				name = fmt.Sprintf("COLOR%02d", (regOff-0x180)/2)
			case regOff >= 0x0E0 && regOff <= 0x0F6:
				planeIdx := (regOff - 0x0E0) / 4
				if regOff%4 == 0 {
					name = fmt.Sprintf("BPL%dPTH", planeIdx+1)
				} else {
					name = fmt.Sprintf("BPL%dPTL", planeIdx+1)
				}
			default:
				name = fmt.Sprintf("REG $%03X", regOff)
			}
			fmt.Printf("  $%06X: MOVE %s = $%04X\n", addr, name, ir2)
		} else if ir2&1 == 0 {
			vp := (ir1 >> 8) & 0xFF
			hp := (ir1 & 0xFE) >> 1
			vm := ((ir2 >> 8) & 0x7F) | 0x80
			hm := (ir2 & 0xFE) >> 1
			fmt.Printf("  $%06X: WAIT vpos>=%d hpos>=%d (vmask=$%02X hmask=$%02X)\n", addr, vp, hp, vm, hm)
		} else {
			fmt.Printf("  $%06X: SKIP $%04X $%04X\n", addr, ir1, ir2)
		}
	}
}
