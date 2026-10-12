# goAmiga architecture

goAmiga emulates the Amiga 500 to 1200 (OCS, ECS and AGA chipsets; 68000 or
68020). The CPU is the pure-Go 680x0 core in `m68k/`
(`github.com/andrewsjg/goamiga/m68k`), part of this module.

## Layout

| Path | Contents |
|---|---|
| `cmd/goamiga/` | CLI and window: `main.go` (flags, no arguments opens the configuration screen), `machine.go` (builds an Amiga from `config.Settings`), `display.go` (the `app` game: configuration screen or running machine), `launcher.go` and `ui.go` (the configuration screen) |
| `internal/config/` | Settings: the `Settings` struct behind the flags, settings files, and saved configurations (`~/.config/goamiga/configs/`) |
| `m68k/` | The CPU: 68000 and 68020 instruction set, 68881/68882 FPU, exceptions, prefetch, cycle timing (`m68k/README.md`); `m68kmem/` memory interfaces; tests include the SingleStepTests 68000 suite (data in `m68k/testdata/680x0`, downloaded, not in git) |
| `amiga/` | The machine: chipset, CIAs, floppy, input, display, and all tests |
| `diag/` | Diagnostic tools: `adf.py` (list and extract files in OFS/FFS disk images) and `disasm68k.py` (68000/68020 disassembler for ROMs and memory dumps; needs capstone) |
| `docs/` | This file, the progress log, and write-ups of the harder bugs |

## Machine (`amiga/amiga.go`)

`Amiga` owns every component and drives time. The main loop is `Step()`:

1. Run one CPU instruction (or, when the CPU is in `STOP`, fast-forward to
   the next VBLANK or CIA timer event).
2. Take the instruction's 68000 cycle count (`CPU.LastCycles`) as elapsed time.
3. Tick deferred completions (blitter, disk DMA).
4. Advance the beam (`advanceBeamPerCycle` / `advanceBeamFast`): run the
   Copper, render finished scanlines, tick CIA E-clock timers (every 10 CPU
   cycles) and TOD counters (CIA-B per scanline, CIA-A per frame), raise
   VERTB at line 0.
5. Wake the CPU from `STOP` if an interrupt above the current mask is pending.

`RunFrame()` steps until the next VBLANK so the display always presents a
complete frame.

Timing: the beam counts PAL colour clocks (3.546895 MHz); the CPU runs at
twice that. A line is `ColorClocksPerLine = 227` colour clocks =
`CPUClocksPerLine = 454` CPU cycles. A frame is 313 lines when LOF is set
(always, unless interlaced) and 312 when clear; with BPLCON0 LACE set,
`startFrame()` toggles LOF each frame. With `NTSC` set
(`SetVideoStandard`), frames are 263/262 lines, and the colour clock
(`ColorClock()`) is 3.579545 MHz. The CPU and chipset run the same number of
cycles per line, so only frame length, audio resampling and host pacing
(`FrameRate()`) change. `a.hpos` is in CPU cycles; the chipset sees `hpos/2` (VHPOSR, and
the Copper via `beamHP()`). The Copper starts one instruction per
`CopperCyclesPerInstr = 8` CPU cycles.

The CPU emulates the 68000's prefetch (`m68k.Processor.Prefetch`). Code
that rewrites the instruction words just ahead of itself still runs the
old ones, as copy protection expects. Exceptions stack 68000 frames:
illegal, line A and line F stack the faulting instruction's own address,
and the T bit raises trace exceptions.

DMA contention (`dma.go`): CPU chip RAM/custom accesses are counted by a
`busCounter` wrapper in the Mapper; after each instruction `chipBusWait` adds
the cycles spent waiting for even slots held by bitplane DMA or the
blitter. The blitter's busy time only counts down on slots bitplane,
refresh and sprite DMA leave free (`blitterSlots`). CIA accesses are
counted the same way and synchronised to the E clock (`ciaAccessWait` in
`cia.go`): 10-19 CPU cycles each, depending on `eClockCounter`.

## Memory map

Built by `wireMemoryMap(overlay)` on a `m68kmem.Mapper` in **OpenBus** mode
(unmapped reads return 0, unmapped/ROM writes are ignored — the 68000 never
faults on the Amiga bus).

| Range | Device |
|---|---|
| `$000000-$0FFFFF` | Chip RAM, 512 KB or 1 MB (`chipram.go`, `SetChipRAM`, `--chipram`). Addresses wrap at the fitted size as Agnus's do, so DMA pointers wrap too. ROM appears here while the CIA-A OVL bit is set at reset |
| `$200000-$9FFFFF` | Zorro II fast RAM once Autoconfig has placed it (`--fastram`; normally at `$200000`) |
| `$BF0000-$BFFFFF` | Both CIAs, partially decoded (`ciaSpace`): CIA-A when A12 is low, odd bytes (`$BFE001`: OVL, LED, disk status, keyboard serial, mouse button, TOD = frame counter); CIA-B when A13 is low, even bytes (`$BFD000`: disk control, index pulse on /FLAG, TOD = scanline counter) |
| `$C00000-$D7FFFF` | Slow RAM (`--slowram`, on the chip bus) |
| `$DFF000-$DFFFFF` | Custom chip registers (`custom.go`) |
| `$E80000-$E8FFFF` | Autoconfig space: the ID of the first board in the chain not yet configured (fast RAM, then the hard disk controller) |
| `$E90000-$EFFFFF` | The hard disk controller once configured (normally `$E90000`, 64 KB: trap register, boot ROM, device structures) |
| `$F80000-$FFFFFF` | Kickstart ROM: 512KB (2.x/3.x) once, or 256KB (1.x) mirrored at `$F80000` and `$FC0000` |

