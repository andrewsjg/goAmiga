# goAmiga architecture

goAmiga emulates an Amiga 500 (OCS, PAL, 512KB chip RAM, Kickstart 1.3) on
top of the pure-Go 68000 core in `../go-m68k` (imported through a `replace`
directive in `go.mod`).

## Layout

| Path | Contents |
|---|---|
| `cmd/goamiga/` | CLI: `goamiga --rom Kick13.rom --disk wb13.adf` (windowed) or `--headless --cycles N` |
| `amiga/` | The machine: chipset, CIAs, floppy, input, display, and all tests |
| `diag/` | One-off diagnostic tools: Go programs (`bootdiag/`, `romanalysis/`) and Python ROM disassembly/search scripts |
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
`startFrame()` toggles LOF each frame. `a.hpos` is in CPU cycles; the chipset sees `hpos/2` (VHPOSR, and
the Copper via `beamHP()`). The Copper starts one instruction per
`CopperCyclesPerInstr = 8` CPU cycles.

DMA contention (`dma.go`): CPU chip RAM/custom accesses are counted by a
`chipBus` wrapper in the Mapper; after each instruction `chipBusWait` adds
the cycles spent waiting for even slots held by bitplane DMA or the
blitter. The blitter's busy time only counts down on slots bitplane,
refresh and sprite DMA leave free (`blitterSlots`).

## Memory map

Built by `wireMemoryMap(overlay)` on a `m68kmem.Mapper` in **OpenBus** mode
(unmapped reads return 0, unmapped/ROM writes are ignored — the 68000 never
faults on the Amiga bus).

| Range | Device |
|---|---|
| `$000000-$07FFFF` | Chip RAM (`chipram.go`; addresses wrap at 512KB like Agnus). ROM while the CIA-A OVL bit is set at reset |
| `$BFD000-$BFDFFF` | CIA-B (registers on even addresses; disk control, TOD = scanline counter) |
| `$BFE000-$BFEFFF` | CIA-A (registers on odd addresses; OVL, LED, disk status, keyboard serial, mouse button, TOD = frame counter) |
| `$DFF000-$DFFFFF` | Custom chip registers (`custom.go`) |
| `$FC0000-$FFFFFF` | Kickstart ROM (256KB) |

Chipset DMA (Copper, blitter, bitplanes, sprites, disk) reads and writes
`Amiga.ChipRAM` directly rather than through the CPU's mapper.

## Components

| File | Component | Notes |
|---|---|---|
| `custom.go` | Custom register file | Set/clear registers (DMACON, INTENA, INTREQ, ADKCON), interrupt priority → IPL, register dispatch to other components |
| `copper.go` | Copper | MOVE / WAIT / SKIP, COPJMP1/2, run per scanline |
| `blitter.go` | Blitter | Area mode (both directions, minterms, shifts, masks, fill) and line mode. Blits complete instantly at the BLTSIZE write; busy/interrupt are deferred |
| `video.go` | Bitplane renderer | Per-scanline planar→RGBA using DDFSTRT/DDFSTOP, BPLxMOD, 32-colour palette |
| `sprites.go` | Sprites | 8 hardware sprites, DMA and manual modes |
| `cia.go` | 8520 CIAs | Timers A/B, ICR, TOD with alarm, serial shift register (keyboard) |
| `disk.go` | DF0: | ADF → AmigaDOS MFM track encoding, read DMA, WORDSYNC, DSKBLK. Read only |
| `keyboard.go`, `mouse.go` | Input | Keyboard serial handshake via CIA-A; mouse counters in JOY0DAT, button on CIA-A PRA |
| `display.go`, `keyboard_ebiten.go` | Host window | Ebitengine, 50 fps; excluded by `-tags noebitengine` |
| `flood.go` | HLE patch | Intercepts KS1.3 `graphics.library` Flood at `$FC5AD0` and fills natively (a workaround — see `progress.md`) |

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
