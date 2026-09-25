# goAmiga progress log

Newest entries first. Each entry records what changed, why, and how it was
verified. Longer investigations get their own write-up in `docs/`.

## Status

**Works:** Kickstart 1.3 boots; Workbench 1.3 boots from ADF; drawers open,
windows drag, Preferences launches. Keyboard and mouse work in the window.

**Not yet implemented / known gaps** (roughly in planned order):

1. **Timing (remaining).** Copper, audio and disk DMA slots are not
   charged against the CPU/blitter; floppy DMA completes far faster than a real drive; blit data
   still moves instantly at BLTSIZE (only BBUSY/IRQ timing is real).
2. **Display.** Missing: DIWSTRT/DIWSTOP horizontal positioning,
   BPLCON1 scroll, BPLCON2 priorities, EHB, HAM, dual playfield,
   interlace field weaving, overscan, collision detection (CLXDAT),
   changing registers partway through a line.
3. **Audio.** Paula audio DMA is not implemented.
4. **Machine.** Chip RAM only (no `$C00000` slow RAM or fast RAM). Floppy is
   read-only DF0:. 512KB ROMs are truncated to their upper half, so only
   256KB Kickstarts (1.x) work.
   The CPU does not raise address errors for word/long accesses at odd
   addresses.
5. **Workarounds to remove.** `flood.go` patches the KS1.3 Flood routine
   natively at a ROM-specific address. The underlying blitter problem it
   papers over should be found. `Blitter.GuardWrites` heuristics remain in
   the code (off by default) and could be deleted.

## 2026-09-25 — CIA E-clock access timing and 8520 fixes

**E-clock sync.** The CIAs sit on the 68000's synchronous (6800-style)
bus. The E clock is the CPU clock ÷ 10 (6 cycles low, 4 high). A CIA access
asserts VPA, waits for the next E cycle to start, then takes a whole E
cycle: 10–19 CPU cycles instead of the normal 4. The emulator charged 4.

- The CPU's CIA accesses are counted by the same Mapper wrapper as chip
  bus accesses (`chipBus` renamed `busCounter`).
- `ciaAccessWait` (`cia.go`) places them at the end of the instruction,
  where the CIA operand access normally is. The phase comes from
  `eClockCounter`, which is CPU cycles since the last E-cycle boundary
  (where the CIAs tick). The first access waits for the boundary, and each
  takes one full E cycle.
- Every CIA access now ends on an E boundary, as on hardware. Code that
  times loops on CIA reads, or that assumes a CIA access costs about an
  E cycle (keyboard handshake, floppy step delays), now sees the real cost.

**8520 behaviour fixes** (found reviewing `cia.go` against the datasheet):
- In one-shot mode, writing TAHI/TBHI loads the counter and starts the
  timer even while it is running. Before, this only happened when stopped.
- Timer B INMODE is CRB bits 6–5, not just bit 6. `01` (CNT pin) counted
  the E clock; it now counts nothing, since CNT has no edges in the
  emulator. `11` (Timer A underflows gated by CNT) counts like `10`.
- ICR mask writes keep only bits 0–4.

**Measured.** Boot to `workbench.task` 1,012 → 1,013 frames; 0.3% of CPU
cycles are E-clock sync. The boot trace is identical step for step with and
without the 8520 fixes, so KS 1.3 does not use those paths.

**Tests.** `cia_timing_test.go` checks the wait formula, and that a CPU
`MOVE.B $BFE001,D0` ends on an E boundary with 6–15 extra cycles and takes
all 10 values across the 10 phases. `cia_test.go` covers one-shot restart,
INMODE and the ICR mask. Full suite passes.

**Still simplified.** CIA-A answers on odd and CIA-B on even addresses
only; the emulator decodes both parities for both chips and puts a word
read's value in the high byte. There is no 2-cycle start delay on timers.
The serial port only models keyboard input.

## 2026-09-25 — DMA cycle stealing

**Problem.** Bitplane and blitter DMA never slowed the CPU, and bitplane
DMA never slowed the blitter. On a real Amiga a 4-plane hires or 6-plane
lores screen, or a running blit, costs the CPU a lot of chip bus time.

**How the chip bus works** (HRM, "DMA time slot allocation"). One slot per
colour clock, 227 per line. The 68000's bus cycle is 4 CPU cycles = 2
colour clocks, and it uses only the even slots. Fixed DMA (refresh, disk,
audio, sprites) sits on odd slots early in the line. Bitplanes fetch in
units of 8 colour clocks (lores, order `- 4 6 2 - 3 5 1`) or 4 (hires,
`4 2 3 1`), so only lores planes 5–6 and hires planes 3–4 land on even
slots. The blitter takes any free slot. Unless BLTPRI ("nasty") is set, it
gives the CPU a slot after refusing it for 3 cycles.

