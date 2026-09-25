# Debugging: emulator freezes when opening Prefs from the Workbench disk

**Symptom:** boot Workbench, open the Workbench1.3 disk window, open the Prefs
drawer, then click the Preferences icon — the whole emulator appears to hang.
The screen stops updating and no further input has any effect.

**It was not a hang of the Go program.** The 68000 kept executing the whole
time; the *Amiga* had deadlocked. Every task was idle and the Workbench task
was blocked forever in `Wait($00000010)` while holding the layer list and
IntuitionBase locks — which is why the entire UI froze rather than just
Workbench.

**Root cause: CIA-B's TOD counter was wired to the wrong clock, and the TOD
alarm was not implemented at all.**

On the Amiga the two CIA TOD inputs are connected to the video sync signals:

| CIA   | TOD clock source | Rate            |
|-------|------------------|-----------------|
| CIA-A | vertical sync    | 50 Hz (PAL)     |
| CIA-B | horizontal sync  | once per scanline |

So **CIA-B's TOD counts scanlines**, and `graphics.library` uses it as a raster
position counter. Its VBLANK server reloads CIA-B TOD with 1 every frame
(`CRB=$00`, write TODHI/TODMID/TODLO), then sets `CRB=$80` so subsequent TOD
writes load the **alarm**, and enables ICR bit 2 (ALARM). To run a
beam-synchronised blit it writes the target raster line into the alarm; when
TOD reaches that line the CIA-B ALARM interrupt fires (via EXTER) and the
queued blit is started.

The emulator instead had:

```go
// CIA-B TOD is clocked by the disk index pulse: one per revolution at 300 RPM = 5 Hz.
a.CIAB.todClkRate = 141876
```

plus, in `CIA.Tick`, `// TODO: alarm comparison and interrupt generation`, and
in `CIA.Write`, `// else: alarm low byte (not implemented)`.

Consequently the alarm could never fire. Clicking a Workbench icon makes
Workbench build a Bob for the icon (`AddBob` → `SortGList` → `DrawGList`) and
queue it with **`QBSBlit`** — a beam-synchronised blit. It then waits for the
blit to complete. That completion never came, so:

- `GfxBase.bsblthd` (the beam-sync queue) filled and never drained,
- Workbench blocked in `Wait($10)` forever,
- and because it had already taken `LockLayers` + IntuitionBase, every other
  task that touched the display blocked behind it → total freeze.

## Fix

`amiga/cia.go`:
- Added the TOD **alarm** register. TOD writes go to the alarm when CRB bit 7
  is set, to the counter otherwise.
- Added the alarm comparison: on each TOD tick, a match raises ICR bit 2 and
  fires the interrupt on the rising edge (shared `fireIfPending`).
- Implemented the 8520 rule that writing TODHI halts the counter until TODLO
  is written (`todStopped`).
- Added `TickTOD()`, driven by the beam rather than the E clock.

`amiga/amiga.go`:
- `todClkRate` for both CIAs set to 0 (E-clock TOD source retired).
- `CIAB.TickTOD()` is called once per scanline and `CIAA.TickTOD()` once per
  frame, from all three beam-advance paths (`advanceBeamPerCycle`, and both
  the headless and display-active branches of `advanceBeamFast`).

## Result

`bsblthd` drains, the icon Bob is drawn, and double-clicking Preferences now
launches it — the editor opens with all gadgets rendered
(`images/wb_diag_preferences_running.png`).

## How it was found

1. **Reproduced headlessly.** `bootToPrefsWindow` (in
   `wb_diag_helpers_test.go`) boots WB, opens the disk drawer and then the
   Prefs drawer with real double-clicks. Note the drawer needs an *activation*
   click before the double-click registers, and screen Y must be doubled for
   IntuitionBase coordinates (see `debugging-window-drag-crash.md`).
2. **Ruled out a regression.** The blitter descending-modulo fix from the
   previous session was temporarily reverted; the hang reproduced identically,
   proving it was pre-existing and merely newly reachable now that dragging no
   longer crashed first.
3. **Located the blocked task.** Walking Exec's task lists showed every task
   idle and Workbench in TS_WAIT on signal bit 4. A scan of all MsgPorts found
   *no* port with Workbench as SigTask and SigBit 4 — so no message could ever
   wake it; the wakeup had to come from a bare `Signal()`.
4. **Named the call chain.** Hooking every library's LVO jump table and
   logging the ordered calls made by the Workbench task gave the sequence
   `ModifyIDCMP → LockLayers → AllocMem → AddBob → SortGList → DrawGList →
   QBSBlit → Wait($10)`.
5. **Read the ROM.** Tracing the instructions inside `QBSBlit` showed it
   comparing `n_beamsync` against `$DFF004` (VPOSR) and then writing
   `$BFD800`/`$BFD900` — CIA-B TODLO/TODMID — followed by a `cia.resource`
   `AbleICR` call. Logging CIA-B traffic confirmed the
   `CRB=$00 / TOD=1 / CRB=$80` alarm-arming pattern with `icrMask=$04`.

## Verification

- `TestCIATODAlarmFiresOnMatch`, `TestCIATODHaltedByTODHIWrite`,
  `TestCIATODClockedByBeam` — unit coverage for the alarm, the TODHI halt rule
  and the scanline/frame tick rates.
- `TestWBBeamSyncBlitQueueDrains` — asserts `GfxBase.bsblthd` empties after an
  icon click.
- `TestWBPrefsClickDeadlock` — asserts Workbench never ends up blocked on the
  beam-sync signal and that the screen keeps updating.
- `TestWBLaunchPreferences` — end-to-end: Preferences 34.66 opens.
- Full `go test ./amiga/`: unchanged failures `TestBitMapStruct` and
  `TestDiskIllegalOpcode` (both pre-existing, verified before the change).

## Note for future work

Beam-synchronised blits are now *started* correctly. The emulator still
executes each blit instantly at `BLTSIZE` write time rather than metering it
against the beam, so QBSBlit timing is approximate — fine for Workbench, but
demos that rely on precise raster/blitter interleaving may need real cycle
accounting.
