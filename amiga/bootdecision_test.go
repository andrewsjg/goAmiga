package amiga

import (
	"fmt"
	"os"
	"testing"
)

// TestBootDecision traces the cold boot disk decision path.
// Architecture:
//
//	Cold boot: RESET→FC00D2→FC00FE→FC3100→FC014C (exec init)→FC0546 (mem test)
//	           →JMP(A5) at FC05B2→FC3090 (cold boot disk probe)→FC237E (disk boot)
//
//	FC3090-FC30FA = cold boot disk probe:
//	  Checks DSKBYTR, if ready → FC237E (disk boot), else → FC05F0 (ROM boot)
//
//	FC2FF0 = warm probe (only from user-mode exceptions — not the normal cold boot path)
func TestBootDecision(t *testing.T) {
	requireDiag(t)
	romData, err := LoadROM("../Kick13.rom")
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
	for _, p := range []string{
		"../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf",
		"../wb13.adf",
	} {
		if data, err := os.ReadFile(p); err == nil {
			if err2 := a.Disk.InsertDisk(data); err2 == nil {
				t.Logf("Inserted disk: %s (%d bytes)", p, len(data))
				break
			}
		}
	}

	// Run for enough steps to reach the disk probe during cold boot.
	// strap module (pri=-60) is the LAST COLDSTART resident — needs many steps.
	const maxSteps = 55_000_000

	// Hit counters
	fc0546Hits := 0
	fc0564Hits := 0 // MOVEC (illegal on 68000)
	fc0582Hits := 0 // illegal instruction handler entry
	fc05b2Hits := 0 // JMP (A5)
	// exec init: disk boot handler installation
	fc043aHits := 0 // BSR.W $FC2336 (installs disk boot handler at ExecBase+$42)
	fc2336Hits := 0 // MOVE.L #$FC237E,($42,A6) — writes disk boot ptr into ExecBase
	// FC195A: library init with AllocMem — critical for bootstrap task creation
	fc197cHits := 0 // TST.L D0 right after AllocMem — see if it succeeded
	fc19deHits := 0 // AllocMem failure skip path — if hit, bootstrap task never created
	// exec STOP: exec enters idle loop here after all resident init
	fc0f90Hits := 0
	fc0d14Hits := 0
	fc0d14PostStop := 0 // count FC0D14 fires after STOP
	fc0d4aHits := 0     // VERTB server chain dispatch (FC0D4A)
	fc0d4aPostStop := 0
	exc0652Hits := 0 // ExecBase-$24 = after server chain returns
	fc0f7cHits := 0  // exec init idle loop entry
	fc0f7cPostStop := 0
	fc0fdcHits := 0 // task context switch (movea.l $36(A3),A5)
	// AddTask call sites in ROM (all AddTask JSR -$11A,A6 occurrences)
	fe5ba2Hits := 0 // AddTask at FE5BA2
	fe9b4cHits := 0 // AddTask at FE9B4C
	ff426cHits := 0 // AddTask at FF426C
	ff43e6Hits := 0 // AddTask at FF43E6
	// cold boot disk probe
	fc3090Hits := 0 // MOVE.L #$40000,A7 (disk probe entry)
	fc30b2Hits := 0 // MOVE #$2700,SR
	fc30daHits := 0 // MOVE.W $DFF018,D0 (read DSKBYTR)
	fc30ecHits := 0 // CMPI.B #$7F,D0
	fc30f4Hits := 0 // BMI.W $FC05F0 (ROM boot)
	fc30faHits := 0 // JMP $FC237E (DISK BOOT)
	fea652Hits := 0 // Track decode result trace limiter
	// disk device/resource init
	fc48d6Hits := 0        // LEA $BFD000,A1 → MOVE.B #$FF,PRB,DDRB (disk resource init)
	fc4986Hits := 0        // LEA $BFD100,A0 (PRB direct access)
	fc08f6Hits := 0        // ORI #$2000,SR supervisor trampoline — NOT just Wait()!
	fc6da6Hits := 0        // VBL server Signal() call — which task gets signaled
	execSignalHits := 0    // ALL Signal() calls via exec LVO $0532 (SysBase-$144)
	waitLVOHits := 0       // Wait() LVO at $0538 (SysBase-$13E)
	supervisorLVOHits := 0 // Supervisor() LVO at $0658 (SysBase-$1E)
	fc65ecHits := 0        // Wait() body entry at FC65EC
	// COLDSTART module init tracking
	strapInitHits := 0            // FE8444 = strap init (pri=-60, LAST COLDSTART)
	rombootInitHits := 0          // FEB0A8 = romboot.library init (pri=-40)
	trackdiskInitHits := 0        // FE97BE = trackdisk.device init (pri=20)
	graphicsInitHits := 0         // FCABA2 = graphics.library init (pri=65)
	intuitionInitHits := 0        // FD3DA6 = intuition.library AUTOINIT table (pri=10)
	addTaskLVOHits := 0           // AddTask LVO at $055C (SysBase-$11A) - catches ALL AddTask calls
	openLibLVOHits := 0           // OpenLibrary LVO at $044E (SysBase-$228)
	dosInitHits := 0              // dos.library init entry at $FF3E94
	doIOLVOHits := 0              // DoIO LVO at $04AE (SysBase-$1C8)
	openDevLVOHits := 0           // OpenDevice LVO at $04BA (SysBase-$1BC)
	sendIOLVOHits := 0            // SendIO LVO at $04A8 (SysBase-$1CE)
	fe935aHits := 0               // timer.device VBL server entry ($FE935A)
	trackdiskTraceActive := false // trace trackdisk task PCs
	trackdiskTraceCount := 0
	strapTraceActive := false // trace PCs after strap Process allocation
	strapTraceCount := 0
	// warm probe (user-mode exceptions only — not cold boot path)
	fc2ff0Hits := 0
	fc3064Hits := 0
	fc307cHits := 0
	// boot targets
	fc237eHits := 0
	fc05f0Hits := 0
	fe86ecHits := 0 // strap JMP (A0) to dos.library
	// other
	diskMotorSeen := false
	diskSelectedSeen := false
	prevDsklenWrites := 0
	prevIntena := uint16(0) // track INTENA changes
	tdMotorLoopTrace := 0   // trackdisk motor-ready loop trace counter

	// One-time dumps for early system state
	stateChecked := false
	// Track bootstrap task after Wait returns — first few post-Wait executions
	bootstrapPostWaitCount := 0
	lastBootstrapWaitStep := 0
	_ = lastBootstrapWaitStep
	// Trace first bootstrap task execution after dispatch
	traceBootstrapPCs := false
	_ = traceBootstrapPCs
	traceBootstrapCount := 0
	_ = traceBootstrapCount
	lastStep := 0

	for i := 0; i < maxSteps; i++ {
		lastStep = i
		pc := a.CPU.PC
		a.CIAA.StepCount = i
		a.CIAB.StepCount = i

		// After exec init (around step 500K), dump key vectors and SysBase layout
		if !stateChecked && i > 500_000 {
			stateChecked = true
			vec0020, _ := a.CPU.M.Long(0x0020) // VecPrivilege handler
			vec006c, _ := a.CPU.M.Long(0x006C) // Level-3 autovector (VBL)
			vec0068, _ := a.CPU.M.Long(0x0068) // Level-2 autovector (CIA-A)
			sysBase, _ := a.CPU.M.Long(0x0004)
			fmt.Printf("\n[step=%d STATE DUMP]\n", i)
			fmt.Printf("  VecPrivilege($0020)=$%08X\n", vec0020)
			fmt.Printf("  Level2-CIA ($0068)=$%08X\n", vec0068)
			fmt.Printf("  Level3-VBL ($006C)=$%08X\n", vec006c)
			fmt.Printf("  SysBase($0004)=$%08X\n", sysBase)
			if sysBase != 0 {
				// ExecBase layout: ThisTask is at SysBase+$114 (=0x114)
				thisTask, _ := a.CPU.M.Long(int(sysBase) + 0x114)
				fmt.Printf("  ExecBase.ThisTask=$%08X\n", thisTask)
				// Verify LVO addresses: check JMP targets at $04AE and $04C0
				jt04AE, _ := a.CPU.M.Long(0x04AE + 2) // JMP target (skip $4EF9)
				jt04C0, _ := a.CPU.M.Long(0x04C0 + 2) // JMP target
				op04AE, _ := a.CPU.M.Word(0x04AE)
				op04C0, _ := a.CPU.M.Word(0x04C0)
				fmt.Printf("  LVO $04AE: op=$%04X target=$%08X\n", op04AE, jt04AE)
				fmt.Printf("  LVO $04C0: op=$%04X target=$%08X\n", op04C0, jt04C0)
				// See if task $3342 exists: read tc_State
				tcState, _ := a.CPU.M.Byte(0x3342 + 0x0F)    // tc_State
				tcFlags, _ := a.CPU.M.Byte(0x3342 + 0x0E)    // tc_Flags
				tcSigWait, _ := a.CPU.M.Long(0x3342 + 0x16)  // tc_SigWait
				tcSigRecvd, _ := a.CPU.M.Long(0x3342 + 0x1A) // tc_SigRecvd
				fmt.Printf("  Task$3342: tc_State=$%02X tc_Flags=$%02X tc_SigWait=$%08X tc_SigRecvd=$%08X\n",
					tcState, tcFlags, tcSigWait, tcSigRecvd)
			}
		}

		switch pc {
		// ---------- COLD BOOT PATH through exec init ----------
		case 0x00FC043A:
			// BSR.W $FC2336 — installs disk boot handler (MOVE.L #$FC237E,ExecBase+$42)
			fc043aHits++
			if fc043aHits == 1 {
				a6 := a.CPU.A[6]
				bootPtr, _ := a.CPU.M.Long(int(a6) + 0x42)
				vec0020, _ := a.CPU.M.Long(0x0020) // VecPrivilege handler
				fmt.Printf("[step=%d FC043A] BSR to disk-boot-ptr installer #%d: A6(ExecBase)=$%08X ExecBase+$42(cur)=$%08X\n",
					i, fc043aHits, a6, bootPtr)
				fmt.Printf("  VecPrivilege($0020)=$%08X\n", vec0020)
			}

		case 0x00FC2336:
			// Entry to function that installs disk boot handler into ExecBase+$42
			fc2336Hits++
			if fc2336Hits == 1 {
				a6 := a.CPU.A[6]
				fmt.Printf("[step=%d FC2336] disk-boot-ptr installer #%d: A6=$%08X SP=$%08X\n",
					i, fc2336Hits, a6, a.CPU.A[7])
			}

		case 0x00FC2344:
			// MOVE.L #$FC237E,($42,A6) — the actual write of disk boot ptr
			a6 := a.CPU.A[6]
			bootPtr, _ := a.CPU.M.Long(int(a6) + 0x42)
			fmt.Printf("[step=%d FC2344] *** Installing BootPtr=$FC237E at ExecBase+$42 ***\n", i)
			fmt.Printf("  A6(ExecBase)=$%08X, current[+$42]=$%08X (will become $FC237E after)\n", a6, bootPtr)

		case 0x00FC197C:
			// TST.L D0 — just returned from AllocMem (-$C6,A6) in FC195A
			// If D0=0, BEQ at FC197E will skip the library initialization entirely
			fc197cHits++
			a6 := a.CPU.A[6]
			jmpSlot, _ := a.CPU.M.Long(int(a6) - 0xC6)
			fmt.Printf("[step=%d FC197C] TST.L D0=$%08X after AllocMem: A6=$%08X, A6-$C6 slot=$%08X → %s\n",
				i, a.CPU.D[0], a6, jmpSlot,
				func() string {
					if a.CPU.D[0] == 0 {
						return "D0=0 → AllocMem FAILED! BEQ will skip init"
					}
					return "D0!=0 → AllocMem SUCCESS"
				}())

		case 0x00FC19DE:
			// AllocMem failure path - this fires when AllocMem returns 0 at FC197E
			fc19deHits++
			fmt.Printf("[step=%d FC19DE] *** AllocMem FAILED path hit! A6=$%08X D0=$%08X ***\n",
				i, a.CPU.A[6], a.CPU.D[0])
			fmt.Printf("  This means dos.library init will ABORT — bootstrap task NOT created!\n")
			// Walk back: what is at the dos.library jump table slot A6-$C6?
			a6 := a.CPU.A[6]
			jmpSlot, _ := a.CPU.M.Long(int(a6) - 0xC6)
			jmpSlot2, _ := a.CPU.M.Long(int(a6) - 0xC6 + 2)
			fmt.Printf("  A6-$C6 = $%08X, mem[$%06X] = $%08X $%08X\n",
				int(a6)-0xC6, int(a6)-0xC6, jmpSlot, jmpSlot2)

		// ---------- COLDSTART MODULE INIT TRACKING ----------
		case 0x00FE8444:
			// strap init entry (pri=-60, LAST COLDSTART module)
			strapInitHits++
			fmt.Printf("[step=%d FE8444] *** STRAP INIT ENTRY *** #%d A6(ExecBase)=$%08X D0=$%08X SP=$%08X SR=$%04X\n",
				i, strapInitHits, a.CPU.A[6], a.CPU.D[0], a.CPU.A[7], a.CPU.SR)

		case 0x00FE8476:
			// strap: JSR AllocMem(A6) for Process struct (D0=$488, D1=$10002)
			fmt.Printf("[step=%d FE8476] strap AllocMem: D0(size)=$%08X D1(type)=$%08X A6=$%08X\n",
				i, a.CPU.D[0], a.CPU.D[1], a.CPU.A[6])

		case 0x00FE847A:
			// strap: TST.L D0 after AllocMem — checks if alloc succeeded
			fmt.Printf("[step=%d FE847A] strap AllocMem result: D0=$%08X → %s\n",
				i, a.CPU.D[0], func() string {
					if a.CPU.D[0] == 0 {
						return "FAILED! Will call Alert($30010000)"
					}
					return "SUCCESS"
				}())

		case 0x00FE8498:
			// strap: MOVEA.L D0,A4 — alloc succeeded, A4=Process struct
			fmt.Printf("[step=%d FE8498] strap Process allocated: A4=$%08X\n", i, a.CPU.D[0])
			strapTraceActive = true
			strapTraceCount = 0

		case 0x0000055C:
			// AddTask LVO entry (SysBase $0676 - $11A = $055C)
			// All AddTask calls go through here
			addTaskLVOHits++
			fmt.Printf("[step=%d $055C] AddTask LVO #%d: A0(initPC)=$%08X A1(task)=$%08X D0=$%08X A6=$%08X\n",
				i, addTaskLVOHits, a.CPU.A[0], a.CPU.A[1], a.CPU.D[0], a.CPU.A[6])

		case 0x0000044E:
			// OpenLibrary LVO entry (SysBase $0676 - $228 = $044E)
			openLibLVOHits++
			if openLibLVOHits <= 20 {
				// A1 = library name string ptr
				nameBytes := make([]byte, 32)
				a.CPU.M.Read(int(a.CPU.A[1]), nameBytes)
				nameStr := ""
				for _, b := range nameBytes {
					if b == 0 {
						break
					}
					nameStr += string(rune(b))
				}
				fmt.Printf("[step=%d $044E] OpenLibrary LVO #%d: A1(name)=$%08X \"%s\" D0(ver)=$%08X A6=$%08X\n",
					i, openLibLVOHits, a.CPU.A[1], nameStr, a.CPU.D[0], a.CPU.A[6])
			}

		case 0x00FF3E94:
			// dos.library init entry (called from InitResident for dos.library)
			dosInitHits++
			fmt.Printf("[step=%d FF3E94] *** DOS.LIBRARY INIT *** #%d D0=$%08X A0=$%08X A6=$%08X SP=$%08X\n",
				i, dosInitHits, a.CPU.D[0], a.CPU.A[0], a.CPU.A[6], a.CPU.A[7])

		case 0x000004BA:
			// OpenDevice LVO (SysBase $0676 - $1BC = $04BA)
			// A0=name, D0=unit, A1=ioReq, D1=flags
			openDevLVOHits++
			if openDevLVOHits <= 20 {
				nameAddr := a.CPU.A[0]
				unitNum := a.CPU.D[0]
				ioReq := a.CPU.A[1]
				flags := a.CPU.D[1]
				// Read device name string
				devName := ""
				for ci := 0; ci < 40; ci++ {
					ch, _ := a.CPU.M.Byte(int(nameAddr) + ci)
					if ch == 0 {
						break
					}
					devName += string(rune(ch))
				}
				// Find calling task
				curTask, _ := a.CPU.M.Long(0x0676 + 0x114) // SysBase+$114 = ThisTask
				fmt.Printf("[step=%d $04BA] OpenDevice #%d: name=\"%s\" unit=%d ioReq=$%08X flags=$%08X task=$%08X\n",
					i, openDevLVOHits, devName, unitNum, ioReq, flags, curTask)
			}

		case 0x000004A8:
			// SendIO LVO (SysBase $0676 - $1CE = $04A8)
			// A1 = IORequest
			sendIOLVOHits++
			if sendIOLVOHits <= 20 {
				ioCmd, _ := a.CPU.M.Word(int(a.CPU.A[1]) + 0x1C)
				ioDevice, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x14)
				ioUnit, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x18)
				ioFlags, _ := a.CPU.M.Byte(int(a.CPU.A[1]) + 0x1E)
				curTask, _ := a.CPU.M.Long(0x0676 + 0x114)
				fmt.Printf("[step=%d $04A8] SendIO #%d: A1=$%08X cmd=$%04X dev=$%08X unit=$%08X flags=$%02X task=$%08X\n",
					i, sendIOLVOHits, a.CPU.A[1], ioCmd, ioDevice, ioUnit, ioFlags, curTask)
			}

		case 0x000004AE:
			// DoIO LVO (SysBase $0676 - $1C8 = $04AE)
			doIOLVOHits++
			if doIOLVOHits <= 10 || doIOLVOHits == 100 || doIOLVOHits == 1000 {
				// A1 = IORequest, read io_Command at ($1C,A1)
				ioCmd, _ := a.CPU.M.Word(int(a.CPU.A[1]) + 0x1C)
				ioDevice, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x14)
				ioUnit, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x18)
				ioLength, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x24)
				ioData, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x28)
				ioOffset, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x2C)
				// Get caller return PC from stack
				retPC, _ := a.CPU.M.Long(int(a.CPU.A[7]))
				fmt.Printf("[step=%d $04AE] DoIO LVO #%d: A1(ioReq)=$%08X cmd=$%04X dev=$%08X unit=$%08X len=$%08X data=$%08X off=$%08X retPC=$%08X\n",
					i, doIOLVOHits, a.CPU.A[1], ioCmd, ioDevice, ioUnit, ioLength, ioData, ioOffset, retPC)
			}

		case 0x00FE8560:
			// After first DoIO (CMD_CLEAR) — if this fires, DoIO returned!
			fmt.Printf("[step=%d FE8560] *** STRAP DoIO RETURNED! *** D0=$%08X (0=success)\n",
				i, a.CPU.D[0])

		case 0x0000626C:
			// trackdisk task entry (initPC from AddTask)
			fmt.Printf("[step=%d $626C] *** TRACKDISK TASK ENTRY *** SR=$%04X SP=$%08X D0=$%08X\n",
				i, a.CPU.SR, a.CPU.A[7], a.CPU.D[0])
			trackdiskTraceActive = true
			trackdiskTraceCount = 0

		case 0x00FEB0A8:
			// romboot.library init entry (pri=-40)
			rombootInitHits++
			fmt.Printf("[step=%d FEB0A8] *** ROMBOOT INIT ENTRY *** #%d A6=$%08X D0=$%08X SP=$%08X\n",
				i, rombootInitHits, a.CPU.A[6], a.CPU.D[0], a.CPU.A[7])

		case 0x00FE935A:
			// timer.device VBL server entry
			fe935aHits++
			if fe935aHits <= 5 || (fe935aHits%100 == 0) {
				// Read CIA-A TOD state
				todHI := a.CIAA.todCounter >> 16 & 0xFF
				todMID := a.CIAA.todCounter >> 8 & 0xFF
				todLO := a.CIAA.todCounter & 0xFF
				// Read is_Data+$40 (elapsed?) and list head
				isDataBase := a.CPU.A[1] // is_Data passed in A1
				d40, _ := a.CPU.M.Long(int(isDataBase) + 0x40)
				lhHead, _ := a.CPU.M.Long(int(isDataBase))
				lhTailPred, _ := a.CPU.M.Long(int(isDataBase) + 0x08)
				listEmpty := isDataBase == lhTailPred
				fmt.Printf("[step=%d FE935A] timer VBL server #%d: TOD=$%02X%02X%02X data+40=$%08X listEmpty=%v lhHead=$%08X\n",
					i, fe935aHits, todHI, todMID, todLO, d40, listEmpty, lhHead)
			}

		case 0x00FE97BE:
			// trackdisk.device init entry (pri=20)
			trackdiskInitHits++
			fmt.Printf("[step=%d FE97BE] trackdisk.device init #%d: A6=$%08X\n",
				i, trackdiskInitHits, a.CPU.A[6])
			if trackdiskInitHits == 1 {
				// Dump ROM $FEA080-$FEA200 — motor ready check + delay + DMA setup
				fmt.Printf("  ROM dump $FEA080-$FEA200 (motor check+delay):\n")
				for j := 0; j < 192; j++ {
					w, _ := a.CPU.M.Word(0xFEA080 + j*2)
					if j%8 == 0 {
						fmt.Printf("  $%06X:", 0xFEA080+j*2)
					}
					fmt.Printf(" %04X", w)
					if j%8 == 7 {
						fmt.Printf("\n")
					}
				}
				// Dump ROM $FEA400-$FEA6C0 — outer loop / CMD_READ handler
				fmt.Printf("  ROM dump $FEA400-$FEA6C0 (CMD_READ handler):\n")
				for j := 0; j < 352; j++ {
					w, _ := a.CPU.M.Word(0xFEA400 + j*2)
					if j%8 == 0 {
						fmt.Printf("  $%06X:", 0xFEA400+j*2)
					}
					fmt.Printf(" %04X", w)
					if j%8 == 7 {
						fmt.Printf("\n")
					}
				}
			}

		case 0x00FCABA2:
			// graphics.library init entry (pri=65)
			graphicsInitHits++
			fmt.Printf("[step=%d FCABA2] graphics.library init #%d: A6=$%08X\n",
				i, graphicsInitHits, a.CPU.A[6])

		case 0x00FD3DA6:
			// intuition.library AUTOINIT table entry (pri=10)
			intuitionInitHits++
			fmt.Printf("[step=%d FD3DA6] intuition.library AUTOINIT #%d: A6=$%08X\n",
				i, intuitionInitHits, a.CPU.A[6])

		case 0x00FC0F90:
			// STOP #$2000 — exec has finished all resident init and is halting CPU
			// waiting for an interrupt (VBL/CIA) to dispatch the first real task
			fc0f90Hits++
			if fc0f90Hits <= 3 {
				intena := a.Custom.ReadReg16(0x09A)
				intreq := a.Custom.ReadReg16(0x09C)
				fmt.Printf("[step=%d FC0F90] STOP #$2000 #%d: SR=$%04X SP=$%08X pendingIPL=%d\n",
					i, fc0f90Hits, a.CPU.SR, a.CPU.A[7], a.CPU.PendingIPL())
				fmt.Printf("  INTENA=$%04X INTREQ=$%04X master=%v VERTB=%v PORTS=%v\n",
					intena, intreq,
					intena&0x4000 != 0, intena&(1<<5) != 0, intena&(1<<3) != 0)
				sysBase, _ := a.CPU.M.Long(0x0004)
				fmt.Printf("  SysBase=$%08X\n", sysBase)
				// Critical: what's installed at autovector level3 ($006C)?
				vec3, _ := a.CPU.M.Long(0x006C)
				vec5b6, _ := a.CPU.M.Long(0x006C)
				fmt.Printf("  [$006C] Level-3 autovector handler=$%08X (if==$FC05B6 then default handler)\n", vec3)
				_ = vec5b6
			}

		case 0x00FE5BA2:
			// AddTask(A1=task, A0=initPC, A3=finalPC, D0=priority) — site FE5BA2
			fe5ba2Hits++
			if fe5ba2Hits <= 5 {
				fmt.Printf("[step=%d FE5BA2] AddTask#%d: A0(initPC)=$%08X A1(task)=$%08X D0(pri)=$%08X A3(final)=$%08X A6(SysBase)=$%08X\n",
					i, fe5ba2Hits, a.CPU.A[0], a.CPU.A[1], a.CPU.D[0], a.CPU.A[3], a.CPU.A[6])
			}

		case 0x00FE9B4C:
			// AddTask(A1=task, A0=initPC, ...) — site FE9B4C
			fe9b4cHits++
			if fe9b4cHits <= 5 {
				fmt.Printf("[step=%d FE9B4C] AddTask#%d: A0(initPC)=$%08X A1(task)=$%08X D0(pri)=$%08X A3(final)=$%08X A6(SysBase)=$%08X\n",
					i, fe9b4cHits, a.CPU.A[0], a.CPU.A[1], a.CPU.D[0], a.CPU.A[3], a.CPU.A[6])
			}

		case 0x00FF426C:
			// AddTask — site FF426C
			ff426cHits++
			if ff426cHits <= 5 {
				fmt.Printf("[step=%d FF426C] AddTask#%d: A0(initPC)=$%08X A1(task)=$%08X D0(pri)=$%08X A3(final)=$%08X A6(SysBase)=$%08X\n",
					i, ff426cHits, a.CPU.A[0], a.CPU.A[1], a.CPU.D[0], a.CPU.A[3], a.CPU.A[6])
			}

		case 0x00FF43E6:
			// AddTask — site FF43E6
			ff43e6Hits++
			if ff43e6Hits <= 5 {
				fmt.Printf("[step=%d FF43E6] AddTask#%d: A0(initPC)=$%08X A1(task)=$%08X D0(pri)=$%08X A3(final)=$%08X A6(SysBase)=$%08X\n",
					i, ff43e6Hits, a.CPU.A[0], a.CPU.A[1], a.CPU.D[0], a.CPU.A[3], a.CPU.A[6])
			}

		case 0x00FE5E30:
			// AddTask trampoline — first PC for task $3342 and others
			sysBaseT, _ := a.CPU.M.Long(0x0004)
			curTaskT := uint32(0)
			if sysBaseT != 0 {
				ctVal, _ := a.CPU.M.Long(int(sysBaseT) + 0x114)
				curTaskT = uint32(ctVal)
			}
			fmt.Printf("[step=%d FE5E30] TRAMPOLINE task=$%08X D0=$%08X A0=$%08X A5=$%08X A6=$%08X SR=$%04X SP=$%08X SysBase=$%08X\n",
				i, curTaskT, a.CPU.D[0], a.CPU.A[0], a.CPU.A[5], a.CPU.A[6], a.CPU.SR, a.CPU.A[7], sysBaseT)
			if curTaskT == 0x3342 {
				// Dump 16 words at FE5E30 to see what this code does
				for j := 0; j < 16; j++ {
					w, _ := a.CPU.M.Word(0xFE5E30 + j*2)
					if j%8 == 0 {
						fmt.Printf("  $%06X:", 0xFE5E30+j*2)
					}
					fmt.Printf(" %04X", w)
					if j%8 == 7 {
						fmt.Printf("\n")
					}
				}
			}

		case 0x00004396:
			// Bootstrap task entry point (confirmed from AddTask at FE5BA2)
			// This fires when the bootstrap task gets its first CPU time after STOP wakeup
			fmt.Printf("[step=%d $4396] *** BOOTSTRAP TASK ENTRY *** SR=$%04X SP=$%08X\n",
				i, a.CPU.SR, a.CPU.A[7])
			// Dump bootstrap task code (first 64 words = 128 bytes)
			fmt.Printf("  Bootstrap task code at $4396:\n")
			for j := 0; j < 64; j++ {
				w, _ := a.CPU.M.Word(0x4396 + j*2)
				if j%8 == 0 {
					fmt.Printf("  $%04X:", 0x4396+j*2)
				}
				fmt.Printf(" %04X", w)
				if j%8 == 7 {
					fmt.Printf("\n")
				}
			}

		// Bootstrap task execution trace (key addresses)
		case 0x00004400, 0x00004420, 0x00004440, 0x00004460, 0x00004480:
			curTask := uint32(0x3342)
			tcSigRecvd, _ := a.CPU.M.Long(int(curTask) + 0x1A)
			fmt.Printf("[step=%d $%04X] bootstrap D0=$%08X D1=$%08X A0=$%08X A6=$%08X SigRecvd=$%08X\n",
				i, pc, a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[6], tcSigRecvd)

		case 0x00FC0D14:
			// Exec level-3 interrupt handler (installed at $006C)
			// Note: ROM reads $DFF01C=INTENAR (mirrors INTENA=$09A), $DFF01E=INTREQR (mirrors INTREQ=$09C)
			fc0d14Hits++
			if fc0f90Hits > 0 {
				fc0d14PostStop++
			}
			if fc0d14Hits <= 5 || fc0d14PostStop <= 6 {
				intena := a.Custom.ReadReg16(0x09A) // INTENA (= what INTENAR returns)
				intreq := a.Custom.ReadReg16(0x09C) // INTREQ (= what INTREQR returns)
				active := intena & intreq
				fmt.Printf("[step=%d FC0D14] #%d SR=$%04X INTENA=$%04X INTREQ=$%04X active=$%04X VERTB=%v BLIT=%v\n",
					i, fc0d14Hits, a.CPU.SR, intena, intreq, active,
					(active>>5)&1 == 1, (active>>6)&1 == 1)
			}

		case 0x00FC0D4A:
			// VERTB server chain dispatch: movem.l $90(A6),A1/A5 — VERTB is active!
			fc0d4aHits++
			if fc0f90Hits > 0 {
				fc0d4aPostStop++
			}
			if fc0d4aHits == 1 || fc0d4aPostStop == 1 {
				a6 := a.CPU.A[6]
				listHead, _ := a.CPU.M.Long(int(a6) + 0x90) // IntVect iv_Data = list head
				disp, _ := a.CPU.M.Long(int(a6) + 0x94)     // IntVect iv_Code = dispatcher
				fmt.Printf("[step=%d FC0D4A] #%d VBL SERVER CHAIN WALK: listHead=$%08X disp=$%08X\n",
					i, fc0d4aHits, listHead, disp)
				// Walk the entire server list (Exec-style MinList)
				// listHead is &list.lh_Head; *(listHead) = first node
				node, _ := a.CPU.M.Long(int(listHead))
				for si := 0; si < 20 && node != 0; si++ {
					succ, _ := a.CPU.M.Long(int(node)) // ln_Succ
					if succ == 0 {
						break // end of list (tail sentinel)
					}
					lnType, _ := a.CPU.M.Byte(int(node) + 0x08) // ln_Type
					lnPri, _ := a.CPU.M.Byte(int(node) + 0x09)  // ln_Pri
					lnName, _ := a.CPU.M.Long(int(node) + 0x0A) // ln_Name
					isData, _ := a.CPU.M.Long(int(node) + 0x0E) // is_Data
					isCode, _ := a.CPU.M.Long(int(node) + 0x12) // is_Code
					// Read name string if non-null
					srvName := ""
					if lnName != 0 {
						for ci := 0; ci < 40; ci++ {
							ch, _ := a.CPU.M.Byte(int(lnName) + ci)
							if ch == 0 {
								break
							}
							srvName += string(rune(ch))
						}
					}
					fmt.Printf("  VBL server #%d: node=$%08X type=$%02X pri=%d name=\"%s\" is_Data=$%08X is_Code=$%08X\n",
						si, node, lnType, int8(lnPri), srvName, isData, isCode)
					node = succ
				}
			}

		case 0x00000652:
			// ExecBase - $24 = return address after VERTB server chain
			exc0652Hits++
			if exc0652Hits <= 3 {
				b0, _ := a.CPU.M.Byte(0x0652)
				b1, _ := a.CPU.M.Byte(0x0653)
				b2, _ := a.CPU.M.Byte(0x0654)
				b3, _ := a.CPU.M.Byte(0x0655)
				b4, _ := a.CPU.M.Byte(0x0656)
				b5, _ := a.CPU.M.Byte(0x0657)
				fmt.Printf("[step=%d $0652] #%d SR=$%04X bytes: %02X %02X %02X %02X %02X %02X\n",
					i, exc0652Hits, a.CPU.SR, b0, b1, b2, b3, b4, b5)
			}

		case 0x00FC0F7C:
			// Exec init idle loop: checks TaskReady queue via A0
			fc0f7cHits++
			if fc0f7cHits <= 3 || (fc0f90Hits > 0 && fc0f7cPostStop <= 3) {
				if fc0f90Hits > 0 {
					fc0f7cPostStop++
				}
				a0 := a.CPU.A[0]
				a3, _ := a.CPU.M.Long(int(a0))
				d0, _ := a.CPU.M.Long(int(a3))
				fmt.Printf("[step=%d FC0F7C] #%d A0=$%08X (A0)=$%08X (A3)=$%08X hasTask=%v A4=$%08X\n",
					i, fc0f7cHits, a0, a3, d0, d0 != 0, a.CPU.A[4])
			}

		case 0x00FC0FDC:
			// movea.l $36(A3),A5 — loads task's tc_SPReg (saved stack ptr) before jmp(A4)
			fc0fdcHits++
			a3_fdc := a.CPU.A[3]
			// Activate trackdisk trace when dispatching task $621E
			if a3_fdc == 0x0000621E && !trackdiskTraceActive {
				trackdiskTraceActive = true
				trackdiskTraceCount = 0
			}
			if fc0fdcHits <= 10 || a3_fdc == 0x0000621E {
				a3 := a3_fdc
				a4 := a.CPU.A[4]                               // context switch target (= FC0FE2)
				tcFlags, _ := a.CPU.M.Byte(int(a3) + 0xE)      // tc_Flags
				tcIDNestCnt, _ := a.CPU.M.Byte(int(a3) + 0x10) // tc_IDNestCnt
				tcSPReg, _ := a.CPU.M.Long(int(a3) + 0x36)     // tc_SPReg (saved stack)
				// At tc_SPReg: [PC (4 bytes), SR (2 bytes), d0-d7/a0-a6 (60 bytes)]
				savedPC, _ := a.CPU.M.Long(int(tcSPReg))     // first word after SPReg = PC
				savedSR, _ := a.CPU.M.Word(int(tcSPReg) + 4) // SR after PC
				// Read task name
				tcNamePtr, _ := a.CPU.M.Long(int(a3) + 0x0A)
				taskName := ""
				if tcNamePtr != 0 {
					nb := make([]byte, 24)
					a.CPU.M.Read(int(tcNamePtr), nb)
					for _, b := range nb {
						if b == 0 {
							break
						}
						taskName += string(rune(b))
					}
				}
				fmt.Printf("[step=%d FC0FDC] #%d task=$%08X \"%s\" A4=$%08X tc_Flags=$%02X tc_IDNestCnt=$%02X tc_SPReg=$%08X savedPC=$%08X savedSR=$%04X\n",
					i, fc0fdcHits, a3, taskName, a4, tcFlags, tcIDNestCnt, tcSPReg, savedPC, savedSR)
			}

		case 0x00FC0FF0:
			// RTE — after context switch restore, dispatches to task
			// Check what SR and PC are on the supervisor stack
			sp := a.CPU.A[7]
			rteSR, _ := a.CPU.M.Word(int(sp))
			rtePC, _ := a.CPU.M.Long(int(sp) + 2)
			sysBaseR, _ := a.CPU.M.Long(0x0004)
			curTaskR, _ := a.CPU.M.Long(int(sysBaseR) + 0x114)
			if fc0fdcHits <= 5 || curTaskR == 0x3342 {
				fmt.Printf("[step=%d FC0FF0] RTE dispatch: SP=$%08X rteSR=$%04X rtePC=$%08X curTask=$%08X\n",
					i, sp, rteSR, rtePC, curTaskR)
			}
			// Trigger PC tracing after first dispatch to task $3342
			if curTaskR == 0x3342 && !traceBootstrapPCs {
				traceBootstrapPCs = true
				traceBootstrapCount = 0
				fmt.Printf("[step=%d] *** STARTING BOOTSTRAP PC TRACE ***\n", i)
			}

		// ---------- DISK RESOURCE + DEVICE INIT ----------
		case 0x00FC48D6:
			// LEA $BFD000.l, A1: disk resource init code (writes #$FF to PRB/DDRB)
			fc48d6Hits++
			a1 := a.CPU.A[1]
			a6 := a.CPU.A[6]
			curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
			fmt.Printf("[step=%d FC48D6] disk_resource_init #%d: A1=$%08X A6(SysBase)=$%08X curTask=$%08X\n",
				i, fc48d6Hits, a1, a6, curTask)

		case 0x00FC4986:
			// LEA $BFD100.l, A0: direct PRB access
			fc4986Hits++
			fmt.Printf("[step=%d FC4986] LEA $BFD100, A0 #%d A0=$%08X A6=$%08X\n",
				i, fc4986Hits, a.CPU.A[0], a.CPU.A[6])

		// Drive probe CIA-A PRA read: ROM reads $BFE001 to check /DKRDY
		case 0x00FC49A8:
			// BTST.B #$5, $BFE001.l — checking /DKRDY during drive probe
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			d3 := a.CPU.D[3]
			fmt.Printf("[step=%d FC49A8] BTST #5,BFE001: BFE001=$%02X bit5=%d (0=ready) D3=$%08X sel=%v mtr=%v\n",
				i, praVal, (praVal>>5)&1, d3, a.Disk.selected, a.Disk.motor)

		// DF0 probe area (FC4900-FC4985)
		case 0x00FC4900:
			// DF0 drive detect area start
			fmt.Printf("[step=%d FC4900] DF0 init area entry: D0=$%08X D3=$%08X A0=$%08X A6=$%08X\n",
				i, a.CPU.D[0], a.CPU.D[3], a.CPU.A[0], a.CPU.A[6])

		case 0x00FC4910:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4910] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D3=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[3])

		case 0x00FC4920:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4920] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3])

		case 0x00FC4930:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4930] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00FC4940:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4940] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00FC4950:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4950] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00FC4960:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4960] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00FC4970:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4970] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00FC4975:
			praVal, _ := a.CPU.M.Byte(0xBFE001)
			fmt.Printf("[step=%d FC4975] DF0 probe: BFE001=$%02X sel=%v mtr=%v D0=$%08X D1=$%08X D2=$%08X D3=$%08X A0=$%08X\n",
				i, praVal, a.Disk.selected, a.Disk.motor, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.A[0])

		case 0x00000532:
			// exec Signal() LVO table entry (SysBase $0676 - $144 = $0532)
			// JMP [Signal_impl] — fires for EVERY Signal() call
			execSignalHits++
			a1 := a.CPU.A[1] // task to signal
			d0 := a.CPU.D[0] // signal mask
			if execSignalHits <= 20 {
				// Read target task state BEFORE signal is applied
				tcState, _ := a.CPU.M.Byte(int(a1) + 0x0F)    // tc_State
				tcSigWait, _ := a.CPU.M.Long(int(a1) + 0x16)  // tc_SigWait
				tcSigRecvd, _ := a.CPU.M.Long(int(a1) + 0x1A) // tc_SigRecvd
				wouldWake := (d0 & tcSigWait) != 0
				fmt.Printf("[step=%d Signal()] #%d task=$%08X mask=$%08X State=$%02X SigWait=$%08X SigRecvd=$%08X wouldWake=%v\n",
					i, execSignalHits, a1, d0, tcState, tcSigWait, tcSigRecvd, wouldWake)
			}

		case 0x00000538:
			// Wait() LVO table entry (SysBase $0676 - $13E = $0538)
			waitLVOHits++
			if waitLVOHits <= 20 {
				a6 := a.CPU.A[6]
				curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
				// Check actual JMP target at $0538 (should be $4EF9 then 4-byte address)
				jmpOpcode, _ := a.CPU.M.Word(0x0538)
				jmpTarget, _ := a.CPU.M.Long(0x053A)
				fmt.Printf("[step=%d Wait()LVO] #%d D0=$%08X A1=$%08X A5=$%08X A6=$%08X task=$%08X SR=$%04X jmpOp=$%04X jmpTarget=$%08X\n",
					i, waitLVOHits, a.CPU.D[0], a.CPU.A[1], a.CPU.A[5], a6, curTask, a.CPU.SR, jmpOpcode, jmpTarget)
			}
			// Track bootstrap task Wait calls
			a6w := a.CPU.A[6]
			curTaskW, _ := a.CPU.M.Long(int(a6w) + 0x114)
			if curTaskW == 0x3342 {
				lastBootstrapWaitStep = i
			}

		case 0x00000658:
			// Supervisor() LVO table entry (SysBase $0676 - $1E = $0658)
			supervisorLVOHits++
			if supervisorLVOHits <= 20 {
				a6 := a.CPU.A[6]
				curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
				fmt.Printf("[step=%d Supervisor()LVO] #%d A5=$%08X D0=$%08X A6=$%08X task=$%08X SR=$%04X\n",
					i, supervisorLVOHits, a.CPU.A[5], a.CPU.D[0], a6, curTask, a.CPU.SR)
			}

		case 0x00FC65EC:
			// Wait() body entry (real implementation)
			fc65ecHits++
			if fc65ecHits <= 20 {
				a6 := a.CPU.A[6]
				curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
				// A1 is key: MOVEA.L (A1),A5 is the 2nd instruction
				a1val := uint32(0)
				if a.CPU.A[1] != 0 {
					v, _ := a.CPU.M.Long(int(a.CPU.A[1]))
					a1val = uint32(v)
				}
				tcSigWait, _ := a.CPU.M.Long(int(curTask) + 0x16)
				tcSigRecvd, _ := a.CPU.M.Long(int(curTask) + 0x1A)
				fmt.Printf("[step=%d FC65EC] Wait()body #%d: D0=$%08X A1=$%08X (A1)=$%08X A5=$%08X task=$%08X SigWait=$%08X SigRecvd=$%08X\n",
					i, fc65ecHits, a.CPU.D[0], a.CPU.A[1], a1val, a.CPU.A[5], curTask, tcSigWait, tcSigRecvd)
			}

		case 0x00FC08E6:
			// Supervisor trampoline: ORI #$2000,SR → privilege violation → JMP(A5)
			// Used by Supervisor() LVO and potentially other functions
			fc08f6Hits++
			a6 := a.CPU.A[6]
			curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
			if fc08f6Hits <= 15 || curTask == 0x00003342 {
				// exec Task offsets: tc_SigAlloc=0x12, tc_SigWait=0x16, tc_SigRecvd=0x1A, tc_SigExcept=0x1E
				tcSigAlloc, _ := a.CPU.M.Long(int(curTask) + 0x12) // tc_SigAlloc
				tcSigWait, _ := a.CPU.M.Long(int(curTask) + 0x16)  // tc_SigWait
				tcSigRecvd, _ := a.CPU.M.Long(int(curTask) + 0x1A) // tc_SigRecvd
				tcState, _ := a.CPU.M.Byte(int(curTask) + 0x0F)    // tc_State (TS_RUN=2, TS_WAIT=4)
				overlap := a.CPU.D[0] & tcSigRecvd
				a5 := a.CPU.A[5] // A5 = target of JMP(A5) in priv viol handler!
				fmt.Printf("[step=%d FC08E6] Wait() #%d: D0=$%08X task=$%08X State=$%02X A5=$%08X SigAlloc=$%08X SigWait=$%08X SigRecvd=$%08X overlap=$%08X\n",
					i, fc08f6Hits, a.CPU.D[0], curTask, tcState, a5, tcSigAlloc, tcSigWait, tcSigRecvd, overlap)
			}

		// Wait() implementation body (inside privilege violation handler, after FC08E6)
		// These are in the exec privilege violation dispatch: ~FC090E - FC0916
		// After BEQ branch at FC0916, execution goes to FC0922 (MOVE.L #FC08F4,(2,A7))
		// then FC092A: JMP(A5) which jumps to wherever A5 points (actual Wait body)
		case 0x00FC090E, 0x00FC0916:
			// Trace Wait() body — first 5 times only to avoid flood
			if fc08f6Hits <= 5 {
				a6 := a.CPU.A[6]
				curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
				tcSigWait, _ := a.CPU.M.Long(int(curTask) + 0x16)
				tcSigRecvd, _ := a.CPU.M.Long(int(curTask) + 0x1A)
				tcState, _ := a.CPU.M.Byte(int(curTask) + 0x0F)
				a5 := a.CPU.A[5]
				fmt.Printf("[step=%d $%06X] priv-viol-handler: D0=$%08X A5=$%08X task=$%08X State=$%02X tc_SigWait=$%08X tc_SigRecvd=$%08X\n",
					i, pc, a.CPU.D[0], a5, curTask, tcState, tcSigWait, tcSigRecvd)
			}

		// Actual Wait() supervisor body — A5 in exec is the "real" Wait() impl
		// Known KS1.3 Wait body: FC0938-FC0990 area (after JMP(A5))
		// Monitor several potential Wait body entry points
		case 0x00FC0938, 0x00FC093A, 0x00FC093C, 0x00FC093E,
			0x00FC0940, 0x00FC0942, 0x00FC0944, 0x00FC0946,
			0x00FC0948, 0x00FC094A, 0x00FC094C, 0x00FC094E,
			0x00FC0950, 0x00FC0952, 0x00FC0954, 0x00FC0956,
			0x00FC0958, 0x00FC095A, 0x00FC095C, 0x00FC095E,
			0x00FC0960, 0x00FC0962, 0x00FC0964, 0x00FC0966:
			// Only first 3 Wait() calls
			if fc08f6Hits <= 3 {
				a6 := a.CPU.A[6]
				curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
				tcSigWait, _ := a.CPU.M.Long(int(curTask) + 0x16)
				tcState, _ := a.CPU.M.Byte(int(curTask) + 0x0F)
				fmt.Printf("[step=%d $%06X] WaitBody: D0=$%08X D1=$%08X A0=$%08X A5=$%08X task=$%08X State=$%02X tc_SigWait=$%08X\n",
					i, pc, a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[5], curTask, tcState, tcSigWait)
			}

		case 0x00FC08F6:
			// Wait() entry — capture which task is waiting and for what signals
			fc08f6Hits++
			a6 := a.CPU.A[6]
			curTask, _ := a.CPU.M.Long(int(a6) + 0x114)
			tcSigAlloc, _ := a.CPU.M.Long(int(curTask) + 0x12) // tc_SigAlloc
			tcSigWait, _ := a.CPU.M.Long(int(curTask) + 0x16)  // tc_SigWait
			tcSigRecvd, _ := a.CPU.M.Long(int(curTask) + 0x1A) // tc_SigRecvd
			fmt.Printf("[step=%d FC08F6] Wait() #%d: D0(sigmask)=$%08X task=$%08X tc_SigAlloc=$%08X SigWait=$%08X SigRecvd=$%08X\n",
				i, fc08f6Hits, a.CPU.D[0], curTask, tcSigAlloc, tcSigWait, tcSigRecvd)

		case 0x00FC6DA6:
			// Signal(A1, D0=$10) — VBL server signals a task each frame
			fc6da6Hits++
			a1 := a.CPU.A[1] // task to signal
			d0 := a.CPU.D[0] // signal mask
			if fc6da6Hits <= 10 {
				fmt.Printf("[step=%d FC6DA6] VBL Signal(task=$%08X, mask=$%08X) #%d\n",
					i, a1, d0, fc6da6Hits)
			}

		// ---------- COLD BOOT PATH through exec init ----------
		case 0x00FC0546:
			fc0546Hits++
			if fc0546Hits == 1 {
				fmt.Printf("[step=%d FC0546] ENTER memory size fn: SP=$%08X A5=$%08X SR=$%04X\n",
					i, a.CPU.A[7], a.CPU.A[5], a.CPU.SR)
			}

		// Memory sizing loop instrumentation
		case 0x00FC0592:
			// MOVEQ #0,D1 — entry to memory sizing loop
			fmt.Printf("[step=%d FC0592] MEM SIZING ENTRY: A0=$%08X A1=$%08X A5=$%08X\n",
				i, a.CPU.A[0], a.CPU.A[1], a.CPU.A[5])
			// Test: can we read/write Chip RAM directly?
			testBuf := []byte{0xDE, 0xAD, 0xBE, 0xEF}
			a.ChipRAM.Write(0x1000, testBuf)
			readBuf := make([]byte, 4)
			a.ChipRAM.Read(0x1000, readBuf)
			fmt.Printf("  ChipRAM direct: wrote DEADBEEF at $1000, read back: %02X%02X%02X%02X\n",
				readBuf[0], readBuf[1], readBuf[2], readBuf[3])
			// Test via mapper
			a.CPU.M.Write(0x1000, testBuf)
			v1000, _ := a.CPU.M.Long(0x1000)
			fmt.Printf("  Mapper: wrote DEADBEEF at $1000, read back: $%08X\n", v1000)
			// Test location 0 (which sizing clears)
			v0000, _ := a.CPU.M.Long(0x0000)
			fmt.Printf("  Mapper: ($0000)=$%08X overlay=%v\n", v0000, a.overlay)
			// Clean up test writes
			zeroBuf := []byte{0, 0, 0, 0}
			a.ChipRAM.Write(0x1000, zeroBuf)

		case 0x00FC05A6:
			// MOVE.L D0,(A0) — writing magic pattern at A0
			// Only trace first 5 and last (when it fails)
			if fc05b2Hits == 0 { // only before JMP(A5)
				a0 := a.CPU.A[0]
				if a0 <= 0x5000 || a0 >= 0x7F000 {
					v0, _ := a.CPU.M.Long(0)
					fmt.Printf("[step=%d FC05A6] WRITE D0=$%08X→($%08X) (A2)=$%08X\n",
						i, a.CPU.D[0], a0, v0)
				}
			}

		case 0x00FC05AC:
			// CMP.L (A0),D0 — reading pattern back from A0
			if fc05b2Hits == 0 {
				a0 := a.CPU.A[0]
				if a0 <= 0x5000 || a0 >= 0x7F000 {
					readback, _ := a.CPU.M.Long(int(a0))
					fmt.Printf("[step=%d FC05AC] CMP (A0=$%08X)=$%08X vs D0=$%08X → %s\n",
						i, a0, readback, a.CPU.D[0],
						func() string {
							if uint32(readback) == a.CPU.D[0] {
								return "MATCH"
							}
							return "MISMATCH!"
						}())
				}
			}

		case 0x00FC05AA:
			// BNE.B FC05B0 — mirror detection (TST.L (A2) was non-zero)
			if fc05b2Hits == 0 {
				a0 := a.CPU.A[0]
				a2 := a.CPU.A[2]
				v0, _ := a.CPU.M.Long(int(a2))
				fmt.Printf("[step=%d FC05AA] MIRROR CHECK: A0=$%08X A2=$%08X (A2)=$%08X → %s\n",
					i, a0, a2, v0,
					func() string {
						if v0 != 0 {
							return "BNE TAKEN → mirror detected, sizing stops!"
						}
						return "BNE not taken → no mirror"
					}())
			}

		case 0x00FC05B0:
			// MOVEA.L A0,A3 — A3 = memory top result
			if fc05b2Hits == 0 {
				fmt.Printf("[step=%d FC05B0] MEM SIZING RESULT: A0=$%08X → A3 (memory top)\n",
					i, a.CPU.A[0])
			}

		case 0x00FC021A:
			// CMPA.L #$40000,A3 — check if memory >= 256K
			fmt.Printf("[step=%d FC021A] COLD BOOT MEM CHECK: A3=$%08X vs $40000 → %s\n",
				i, a.CPU.A[3],
				func() string {
					if a.CPU.A[3] < 0x40000 {
						return "A3 < 256K → BCS TAKEN (minimal boot!)"
					}
					return "A3 >= 256K → normal cold boot path"
				}())

		case 0x00FC0564:
			// MOVEC D1,VBR — illegal on 68000, triggers VecIllegal → $0010 handler
			fc0564Hits++
			if fc0564Hits == 1 {
				fmt.Printf("[step=%d FC0564] MOVEC (4E7B) #%d: D0=$%08X D1=$%08X A1=$%08X SP=$%08X\n",
					i, fc0564Hits, a.CPU.D[0], a.CPU.D[1], a.CPU.A[1], a.CPU.A[7])
				vec0010, _ := a.CPU.M.Long(0x10)
				fmt.Printf("  $0010 handler=$%08X (should be $FC0582)\n", vec0010)
			}

		case 0x00FC0582:
			// Illegal instruction handler — runs after 4E7B trap on 68000.
			// Also reached on 68010+ via BNE after MOVEC succeeds.
			fc0582Hits++
			if fc0582Hits <= 4 {
				fmt.Printf("[step=%d FC0582] illegal-insn handler #%d: A1=$%08X A7=$%08X D0=$%08X D1=$%08X\n",
					i, fc0582Hits, a.CPU.A[1], a.CPU.A[7], a.CPU.D[0], a.CPU.D[1])
			}

		case 0x00FC05B2:
			// JMP (A5) — the critical jump from memory test to cold boot disk probe
			fc05b2Hits++
			fmt.Printf("[step=%d FC05B2] JMP(A5) #%d: A5=$%08X A3=$%08X SP=$%08X D0=$%08X D1=$%08X\n",
				i, fc05b2Hits, a.CPU.A[5], a.CPU.A[3], a.CPU.A[7], a.CPU.D[0], a.CPU.D[1])
			if a.CPU.A[5] != 0x00FC3090 {
				fmt.Printf("  *** WARNING: A5=$%08X is NOT FC3090 — wrong cold boot target!\n", a.CPU.A[5])
			} else {
				fmt.Printf("  A5=FC3090 ✓ → entering cold boot disk probe\n")
			}

		// ---------- COLD BOOT DISK PROBE (FC3090-FC30FA) ----------
		case 0x00FC3090:
			// Entry to cold boot disk probe: MOVE.L #$40000,A7
			fc3090Hits++
			fmt.Printf("[step=%d FC3090] *** COLD BOOT DISK PROBE ENTRY #%d ***\n", i, fc3090Hits)
			fmt.Printf("  SP=$%08X D0=$%08X D1=$%08X SR=$%04X\n",
				a.CPU.A[7], a.CPU.D[0], a.CPU.D[1], a.CPU.SR)

		case 0x00FC30B2:
			// MOVE #$2700,SR — supervisor mode IPL=7
			fc30b2Hits++
			if fc30b2Hits == 1 {
				fmt.Printf("[step=%d FC30B2] MOVE #$2700,SR (about to disable IRQs): SR=$%04X→$2700\n",
					i, a.CPU.SR)
			}

		case 0x00FC30DA:
			// MOVE.W $DFF018,D0 — read DSKBYTR
			fc30daHits++
			if fc30daHits <= 10 {
				dskbytr, _ := a.CPU.M.Word(0xDFF018)
				fmt.Printf("[step=%d FC30DA] DSKBYTR read #%d: $%04X D1(retry)=%d motor=%v sel=%v adf=%v\n",
					i, fc30daHits, dskbytr, a.CPU.D[1]&0xFFFF, a.Disk.motor, a.Disk.selected, a.Disk.adf != nil)
			}

		case 0x00FC30EC:
			// CMPI.B #$7F,D0 — is DSKBYTR bits[6:0] == $7F? (disk sync ready)
			fc30ecHits++
			if fc30ecHits <= 10 {
				d0b := a.CPU.D[0] & 0x7F
				fmt.Printf("[step=%d FC30EC] CMPI #$7F check #%d: D0.B=$%02X (masked) → %s\n",
					i, fc30ecHits, d0b,
					func() string {
						if d0b == 0x7F {
							return "EQ → disk sync found → disk boot"
						}
						return fmt.Sprintf("NEQ → retry/ROM (D1=%d retries left)", a.CPU.D[1]&0xFFFF)
					}())
			}

		case 0x00FC30F4:
			// BMI.W $FC05F0 — ROM boot if disk not ready
			fc30f4Hits++
			d0b := a.CPU.D[0] & 0xFF
			fmt.Printf("[step=%d FC30F4] BMI to ROM BOOT check #%d: D0.B=$%02X SR=$%04X → %s\n",
				i, fc30f4Hits, d0b, a.CPU.SR,
				func() string {
					if a.CPU.SR&0x0008 != 0 { // N flag
						return "BMI TAKEN → FC05F0 (ROM boot)"
					}
					return "BMI NOT taken → fall to FC30F8"
				}())

		case 0x00FC30FA:
			// JMP $FC237E — DISK BOOT!
			fc30faHits++
			fmt.Printf("[step=%d FC30FA] *** JMP to DISK BOOT FC237E *** hit#%d D7=$%08X SP=$%08X\n",
				i, fc30faHits, a.CPU.D[7], a.CPU.A[7])

		// ---------- WARM PROBE PATH (via user-mode exceptions only) ----------
		case 0x00FC2FF0:
			fc2ff0Hits++
			execBase, _ := a.CPU.M.Long(4)
			fmt.Printf("[step=%d FC2FF0] warm_probe entry #%d: SP=$%08X ExecBase=$%08X SR=$%04X\n",
				i, fc2ff0Hits, a.CPU.A[7], execBase, a.CPU.SR)

		case 0x00FC3064:
			// BSR.W $FC3128 inside warm probe
			fc3064Hits++
			fmt.Printf("[step=%d FC3064] warm-probe disk BSR: D7=$%08X D0=$%08X A6=$%08X\n",
				i, a.CPU.D[7], a.CPU.D[0], a.CPU.A[6])

		case 0x00FC307C:
			// BRA.W $FC237E (warm probe path → disk boot)
			fc307cHits++
			fmt.Printf("[step=%d FC307C] warm-probe disk boot BRA hit#%d\n", i, fc307cHits)

		// ---------- BOOT TARGETS ----------
		case 0x00FC237E:
			fc237eHits++
			fmt.Printf("[step=%d FC237E] *** DISK BOOT ENTRY *** hit#%d SP=$%08X D0=$%08X D7=$%08X\n",
				i, fc237eHits, a.CPU.A[7], a.CPU.D[0], a.CPU.D[7])

		// ---------- TRACK DECODE DIAGNOSTICS ----------
		// (suppressed after first 2 decodes to reduce output for long runs)
		case 0x00FEA652:
			fea652Hits++
			if fea652Hits <= 2 {
				// Return from decode function ($FEAC62) — D0 = error code
				// At FEA652: A3=unit, A2=IO request from unit+$4E
				unitAddr := a.CPU.A[3]
				ioReqFromUnit, _ := a.CPU.M.Long(int(unitAddr) + 0x4E)
				fmt.Printf("[step=%d FEA652] DECODE RESULT: D0=$%02X unit=$%08X ioReq(unit+4E)=$%08X\n",
					i, a.CPU.D[0]&0xFF, unitAddr, ioReqFromUnit)
				// Dump registers A0-A5, D0-D5 for full context
				fmt.Printf("[step=%d] REGS: A0=$%08X A1=$%08X A2=$%08X A3=$%08X A4=$%08X A5=$%08X\n",
					i, a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3], a.CPU.A[4], a.CPU.A[5])
				fmt.Printf("[step=%d] REGS: D0=$%08X D1=$%08X D2=$%08X D3=$%08X D4=$%08X D5=$%08X\n",
					i, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.D[3], a.CPU.D[4], a.CPU.D[5])
				// Read IO request fields using the ioReq from unit+$4E
				if ioReqFromUnit > 0 && ioReqFromUnit < 0x80000 {
					ioCmd2, _ := a.CPU.M.Word(int(ioReqFromUnit) + 0x1C)
					ioLen2, _ := a.CPU.M.Long(int(ioReqFromUnit) + 0x24)
					ioBuf2, _ := a.CPU.M.Long(int(ioReqFromUnit) + 0x28)
					ioOff2, _ := a.CPU.M.Long(int(ioReqFromUnit) + 0x2C)
					fmt.Printf("[step=%d] IO(unit+4E) @$%08X: cmd=%d len=$%08X buf=$%08X off=$%08X\n",
						i, ioReqFromUnit, ioCmd2, ioLen2, ioBuf2, ioOff2)
				}
				// Read IO at $614E as well for comparison
				ioBuf, _ := a.CPU.M.Long(0x614E + 0x28)
				ioLen, _ := a.CPU.M.Long(0x614E + 0x24)
				ioOff, _ := a.CPU.M.Long(0x614E + 0x2C)
				ioCmd, _ := a.CPU.M.Word(0x614E + 0x1C)
				fmt.Printf("[step=%d] IO@$614E: cmd=%d len=$%08X buf=$%08X off=$%08X\n", i, ioCmd, ioLen, ioBuf, ioOff)
				// Dump decoded sector data from unit+$52 (trackdisk sector buffer)
				sectorBuf := unitAddr + 0x52
				fmt.Printf("[step=%d] Sector buf (unit+$52=$%08X): ", i, sectorBuf)
				for j := 0; j < 32; j++ {
					b := make([]byte, 1)
					a.ChipRAM.Read(int(sectorBuf)+j, b)
					fmt.Printf("%02X", b[0])
				}
				fmt.Println()
				// Also dump at unit+$680 (raw track DMA base)
				dmaBufBase := ioReqFromUnit + 0x680
				fmt.Printf("[step=%d] Raw DMA base (ioReq+$680=$%08X): ", i, dmaBufBase)
				for j := 0; j < 32; j++ {
					b := make([]byte, 1)
					a.ChipRAM.Read(int(dmaBufBase)+j, b)
					fmt.Printf("%02X", b[0])
				}
				fmt.Println()
				dmaBufData := ioReqFromUnit + 0x684
				fmt.Printf("[step=%d] Raw DMA data (ioReq+$684=$%08X): ", i, dmaBufData)
				for j := 0; j < 32; j++ {
					b := make([]byte, 1)
					a.ChipRAM.Read(int(dmaBufData)+j, b)
					fmt.Printf("%02X", b[0])
				}
				fmt.Println()
				// Expected ADF boot block
				fmt.Printf("[step=%d] Expected (ADF): %x\n", i, a.Disk.adf[:32])
				// Dump decoded data at $5C40 (strap boot block destination)
				fmt.Printf("[step=%d] Boot block at $5C40: ", i)
				for j := 0; j < 32; j++ {
					b := make([]byte, 1)
					a.ChipRAM.Read(0x5C40+j, b)
					fmt.Printf("%02X", b[0])
				}
				fmt.Println()
			} // end fea652Hits <= 2

		case 0x00FEA932, 0x00FEA95C, 0x00FEA970, 0x00FEAC86:
			// ROM MFM decode functions — suppressed (working correctly)

		case 0x00FE85A4:
			// Boot block DOS magic check
			fmt.Printf("[step=%d FE85A4] DOS MAGIC CHECK: A4(bootblock)=$%08X\n", i, a.CPU.A[4])
			// Read first 4 bytes at A4
			if a.CPU.A[4] > 0 && a.CPU.A[4] < 0x80000 {
				magic, _ := a.CPU.M.Long(int(a.CPU.A[4]))
				chksum, _ := a.CPU.M.Long(int(a.CPU.A[4]) + 4)
				fmt.Printf("  magic=$%08X (expect $444F5300='DOS\\0') chksum=$%08X\n", magic, chksum)
			}

		case 0x00FE85BE:
			// Boot block checksum result
			fmt.Printf("[step=%d FE85BE] CHECKSUM D0=$%08X (NOT'd, must be 0)\n", i, ^a.CPU.D[0])

		case 0x00FE85C6:
			// JSR $000C(A4) — call boot block code
			fmt.Printf("[step=%d FE85C6] *** JSR BOOT BLOCK CODE *** A4=$%08X target=$%08X A1(IOReq)=$%08X\n",
				i, a.CPU.A[4], a.CPU.A[4]+0x0C, a.CPU.A[1])

		case 0x00FE86EC:
			// Strap module's JMP (A0) — after boot block returns
			fe86ecHits++
			fmt.Printf("[step=%d FE86EC] *** STRAP JMP (A0) → dos.library *** A0=$%08X D0=$%08X\n",
				i, a.CPU.A[0], a.CPU.D[0])

		case 0x00FC05F0:
			fc05f0Hits++
			fmt.Printf("[step=%d FC05F0] ROM BOOT hit#%d D0=$%08X\n",
				i, fc05f0Hits, a.CPU.D[0])

		default:
			// Trace bootstrap task user-code execution after Wait returns
			// Widen range: maybe the bootstrap code isn't exactly at $4396
			if pc >= 0x3000 && pc <= 0x7000 && bootstrapPostWaitCount < 50 {
				sysBaseD, _ := a.CPU.M.Long(0x0004)
				curTask, _ := a.CPU.M.Long(int(sysBaseD) + 0x114)
				if curTask == 0x3342 {
					bootstrapPostWaitCount++
					w0, _ := a.CPU.M.Word(int(pc))
					w1, _ := a.CPU.M.Word(int(pc) + 2)
					fmt.Printf("[step=%d $%04X] BTS USER: D0=$%08X D1=$%08X A0=$%08X A1=$%08X A5=$%08X A6=$%08X SR=$%04X op=$%04X %04X\n",
						i, pc, a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[1], a.CPU.A[5], a.CPU.A[6], a.CPU.SR, w0, w1)
				}
			}
		}

		// Trace ALL PCs after first bootstrap task dispatch (DISABLED — task $3342 is input.device)
		// if traceBootstrapPCs && traceBootstrapCount < 200 { ... }

		// Trace strap init after Process allocation — only track strap ROM code (FE84xx-FE86xx)
		if strapTraceActive {
			if pc >= 0xFE8440 && pc <= 0xFE86FF {
				strapTraceCount++
				fmt.Printf("[STRAP_TRACE step=%d] PC=$%08X SR=$%04X D0=$%08X D1=$%08X A0=$%08X A1=$%08X A4=$%08X A6=$%08X A7=$%08X\n",
					i, pc, a.CPU.SR, a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[1], a.CPU.A[4], a.CPU.A[6], a.CPU.A[7])
			}
			if strapTraceCount >= 500 {
				strapTraceActive = false
			}
		}

		// Trace trackdisk task execution — log first 500 unique PCs
		if trackdiskTraceActive {
			curTask, _ := a.CPU.M.Long(0x0676 + 0x114)
			if curTask == 0x621E {
				trackdiskTraceCount++
				if trackdiskTraceCount <= 500 {
					fmt.Printf("[TD_TRACE step=%d #%d] PC=$%08X SR=$%04X D0=$%08X D1=$%08X A0=$%08X A1=$%08X A6=$%08X A7=$%08X\n",
						i, trackdiskTraceCount, pc, a.CPU.SR, a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[1], a.CPU.A[6], a.CPU.A[7])
				}
				if trackdiskTraceCount == 500 {
					trackdiskTraceActive = false
				}
			}
		}

		// Specifically trace trackdisk around the DSKLEN write
		// (steps around 26396550 to catch DMA setup context)
		if i >= 26396500 && i <= 26397200 {
			curTask, _ := a.CPU.M.Long(0x0676 + 0x114)
			if curTask == 0x621E && tdMotorLoopTrace < 800 {
				tdMotorLoopTrace++
				prbVal := a.CIAB.Regs(CIARegPRB)
				fmt.Printf("[TD_DMA step=%d] PC=$%08X D0=$%08X D1=$%08X D2=$%08X A3=$%08X A6=$%08X SP=$%08X sel=%v PRB=$%02X\n",
					i, pc, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.A[3], a.CPU.A[6], a.CPU.A[7], a.Disk.selected, prbVal)
			}
		}
		// Trace trackdisk right before DoIO #6 — after 500ms timer returns
		if i >= 2202500 && i <= 2202600 {
			curTask, _ := a.CPU.M.Long(0x0676 + 0x114)
			if curTask == 0x621E {
				fmt.Printf("[TD_WAKE step=%d] PC=$%08X D0=$%08X D1=$%08X D2=$%08X A2=$%08X A3=$%08X A4=$%08X A6=$%08X SP=$%08X\n",
					i, pc, a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.A[2], a.CPU.A[3], a.CPU.A[4], a.CPU.A[6], a.CPU.A[7])
			}
		}

		// Monitor disk state changes
		if !diskMotorSeen && a.Disk.motor {
			diskMotorSeen = true
			fmt.Printf("[step=%d] DISK MOTOR ON! cylinder=%d selected=%v\n",
				i, a.Disk.cylinder, a.Disk.selected)

		}

		if !diskSelectedSeen && a.Disk.selected {
			diskSelectedSeen = true
			fmt.Printf("[step=%d] DISK SELECTED! motor=%v cylinder=%d\n",
				i, a.Disk.motor, a.Disk.cylinder)
		}
		if a.Disk.dsklenWrites != prevDsklenWrites {
			prevDsklenWrites = a.Disk.dsklenWrites
			// Read PRB to see what select/motor bits are asserted
			prbVal := a.CIAB.Regs(CIARegPRB)
			fmt.Printf("[step=%d PC=$%08X] DSKLEN write: dsklen=$%04X writes=%d motor=%v sel=%v adf=%v cyl=%d dskpt=$%08X PRB=$%02X\n",
				i, pc, a.Disk.dsklen, a.Disk.dsklenWrites, a.Disk.motor, a.Disk.selected, a.Disk.adf != nil,
				a.Disk.cylinder, a.Disk.dskpt, prbVal)
		}
		// Periodic progress dump every 2M steps after DMA
		if i >= 26_000_000 && i%2_000_000 == 0 {
			curTask, _ := a.CPU.M.Long(0x0676 + 0x114)
			fmt.Printf("[PROGRESS step=%d] PC=$%08X curTask=$%08X stopped=%v SR=$%04X A0=$%08X A1=$%08X\n",
				i, pc, curTask, a.cpuStopped, a.CPU.SR, a.CPU.A[0], a.CPU.A[1])
			// If in QBlit loop, show blit node counter
			if pc >= 0xFC7C6C && pc <= 0xFC7CC2 {
				a1 := a.CPU.A[1]
				if a1 > 0 && a1 < 0x80000 {
					cnt, _ := a.CPU.M.Byte(int(a1) + 1)
					idx, _ := a.CPU.M.Word(int(a1) + 2)
					fmt.Printf("[PROGRESS] QBlit node @$%08X: count(+1)=%d idx(+2)=%d\n", a1, cnt, idx)
				}
			}
		}

		if err := a.Step(); err != nil {
			fmt.Printf("Error at step %d PC=%08X: %v\n", i, pc, err)
			break
		}

		// Track INTENA changes AFTER step — catch any write to $DFF09A
		// (suppressed for long runs to reduce output)
		if false {
			if intena := a.Custom.ReadReg16(0x09A); intena != prevIntena {
				fmt.Printf("[step=%d PC=%08X] INTENA: $%04X → $%04X\n", i, pc, prevIntena, intena)
				prevIntena = intena
			}
		}

		// Stop if we've reached a boot target
		if fc237eHits > 0 || fc05f0Hits > 0 || fe86ecHits > 0 {
			fmt.Printf("\n=== BOOT TARGET REACHED at step %d: fc237e=%d fc05f0=%d fe86ec(strap→dos)=%d ===\n",
				i, fc237eHits, fc05f0Hits, fe86ecHits)
			break
		}
	}

	fmt.Printf("\n=== Summary (lastStep=%d/%d) ===\n", lastStep, maxSteps)
	// Check BootPtr at ExecBase+$42
	sysBaseEnd, _ := a.CPU.M.Long(0x0004)
	bootPtr, _ := a.CPU.M.Long(int(sysBaseEnd) + 0x42)
	coldCapture, _ := a.CPU.M.Long(int(sysBaseEnd) + 0x2A)
	coolCapture, _ := a.CPU.M.Long(int(sysBaseEnd) + 0x2E)
	fmt.Printf("  ExecBase=$%08X BootPtr(+$42)=$%08X ColdCapture(+$2A)=$%08X CoolCapture(+$2E)=$%08X\n",
		sysBaseEnd, bootPtr, coldCapture, coolCapture)
	// Check the actual LVO table entries for key functions
	// Read JMP targets at SendIO ($04BA), GetMsg ($0502), OpenDevice ($04CC), DoIO ($04C0)
	for _, entry := range []struct {
		name string
		addr int
	}{
		{"SendIO(-$1BC)", 0x04BA}, {"GetMsg(-$174)", 0x0502},
		{"OpenDevice(-$1AA)", 0x04CC}, {"DoIO(-$1B6)", 0x04C0},
		{"WaitPort(-$180)", 0x04F6}, {"PutMsg(-$16E)", 0x0508},
		{"RemResource(-$1DA)", 0x049C}, {"WaitIO(-$1C8)", 0x04AE},
	} {
		jmpOp, _ := a.CPU.M.Word(entry.addr)
		jmpTarget, _ := a.CPU.M.Long(entry.addr + 2)
		fmt.Printf("  LVO %s at $%04X: op=$%04X target=$%08X\n", entry.name, entry.addr, jmpOp, jmpTarget)
	}
	// Check what's at $32EC (the TaskData/LibBase that LaunchPoint reads)
	v32EC, _ := a.CPU.M.Long(0x32EC)
	v32F0, _ := a.CPU.M.Long(0x32F0)
	// Check task names for key tasks
	for _, taskAddr := range []uint32{0x3342, 0x621E, 0xA880, 0x1088, 0xB9F8, 0xD1D0, 0xCCD8} {
		tcName, _ := a.CPU.M.Long(int(taskAddr) + 0x0A) // ln_Name
		tcState, _ := a.CPU.M.Byte(int(taskAddr) + 0x0F)
		nameStr := ""
		if tcName != 0 && tcName < 0x80000 {
			nameBytes := make([]byte, 32)
			a.CPU.M.Read(int(tcName), nameBytes)
			for _, b := range nameBytes {
				if b == 0 {
					break
				}
				nameStr += string(b)
			}
		}
		fmt.Printf("  Task$%04X: state=%d name=%q\n", taskAddr, tcState, nameStr)
	}
	// Check task $3342 state
	tcNode, _ := a.CPU.M.Long(0x3342)        // ln_Succ
	tcNode4, _ := a.CPU.M.Long(0x3342 + 4)   // ln_Pred
	tcName, _ := a.CPU.M.Long(0x3342 + 0x0A) // ln_Name
	fmt.Printf("  Task$3342: ln_Succ=$%08X ln_Pred=$%08X ln_Name=$%08X\n", tcNode, tcNode4, tcName)
	// Read task name string
	if tcName != 0 && tcName < 0x80000 {
		nameBytes := make([]byte, 32)
		a.CPU.M.Read(int(tcName), nameBytes)
		nameStr := ""
		for _, b := range nameBytes {
			if b == 0 {
				break
			}
			nameStr += string(rune(b))
		}
		fmt.Printf("  Task$3342 name: \"%s\"\n", nameStr)
	}
	fmt.Printf("  $32EC (LaunchPoint arg): $%08X $%08X\n", v32EC, v32F0)
	fmt.Printf("  EXEC INIT DISK BOOT SETUP:\n")
	fmt.Printf("    FC043A (BSR→disk-boot installer): %d hits\n", fc043aHits)
	fmt.Printf("    FC2336 (disk-boot-ptr fn entry):  %d hits\n", fc2336Hits)
	fmt.Printf("  FC195A AllocMem-based library init:\n")
	fmt.Printf("    FC197C (TST.L D0 after AllocMem):     %d hits\n", fc197cHits)
	fmt.Printf("    FC19DE (AllocMem FAIL skip path):     %d hits\n", fc19deHits)
	fmt.Printf("  EXEC STOP + ADDTASK SITES:\n")
	fmt.Printf("    FC0F90 (STOP #$2000):        %d hits\n", fc0f90Hits)
	fmt.Printf("    FE5BA2 (AddTask site 1):     %d hits\n", fe5ba2Hits)
	fmt.Printf("    FE9B4C (AddTask site 2):     %d hits\n", fe9b4cHits)
	fmt.Printf("    FF426C (AddTask site 3):     %d hits\n", ff426cHits)
	fmt.Printf("    FF43E6 (AddTask site 4):     %d hits\n", ff43e6Hits)
	fmt.Printf("  COLD BOOT PATH:\n")
	fmt.Printf("    FC0546 (mem size fn):       %d hits\n", fc0546Hits)
	fmt.Printf("    FC0564 (MOVEC/illegal):      %d hits\n", fc0564Hits)
	fmt.Printf("    FC0582 (illegal insn hdlr):  %d hits\n", fc0582Hits)
	fmt.Printf("    FC05B2 (JMP A5):             %d hits\n", fc05b2Hits)
	fmt.Printf("    FC3090 (disk probe entry):   %d hits\n", fc3090Hits)
	fmt.Printf("    FC30B2 (MOVE SR $2700):      %d hits\n", fc30b2Hits)
	fmt.Printf("    FC30DA (DSKBYTR read):       %d hits\n", fc30daHits)
	fmt.Printf("    FC30EC (CMPI #$7F,D0):       %d hits\n", fc30ecHits)
	fmt.Printf("    FC30F4 (BMI ROM boot check): %d hits\n", fc30f4Hits)
	fmt.Printf("    FC30FA (JMP FC237E):         %d hits\n", fc30faHits)
	fmt.Printf("  WARM PROBE PATH (user-mode exception only):\n")
	fmt.Printf("    FC2FF0 (warm probe entry):   %d hits\n", fc2ff0Hits)
	fmt.Printf("    FC3064 (warm disk BSR):      %d hits\n", fc3064Hits)
	fmt.Printf("    FC307C (warm disk boot BRA): %d hits\n", fc307cHits)
	fmt.Printf("  BOOT TARGETS:\n")
	fmt.Printf("    FC237E (BootNode list):      %d hits\n", fc237eHits)
	fmt.Printf("    FC05F0 (ROM boot):           %d hits\n", fc05f0Hits)
	fmt.Printf("    FE86EC (strap→dos.library):  %d hits\n", fe86ecHits)
	fmt.Printf("  Disk: motor=%v selected=%v adf=%v cylinder=%d DSKLEN writes=%d diskChange=%v\n",
		a.Disk.motor, a.Disk.selected, a.Disk.adf != nil, a.Disk.cylinder, a.Disk.dsklenWrites, a.Disk.diskChange)
	fmt.Printf("  Disk status reads: total=%d selected_true=%d selected_false=%d selects=%d deselects=%d\n",
		a.Disk.StatusReads, a.Disk.StatusSelectedTrue, a.Disk.StatusSelectedFalse, a.Disk.SelectCount, a.Disk.DeselectCount)
	fmt.Printf("  DISK RESOURCE/DEVICE:\n")
	fmt.Printf("    FC48D6 (disk_resource_init):  %d hits\n", fc48d6Hits)
	fmt.Printf("    FC4986 (LEA $BFD100 access):  %d hits\n", fc4986Hits)
	fmt.Printf("  TASK/SIGNAL:\n")
	fmt.Printf("    FC08F6 (Supervisor trampoline): %d hits\n", fc08f6Hits)
	fmt.Printf("    $0538 (Wait() LVO):           %d hits\n", waitLVOHits)
	fmt.Printf("    $0658 (Supervisor() LVO):     %d hits\n", supervisorLVOHits)
	fmt.Printf("    FC65EC (Wait() body):         %d hits\n", fc65ecHits)
	fmt.Printf("    FC6DA6 (VBL Signal() calls):  %d hits\n", fc6da6Hits)
	fmt.Printf("    $0532 (exec Signal() LVO):    %d hits\n", execSignalHits)
	fmt.Printf("  COLDSTART MODULE INITS:\n")
	fmt.Printf("    FCABA2 (graphics.library):    %d hits\n", graphicsInitHits)
	fmt.Printf("    FE97BE (trackdisk.device):    %d hits\n", trackdiskInitHits)
	fmt.Printf("    FD3DA6 (intuition.library):   %d hits\n", intuitionInitHits)
	fmt.Printf("    FEB0A8 (romboot.library):     %d hits\n", rombootInitHits)
	fmt.Printf("    FE8444 (strap):               %d hits\n", strapInitHits)
	fmt.Printf("    $055C (AddTask LVO):          %d hits\n", addTaskLVOHits)
	fmt.Printf("    $044E (OpenLibrary LVO):      %d hits\n", openLibLVOHits)
	fmt.Printf("    $04AE (DoIO LVO):             %d hits\n", doIOLVOHits)
	fmt.Printf("    $04BA (OpenDevice LVO):       %d hits\n", openDevLVOHits)
	fmt.Printf("    $04A8 (SendIO LVO):           %d hits\n", sendIOLVOHits)
	fmt.Printf("    FE935A (timer VBL server):     %d hits\n", fe935aHits)
	fmt.Printf("    FF3E94 (dos.library init):    %d hits\n", dosInitHits)
	// Check second task $621E name
	tc2Name, _ := a.CPU.M.Long(0x621E + 0x0A)
	if tc2Name != 0 {
		nameBytes := make([]byte, 32)
		a.CPU.M.Read(int(tc2Name), nameBytes)
		nameStr := ""
		for _, b := range nameBytes {
			if b == 0 {
				break
			}
			nameStr += string(rune(b))
		}
		fmt.Printf("  Task$621E name: \"%s\" (ln_Name=$%08X)\n", nameStr, tc2Name)
		// Check task $621E state
		tc2State, _ := a.CPU.M.Byte(0x621E + 0x0F)
		tc2SigWait, _ := a.CPU.M.Long(0x621E + 0x16)
		tc2SigRecvd, _ := a.CPU.M.Long(0x621E + 0x1A)
		fmt.Printf("  Task$621E: State=$%02X SigWait=$%08X SigRecvd=$%08X\n", tc2State, tc2SigWait, tc2SigRecvd)
		// Check task $1970 state (exec initial task)
		tc3State, _ := a.CPU.M.Byte(0x1970 + 0x0F)
		tc3SigWait, _ := a.CPU.M.Long(0x1970 + 0x16)
		tc3SigRecvd, _ := a.CPU.M.Long(0x1970 + 0x1A)
		fmt.Printf("  Task$1970 (exec initial): State=$%02X SigWait=$%08X SigRecvd=$%08X\n", tc3State, tc3SigWait, tc3SigRecvd)
	}
	fmt.Printf("  FC0FDC context switches: %d total\n", fc0fdcHits)
	fmt.Printf("  Fast-idle: count=%d totalCyclesSkipped=%d avgSkip=%.1f\n",
		a.FastIdleCount, a.FastIdleSkips, func() float64 {
			if a.FastIdleCount == 0 {
				return 0
			}
			return float64(a.FastIdleSkips) / float64(a.FastIdleCount)
		}())
	// CIA-B timer state
	fmt.Printf("  CIA-B Timer A: CRA=$%02X latch=$%04X counter=$%04X ICRmask=$%02X ICRdata=$%02X\n",
		a.CIAB.regs[CIARegCRA], a.CIAB.timerALatch, a.CIAB.timerACounter, a.CIAB.icrMask, a.CIAB.icrData)
	fmt.Printf("  CIA-B Timer B: CRB=$%02X latch=$%04X counter=$%04X\n",
		a.CIAB.regs[CIARegCRB], a.CIAB.timerBLatch, a.CIAB.timerBCounter)
	fmt.Printf("  CIA-A Timer A: CRA=$%02X latch=$%04X counter=$%04X ICRmask=$%02X ICRdata=$%02X\n",
		a.CIAA.regs[CIARegCRA], a.CIAA.timerALatch, a.CIAA.timerACounter, a.CIAA.icrMask, a.CIAA.icrData)
	fmt.Printf("  CIA-A Timer B: CRB=$%02X latch=$%04X counter=$%04X\n",
		a.CIAA.regs[CIARegCRB], a.CIAA.timerBLatch, a.CIAA.timerBCounter)
	// CIA-A TOD counter state
	fmt.Printf("  CIA-A TOD: counter=$%06X latch=$%06X latched=%v clkRate=%d clkDiv=%d\n",
		a.CIAA.todCounter, a.CIAA.todLatch, a.CIAA.todLatched, a.CIAA.todClkRate, a.CIAA.todClkDiv)
	fmt.Printf("  CIA-B TOD: counter=$%06X latch=$%06X latched=%v clkRate=%d clkDiv=%d\n",
		a.CIAB.todCounter, a.CIAB.todLatch, a.CIAB.todLatched, a.CIAB.todClkRate, a.CIAB.todClkDiv)
	// CIA debug counters
	fmt.Printf("  CIA-A: TimerA_UF=%d TimerB_UF=%d TimerB_Ticks=%d IntFires=%d CRA_starts=%d CRB_starts=%d CRA=$%02X CRB=$%02X icrMask=$%02X icrData=$%02X\n",
		a.CIAA.TimerAUnderflows, a.CIAA.TimerBUnderflows, a.CIAA.TimerBTicks, a.CIAA.InterruptFires,
		a.CIAA.CRAWrites, a.CIAA.CRBWrites,
		a.CIAA.Regs(CIARegCRA), a.CIAA.Regs(CIARegCRB), a.CIAA.IcrMask(), a.CIAA.IcrData())
	fmt.Printf("  CIA-B: TimerA_UF=%d TimerB_UF=%d TimerB_Ticks=%d IntFires=%d CRA_starts=%d CRB_starts=%d CRA=$%02X CRB=$%02X icrMask=$%02X icrData=$%02X\n",
		a.CIAB.TimerAUnderflows, a.CIAB.TimerBUnderflows, a.CIAB.TimerBTicks, a.CIAB.InterruptFires,
		a.CIAB.CRAWrites, a.CIAB.CRBWrites,
		a.CIAB.Regs(CIARegCRA), a.CIAB.Regs(CIARegCRB), a.CIAB.IcrMask(), a.CIAB.IcrData())
	fmt.Printf("  CIA-A CRB writes: %d total (log capped at %d)\n", a.CIAA.CRBWrites, len(a.CIAA.CRBWriteLog))
	for i, entry := range a.CIAA.CRBWriteLog {
		if i >= 20 {
			break
		}
		fmt.Printf("    %s\n", entry)
	}
	fmt.Printf("  CIA-B CRB writes: %d total (log capped at %d)\n", a.CIAB.CRBWrites, len(a.CIAB.CRBWriteLog))
	for i, entry := range a.CIAB.CRBWriteLog {
		if i >= 20 {
			break
		}
		fmt.Printf("    %s\n", entry)
	}
	fmt.Printf("  CIA-B TimerA: counter=$%04X latch=$%04X  TimerB: counter=$%04X latch=$%04X\n",
		a.CIAB.TimerACounter(), a.CIAB.TimerALatch(), a.CIAB.TimerBCounter(), a.CIAB.TimerBLatch())
	// Timer.device full structure dump from $2F4E
	fmt.Printf("  Timer.device base at $2F4E (full structure to $3050):\n")
	for doff := 0; doff < 0x102; doff += 4 {
		v, _ := a.CPU.M.Long(0x2F4E + doff)
		fmt.Printf("    $%04X +$%02X: $%08X\n", 0x2F4E+doff, doff, v)
	}
	// Timer.device VBL server data structure dump (is_Data = $2FC6 from VBL chain)
	fmt.Printf("  Timer.device UNIT_VBLANK data at $2FC6:\n")
	for doff := 0; doff < 0x50; doff += 4 {
		v, _ := a.CPU.M.Long(0x2FC6 + doff)
		fmt.Printf("    +$%02X: $%08X\n", doff, v)
	}
	// Walk timer request list at is_Data+$00 (MinList header)
	listHead, _ := a.CPU.M.Long(0x2FC6)
	fmt.Printf("  Timer request list: lh_Head=$%08X\n", listHead)
	if listHead != 0 && listHead < 0x80000 {
		for ni := 0; ni < 10; ni++ {
			succ, _ := a.CPU.M.Long(int(listHead))
			if succ == 0 {
				fmt.Printf("    (end of list)\n")
				break
			}
			// IORequest+$20 = timeval.tv_secs, +$24 = timeval.tv_micro
			tvSecs, _ := a.CPU.M.Long(int(listHead) + 0x20)
			tvMicro, _ := a.CPU.M.Long(int(listHead) + 0x24)
			// IORequest reply port at +$0E, then mp_SigTask, mp_SigBit
			replyPort, _ := a.CPU.M.Long(int(listHead) + 0x0E)
			rpTask := uint32(0)
			rpBit := byte(0)
			if replyPort != 0 && replyPort < 0x80000 {
				rpTask, _ = a.CPU.M.Long(int(replyPort) + 0x10) // mp_SigTask
				rpBit, _ = a.CPU.M.Byte(int(replyPort) + 0x0F)  // mp_SigBit
			}
			fmt.Printf("    node=$%08X succ=$%08X tv_secs=$%08X tv_micro=$%08X replyPort=$%08X sigTask=$%08X sigBit=%d\n",
				listHead, succ, tvSecs, tvMicro, replyPort, rpTask, rpBit)
			listHead = succ
		}
	}
	// Also check trackdisk task signal allocation
	tc2SigAlloc, _ := a.CPU.M.Long(0x621E + 0x12) // tc_SigAlloc
	fmt.Printf("  Task$621E SigAlloc=$%08X\n", tc2SigAlloc)
	// Check trackdisk's timer IORequest at $614E
	timerIODev, _ := a.CPU.M.Long(0x614E + 0x14)   // io_Device
	timerIOUnit, _ := a.CPU.M.Long(0x614E + 0x18)  // io_Unit
	timerIOCmd, _ := a.CPU.M.Word(0x614E + 0x1C)   // io_Command
	timerIOFlags, _ := a.CPU.M.Byte(0x614E + 0x1E) // io_Flags
	timerIOErr, _ := a.CPU.M.Byte(0x614E + 0x1F)   // io_Error
	timerTVSecs, _ := a.CPU.M.Long(0x614E + 0x20)  // tv_secs
	timerTVMicro, _ := a.CPU.M.Long(0x614E + 0x24) // tv_micro
	timerReplyPort, _ := a.CPU.M.Long(0x614E + 0x0E)
	timerSigTask := uint32(0)
	timerSigBit := byte(0)
	if timerReplyPort != 0 && timerReplyPort < 0x80000 {
		timerSigTask, _ = a.CPU.M.Long(int(timerReplyPort) + 0x10)
		timerSigBit, _ = a.CPU.M.Byte(int(timerReplyPort) + 0x0F)
	}
	// Also read ln_Succ and ln_Pred to see if it's in a list
	timerLnSucc, _ := a.CPU.M.Long(0x614E)
	timerLnPred, _ := a.CPU.M.Long(0x614E + 4)
	fmt.Printf("  IOReq$614E: dev=$%08X unit=$%08X cmd=$%04X flags=$%02X err=$%02X\n",
		timerIODev, timerIOUnit, timerIOCmd, timerIOFlags, timerIOErr)
	fmt.Printf("    tv_secs=$%08X tv_micro=$%08X replyPort=$%08X sigTask=$%08X sigBit=%d\n",
		timerTVSecs, timerTVMicro, timerReplyPort, timerSigTask, timerSigBit)
	fmt.Printf("    ln_Succ=$%08X ln_Pred=$%08X (in list? succ!=0: %v)\n",
		timerLnSucc, timerLnPred, timerLnSucc != 0)
	// Dump both timer unit data structures if io_Device reveals a different base
	if timerIODev != 0x2F4E {
		fmt.Printf("  Timer UNIT data at io_Device+$78 = $%08X:\n", timerIODev)
		unitDataBase := int(timerIODev) + 0x78
		for doff := 0; doff < 0x20; doff += 4 {
			v, _ := a.CPU.M.Long(unitDataBase + doff)
			fmt.Printf("    +$%02X: $%08X\n", doff, v)
		}
	}
}