**Fix** (`amiga/dma.go`).
- `chipBus` (now `busCounter`) wraps chip RAM and the custom registers in the CPU's Mapper
  and counts CPU word accesses. DMA channels read chip RAM directly, so
  they are not counted; ROM and CIA accesses never wait.
- After each instruction `chipBusWait` places the accesses as back-to-back
  bus cycles on even slots from the instruction's start beam position,
  skipping slots held by bitplane DMA (`bitplaneFetch.uses`, same fetch
  window/conditions as `Video.RenderLine`; the DDF word count is now the
  shared `fetchWords`). The extra colour clocks are added to the step.
- Blitter vs CPU: in nasty mode the first chip access waits for the blit;
  otherwise a blit whose cycle pattern has no idle slot (`hogsBus`: AC,
  AD, ACD, ABD, ABC, ABCD) costs 3 colour clocks per CPU access and the
  blit loses that slot. Blits with idle slots leave the CPU alone.
- Blitter vs bitplanes: the blit's remaining time only counts down on
  colour clocks not used by bitplanes, refresh or (with SPREN) sprites
  (`blitterSlots`).

**Measured** (`TestTimingProfile`): boot to `workbench.task` 990 → 1,012
frames; 0.3% of CPU cycles lost to DMA. Workbench 1.3 is hires 2-plane,
which leaves the CPU's slots alone, so only blits cost anything. Host
speed unchanged.

**Tests.** `dma_test.go`: slot counts per mode, CPU waits for hires 4 /
hires 2 / lores 6 planes, access counting (chip, custom, not ROM), blitter
slots per line with/without sprites, blitter hogging and nasty mode.
`TestStoppedCPUWakesForBlit` now allows a second short idle step, since
refresh slots can push completion past the fast-idle lower bound. Full
suite passes.

## 2026-09-25 — Real blitter busy time

**Problem.** Blit data moves instantly (in `Execute`), and BBUSY then stayed
set for `words × channels × 2` CPU cycles, capped at 512. A full-screen
clear therefore "took" 512 cycles instead of ~41,000. The cap's comment said
real timing stalled window drags; that was written while the
descending-modulo bug was still corrupting memory, and does not reproduce.

A second bug: when the CPU was in STOP, the fast-idle skip only looked at
the next VBLANK and CIA timer events. A task sleeping on a blit or disk
DMA interrupt overslept until one of those, often most of a frame.

**Fix.**
- `blitDuration()` in `blitter.go`: colour clocks per word from the HRM
  cycle-sequence table, indexed by BLTCON0 USEA–D (2, 3 or 4), 4 per pixel
  in line mode, plus 2 for pipeline startup; ×2 for CPU cycles. No cap.
  Contention with bitplane DMA is not modelled yet.
- Fast-idle `skip` is bounded by the blitter's and disk's remaining
  completion cycles.

**Measured** (`TestTimingProfile`): boot to `workbench.task` went from
1,338 to 990 frames. With only the disk bound removed it is 1,044; with only
the blitter bound removed, 1,312. The time was spent oversleeping blit
interrupts.

**Tests.** `TestBlitterBusyDuration` replaces `TestBlitterBusyClearsQuickly`
(which asserted the cap); `TestStoppedCPUWakesForBlit`. Full suite passes,
including the Workbench window-drag tests.

## 2026-09-25 — PAL long/short frames

**Problem.** Every frame was 312 lines and LOF (VPOSR bit 15) flipped each
VBLANK regardless of mode. A real PAL frame is 312.5 lines: non-interlaced,
Agnus produces only long frames (313 lines, LOF=1); with BPLCON0 LACE set it
alternates long and short (313/312) frames and LOF tells software which
field is on screen. Software that checks LOF to pick a copper list saw
the wrong parity every other frame. The end-of-line/VBLANK code was also
copied three times (per-cycle, headless fast-idle, display fast-idle).

**Fix.** One `nextLine()`/`startFrame()` pair in `amiga.go` handles every
path. `startFrame` sets `lof = true` unless LACE is set, in which case it
toggles; `linesThisFrame()` gives 313 or 312 from LOF, including for the
STOP fast-idle skip-to-VBLANK computation. `VPOSW` writes set LOF, as
graphics.library does when switching interlace. `LinesPerFrame` is now the
long frame (313), so `CyclesPerFrame` = 142,102.

