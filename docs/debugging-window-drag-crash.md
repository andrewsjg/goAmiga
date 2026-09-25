# Debugging: crash shortly after dragging a Workbench window

**Symptom:** Boot Workbench 1.3, open a drawer window, drag it — the screen
corrupts and the system crashes (Guru / task held) shortly afterwards. A long
history of Gurus (#00000004.x, #8408000D AN_BadMessage, #A1000001 bad FreeMem,
freelist `next=$FFFFFF` poisoning) had previously been fought with layered
write-guard heuristics in `Blitter.canWriteD`/`buildDeny` without finding the
root cause.

**Root cause:** the blitter applied line modulos incorrectly in
**descending mode**. The implementation added `BLTxMOD` to the channel
pointers at the end of every line in both directions, with a comment claiming
the HRM says modulos are always added. On real hardware the descending blitter
**subtracts** both the per-word step (−2) *and* the modulo. Consequences:

- Every descending area blit walked its pointers diagonally, off by
  `2×modulo` per row.
- Small blits (window-drag release repaints) sprayed garbage across the
  bitmap — the visible corruption after a drag.
- Tall blits escaped the bitmap entirely and overwrote Exec structures,
  task stacks, MsgPorts and the memory freelist — the crashes. All of the
  historical "impossible" runaway blits were this one bug.

**Fix** (`amiga/blitter.go`, `executeBlit`): subtract modulos in descending
mode, add them in ascending mode. Kickstart 1.3 exercises both conventions:

- Upward-overlap copies use **ascending** blits with *negative* modulos
  (e.g. width 2 words, `dmod=-84` → pitch 4−84 = −80 = exactly one 80-byte
  row upwards). These were already correct.
- True **descending** blits supply the modulo expecting subtraction
  (e.g. width 3 words, `dmod=-86` → pitch −6−(−86) = +80). These were the
  broken ones: "add" produced −92/row, a 12-byte-per-row diagonal drift.

**Guard retirement:** with the root cause fixed, the `canWriteD` heuristics
(fixed address bands, freelist/task/library deny maps, geometric span caps)
are band-aids that can only *deny legitimate blits* (they already broke
`TestBlitterAreaFill`, which blits to $3000 inside a denied band). They are
now gated behind `Blitter.GuardWrites` and **disabled by default**; the
blitter is hardware-accurate and may write anywhere in chip RAM.

## How it was found

1. **Headless reproduction.** Previous headless tests wiggled the mouse but
   never actually opened a window ("headless mouse rarely opens windows"), so
   the interactive crash was never reproduced under instrumentation.
   `wb_open_window_drag_test.go` boots WB (fast: watches for the
   `workbench.task` creation at PC $055C), then drives the pointer
   closed-loop against IntuitionBase and genuinely double-clicks a disk icon
   and drags the opened window by its title bar.

2. **Coordinate-space discovery.** The first attempts pressed in the wrong
   place: **IntuitionBase.MouseY is double the screen Y** (intuition tracks Y
   at interlace resolution; `Screen.MouseY == IB.MouseY/2` on a non-laced
   screen), while MouseX equals screen X on a hires screen. Headless
   interaction must target `(x, 2*y)` in IB coordinates
   (`movePointerToScreen` helper). Symptoms of getting this wrong: clicks
   land on the backdrop, windows never activate, drags never engage.

3. **Pixel forensics.** After a real drag, XOR ghost-box remnants stayed on
   screen and the moved title bar was corrupted. Instrumentation recorded
   every blit's full register state plus the write history of the band the
   remnants occupied (`Blitter.OnExecute` + `DebugWriteHook`, which now also
   fires for line-mode writes). Every ghost draw/erase pair cancelled
   perfectly — except one **descending** area blit (`con1=$2002`,
   width 3 words, `dmod=-86`) whose row starts drifted
   $EE6A → $EE0E → … → $EBE6 (−92 bytes per row instead of −80… or rather
   instead of the correct +80), writing the exact remnant bytes. The modulo
   arithmetic left no other candidate.

## Verification

- `TestWBOpenWindowAndDrag`: opens the RAM DISK window, drags it, asserts
  zero remnant bytes in the uncovered band, freelist intact, no alerts.
- `TestWBMultiDragStress`: six consecutive drags in all directions
  (including upward = descending-heavy paths); window geometry tracks the
  pointer exactly; freelist stays intact; final screen is pixel-clean.
- Full `go test ./amiga/` suite: only `TestBitMapStruct` and
  `TestDiskIllegalOpcode` fail — both are pre-existing diagnostics that
  failed before this fix too. `TestBlitterAreaFill`, previously broken by
  the write guards, passes with guards off. The guard-behavior tests
  (`TestBlitterWriteGuard*`, `TestWB*Denied`/`*ProtectionLive`/`*LVOLive`)
  now set `Blitter.GuardWrites = true` explicitly since they test the
  opt-in guard machinery.

## Useful facts for future sessions

- Fast WB boot in tests: `bootWB(t)` (~4 s) — steps until PC $055C creates
  `"workbench.task"`, then settles; see `wb_drag_release_test.go`.
- IntuitionBase discovery: walk Exec LibList at `ExecBase+$17A` for
  `intuition.library`. Offsets: ActiveWindow $34, FirstScreen $3C,
  MouseY $44, MouseX $46 (MouseY is doubled, see above).
- Window struct: LeftEdge +4, TopEdge +6, Width +8, Height +10, Flags +24
  (bit $2000 = WINDOWACTIVE), Title +32, WLayer +124; Layer bounds at
  layer+16..23.
- The Level-3 interrupt handler entry is $FC0D14 — a PC parked there is the
  normal idle/interrupt state, not a hang.
- The ROM's blitter line-draw loop lives around $FC5740–$FC5790 (writes
  BLTCON0/1 as one long at $DFF040, APTL, BDAT, then BLTSIZE at $FC577A);
  chip-RAM writes logged "at PC $FC577E" are that BLTSIZE store executing.
