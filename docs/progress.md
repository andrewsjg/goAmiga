# goAmiga progress log

Newest entries first. Each entry records what changed, why, and how it was
verified. Longer investigations get their own write-up in `docs/`.

## Status

**Works:** Kickstart 1.3 boots; Workbench 1.3 boots from ADF; drawers open,
windows drag, Preferences launches. Keyboard and mouse work in the window.

**Not yet implemented / known gaps** (roughly in planned order):

1. **Beam/CPU timing.** `CPUClocksPerLine = 114` but a PAL line is ~454
   68000 cycles (7.09 MHz ÷ 15,625 lines/s), so the CPU runs at about a
   quarter speed relative to video. There is no DMA cycle stealing, and
   blits complete instantly.
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
