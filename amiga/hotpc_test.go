package amiga

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

func TestHotPCs(t *testing.T) {
	requireDiag(t)
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
	a.CPU.DebugExceptions = true // enable exception debugging to catch privilege violations

	// Load Workbench 1.3 disk 1 if available
	adfPaths := []string{
		"../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf",
		"../wb13.adf",
		"../wb.adf",
	}
	for _, p := range adfPaths {
		if data, err := os.ReadFile(p); err == nil {
			if err := a.Disk.InsertDisk(data); err == nil {
				t.Logf("Inserted disk: %s (%d bytes)", p, len(data))
				break
			}
		}
	}

	const steps = 30_000_000 // Run longer to see trackdisk and boot progress
	pcCounts := make(map[uint32]int64)
	// Track unique PC addresses visited (for large addr ranges)
	pastInit := false // once we're past step 530000, look for new addresses
	fc1810Entries := 0
	fc3020Visits := 0
	var prevPC uint32
	var prev878 uint32 // watchpoint for $878 = AttnFlags (ExecBase+$202)
	if v, err := a.CPU.M.Long(0x878); err == nil {
		prev878 = v
	}
	prevSupervisor := true // track supervisor mode changes
	exec42708Hits := 0     // count hits at addr $42708 (exec library code that faults)
	var prev8EC uint32     // watchpoint for address $8EC (mc_Bytes of free memory chunk)
	if v, err := a.CPU.M.Long(0x8EC); err == nil {
		prev8EC = v
	}
	var prev69C uint32 // watchpoint for ExecBase+$26 (LowMemChkBase)
	if v, err := a.CPU.M.Long(0x69C); err == nil {
		prev69C = v
	}
	var prev6C6 uint32 // watchpoint for ExecBase+$50 = ChkSum field
	if v, err := a.CPU.M.Long(0x6C6); err == nil {
		prev6C6 = v
	}
	var prevMem0000 uint32 // watchpoint: warm-start cookie at address $0000
	prevA3 := a.CPU.A[3]   // watchpoint for A3 (chip RAM top address)

	// Trace around the drive detection / delay loop area
	const traceStart = 527787
	const traceEnd = 527800 // trace FC12FA→FC081C jump

	for i := 0; i < steps; i++ {
		pc := a.CPU.PC

		// Full trace between the two key events
		if i >= traceStart && i <= traceEnd {
			fmt.Printf("  step=%d PC=%08X D0=%08X A0=%08X A1=%08X A2=%08X A3=%08X SP=%08X\n",
				i, pc, a.CPU.D[0], a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], a.CPU.A[3], a.CPU.A[7])
		}

		// Capture register state when FC1810 is entered from outside the inner loop
		if pc == 0x00FC1810 && prevPC != 0x00FC1812 && fc1810Entries < 3 {
			fc1810Entries++
			fmt.Printf("  [FC1810 entry #%d] D3=$%08X A0=$%08X D0=$%08X D2=$%08X step=%d\n",
				fc1810Entries,
				a.CPU.D[3], a.CPU.A[0], a.CPU.D[0], a.CPU.D[2], i)
		}

		// Capture key exec setup addresses
		switch pc {
		case 0x00FC02A8:
			fmt.Printf("  [FC02A8] PRE-AddMemList: D0=$%08X D1=$%08X D2=$%08X A0=$%08X A1=$%08X SP=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.A[0], a.CPU.A[1], a.CPU.A[7], i)
		case 0x00FC1A68:
			fmt.Printf("  [FC1A68] MOVE.L D0,($4,A1) → D0=$%08X A1=$%08X A0=$%08X A2=$%08X D1=$%08X D2=$%08X SP=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.A[1], a.CPU.A[0], a.CPU.A[2], a.CPU.D[1], a.CPU.D[2], a.CPU.A[7], i)
		case 0x00FC1A26:
			// Read return address from stack to find the call site
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			fmt.Printf("  [FC1A26] AddMemList_entry D0_size=$%08X D1_attrs=$%08X A0_base=$%08X A3=$%08X SP=$%08X ret_addr=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[1], a.CPU.A[0], a.CPU.A[3], a.CPU.A[7], retAddr, i)
		case 0x00FC03BE:
			// ADD.L A3, D0 — computes chip_RAM_top_addr + (-$1800) → size
			fmt.Printf("  [FC03BE] ADD.L A3,D0: A3=$%08X D0_before=$%08X step=%d\n",
				a.CPU.A[3], a.CPU.D[0], i)
		case 0x00FC03C0:
			// SUB.L A0, D0 — subtracts base from size
			fmt.Printf("  [FC03C0] SUB.L A0,D0: D0_before=$%08X A0=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.A[0], i)
		case 0x00FC15B6:
			if i < 2_000_000 {
				fmt.Printf("  [FC15B6] Exec debug/init_late step=%d\n", i)
			}
		case 0x00FC3020:
			// CMP.L ($0000).W, D0 — warm-start check: is mem[$0000] == $48454C50?
			fc3020Visits++
			mem0, _ := a.CPU.M.Long(0x0000)
			fmt.Printf("  [FC3020] warm-start check #%d: D0=$%08X mem[$0000]=$%08X SR=$%04X SP=$%08X step=%d\n",
				fc3020Visits, a.CPU.D[0], mem0, a.CPU.SR, a.CPU.A[7], i)
		case 0x00FC3138:
			// Inside FC3128: MOVE.L ($+202, A6), D2 — reads AttnFlags after delay loop
			attnFlags, _ := a.CPU.M.Long(int(a.CPU.A[6]) + 0x202)
			fmt.Printf("  [FC3138] post-delay: A6=$%08X AttnFlags@+$202=$%08X step=%d\n",
				a.CPU.A[6], attnFlags, i)
		case 0x00FC3140:
			// Inside FC3128: BEQ $FC31B8 — if AttnFlags==-1, skip device open
			fmt.Printf("  [FC3140] BEQ_attn: D2=$%08X Z=%v step=%d → %s\n",
				a.CPU.D[2], a.CPU.SR&0x4 != 0, i,
				func() string {
					if a.CPU.D[2] == 0xFFFFFFFF {
						return "TAKEN→FC31B8 (skip device, cookie NOT cleared!)"
					}
					return "not taken (device open attempt)"
				}())
		case 0x00FC3180:
			// Inside FC3128: JSR (-522, A6) — RawDoFmt: formats Guru alert string
			fmt.Printf("  [FC3180] JSR(-522,A6)=RawDoFmt: A6=$%08X D0=$%08X A0=$%08X A1=$%08X A2=$%08X step=%d\n",
				a.CPU.A[6], a.CPU.D[0], a.CPU.A[0], a.CPU.A[1], a.CPU.A[2], i)
		case 0x00FC318A:
			// Inside FC3128: JSR (-552, A6) — CONFIRMED = OpenLibrary (LVO -552)
			// A1 = "intuition.library" (FC3257), D0 = 0 (any version)
			// Expected: D0=0 returned (not open yet), BEQ taken → cookie cleared
			libName318A := func() string {
				s := ""
				for j := 0; j < 20; j++ {
					c, err := a.CPU.M.Byte(int(a.CPU.A[1]) + j)
					if err != nil || c == 0 {
						break
					}
					s += string(rune(c))
				}
				return s
			}()
			fmt.Printf("  [FC318A] JSR(-552,A6)=OpenLibrary: D0=$%08X A0=$%08X A1=$%08X (A1→'%s') step=%d\n",
				a.CPU.D[0], a.CPU.A[0], a.CPU.A[1], libName318A, i)
		case 0x00FC318E:
			// AFTER OpenLibrary returns: TST.L D0
			// D0=0 → library NOT found (correct for first boot, BEQ taken → cookie cleared)
			// D0≠0 → library found (wrong - Guru path)
			mem0, _ := a.CPU.M.Long(0x0000)
			boolStr := "BAD→BEQ_NOT_TAKEN→Guru"
			if a.CPU.D[0] == 0 {
				boolStr = "GOOD→BEQ_TAKEN→CLR_cookie"
			}
			fmt.Printf("  [FC318E] TST.L D0 after OpenLibrary: D0=$%08X mem[$0]==$%08X → %s step=%d\n",
				a.CPU.D[0], mem0, boolStr, i)
		case 0x00FC1474:
			// Entry to OpenLibrary implementation (target of LVO -552 dispatch)
			// A6=ExecBase, A1=libname, D0=version
			// LibList at ExecBase+$17A; should be empty/not contain "intuition.library" at this point
			libListNext, _ := a.CPU.M.Long(int(a.CPU.A[6]) + 0x17A)
			libListHead, _ := a.CPU.M.Long(int(a.CPU.A[6]) + 0x17A + 4)
			libListTailPred, _ := a.CPU.M.Long(int(a.CPU.A[6]) + 0x17A + 8)
			fmt.Printf("  [FC1474] OpenLibrary_entry: D0_ver=$%08X A1_name=$%08X A6_exec=$%08X LibList[Next=$%08X Tail=$%08X TailPred=$%08X] step=%d\n",
				a.CPU.D[0], a.CPU.A[1], a.CPU.A[6], libListNext, libListHead, libListTailPred, i)
			// Check LibList contents: scan for any library nodes
			fmt.Printf("  [FC1474] LibList scan: ")
			nodePtr := libListNext
			if nodePtr == 0 || nodePtr == uint32(int(a.CPU.A[6])+0x17A+4) {
				fmt.Printf("EMPTY (correct - no libs loaded yet)\n")
			} else {
				count := 0
				for nodePtr != 0 && count < 8 {
					nextPtr, _ := a.CPU.M.Long(int(nodePtr))
					namePtr, _ := a.CPU.M.Long(int(nodePtr) + 0xA) // ln_Name offset
					name := ""
					if namePtr > 0 && namePtr < 0x1000000 {
						for j := 0; j < 32; j++ {
							c, err := a.CPU.M.Byte(int(namePtr) + j)
							if err != nil || c == 0 {
								break
							}
							name += string(rune(c))
						}
					}
					fmt.Printf("[node=$%08X succ=$%08X name='%s'] ", nodePtr, nextPtr, name)
					if nextPtr == 0 {
						break
					}
					nodePtr = nextPtr
					count++
				}
				fmt.Printf("\n")
			}
		case 0x00FC1480:
			// BSR FC1696 = FindName call
			fmt.Printf("  [FC1480] FindName_BSR: A0=%08X(LibList) A1=%08X(name) D2=%08X(ver) step=%d\n",
				a.CPU.A[0], a.CPU.A[1], a.CPU.D[2], i)
		case 0x00FC1484:
			// TST.L D0 after FindName
			fmt.Printf("  [FC1484] TST.L D0 after FindName: D0=$%08X (0=not found) step=%d\n", a.CPU.D[0], i)
		case 0x00FC1486:
			// BEQ to "not found" path
			boolStr := "TAKEN→not_found_path (D0=0, returns null)"
			if a.CPU.D[0] != 0 {
				boolStr = "NOT_TAKEN→found_path (D0≠0, calls open)"
			}
			fmt.Printf("  [FC1486] BEQ after FindName: D0=$%08X → %s step=%d\n", a.CPU.D[0], boolStr, i)
		case 0x00FC149A:
			// JSR Permit (called after library not found, TDNestCnt decrement)
			fmt.Printf("  [FC149A] JSR_Permit (not-found cleanup): D0=$%08X A6=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.A[6], i)
		case 0x00FC14A0:
			// RTS from OpenLibrary
			fmt.Printf("  [FC14A0] RTS_from_OpenLibrary: D0=$%08X (0=notfound,OK) step=%d\n",
				a.CPU.D[0], i)
		case 0x00FC31AE:
			// Inside FC3128: CLR.L ($0000).W — should clear warm-start cookie!
			mem0, _ := a.CPU.M.Long(0x0000)
			fmt.Printf("  [FC31AE] CLR_cookie: mem[$0000]=%08X BEFORE (should become 0) step=%d\n",
				mem0, i)
		case 0x00FC31B8:
			// Inside FC3128: exit point when BEQ at FC3140 taken (AttnFlags==-1)
			fmt.Printf("  [FC31B8] FC3128_early_exit: A2=$%08X D2=$%08X step=%d (cookie NOT cleared)\n",
				a.CPU.A[2], a.CPU.D[2], i)
		case 0x00FC31BE:
			// Inside FC3128: RTS — normal return point
			mem0, _ := a.CPU.M.Long(0x0000)
			fmt.Printf("  [FC31BE] FC3128_RTS: mem[$0000]=$%08X D0=$%08X step=%d\n",
				mem0, a.CPU.D[0], i)
		case 0x00FC3028:
			// MOVE.L D0, ($0000).W — write warm-start cookie to chip RAM
			mem0, _ := a.CPU.M.Long(0x0000)
			if i < 2_000_000 {
				fmt.Printf("  [FC3028] MOVE.L D0,($0000).W: D0=$%08X (writing cookie), mem[$0000]_before=$%08X step=%d\n",
					a.CPU.D[0], mem0, i)
			}
		case 0x00FC305C:
			// LEA $0202(A6), A0  → A0 = &AttnFlags = $878
			if i < 2_000_000 {
				attn, _ := a.CPU.M.Long(0x878)
				fmt.Printf("  [FC305C] LEA $202(A6),A0: A6=$%08X D7=$%08X AttnFlags@$878=$%08X step=%d\n",
					a.CPU.A[6], a.CPU.D[7], attn, i)
			}
		case 0x00FC3060:
			// MOVE.L D7, (A0)+ - writes D7 to AttnFlags at $878!
			attn, _ := a.CPU.M.Long(0x878)
			fmt.Printf("  [FC3060] ***WRITE AttnFlags***: D7=$%08X → (A0=$%08X/$878) was=$%08X step=%d\n",
				a.CPU.D[7], a.CPU.A[0], attn, i)
		case 0x00FC3062:
			// MOVE.L (A5), (A0)+ - writes (A5) to ExecBase+$206
			a5val, _ := a.CPU.M.Long(int(a.CPU.A[5]))
			fmt.Printf("  [FC3062] MOVE.L (A5),(A0)+: A5=$%08X (A5)=$%08X A0=$%08X step=%d\n",
				a.CPU.A[5], a5val, a.CPU.A[0], i)
		case 0x00FC3068:
			// AND.L #$FFFF0000, D7 after BSR FC3128 returns
			attn, _ := a.CPU.M.Long(0x878)
			fmt.Printf("  [FC3068] BSR_RET: D7=$%08X D0=$%08X D2=$%08X A6=$%08X AttnFlags@$878=$%08X step=%d\n",
				a.CPU.D[7], a.CPU.D[0], a.CPU.D[2], a.CPU.A[6], attn, i)
		case 0x00FC306E:
			// BNE.B FC3080 - check drive result (D7 high word != 0 → no disk path)
			fmt.Printf("  [FC306E] BNE: D7=$%08X D0=$%08X → %s step=%d\n",
				a.CPU.D[7], a.CPU.D[0], func() string {
					if a.CPU.D[7]&0xFFFF0000 != 0 {
						return "→FC3080 (no disk)"
					}
					return "→FC3070 (check SERDATR)"
				}(), i)
		case 0x00FC3070:
			// TST.L D0 - check D0 after TST
			fmt.Printf("  [FC3070] TST.L D0=$%08X D7=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[7], i)
		case 0x00FC3078:
			// BNE.W $FC05F0 - check if successful boot from ROM
			fmt.Printf("  [FC3078] BNE: flags D0=$%08X D7=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[7], i)
		case 0x00FC307C:
			// BRA.W $FC237E - boot from disk!
			fmt.Printf("  [FC307C] BRA to disk boot $FC237E step=%d *** DISK BOOT ***\n", i)
		case 0x00FC3034:
			// MOVE.L ($0004).W, D0 — reads SysBase for first-pass validation
			sysBase, _ := a.CPU.M.Long(0x0004)
			lowMemChk, _ := a.CPU.M.Long(int(sysBase) + 0x26)
			if i < 2_000_000 {
				fmt.Printf("  [FC3034] SysBase_check: @$4=$%08X LowMemChkBase@+$26=$%08X step=%d\n",
					sysBase, lowMemChk, i)
			}
		case 0x00FC3040:
			// BNE.B FC309A — if SysBase invalid (D1 = SysBase & $00FF0001 != 0) → Guru
			if i < 2_000_000 {
				fmt.Printf("  [FC3040] BNE_SysBase_oddness: D1=$%08X Z=%v (0=notaken/ok) step=%d → %s\n",
					a.CPU.D[1], a.CPU.SR&0x4 != 0, i,
					func() string {
						if a.CPU.D[1] != 0 {
							return "TAKEN→FC309A(Guru)"
						}
						return "not taken(ok)"
					}())
			}
		case 0x00FC304A:
			// BNE.B FC309A — if SysBase + LowMemChkBase + 1 != 0 → Guru
			if i < 2_000_000 {
				fmt.Printf("  [FC304A] BNE_LowMemChk: D0=$%08X Z=%v (0=notaken/ok) step=%d → %s\n",
					a.CPU.D[0], a.CPU.SR&0x4 != 0, i,
					func() string {
						if a.CPU.D[0] != 0 {
							return "TAKEN→FC309A(Guru)"
						}
						return "not taken(ok)"
					}())
			}
		case 0x00FC305A:
			// BNE.B — ROM checksum check
			if i < 2_000_000 {
				fmt.Printf("  [FC305A] BNE_ROM_chk: D0=$%08X D5=$%08X Z=%v step=%d → %s\n",
					a.CPU.D[0], a.CPU.D[5], a.CPU.SR&0x4 != 0, i,
					func() string {
						if a.CPU.SR&0x4 == 0 {
							return "TAKEN→FC3090(Guru)"
						}
						return "not taken(ok)"
					}())
			}
		case 0x00FC3080:
			// No disk present path (target of BNE.B FC306E when D7 upper word != 0)
			fmt.Printf("  [FC3080] no-disk path: D7=$%08X D0=$%08X step=%d\n", a.CPU.D[7], a.CPU.D[0], i)
		case 0x00FC3090:
			// MOVEA.L #$40000, A7 — resets stack pointer! (inside FC3080 path)
			fmt.Printf("  [FC3090] MOVEA.L #$40000,A7 (stack reset): D7=$%08X D0=$%08X A6=$%08X SR=$%04X step=%d\n",
				a.CPU.D[7], a.CPU.D[0], a.CPU.A[6], a.CPU.SR, i)
		case 0x00042708:
			// Exec library code in RAM — this is where the privilege violation originates
			exec42708Hits++
			if exec42708Hits <= 3 {
				w0, _ := a.CPU.M.Word(int(pc))
				w1, _ := a.CPU.M.Word(int(pc) + 2)
				fmt.Printf("  [$42708] exec_RAM_code: SR=$%04X supervisor=%v D0=$%08X A6=$%08X [PC]=%04X %04X step=%d (hit#%d)\n",
					a.CPU.SR, a.CPU.SR&0x2000 != 0, a.CPU.D[0], a.CPU.A[6], w0, w1, i, exec42708Hits)
				// Also dump stack top to see what's there
				sp := a.CPU.A[7]
				s0, _ := a.CPU.M.Long(int(sp))
				s4, _ := a.CPU.M.Long(int(sp) + 4)
				fmt.Printf("  [$42708]   SP=$%08X [SP]=$%08X [SP+4]=$%08X\n", sp, s0, s4)
			}
		case 0x00FC30A2:
			// ANDI.B #$FE, $BFE001 — clear OVL bit (CIA-A PRA bit 0)
			ciaPRA, _ := a.CPU.M.Byte(0xBFE001)
			vec8, _ := a.CPU.M.Long(0x08)  // bus error vector
			vec20, _ := a.CPU.M.Long(0x20) // privilege violation vector
			fmt.Printf("  [FC30A2] ANDI.B #$FE,$BFE001 (clear OVL): CIA_PRA=$%02X SR=$%04X (super=%v) vec8=$%08X vec20=$%08X step=%d\n",
				ciaPRA, a.CPU.SR, a.CPU.SR&0x2000 != 0, vec8, vec20, i)
		case 0x00FC30AA:
			// MOVE.L #$FC30B2, $0020.W — install privilege violation handler
			vec20, _ := a.CPU.M.Long(0x20)
			fmt.Printf("  [FC30AA] install_priv_viol_vector: current@$20=$%08X step=%d\n",
				vec20, i)
		case 0x00FC30B2:
			// MOVE #$2700, SR — this IS the privilege violation handler entry!
			// Reaching here means a privilege violation exception fired
			sp := a.CPU.A[7]
			// On exception entry, stack has: SR (word), PC (long) = 6 bytes pushed
			savedPC, _ := a.CPU.M.Long(int(sp) + 2)
			savedSR, _ := a.CPU.M.Word(int(sp))
			fmt.Printf("  [FC30B2] ***PRIV_VIOL_HANDLER***: saved_PC=$%08X saved_SR=$%04X SP=$%08X D7=$%08X SR=$%04X(super=%v) step=%d\n",
				savedPC, savedSR, sp, a.CPU.D[7], a.CPU.SR, a.CPU.SR&0x2000 != 0, i)
			// Dump exec JMP table entries at key offsets
			fmt.Printf("  [FC30B2]   ExecBase=$%08X\n", a.CPU.A[6])
			for _, eaddr := range []int{0x44E, 0x454, 0x45A, 0x460, 0x466, 0x46C, 0x472, 0x478, 0x47E} {
				v, _ := a.CPU.M.Long(eaddr)
				fmt.Printf("  [FC30B2]   JMP[$%04X]: $%08X\n", eaddr, v)
			}
			// Dump stack to see exception frames
			fmt.Printf("  [FC30B2]   stack dump:\n")
			for soff := 0; soff <= 24; soff += 4 {
				v, _ := a.CPU.M.Long(int(sp) + soff)
				fmt.Printf("  [FC30B2]     [SP+%2d=$%08X]: $%08X\n", soff, uint32(int(sp)+soff), v)
			}
		case 0x00FC30B6:
			// MOVEQ #5, D1 — THE Guru blink outer loop count setup
			fmt.Printf("  [FC30B6] ***GURU_ENTRY*** MOVEQ #5,D1: D0=$%08X D7=$%08X A6=$%08X SP=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[7], a.CPU.A[6], a.CPU.A[7], i)
		case 0x00FC237E:
			// Disk boot code
			fmt.Printf("  [FC237E] DISK BOOT CODE REACHED! step=%d SP=$%08X\n", i, a.CPU.A[7])
		case 0x00FC05F0:
			// ROM boot code
			fmt.Printf("  [FC05F0] ROM boot path step=%d D0=$%08X D7=$%08X\n", i, a.CPU.D[0], a.CPU.D[7])

		// --- Disk DMA and boot progress watchpoints ---
		case 0x00FC060E:
			// Disk fill loop entry: MOVE.L D2,(A0) / DBEQ D0,FC060E
			// Only print once per million steps to avoid spam.
			if i-int(i/1_000_000)*1_000_000 < 10 {
				fmt.Printf("  [FC060E] disk_fill: D0=$%08X D1=$%08X D2=$%08X A0=$%08X step=%d\n",
					a.CPU.D[0], a.CPU.D[1], a.CPU.D[2], a.CPU.A[0], i)
			}
		case 0x00FCAFBA:
			// After WaitTOF: MOVE.W #$0102, $100(A2) — arm disk DMA field
			// Print once per 500K steps to avoid spam (fires every ~35K steps normally)
			if i%500_000 < 10 {
				a2 := a.CPU.A[2]
				dmafield, _ := a.CPU.M.Word(int(a2) + 0x100)
				fmt.Printf("  [FCAFBA] DMA_arm: A2=$%08X A2+$100=$%04X→$0102 step=%d\n", a2, dmafield, i)
			}
		case 0x00FC2330:
			// Possible DOS boot entry / DoIO for disk read
			fmt.Printf("  [FC2330] step=%d D0=$%08X A0=$%08X A1=$%08X SP=$%08X\n",
				i, a.CPU.D[0], a.CPU.A[0], a.CPU.A[1], a.CPU.A[7])
		case 0x00FC23E0:
			// Bootblock execution: JSR to bootblock code in chip RAM
			fmt.Printf("  [FC23E0] *** BOOTBLOCK EXEC *** step=%d D0=$%08X A0=$%08X\n", i, a.CPU.D[0], a.CPU.A[0])
		case 0x00FC241E:
			// After bootblock returns: check result
			fmt.Printf("  [FC241E] bootblock_result: D0=$%08X step=%d\n", a.CPU.D[0], i)

		// --- trackdisk.device timer/TOD polling loop ---
		case 0x00FE8F8E:
			// Entry to the CIA-B polling loop (first read of TODLO)
			// A0 should be the CIA-B base address
			a0 := a.CPU.A[0]
			ciaBase := int(a0)
			// Read CIA-B registers relative to A0
			todlo, _ := a.CPU.M.Byte(ciaBase + 0x800)
			tbhi, _ := a.CPU.M.Byte(ciaBase + 0x700)
			tblo, _ := a.CPU.M.Byte(ciaBase + 0x600)
			crb, _ := a.CPU.M.Byte(ciaBase + 0xF00)
			fmt.Printf("  [FE8F8E] trackdisk_wait: A0=$%08X TODLO=$%02X TBHI=$%02X TBLO=$%02X CRB=$%02X D0=$%08X step=%d\n",
				a0, todlo, tbhi, tblo, crb, a.CPU.D[0], i)
			_ = tblo
		case 0x00FE8FB4:
			// Exit of inner loop (BNE → here): TODLO changed, proceed
			// Also entry of outer loop exit path via BRA
			if i < 10_000_000 {
				fmt.Printf("  [FE8FB4] exit_inner_loop: D0=$%08X step=%d\n", a.CPU.D[0], i)
			}
		case 0x00FC30B4:
			// The drive probe loop entry / also fallthrough after MOVE #$2700,SR (priv viol handler)
			fmt.Printf("  [FC30B4] drive_probe/pre-guru: D1=%d D7=$%08X D0=$%08X A6=$%08X SP=$%08X step=%d\n",
				int32(a.CPU.D[1]), a.CPU.D[7], a.CPU.D[0], a.CPU.A[6], a.CPU.A[7], i)
		case 0x00FC30DA:
			// READ SERDATR
			fmt.Printf("  [FC30DA] READ $DFF018 (SERDATR) D7=$%08X step=%d\n", a.CPU.D[7], i)
		case 0x00FC30EC:
			// CMPI.B #$7F, D0 - check serial terminal
			fmt.Printf("  [FC30EC] CMPI.B #$7F, D0=$%02X (D0_word=$%04X) step=%d\n",
				a.CPU.D[0]&0xFF, a.CPU.D[0]&0xFFFF, i)
		case 0x00FC33E2:
			if i < 2_000_000 {
				fmt.Printf("  [FC33E2] disk/boot attempt? step=%d PC_sp=%08X\n", i, a.CPU.A[7])
			}
		case 0x00FC0100:
			if i < 2_000_000 {
				fmt.Printf("  [FC0100] cold_start step=%d\n", i)
			}
		case 0x00FCEEF2:
			if i < 2_000_000 {
				fmt.Printf("  [FCEEF2] possible RESET/reboot? step=%d\n", i)
			}
		case 0x00FC016E:
			// NEG.W D1 - after summing 25 words from ExecBase+$22
			if i < 8_000_000 {
				// Dump ExecBase+$22 through ExecBase+$52 (25 words)
				execBase := a.CPU.A[6] // A6 = ExecBase at this point
				var sum uint16
				words := make([]uint16, 25)
				for j := 0; j < 25; j++ {
					v, _ := a.CPU.M.Word(int(execBase) + 0x22 + j*2)
					words[j] = uint16(v)
					sum += uint16(v)
				}
				// Also read ChkSum at +$50 and surrounding context
				chkSum, _ := a.CPU.M.Word(int(execBase) + 0x50)
				maxLocMem, _ := a.CPU.M.Long(int(execBase) + 0x3C)
				sysStkUpper, _ := a.CPU.M.Long(int(execBase) + 0x34)
				fmt.Printf("  [FC016E] ExecBase=$%08X chksum_sum=$%04X (want 0) ChkSum@+$50=$%04X MaxLocMem@+$3C=$%08X SysStkUpper@+$34=$%08X step=%d\n",
					execBase, sum, chkSum, maxLocMem, sysStkUpper, i)
				fmt.Printf("  [FC016E] words@+$22..$52: ")
				for j := 0; j < 25; j++ {
					fmt.Printf("%04X ", words[j])
					if j == 12 {
						fmt.Printf("\n  [FC016E]                  ")
					}
				}
				fmt.Println()
			}
			// Post-ExecBase-init: dump ExecBase+$22 fields right after setup
			if i > 526000 && i < 528000 {
				execBase := uint32(0x676)
				var sum uint16
				words := make([]uint16, 25)
				for j := 0; j < 25; j++ {
					v, _ := a.CPU.M.Word(int(execBase) + 0x22 + j*2)
					words[j] = uint16(v)
					sum += uint16(v)
				}
				fmt.Printf("  [FC03C8] post-exec-init ExecBase+$22: %04X %04X %04X %04X %04X... sum=$%04X step=%d\n",
					words[0], words[1], words[2], words[3], words[4], sum, i)
			}
			// NEG.W D1 - after summing 25 words from ExecBase+$22
			if i < 8_000_000 {
				// Dump ExecBase+$22 through ExecBase+$52 (25 words)
				execBase := a.CPU.A[6] // A6 = ExecBase at this point
				var sum uint16
				words := make([]uint16, 25)
				for j := 0; j < 25; j++ {
					v, _ := a.CPU.M.Word(int(execBase) + 0x22 + j*2)
					words[j] = uint16(v)
					sum += uint16(v)
				}
				fmt.Printf("  [FC016E] NEG.W: D1_sum=$%04X (pre-negate) step=%d → expect 0 for warm start\n",
					a.CPU.D[1]&0xFFFF, i)
				fmt.Printf("  [FC016E] ExecBase=$%08X words@+$22: %v sum=$%04X\n",
					execBase, words, sum)
			}
		case 0x00FC0170:
			// BNE.B $FC01CE - cold start if checksum invalid
			if i < 8_000_000 {
				fmt.Printf("  [FC0170] BNE (chksum): D1=$%04X (0=warm start OK) step=%d\n",
					a.CPU.D[1]&0xFFFF, i)
			}
		case 0x00FC014C:
			// MOVE.L $4, D0 - reads ExecBase for warm start check
			if i < 8_000_000 && i != 263331 {
				execBase4, _ := a.CPU.M.Long(4)
				fmt.Printf("  [FC014C] warm_check: A6=$%08X D0=$%08X @$4=$%08X SP=$%08X step=%d OVL=%v\n",
					a.CPU.A[6], a.CPU.D[0], execBase4, a.CPU.A[7], i, a.CIAA.OVL())
			}
		case 0x00FC015C:
			// NOT.L D0 after ExecBase + checksum check
			if i < 8_000_000 {
				fmt.Printf("  [FC015C] NOT.L D0 pre: D0=$%08X D6=$%08X A6=$%08X step=%d\n",
					a.CPU.D[0], a.CPU.D[6], a.CPU.A[6], i)
			}
		case 0x00FC022A:
			fmt.Printf("  [FC022A] move.l A3,D0 → A3=$%08X A0=$%08X A1=$%08X step=%d OVL=%v\n",
				a.CPU.A[3], a.CPU.A[0], a.CPU.A[1], i, a.CIAA.OVL())
		case 0x00FC021A:
			// CMPA.L #$40000, A3 - warm start RAM check
			execBase, _ := a.CPU.M.Long(0)
			execBase4, _ := a.CPU.M.Long(4)
			fmt.Printf("  [FC021A] CMPA #$40000,A3: A3=$%08X step=%d OVL=%v ExecBase@$0=$%08X @$4=$%08X\n",
				a.CPU.A[3], i, a.CIAA.OVL(), execBase, execBase4)
		case 0x00FC0222:
			// MOVE.L #0, $0 - clearing ExecBase pointer
			fmt.Printf("  [FC0222] MOVE.L #0,$0: clearing ExecBase step=%d OVL=%v\n", i, a.CIAA.OVL())
		case 0x00FC05FA:
			// RESET instruction - check ExecBase checksum before RESET
			execBase4, _ := a.CPU.M.Long(4)
			if execBase4 > 0 && execBase4 < 0x80000 {
				var sum uint16
				for j := 0; j < 25; j++ {
					v, _ := a.CPU.M.Word(int(execBase4) + 0x22 + j*2)
					sum += uint16(v)
				}
				fmt.Printf("  [FC05FA] RESET: ExecBase=$%08X checksum_sum=$%04X (want 0) step=%d OVL=%v\n",
					execBase4, sum, i, a.CIAA.OVL())
			} else {
				fmt.Printf("  [FC05FA] RESET_instr: ExecBase@$4=$%08X OVL=%v step=%d\n",
					execBase4, a.CIAA.OVL(), i)
			}
		case 0x00FC05FC:
			// MOVE.L $4,A0 - reads ExecBase (after RESET, OVL may be 1)
			execBase, _ := a.CPU.M.Long(4)
			if i < 2_000_000 {
				fmt.Printf("  [FC05FC] MOVE.L #4,A0: reading $4=$%08X (OVL=%v) step=%d\n",
					execBase, a.CIAA.OVL(), i)
			}
		case 0x00FC02AC:
			// Read mc_Bytes from the first MemChunk (ExecBase is at $676, MemChunk is at ExecBase + mh_size)
			// We know mc_Bytes is at $8EC from trace, let's read it
			mcBytes, _ := a.CPU.M.Long(0x8EC)
			mcNext, _ := a.CPU.M.Long(0x8E8)
			fmt.Printf("  [FC02AC] post-AddMemList D0=$%08X D3=$%08X mem[$8E8]=mc_Next=$%08X mem[$8EC]=mc_Bytes=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[3], mcNext, mcBytes, i)
		case 0x00FC17D4:
			fmt.Printf("  [FC17D4] subroutine_entry D0=$%08X D1=$%08X D3=$%08X A6=$%08X step=%d (D3_before_save)\n",
				a.CPU.D[0], a.CPU.D[1], a.CPU.D[3], a.CPU.A[6], i)
		case 0x00FC17D8:
			fmt.Printf("  [FC17D8] MOVE.L D0,D3 → D0=$%08X D1=$%08X A6=$%08X step=%d (mem_pool_size_or_src?)\n",
				a.CPU.D[0], a.CPU.D[1], a.CPU.A[6], i)
		case 0x00FC1808:
			fmt.Printf("  [FC1808] ADDQ.L #3,D3 → D3_before=$%08X D0=$%08X D2=$%08X (size=%d bytes) step=%d\n",
				a.CPU.D[3], a.CPU.D[0], a.CPU.D[2], a.CPU.D[3], i)
		case 0x00FC17FC:
			// TST.L D0 - entry to the fill subroutine
			fmt.Printf("  [FC17FC] fill_entry D0=$%08X D3=$%08X D2=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[3], a.CPU.D[2], i)
		case 0x00FC1720:
			fmt.Printf("  [FC1720] move.l A1,D3 → A1=$%08X A0=$%08X step=%d\n",
				a.CPU.A[1], a.CPU.A[0], i)

		// --- ChkSum write path watchpoints ---
		case 0x00FC041E:
			// BSR.W $FC1298 — InitCode dispatching
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			fmt.Printf("  [FC041E] BSR→FC1298 (InitCode): A6=$%08X SP=$%08X ret=$%08X step=%d\n",
				a.CPU.A[6], a.CPU.A[7], retAddr, i)
		case 0x00FC043A:
			// BSR.W $FC2336 — alert/interrupt bootstrap
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			fmt.Printf("  [FC043A] BSR→FC2336: A6=$%08X SP=$%08X ret=$%08X step=%d\n",
				a.CPU.A[6], a.CPU.A[7], retAddr, i)
		case 0x00FC043E:
			// MOVEQ #0, D1 — ChkSum computation starts HERE
			fmt.Printf("  [FC043E] ChkSum_start: A6=$%08X D1=$%08X step=%d *** SHOULD BE REACHED\n",
				a.CPU.A[6], a.CPU.D[1], i)
		case 0x00FC0450:
			// MOVE.W D1, $52(A6) — ChkSum written
			chksum, _ := a.CPU.M.Word(int(a.CPU.A[6]) + 0x52)
			fmt.Printf("  [FC0450] ChkSum_WRITE: D1=$%04X old=$%04X A6=$%08X step=%d *** CHKSUM FIXED\n",
				a.CPU.D[1]&0xFFFF, chksum, a.CPU.A[6], i)
		case 0x00FC0452:
			// After ChkSum write: LEA $76(PC), A0
			if i >= 528000 && i < 535000 {
				fmt.Printf("  [FC0452] post-chksum LEA: SP=$%08X D0=$%08X A0=$%08X step=%d\n",
					a.CPU.A[7], a.CPU.D[0], a.CPU.A[0], i)
			}
		case 0x00FC04C6:
			// JSR (-138,A6) = LVO dispatch after interrupt init
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			fmt.Printf("  [FC04C6] JSR(-138,A6): A6=$%08X SP=$%08X retaddr=$%08X D0=$%08X step=%d\n",
				a.CPU.A[6], a.CPU.A[7], retAddr, a.CPU.D[0], i)
		case 0x00FC04CA:
			// BRA.B $FC0500 — skip data table, goto FC0500
			fmt.Printf("  [FC04CA] BRA→FC0500: D0=$%08X D7=$%08X SP=$%08X step=%d\n",
				a.CPU.D[0], a.CPU.D[7], a.CPU.A[7], i)
		case 0x00FC1516:
			// JSR (-108, A6) = Wait() — task suspension point
			// Decode where the JMP table sends us
			waitEntryAddr := uint32(int64(a.CPU.A[6]) - 108)
			jmpInstr, _ := a.CPU.M.Word(int(waitEntryAddr))
			jmpTarget, _ := a.CPU.M.Long(int(waitEntryAddr) + 2)
			d0 := a.CPU.D[0]
			fmt.Printf("  [FC1516] Wait() JSR(-108,A6): A6=$%08X WaitJMP@$%08X=[%04X %08X] D0=$%08X(sigmask) step=%d\n",
				a.CPU.A[6], waitEntryAddr, jmpInstr, jmpTarget, d0, i)
		case 0x00FC0A20:
			// Exec Wait() implementation entry (common location)
			if i < 3_000_000 {
				fmt.Printf("  [FC0A20] Wait_impl: D0=$%08X A6=$%08X SR=$%04X SP=$%08X step=%d\n",
					a.CPU.D[0], a.CPU.A[6], a.CPU.SR, a.CPU.A[7], i)
			}
		case 0x00FC0A30:
			// Possible Wait() body - check for scheduler call
			if i < 3_000_000 {
				fmt.Printf("  [FC0A30] Wait_body: D0=$%08X A6=$%08X SR=$%04X SP=$%08X step=%d\n",
					a.CPU.D[0], a.CPU.A[6], a.CPU.SR, a.CPU.A[7], i)
			}
		case 0x00FC1502:
			// BEQ $FC151E — skip Wait() if flag not set
			if i < 3_000_000 {
				w := a.CPU.D[0] & 0xFFFF
				a1 := a.CPU.A[1]
				flag1c, _ := a.CPU.M.Word(int(a1) + 0x1C)
				fmt.Printf("  [FC1502] BEQ_skip_wait: D0(d1)=$%04X A1=$%08X A1[+$1C]=$%04X Z=%v step=%d → %s\n",
					w, a1, flag1c, a.CPU.SR&0x4 != 0, i,
					func() string {
						if a.CPU.SR&0x4 != 0 {
							return "BEQ→FC151E (skip Wait, ok)"
						}
						return "not taken (will call Wait)"
					}())
			}
		case 0x00FC0EB4:
			// BEQ $FC0EBC — skip reschedule if AttnResched not set
			attnResched, _ := a.CPU.M.Byte(int(a.CPU.A[6]) + 0x124)
			idNest, _ := a.CPU.M.Byte(int(a.CPU.A[6]) + 0x127)
			// Check if interrupted in user mode (bit 5 of saved SR on stack)
			savedSRw, _ := a.CPU.M.Word(int(a.CPU.A[7]) + 0x18) // BTST tested $18(A7)
			fmt.Printf("  [FC0EB4] AttnResched_check: A6=$%08X AttnResched@+$124=%02X idNest@+$127=%d savedSR_bit5=%v step=%d → %s\n",
				a.CPU.A[6], attnResched, int8(idNest), (savedSRw>>5)&1 != 0, i,
				func() string {
					if a.CPU.SR&0x4 != 0 {
						return "BEQ→FC0EBC (skip, AttnResched=0)"
					}
					return "NOT_TAKEN→reschedule!"
				}())
		case 0x00FC6D48:
			// VERTB server entry — A1 = server node, A6 = ExecBase (set by interrupt handler context)
			if i < 3_000_000 && i > 1_000_000 {
				// Decode the VERTB server node pointed to by A1
				a1 := a.CPU.A[1]
				// VERTB server IS structure (Interrupt): at A1
				// is_Node (14 bytes) + is_Data (4) + is_Code (4)
				// is_Node: ln_Succ(4), ln_Pred(4), ln_Type(1), ln_Pri(1), ln_Name(4)
				isCodePtr, _ := a.CPU.M.Long(int(a1) + 0x12) // is_Code = A1+$12
				isDataPtr, _ := a.CPU.M.Long(int(a1) + 0x0E) // is_Data = A1+$0E
				// A1+$C0 is the VERTB-specific data field used by the handler
				ptrC0, _ := a.CPU.M.Long(int(a1) + 0xC0)
				var d2AtC0 uint32
				if ptrC0 != 0 {
					d2AtC0, _ = a.CPU.M.Long(int(ptrC0))
				}
				fmt.Printf("  [FC6D48] VERTB_entry: A1=$%08X is_Data=$%08X is_Code=$%08X A1+$C0=$%08X (A1+$C0)=$%08X step=%d\n",
					a1, isDataPtr, isCodePtr, ptrC0, d2AtC0, i)
			}
		case 0x00FC6D9E:
			// BEQ skip signal — check D2 (read from (A1))
			if i < 3_000_000 && i > 1_000_000 {
				a1 := a.CPU.A[1]
				d2 := a.CPU.D[2]
				origA1, _ := a.CPU.M.Long(int(a.CPU.A[7])) // D2 is on stack
				fmt.Printf("  [FC6D9E] BEQ_skip_signal: D2=$%08X A1=$%08X (A1)=signal stacked_D2=$%08X Z=%v step=%d → %s\n",
					d2, a1, origA1, a.CPU.SR&0x4 != 0, i,
					func() string {
						if a.CPU.SR&0x4 != 0 {
							return "BEQ→FC6DB0 (A1=0, no signal)"
						}
						return "NOT_TAKEN→signal task"
					}())
			}
		case 0x00FC0500:
			// After data table - LEA $FFE2(PC), A0 = LEA some list
			if i < 2_000_000 {
				fmt.Printf("  [FC0500] LEA_list: D0=$%08X D1=$%08X D7=$%08X SP=$%08X step=%d\n",
					a.CPU.D[0], a.CPU.D[1], a.CPU.D[7], a.CPU.A[7], i)
			}
		case 0x00FC0504:
			// BSR.W $FC093C - mysterious call after data table
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			if i < 2_000_000 {
				fmt.Printf("  [FC0504] BSR→FC093C: SP=$%08X retaddr=$%08X D0=$%08X D7=$%08X step=%d\n",
					a.CPU.A[7], retAddr, a.CPU.D[0], a.CPU.D[7], i)
			}
		case 0x00FC093C:
			// Entry to FC093C - this appears to be the Exec scheduler start
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			if i < 2_000_000 {
				fmt.Printf("  [FC093C] entry: SP=$%08X retaddr=$%08X D0=$%08X D1=$%08X D7=$%08X SR=$%04X step=%d\n",
					a.CPU.A[7], retAddr, a.CPU.D[0], a.CPU.D[1], a.CPU.D[7], a.CPU.SR, i)
				// Dump a few instructions at this address
				for j := 0; j < 8; j++ {
					w, _ := a.CPU.M.Word(0x00FC093C + j*2)
					fmt.Printf("  [FC093C]   +%d: %04X\n", j*2, w)
				}
			}
		case 0x00FC2FF0:
			// Warm start probe - should be called from FC093C eventually
			retAddr, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			mem0, _ := a.CPU.M.Long(0)
			execBase4, _ := a.CPU.M.Long(4)
			fmt.Printf("  [FC2FF0] WARM_START_PROBE: SP=$%08X retaddr=$%08X D0=$%08X mem[4]=$%08X mem[0]=$%08X step=%d\n",
				a.CPU.A[7], retAddr, a.CPU.D[0], execBase4, mem0, i)
		case 0x00FC0888:
			// Point just before FC088C modifies stack - trap, show stack context
			sp4, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 4)
			sp4w, _ := a.CPU.M.Word(int(a.CPU.A[7]) + 4)
			fmt.Printf("  [FC0888] (before FC088C): SP=$%08X SP+4=$%08X bit5_of_word_at_SP+4=%v step=%d\n",
				a.CPU.A[7], sp4, (sp4w>>5)&1, i)
		case 0x00FC12FA:
			// MOVEM.L D1/A1, $54(A6, D3.W) — the interrupt vector install
			// Compute EA: A6 + $54 + D3.W (sign-extended)
			d3w := int32(int16(uint16(a.CPU.D[3])))
			ea := uint32(int32(a.CPU.A[6]) + 0x54 + d3w)
			// Read exception vectors for bus error and address error
			busErrVec, _ := a.CPU.M.Long(0x08)
			addrErrVec, _ := a.CPU.M.Long(0x0C)
			level3Vec, _ := a.CPU.M.Long(0x6C)
			fmt.Printf("  [FC12FA] MOVEM D1/A1→$54(A6,D3.W): A6=$%08X D3=$%08X D3.W=%d EA=$%08X SP=$%08X step=%d\n",
				a.CPU.A[6], a.CPU.D[3], a.CPU.D[3]&0xFFFF, ea, a.CPU.A[7], i)
			fmt.Printf("  [FC12FA]   D1=$%08X A1=$%08X BusErrVec=$%08X AddrErrVec=$%08X Level3Vec=$%08X\n",
				a.CPU.D[1], a.CPU.A[1], busErrVec, addrErrVec, level3Vec)
			// Compute EA properly considering sign extension
			d3w_signed := int32(int16(uint16(a.CPU.D[3])))
			ea_full := uint32(int64(a.CPU.A[6]) + 0x54 + int64(d3w_signed))
			fmt.Printf("  [FC12FA]   D3.W_signed=%d EA=$%08X (A6+0x54+D3.W)\n", d3w_signed, ea_full)
			_ = d3w
		case 0x00FC088C:
			// SUBI.L #$FC0816, (A7) then LSR.W then BTST.B #5, $4(A7)
			sp0, _ := a.CPU.M.Long(int(a.CPU.A[7]))
			sp4, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 4)
			fmt.Printf("  [FC088C] BSR-trick: SP=$%08X (A7)=$%08X $4(A7)=$%08X bit5=%v step=%d\n",
				a.CPU.A[7], sp0, sp4, (sp4>>21)&1, i)
		case 0x00FC0896:
			// BTST.B #5, $4(A7) - the critical branch test
			sp4w, _ := a.CPU.M.Word(int(a.CPU.A[7]) + 4)
			fmt.Printf("  [FC0896] BTST.B #5, $4(A7): $4(A7)_word=$%04X bit5=%v step=%d → %s\n",
				sp4w, (sp4w>>5)&1, i,
				func() string {
					if (sp4w>>5)&1 == 0 {
						return "BEQ→FC08D0 (GOOD: returns normally)"
					}
					return "BRA→FC2FF0 (BAD: diverts to boot, skips FC043E-FC0450!)"
				}())
		}
		if i == 530000 {
			pastInit = true
			_ = pastInit // suppress "unused" lint warning
		}

		// Full trace for narrow window around the priv viol trigger
		if i >= 1249490 && i <= 1249510 {
			fmt.Printf("  [TRACE] step=%d PC=%08X SR=$%04X sup=%v SP=$%08X D0=$%08X\n",
				i, pc, a.CPU.SR, a.CPU.SR&0x2000 != 0, a.CPU.A[7], a.CPU.D[0])
		}

		// Extended trace around the first Level 3 interrupt fire
		if i >= 1268730 && i <= 1268950 {
			fmt.Printf("  [TRACE2] step=%d PC=%08X SR=$%04X sup=%v SP=$%08X D0=$%08X pIPL=%d\n",
				i, pc, a.CPU.SR, a.CPU.SR&0x2000 != 0, a.CPU.A[7], a.CPU.D[0], a.CPU.PendingIPL())
		}

		// Trace user-mode execution after interrupt returns (FC124C context)
		if i >= 1268802 && i <= 1269200 {
			fmt.Printf("  [TRACE3] step=%d PC=%08X SR=$%04X sup=%v SP=$%08X D0=$%08X A0=$%08X A1=$%08X\n",
				i, pc, a.CPU.SR, a.CPU.SR&0x2000 != 0, a.CPU.A[7], a.CPU.D[0], a.CPU.A[0], a.CPU.A[1])
		}

		// Capture register state BEFORE the step (to detect exact step that changes SR)
		// Only do expensive work during early boot (first 3M steps)
		srBeforeStep := a.CPU.SR
		superBeforeStep := srBeforeStep&0x2000 != 0

		if i < 3_000_000 {
			// Check BEFORE step: RTE watchpoint (inspect stack frame before it's popped)
			if op, _ := a.CPU.M.Word(int(pc)); op == 0x4E73 { // RTE opcode
				sp := a.CPU.A[7]
				stackSR, _ := a.CPU.M.Word(int(sp))
				stackPC, _ := a.CPU.M.Long(int(sp) + 2)
				willBeSuper := stackSR&0x2000 != 0
				fmt.Printf("  [RTE] step=%d PC=%08X SP=%08X → SR=$%04X PC=$%08X supv_after=%v\n",
					i, pc, sp, stackSR, stackPC, willBeSuper)
			}

			// Check BEFORE step: if already in user mode, dump the instruction context
			if a.CPU.SR&0x2000 == 0 {
				w0, _ := a.CPU.M.Word(int(pc))
				w1, _ := a.CPU.M.Word(int(pc) + 2)
				fmt.Printf("  [USER_EXEC] step=%d PC=%08X SR=$%04X USP=$%08X instr=%04X %04X\n",
					i, pc, a.CPU.SR, a.CPU.A[7], w0, w1)
			}
		}

		if err := a.Step(); err != nil {
			fmt.Printf("Error at step %d PC=%08X: %v\n", i, pc, err)
			break
		}

		// Detect if THIS step cleared the supervisor bit (only during early boot)
		if i < 3_000_000 && superBeforeStep && a.CPU.SR&0x2000 == 0 {
			fmt.Printf("  [WP:S_BIT_CLEAR] step=%d PC_was=%08X SR_before=$%04X SR_after=$%04X → USER MODE\n",
				i, pc, srBeforeStep, a.CPU.SR)
			// Dump instruction bytes that caused this
			b0, _ := a.CPU.M.Byte(int(pc))
			b1, _ := a.CPU.M.Byte(int(pc) + 1)
			b2, _ := a.CPU.M.Byte(int(pc) + 2)
			b3, _ := a.CPU.M.Byte(int(pc) + 3)
			fmt.Printf("  [WP:S_BIT_CLEAR]   instr bytes: %02X %02X %02X %02X\n", b0, b1, b2, b3)
			fmt.Printf("  [WP:S_BIT_CLEAR]   new PC=$%08X new SR=$%04X USP=$%08X SSP=$%08X\n",
				a.CPU.PC, a.CPU.SR, a.CPU.A[7], a.CPU.SSP)
		}

		// Watchpoint: $878 = AttnFlags (ExecBase+$202) - watch all changes
		if i >= 520000 && i <= 700000 {
			cur878, _ := a.CPU.M.Long(0x878)
			if cur878 != prev878 {
				fmt.Printf("  [WP:$878/AttnFlags] step=%d PC_before=%08X changed: $%08X → $%08X (D7=$%08X)\n",
					i, pc, prev878, cur878, a.CPU.D[7])
				prev878 = cur878
			}
		}

		// Watchpoint: detect supervisor→user mode changes - only during early boot
		isSupervisor := a.CPU.SR&0x2000 != 0
		if i < 3_000_000 {
			if prevSupervisor && !isSupervisor {
				// Also dump what's at the faulting PC area
				w0, _ := a.CPU.M.Word(int(a.CPU.PC))
				w1, _ := a.CPU.M.Word(int(a.CPU.PC) + 2)
				fmt.Printf("  [WP:USER_MODE] step=%d PC_before=%08X SR=$%04X (was supervisor) → now USER MODE\n",
					i, pc, a.CPU.SR)
				fmt.Printf("  [WP:USER_MODE]   A7(USP)=$%08X SSP=$%08X D0=$%08X A6=$%08X\n",
					a.CPU.A[7], a.CPU.SSP, a.CPU.D[0], a.CPU.A[6])
				fmt.Printf("  [WP:USER_MODE]   new_PC=$%08X [PC]=%04X %04X\n",
					a.CPU.PC, w0, w1)
			} else if !prevSupervisor && isSupervisor {
				fmt.Printf("  [WP:SUPER_MODE] step=%d PC_before=%08X SR=$%04X re-entered supervisor mode\n",
					i, pc, a.CPU.SR)
			}
		}
		prevSupervisor = isSupervisor

		// Watchpoint: detect exec-in-user-mode (only during early boot - sanity check)
		if i < 3_000_000 && !isSupervisor && a.CPU.PC >= 0x40000 && a.CPU.PC < 0x50000 {
			w0, _ := a.CPU.M.Word(int(a.CPU.PC))
			fmt.Printf("  [WP:EXEC_USER] step=%d EXEC_IN_USER_MODE! PC=$%08X SR=$%04X [PC]=%04X SP=$%08X\n",
				i, a.CPU.PC, a.CPU.SR, w0, a.CPU.A[7])
		}

		// Watchpoint: detect changes to memory[$8EC] (mc_Bytes of free memory pool)
		if i >= 525680 && i <= 527700 {
			cur8EC, _ := a.CPU.M.Long(0x8EC)
			if cur8EC != prev8EC {
				fmt.Printf("  [WP:$8EC] step=%d PC_before=%08X $8EC changed: $%08X → $%08X (SP=%08X D3=%08X)\n",
					i, pc, prev8EC, cur8EC, a.CPU.A[7], a.CPU.D[3])
				prev8EC = cur8EC
			}
		}

		// Watchpoint: detect changes to ExecBase checksum area ($698-$6CA) during first init
		if i >= 525000 && i <= 530000 {
			// Watch ExecBase+$22 = $698 and +$26 = $69C (longword LowMemChkBase)
			chk26, _ := a.CPU.M.Long(0x69C)
			if chk26 != prev69C {
				fmt.Printf("  [WP:$69C] step=%d PC_before=%08X ExecBase+$26 changed: $%08X → $%08X\n",
					i, pc, prev69C, chk26)
				prev69C = chk26
			}
		}

		// Watchpoint: detect writes to ExecBase+$50..+$54 area ($6C6..$6CA) - track ChkSum
		if i >= 520000 && i <= 2_000_000 {
			cur6C6, _ := a.CPU.M.Long(0x6C6)
			if cur6C6 != prev6C6 {
				// Also dump the full checksum sum when this changes
				var chksum16 uint16
				for j := 0; j < 25; j++ {
					v, _ := a.CPU.M.Word(0x698 + j*2)
					chksum16 += uint16(v)
				}
				fmt.Printf("  [WP:$6C6] step=%d PC_before=%08X ChkSum@$6C6 changed: $%08X → $%08X (sum=$%04X)\n",
					i, pc, prev6C6, cur6C6, chksum16)
				prev6C6 = cur6C6
			}
		}

		// Watchpoint: detect writes to address $0000 (warm-start cookie) - early boot only
		if i < 5_000_000 {
			curMem0000, _ := a.CPU.M.Long(0x0000)
			if curMem0000 != prevMem0000 {
				fmt.Printf("  [WP:$0000] step=%d PC_before=%08X mem[$0000] changed: $%08X → $%08X\n",
					i, pc, prevMem0000, curMem0000)
				prevMem0000 = curMem0000
			}
		}

		// Watchpoint: detect when A3 changes (track all changes after chip RAM detection)
		if i >= 263334 && i <= 526731 {
			curA3 := a.CPU.A[3]
			if curA3 != prevA3 {
				if prevA3 == 0x00080000 {
					fmt.Printf("  [WP:A3] step=%d PC_before=%08X A3 clobbered from $80000 → $%08X (SP=%08X D0=%08X)\n",
						i, pc, curA3, a.CPU.A[7], a.CPU.D[0])
				}
				prevA3 = curA3
			}
		}
		if i%10 == 0 {
			pcCounts[pc]++
		}
	}

	type kv struct {
		k uint32
		v int64
	}
	var sorted []kv
	for k, v := range pcCounts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })

	fmt.Printf("\nTop 25 most-executed PC positions (of %d steps):\n", steps)
	for i, kv := range sorted {
		if i >= 25 {
			break
		}
		pct := float64(kv.v*10) * 100 / steps
		fmt.Printf("  %08X  ×%-8d  %.1f%%\n", kv.k, kv.v*10, pct)
	}
}