**Tests.** `TestFrameLengthAndLOF` (both beam paths: 313 lines/LOF=1
non-interlaced; alternating 313/312 with LOF matching when interlaced) and
`TestVPOSWSetsLOF`. Full suite passes.

## 2026-09-25 — PAL beam timing

**Problem.** `CPUClocksPerLine` was 114, but a PAL scanline is 227 colour
clocks = 454 68000 cycles (the CPU clock is exactly twice the colour clock).
Because instruction timings are real 68000 cycle counts, the CPU got a
quarter of its real time per frame: effectively a 1.8 MHz Amiga. Reading the
code also turned up three related bugs:

- The Copper compared WAIT/SKIP horizontal positions against `hpos*2`
  while the instruction's HP field holds beam bits H8–H2, so horizontal
  waits fired at half the requested position.
- `VHPOSR` returned the CPU-cycle counter instead of colour clocks.
- The per-cycle beam path ran one Copper instruction per CPU cycle; the
  real Copper manages one per 4 colour clocks (8 CPU cycles), ~56 per line.

**Fix.** `ColorClocksPerLine = 227`, `CPUClocksPerLine = 454`
(`CyclesPerFrame = 141,648`). `a.hpos` stays in CPU cycles and is halved
wherever the chipset sees it (`VHPOSR`, `beamHP()` in the Copper). The
Copper runs only on its DMA slots (`CopperCyclesPerInstr = 8`) in both
the per-cycle and per-line paths.

**Measured** with `GOAMIGA_DIAG=1 go test -run TestTimingProfile -v ./amiga/`:

| | Before | After |
|---|---|---|
| Boot to `workbench.task` | 4,330 frames (86.6 s emulated) | 1,338 frames (26.8 s emulated) |
| Host time for that boot | 1.7 s | 1.6 s |
| Idle Workbench, display on | 32× real time, CPU busy 12% | 32× real time, CPU busy 3% |

A busy CPU runs at ~3.5× real speed on the host, so the full-speed CPU
still fits comfortably in real time.

**Tests.** New `beam_timing_test.go` (line length, VHPOSR units, WAIT
horizontal position, Copper throughput per line). `TestWorkbenchBoots`
sampled BPLCON0 at an arbitrary beam position and caught the Copper's
below-display "planes off" value; it now samples at line 150 and also
asserts a hires screen. Full suite passes.

## 2026-09-25 — Housekeeping

- Put goAmiga under git (`main`); CPU changes committed on the
  `amiga-support` branch of `go-m68k`. ROMs, ADFs, the HRM PDF, binaries
  and generated screenshots are git-ignored.
- **go-m68k memory semantics.** The Amiga work had changed the generic RAM,
  ROM and Mapper to return zeros instead of faulting. That broke the
  `dump` examples (the memory dump loop never saw an end of RAM and hung) and
  `ExampleMapper` (ROM padded short reads with `$FF`, hiding the next
  device). Restored strict RAM/ROM behaviour and made open-bus an explicit
  `Mapper.OpenBus` option, which the Amiga enables. Reads and writes now span
  multiple mapped devices and gaps correctly, and wrap at the 24-bit address
  limit. Added `TestMapperOpenBus`. `TestOpenBusReadFFFFFF` now expects the
  wrapped bytes rather than `$FF` padding.
- Chip RAM is now an Amiga-specific device (`amiga/chipram.go`) whose
  addresses wrap at 512KB, as Agnus does, so DMA pointers never fault.
- `-tags noebitengine` builds again (`Video.ScreenImage` moved to `video.go`).
- `diag/` Go tools split into `diag/bootdiag` and `diag/romanalysis` so
  `go build ./...` works.
- Stale tests: deleted `TestBitMapStruct` (waited for a blit to a boot-time
  address that no longer occurs; asserted nothing). Replaced
  `TestDiskIllegalOpcode` — which "failed" when no illegal instruction
  happened — with `TestDiskBootNoIllegalInstruction`, which fails if one
  does (ignoring exec's deliberate MOVEC CPU probe).
- Six print-only diagnostic tests now require `GOAMIGA_DIAG=1`; they
  produced ~2.3M lines of output per test run.
- Added `docs/architecture.md` and this log.

## 2026-08-21 — Prefs hang: CIA TOD alarm

Clicking Preferences deadlocked the OS. CIA-B TOD was clocked from the disk
index pulse and had no alarm, so `QBSBlit` beam-synced blits never ran.
See `debugging-prefs-hang.md`.

## 2026-08-21 — Window-drag crash: blitter descending modulo

Descending blits added modulos instead of subtracting them, walking
diagonally out of bitmaps and corrupting memory. This was the root cause of
a long series of Gurus. See `debugging-window-drag-crash.md`.