Chipset DMA (Copper, blitter, bitplanes, sprites, disk) reads and writes
`Amiga.ChipRAM` directly rather than through the CPU's mapper.

## Components

| File | Component | Notes |
|---|---|---|
| `custom.go` | Custom register file | Set/clear registers (DMACON, INTENA, INTREQ, ADKCON), interrupt priority → IPL, register dispatch to other components |
| `copper.go` | Copper | MOVE / WAIT / SKIP, COPJMP1/2, run per scanline; OCS register protection (illegal MOVE halts until VBLANK) |
| `blitter.go` | Blitter | Area mode (both directions, minterms, shifts, masks, fill) and line mode. An area blit moves word by word on the bus slots it gets (`blitRun`); line mode completes at once. BBUSY, BZERO, BLIT interrupt at the real end |
| `video.go` | Bitplane DMA + Denise | Per-scanline fetch inside the vertical DIW; `drawLine` composes a view of the beam — standard 640×256 (hires columns from H `$81`, lines 44–299) or overscan 720×284 (`SetOverscan`, `--overscan`): DIW clipping, DDFSTRT placement, BPLCON1 scroll, dual playfield, EHB, HAM, BPLCON2 sprite priority, collisions (CLXDAT), mid-line COLOR/BPLCON1/BPLCON2 writes (`noteWrite`), interlace (640×512, fields woven by LOF) |
| `sprites.go` | Sprites | 8 hardware sprites, DMA and manual modes; `lineBuffer` gives per-column sprite colour and pair, with attached sprites |
| `cia.go` | 8520 CIAs | Timers A/B, ICR, TOD with alarm, serial shift register (keyboard), CPU address decoding and E-clock access timing |
| `drive.go` | Floppy drives | One `Drive` per unit: disk image, motor latch, head, select, change latch, swap, external-drive ID |
| `disk.go` | Disk controller | ADF → AmigaDOS MFM track encoding (6,250 words per revolution), rotating head position (227 CPU cycles per word), bit-level read DMA (`readTrack`, shared with IPF) where WORDSYNC matches any sync word at any bit offset and re-aligns on each one, DSKSYN/DSKBLK timing, index pulse; write DMA decoded back to ADF sectors and saved to the `--disk` file (`--write-protect` to prevent); eject and `SwapDisk` (insert once trackdisk has polled the empty drive) |
| `memory.go`, `autoconfig.go` | Expansion memory | Slow RAM at `$C00000`; Zorro II Autoconfig chain; fast RAM board (`ConfigureMemory`) |
| `aga.go` | AGA | Alice/Lisa IDs, the 256-entry 24-bit palette (banks, LOCT), 8 planes, HAM8, PF2OF, BPLCON4, FMODE fetch widths; drawLine uses a 24-bit line palette for every chipset |
| `ipf.go`, `diskimage.go` | Disk images | ADF, zipped/gzipped ADF, and IPF images. IPF tracks are decoded to raw MFM cells (`bitTrack`), and the controller reads them at bit level (`readTrack`). See `ipf-format.md` |
| `hdboard.go`, `hardfile.go` | Hard disk | Autoboot Zorro II board and `goamiga.device`. Go generates the 68000 boot ROM (DiagArea, RomTag, BootPoint, driver glue) for the board's address. The code writes trap numbers to the board and Go performs the request. Hardfiles are plain or RDB (`AttachHardfile`, `--hdf`) |
| `paula.go` | Paula audio | 4 DMA/manual channels, interrupts, ADKCON modulation; 48 kHz box-filtered stereo mix with A500 fixed and LED filters into an `AudioBuffer` (played by `display.go`) |
| `netboard.go`, `bsdsocket*.go` | Network | `--bsdsocket`: Zorro II board whose ROM adds bsdsocket.library; each vector traps to Go, which runs host sockets non-blocking; calls that must wait `Wait()` in 68000 code and are woken by the board's INT2 server |
| `serial.go`, `serial_host.go` | Serial port | Paula's UART (SERDAT/SERDATR/SERPER, TBE and RBF interrupts, baud-rate timing), CIA-B modem lines; `NetSerial` connects it to a telnet or raw TCP listener (`--serial`) |
| `keyboard.go`, `mouse.go`, `joystick.go` | Input | Keyboard serial handshake via CIA-A. Mouse counters in JOY0DAT. Port 2 joystick in JOY1DAT. Fire buttons on CIA-A PRA. Second and third buttons on the POTGO pins |
| `display.go`, `display_joystick.go`, `display_disk.go`, `display_host.go`, `keyboard_ebiten.go` | Host window: F11 disk dialog (zenity), Cmd host commands, warp, status bar | Ebitengine, 50 fps (60 on NTSC); excluded by `-tags noebitengine` |

## Tests

All tests live in `amiga/` and need `Kick13.rom` and the Workbench 1.3 ADFs
in the repository root (they skip otherwise). There are three kinds:

- **Unit tests** of a component with a bare `Amiga` (blitter, CIA, MFM, sprites).
- **Integration tests** that boot Kickstart/Workbench headlessly and assert
  on OS state or the framebuffer (e.g. `TestWBLaunchPreferences`,
  `TestDiskBootNoIllegalInstruction`). Helpers for driving Workbench are in
  `wb_diag_helpers_test.go`, `wb_drag_release_test.go` and
  `wb_open_window_drag_test.go`.
- **Diagnostic traces** that print large amounts of state. They are skipped
  unless `GOAMIGA_DIAG=1` is set.

```bash
go test ./amiga/                                    # full suite, ~6 min
go test -tags noebitengine ./amiga/                 # without the Ebitengine window code
GOAMIGA_DIAG=1 go test -run TestHotPCs -v ./amiga/  # a diagnostic trace
```

Tests write screenshots (PNG) to the repository root or `screenshots/`;
both are git-ignored.
