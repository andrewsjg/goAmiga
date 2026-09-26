package amiga

import (
	"os"
	"testing"
)

// TestADFBoot loads a Workbench 1.3 ADF and traces the full boot sequence
// past dos.library init to check whether the system makes progress.
func TestADFBoot(t *testing.T) {
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

	// Load disk
	loaded := false
	for _, p := range []string{
		"../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf",
		"../wb13.adf",
	} {
		if data, err := os.ReadFile(p); err == nil {
			if err2 := a.Disk.InsertDisk(data); err2 == nil {
				t.Logf("Inserted disk: %s (%d bytes)", p, len(data))
				loaded = true
				break
			}
		}
	}
	if !loaded {
		t.Skip("No ADF file found")
	}

	const maxSteps = 100_000_000

	// Counters
	doIOCount := 0
	addTaskCount := 0
	dsklenWrites := 0
	fe86ecHit := false
	ff3e94Hit := false
	lastDsklen := 0
	alertCount := 0
	stopCount := 0
	lastPCLog := 0

	// Track CPU cycle budget to verify timing
	totalBeamCycles := uint64(0)

	for i := 0; i < maxSteps; i++ {
		pc := a.CPU.PC

		// Track key events
		switch pc {
		case 0x00FE86EC: // strap JMP (A0) → dos.library
			if !fe86ecHit {
				fe86ecHit = true
				t.Logf("Step %d: STRAP → dos.library A0=$%08X", i, a.CPU.A[0])
			}

		case 0x00FF3E94: // dos.library init entry
			if !ff3e94Hit {
				ff3e94Hit = true
				t.Logf("Step %d: dos.library init entry D0=$%08X A6=$%08X", i, a.CPU.D[0], a.CPU.A[6])
			}

		case 0x0000055C: // AddTask LVO
			addTaskCount++
			if addTaskCount <= 20 {
				namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
				name := readCString(a, uint32(namePtr), 40)
				t.Logf("Step %d: AddTask #%d task=$%08X initPC=$%08X name=%q",
					i, addTaskCount, a.CPU.A[1], a.CPU.A[0], name)
			}

		case 0x000004AE: // DoIO LVO
			doIOCount++
			if doIOCount <= 30 {
				ioCmd, _ := a.CPU.M.Word(int(a.CPU.A[1]) + 0x1C)
				ioLen, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x24)
				ioData, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x28)
				ioOff, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x2C)
				sysBase, _ := a.CPU.M.Long(0x0004)
				curTask, _ := a.CPU.M.Long(int(sysBase) + 0x114)
				taskName := ""
				if curTask != 0 {
					np, _ := a.CPU.M.Long(int(curTask) + 0x0A)
					taskName = readCString(a, uint32(np), 32)
				}
				t.Logf("Step %d: DoIO #%d cmd=$%04X len=$%08X data=$%08X off=$%08X task=%q",
					i, doIOCount, ioCmd, ioLen, ioData, ioOff, taskName)
			}

		case 0x00FC0F90: // STOP #$2000
			stopCount++

		// Alert (guru meditation) — FC009A is a common KS1.3 Alert entry
		case 0x00FC009A:
			alertCount++
			t.Logf("Step %d: ALERT! D7=$%08X (alert code)", i, a.CPU.D[7])
		}

		// Periodically track disk DMA writes
		if a.Disk.dsklenWrites > 0 && a.Disk.dsklenWrites != lastDsklen {
			dsklenWrites++
			if dsklenWrites <= 30 {
				t.Logf("Step %d: DSKLEN write #%d dskpt=$%08X len=$%04X motor=%v cyl=%d side=%d",
					i, dsklenWrites, a.Disk.dskpt, a.Disk.dsklen, a.Disk.motor, a.Disk.cylinder, a.Disk.side)
			}
			lastDsklen = a.Disk.dsklenWrites
		}

		// Count beam cycles
		prevCycles := a.cycles

		if err := a.Step(); err != nil {
			t.Logf("Error at step %d PC=$%08X: %v", i, pc, err)
			break
		}

		totalBeamCycles += a.cycles - prevCycles

		// Log progress every 10M steps
		if i > 0 && i%10_000_000 == 0 {
			sysBase, _ := a.CPU.M.Long(0x0004)
			curTask := uint32(0)
			taskName := ""
			if sysBase != 0 {
				ct, _ := a.CPU.M.Long(int(sysBase) + 0x114)
				curTask = uint32(ct)
				if curTask != 0 {
					np, _ := a.CPU.M.Long(int(curTask) + 0x0A)
					taskName = readCString(a, uint32(np), 32)
				}
			}
			t.Logf("Step %dM: PC=$%08X cycles=%d vpos=%d STOPs=%d DoIOs=%d tasks=%d DSKLENs=%d curTask=$%08X %q",
				i/1_000_000, a.CPU.PC, totalBeamCycles, a.vpos, stopCount, doIOCount, addTaskCount, dsklenWrites, curTask, taskName)
		}

		// Every 1M steps, log if we're stuck at one PC
		if i > 0 && i%1_000_000 == 0 && i <= 10_000_000 {
			if pc == uint32(lastPCLog) {
				t.Logf("Step %dM: POSSIBLY STUCK at PC=$%08X", i/1_000_000, pc)
			}
			lastPCLog = int(pc)
		}
	}

	// Summary
	t.Logf("\n=== ADF Boot Summary (100M steps) ===")
	t.Logf("  Total beam cycles: %d (%.2f sec at 7.09MHz)", totalBeamCycles, float64(totalBeamCycles)/7093790.0)
	t.Logf("  VBLANKs: ~%d (%.2f sec)", totalBeamCycles/uint64(CyclesPerFrame), float64(totalBeamCycles)/float64(CyclesPerFrame)/50.0)
	t.Logf("  STOPs: %d", stopCount)
	t.Logf("  DoIOs: %d", doIOCount)
	t.Logf("  AddTasks: %d", addTaskCount)
	t.Logf("  DSKLEN writes: %d", dsklenWrites)
	t.Logf("  Alerts: %d", alertCount)
	t.Logf("  FE86EC hit: %v, FF3E94 hit: %v", fe86ecHit, ff3e94Hit)
	t.Logf("  Disk: motor=%v cyl=%d side=%d selected=%v", a.Disk.motor, a.Disk.cylinder, a.Disk.side, a.Disk.selected)
	t.Logf("  CIA-A TimerB: UF=%d CRB=$%02X latch=$%04X counter=$%04X icrMask=$%02X",
		a.CIAA.TimerBUnderflows, a.CIAA.Regs(CIARegCRB), a.CIAA.TimerBLatch(), a.CIAA.TimerBCounter(), a.CIAA.IcrMask())
	t.Logf("  Fast-idle: count=%d skipped=%d", a.FastIdleCount, a.FastIdleSkips)

	// Dump all tasks in the task ready/wait lists
	sysBase, _ := a.CPU.M.Long(0x0004)
	if sysBase != 0 {
		t.Logf("  Task lists:")
		// TaskReady: at SysBase+$196
		dumpTaskList(t, a, int(sysBase)+0x196, "TaskReady")
		// TaskWait: at SysBase+$1A2
		dumpTaskList(t, a, int(sysBase)+0x1A2, "TaskWait")
		// Current task
		ct, _ := a.CPU.M.Long(int(sysBase) + 0x114)
		np, _ := a.CPU.M.Long(int(ct) + 0x0A)
		t.Logf("    ThisTask=$%08X %q", ct, readCString(a, uint32(np), 32))
	}
}

func readCString(a *Amiga, addr uint32, maxLen int) string {
	if addr == 0 || addr > 0xFFFFFF {
		return ""
	}
	buf := make([]byte, maxLen)
	a.CPU.M.Read(int(addr), buf)
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func dumpTaskList(t *testing.T, a *Amiga, listAddr int, label string) {
	head, _ := a.CPU.M.Long(listAddr)
	for i := 0; i < 20 && head != 0; i++ {
		succ, _ := a.CPU.M.Long(int(head))
		if succ == 0 {
			break // end sentinel
		}
		namePtr, _ := a.CPU.M.Long(int(head) + 0x0A)
		name := readCString(a, uint32(namePtr), 32)
		state, _ := a.CPU.M.Byte(int(head) + 0x0F)
		sigWait, _ := a.CPU.M.Long(int(head) + 0x16)
		sigRecvd, _ := a.CPU.M.Long(int(head) + 0x1A)
		t.Logf("    %s: $%08X %q state=%d sigWait=$%08X sigRecvd=$%08X", label, head, name, state, sigWait, sigRecvd)
		head = succ
	}
}
