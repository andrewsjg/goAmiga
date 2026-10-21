# goAmiga progress log

Newest entries first. Each entry records what changed, why, and how it was
verified. Longer investigations get their own write-up in `docs/`.

## Status

**Works:**
- Kickstart 1.3 and 3.2 (Hyperion 47.96, A500) boot. Workbench 1.3 and 3.2
  run from floppy or hard disk. Drawers open, windows drag, Preferences
  launches.
- Input: keyboard and mouse, plus a joystick from a gamepad or the arrow
  keys.
- Sound plays through Paula.
- Up to four floppy drives, writable and swappable. Hardfiles on an
  autobooting controller or IDE, and host folders as drives.
- The CPU passes the SingleStepTests 68000 suite with 68000 cycle timing,
  and can be a 68020, a 68030 with its MMU, or a 68040 with its MMU and
  FPU (`--cpu 68020|68030|68040`), with an optional 68881/68882 FPU.
- The A3000 preset: the A2091 SCSI controller (disks and a tape drive),
  the battery-backed clock, and the A2065 Ethernet card on a user-mode
  network.
- Amiga Unix (AMIX 2.1) boots from a disk made by `cmd/amixdisk`, runs
  multi-user with networking, and runs X Windows.
- The A4000: the A4000 Kickstart boots Workbench 3.2 from floppy or IDE
  on a 68040 with Ramsey, motherboard RAM, its IDE interface and Zorro
  III RAM.
- RTG: the uaegfx card for Picasso96 2.0 (`--rtg MB`), on Zorro II or III.
  Workbench runs on it in 8-, 16- and 32-bit modes.

**Not yet implemented / known gaps** (roughly in planned order):

1. **Timing (remaining).** The CPU's bus cycles are counted per
   instruction rather than placed within it. The Copper's bus use is
   predicted from its list rather than taken cycle by cycle. IPF tracks'
   variable cell timing isn't modelled.
2. **Display.** A resolution change (hires bit) partway through a line
   isn't shown, and neither are sprite data rewrites while a sprite is
   being shifted out. Collisions are only detected on lines that are drawn
   (not in headless mode).
3. **Paula (other).** The pot counters have no paddles, only an
   analogue joystick.
4. **Machine.** 512 KB, 1 MB or (AGA) 2 MB chip RAM, optional slow and
   Zorro II fast RAM. OCS, ECS or AGA (`--chipset`). Gayle's IDE
   interface is emulated, but not its PCMCIA slot (Kickstart only has
   carddisk.device for SRAM cards; CompactFlash and network cards need
   drivers from Aminet). The programmable beam
   works (BEAMCON0, HTOTAL, VTOTAL; the 31 kHz modes), but the sync and
   blanking registers (HSSTRT, VBSTRT ...) are only stored, and the A2024
   isn't emulated. A plain hardfile (no RDB) can only use Kickstart's
   own filesystems.
   There is no 68060 and no Zorro III I/O boards other than the RTG
   card (see `docs/a4000-68040-plan.md`). The caches aren't emulated
   (CACR is stored).
6. **AMIX.** The arrow keys don't work under X Windows (the X server's
   keymap); they work everywhere else.
5. **Safety nets.** CPU access violations are dropped rather than raised
   (`busErrorCount`); with the open-bus Mapper they should not occur.
   Real bus errors exist only for the A3000/A4000's bus timeout.

## 2026-10-21 — Audio DMA slots follow Paula's requests

The known-gaps list said audio DMA slots weren't charged against the
blitter. They were (`fixedSlot`, since the DMA slot work), but on every
line while a channel's DMA was on. Agnus only uses a channel's slot when
Paula has asked for a word: after the two samples of the last one (one
sample when it modulates another channel). At period 428 that is once
every 856 colour clocks, about every fourth line. `Paula.nextRequest`
gives the time to a channel's next request and the time between them, and
`blitterSlots` takes the slot only when one is due. A channel DMACON has
just started asks at once.

**Verified:** `TestAudioDMASlotsFollowRequests`. At period 428 over 20
lines the channel takes 5 slots, not 20. `TestAudioDMATakesBlitterSlots`
(four channels starting take four slots) still passes.

## 2026-10-21 — CPU-slot RAM on the A3000/A4000

Accelerator cards for the A3000 and A4000 carry fast RAM in the CPU
slot's space, from $08000000 up, just above the motherboard RAM.
`--cpuslotram MB` (`EnableCPUSlotRAM`, up to 128 MB, the slot's space
to $0FFFFFFF) maps it there. Nothing else is needed. The A4000 Kickstart
probes upwards from $08000000, as it probes down from there for the
motherboard RAM, and adds what it finds as fast RAM (attributes $505, as
the motherboard's). Probed with 64 MB up to 1 GB, it found all of it. The
limit keeps the RAM out of Zorro III space, where expansion.library puts
boards from $40000000. Save states and the machine description include
it. Configuration screen: Memory, with a motherboard.

**Verified:** `TestA4000CPUSlotRAM` boots Workbench 3.2 on the A4000
with 64 MB and finds the region at $08000000 in exec's memory list.

## 2026-10-21 — RTG: a hardware pointer

Picasso96 drew the pointer into the screen itself. On every move it
restored what was under the pointer, saved the new spot and drew the
pointer again, all through the emulated CPU. uaegfx.card has no sprite
calls, but rtg.library uses a card's sprite when BoardInfo's Flags has
BIF_HARDWARESPRITE (bit 0, tested as `btst #0,$bd`). It then calls the
routines at SetSprite $206, SetSpritePosition $20A, SetSpriteImage $20E
and SetSpriteColor $212. The offsets and arguments come from
rtg.library's own code:
- SetSprite: D0 = on or off.
- SetSpritePosition: D0/D1 = position, from MouseX/MouseY at $566/$568.
- SetSpriteImage: the image is MouseImage ($56E), in Amiga sprite form,
  MouseWidth × MouseHeight.
- SetSpriteColor: D0 = index 0–2, D1/D2/D3 = R/G/B, from CLUT 17–19.

InitCard (`amiga/rtg_sprite.go`) puts four small routines in the trap
area there, each calling the emulator as the card's own do, and sets the
flag. The pointer is drawn over the picture when it is rendered, and save
states keep it.

**Verified** (`TestRTGPointer`): with the pointer at the same place, the
picture with the hardware pointer is identical, pixel for pixel, to the
one with Picasso96's software pointer (`RTGBoard.SoftPointer`), in CLUT
and ARGB.

## 2026-10-21 — RTG: the card's blitter

uaegfx.card passes each drawing call to the emulator first and draws in
software only if the answer isn't 1. `amiga/rtg_blit.go` now does them
all on the card's memory: FillRect, InvertRect, BlitRect,
BlitRectNoMaskComplete, BlitTemplate (text), BlitPattern,
BlitPlanar2Chunky and BlitPlanar2Direct (icons). Anything outside the
card's memory still goes to Picasso96.

**Checked against Picasso96 itself.** Disassembling uaegfx.card
(`diag/hunkload.py`, which now handles the short relocation hunks)
showed that its stubs keep every register. With `RTGBoard.Verify`, each
call is worked out on the side and answered 0. The software routine then
draws the real thing and returns through a small routine in the trap area,
which compares the two. A first try compared at the next call, which was
too late because the CPU had drawn over the area by then. Picasso96's
routines call the card again from inside (its BlitRect goes through
BlitRectNoMaskComplete), so the checks nest. Workbench was driven on the
card in all 12 pixel formats: a drawer opened, moved, resized and closed,
an icon selected, menus shown. That came to about 900 calls a format,
and the differences found were:

- the minterm comes as Picasso96's 4-bit opcode ($C copy), not an Amiga
  minterm byte;
- a 24-bit pen is stored low byte first, though 16- and 32-bit pens are
  high byte first;
- InvertRect leaves a 32-bit pixel's alpha byte and a 15-bit pixel's spare
  top bit alone.

With those fixed there are no differences (`TestRTGBlitterMatchesSoftware`;
`RTG_ALLFORMATS=1` for all 12).

**Speed** (`TestRTGBlitterSpeed`, A8R8G8B8 640×480, emulated CPU cycles):
50 RectFills take 0.85M cycles instead of 99.3M, and 50 ScrollRasters
0.30M instead of 144.8M.

## 2026-10-20 — RTG: the uaegfx card for Picasso96

`--rtg MB` fits the graphics card UAE gives Picasso96, driven by
Picasso96's own `uaegfx.card` (`amiga/rtg.go`, `amiga/rtg_picture.go`).

**How uaegfx.card talks to the emulator.** Disassembling the card
(`diag/hunkload.py` loads a hunk executable with its relocations for a
disassembler) showed that FindCard looks for a Zorro board from
manufacturer 2011, product 96, and that most entry points push a function
number and `JSR $F0FF60`, UAE's "uaelib" entry in its boot ROM area. Here
that is a small area at $F00000 whose routine writes a trap register
(`$F0FF00`), and Go answers the call with the CPU's registers: the number
at 4(SP), BoardInfo in A0, the result in D0. FindCard, InitCard,
SetSwitch, SetColorArray, SetDAC, SetGC, SetPanning, CalculateBytesPerRow,
SetDisplay and WaitVerticalSync are implemented; the blitter calls answer
0, which sends Picasso96 to its software routines. The board itself is the
video memory, a Zorro II board (1–4 MB) or with a motherboard Zorro III
(16 MB up, at $40000000).

**Why no modes appeared.** The card was found and initialised, but
graphics.library listed no uaegfx modes. Building a ResolutionsList in
InitCard (as UAE's newer card does) made no difference: rtg.library 40.42
takes its resolutions only from the settings file, and only those whose
`STHD` chunk names the board's type (BoardInfo +$AA, 1 for uaegfx). The
supplied presets say 0, "no board"; Picasso96Mode's "Attach settings to"
is what sets it. The tests patch the preset the same way
(`renameSettingsBoard`), after which 13 formats × 6 sizes of
"uaegfx:WxH" modes appear ($5001xxxx up).

**The palette.** rtg.library keeps the CLUT in BoardInfo before calling
SetColorArray; with Workbench open in 256 colours its colours (AA AA AA,
00 00 00, FF FF FF, 66 88 BB) were found at +$242.

**The picture.** While the card has the display (SetSwitch on, a mode
from SetGC, a framebuffer from SetPanning), `Amiga.Picture` converts the
framebuffer from the card's format (CLUT, 15/16-bit in either byte order,
24 and 32-bit in any order) to RGBA, and `PictureSize` is the mode's size.
The window, screenshots, the mouse scaling and the status bar use these
instead of `Video` directly, and the window resizes when the card takes
or gives back the display. Reset gives it back to the chipset. Save
states keep the card (its configuration, mode, palette and memory; the
machine description includes the card, so a state only loads on a machine
with the same card).

**Verified:** `TestRTGCardFound` lists the modes; `TestRTGWorkbench`
boots Workbench 3.2 with Picasso96 into 640×480 CLUT, R5G6B5 and A8R8G8B8
on an A1200 (Zorro II) and A8R8G8B8 on the A4000 (Zorro III) and checks
the picture; `TestRTGStateAndReset` round-trips a save state and resets.
They need `Picasso96/Picasso96.lha` (Aminet) and `lha`.

**Not yet:** the blitter calls and a hardware sprite for the pointer
(both done the next day); running Picasso96's installer and Picasso96Mode by hand
is still to be checked.

## 2026-10-19 — The pot counters; an analogue joystick

Paula's proportional counters now work (`amiga/pots.go`). POT0DAT and
POT1DAT used to read 0.
- Writing POTGO with START clears the four 8-bit counters, which then
  count once a line until each pin's capacitor charges.
- What it charges to:
  - a mouse pin charges at once through the mouse's pull-up (0), unless
    a pressed button grounds it (255);
  - an unconnected pin counts to 255;
  - a pin POTGO makes an output doesn't count;
  - an analogue joystick in port 2 gives its position.
- `--joy-analog` (and a checkbox on the configuration screen) makes the
  gamepad that joystick: its left stick sets the counts. Its buttons are
  on the left and right lines, as on analogue sticks, rather than fire
  and POTY.
- Save states (version 19) keep the counters.

`TestPotCounters` covers START, the stick's position, the mouse pins
with and without a button, an output pin, the counts part-way, and the
analogue buttons.

## 2026-10-19 — Game sweep; the 68LC040's FPU trap

**The sweep.** `diag/games.txt` lists the 26 titles in `ADF/` with
options for each: the A500 originals on Kickstart 1.3 and OCS, the rest
on the A1200. `TestGameBatch` runs them all in about a minute and writes
contact sheets.

Results:

| Title | Reaches |
|---|---|
| Bubble Bobble (both) | play |
| Chaos Engine | character select (with disk 2 in DF1:) |
| Frontier | flight |
| James Pond 2 | in game, after a long load |
| Kick Off 96 | title |
| Lemmings | intro |
| Lotus | track select |
| Pinball Mania | table select |
| Prince of Persia | play |
| SDI (cracked) | title |
| Speedball 2 | title |
| Supremacy | in game |
| Worms | title |
| Civilization | its setup menu |

Running each again on `main` gave the same end state, so the changes of
the last days broke nothing.

Not working, none of them the emulator's fault:
- Colonization and SkidMarks want icon.library on disk.
- Deluxe Galaga is a Workbench version.
- The Flashback and Turrican disks aren't boot disks.
- SDI (Mindscape) asks "Make a ram disk Y/N?" and doesn't take the
  typed answer; its window probably wants a click first.
- FA-18 and Three Stooges stop at cracker intros.
- Alien Breed asks for disk 3 (see below).
- The AGA-fixed Lotus is an FFS disk, which Kickstart 1.3 can't boot.

What looked like regressions (James Pond 2 black, Alien Breed asking for
disk 1) were the harness's doing:
- James Pond 2 loads for over a minute behind a black screen. A disk
  trace showed a track read every 11 frames. 6000 frames gets it in
  game.
- Alien Breed's "Insert disk 1": the old auto-swap changed disks once
  the drive had sat idle for six seconds after use, which happens during
  intros. It now changes disks only when the head has stepped a few
  times with no transfer since the last, which is how a game polls for a
  disk change. Alien Breed's own prompt reads track 0 instead, so it
  isn't detected and the game waits at "Insert disk 3".

Harness additions:
- `more=` gives further disk files, for games zipped one disk to a file
  (Civilization, Kick Off 96, Lemmings, Three Stooges).
- `type=FRAME:TEXT` types an answer.

`TestKeyboardReachesAmigaDOS` checks that typed keys reach AmigaDOS (an
Ask in the Startup-Sequence, under Kickstart 3.2 and 1.3): nothing had
tested keyboard input to the OS before.

**The 68LC040** (`--cpu 68040 --fpu none`). Every FP instruction now
takes the F-line vector with the LC040's format $4 frame (M68040UM
appendix A.5). The frame holds the next instruction's PC, the effective
address it calculated, and the PC of the FP instruction itself, which is
what an FPU emulator in the F-line handler needs. The CPU works out each
FP instruction's length and effective address without running it:
- the general instructions (by opclass and source format, immediate
  operands included);
- FMOVE(M) of control and data registers;
- FScc, FDBcc, FTRAPcc, FBcc.W and .L;
- FSAVE and FRESTORE, which are privileged.

(An)+ and -(An) are left alone, with -(An) reporting the address after
the decrement. `TestLC040FLine` checks eight instructions' frames and
the RTE past them.

## 2026-10-19 — The 31 kHz modes: a programmable beam

Workbench now runs in Multiscan's Productivity (640x480) and in DblPAL,
on AGA and (Productivity) on ECS. The monitor drivers come from the
Storage disk, which isn't among the ADFs here; the tests take them from
an installed Workbench 3.2 on `HDD/dh0.hdf` (`diag/adf.py` now reads
plain hardfiles as well as ADFs, and so do the tests' `adfRead`).

**1. The false genlock.** With the drivers loaded, IPrefs still opened
Workbench in PAL. To find out why, I wrote a test helper, `callLib`,
that calls a library function from the host as the running task.
`ModeNotAvailable()` said `DI_AVAIL_NOTWITHGENLOCK`, and `GfxBase->
DisplayFlags` had GENLOC set.

graphics.library tests for a genlock at $F8F378 in the 3.2 ROM: it sets
BPLCON0 ERSY (external resync), reads VHPOSR for a while, then reads it
again. On real chips with no genlock the counters wait for an external
sync that never comes, so the two readings match. Ours kept counting, so
every machine had a "genlock", and every 31 kHz mode was ruled out.
VPOSR/VHPOSR now read a latched position while ERSY is set. VPOSR also
gives ECS's V9/V10.

**2. The programmable beam.** The copper lists then programmed it (on AGA
BEAMCON0 $1B88: VARBEAMEN, VARVBEN, VARHSYEN, VARVSYEN, LOLDIS,
BLANKEN):

| Mode | HTOTAL+1 | VTOTAL+1 | Rate | Picture |
|---|---|---|---|---|
| Productivity (AGA) | 122 | 506 | 57.5 Hz | 640x480 super-hires |
| Productivity (ECS) | 114 | 525 | 59.3 Hz | 640x480 super-hires |
| DblPAL High Res (AGA) | 130 | 574 | 47.5 Hz | 640x256 scan-doubled to 512 lines |

The line length had been the constant `CPUClocksPerLine` throughout.
Now `Amiga.lineCycles` is recomputed at each line start: HTOTAL+1 colour
clocks with VARBEAMEN, else 227. The frame is VTOTAL+1 lines. The beam
loop, the fast idle path, the DMA slot model and `FrameRate` use them.
Without VARBEAMEN, BEAMCON0's PAL bit picks the PAL or NTSC frame on ECS,
as on the chips; it comes up from `--video`. The copper already compared
8 bits of the line, as the hardware does, so it needed nothing.

**3. The picture.** For the standard beam the framebuffer still shows a
fixed piece of it. For a programmable beam it shows what the display
window covered on the last frame's displayed lines, as a monitor syncs
to the picture. It is drawn at 4:3 with all its lines
(`Video.DisplaySize`).

**4. Scan doubling** (AGA FMODE BSCAN2 and SSCAN2, for DblPAL/DblNTSC).
With BSCAN2 each line's modulo is BPL1MOD or BPL2MOD for every plane,
chosen by the line's parity against DIWSTRT's start line (from memory of
WinUAE). DblPAL uses -88 and +72: each line shows twice. With SSCAN2
sprite DMA fetches data every other line, so the pointer is twice as
tall, as graphics.library expects (its VSTOP is 32 lines on).

**5. The pointer.** On AGA in these modes graphics.library writes sprite
positions 256 lores pixels further right than the display window's
coordinates: the pointer at the left edge of a screen starting at H $53
is at $152, where on a PAL screen starting at $81 it is at $80. ECS
Productivity keeps the PAL relation ($3F for $40). So on AGA with the
programmable beam, sprite positions are taken 256 lores pixels back.
This is derived from what graphics.library writes, consistent across
Productivity and DblPAL.

**6. ECS super-hires was a word off** (at 15 kHz too). The first pixel
was placed with hires's delay (DIW = 2×DDFSTRT + 9). A super-hires word
takes half as long, so it is +5, continuing lores +17 and hires +9. The
picture sat 16 pixels to the right, with its right border wrapped round
to the left edge. AGA's wide fetches reach the 8-clock cap either way,
which is why AGA was right.

**Tests:**
- `TestWorkbench31kHzModes` covers Productivity on AGA and ECS, and
  DblPAL on AGA. It checks the beam (line and frame length), the frame
  size, and the pointer in the corner.
- `TestWorkbenchSuperHiresECS` checks the placement fix.
- The full suite passes.

Not done: the sync and blanking registers only affect timing that isn't
modelled (HSSTRT, VSSTRT, HBSTRT, VBSTRT ...). The 31 kHz modes on a
real monitor also need its sync polarity (BEAMCON0 HSYTRUE ...), which a
window doesn't.

## 2026-10-19 — Bus timeouts on the A3000/A4000

Gary's bus timeout enable (bit 7 of $DE0001) now works. With it set, an
access to nothing in Gary's range ends in a bus error, as a Unix expects
when it probes for hardware. Kickstart writes 0 there and never sets it,
so AmigaOS sees no change: empty addresses still read 0.

- `m68kmem`: a Mapper's `Fault` hook can end an unmapped access with a
  bus error (`IsBusError`) instead of floating. The Decoder hands bus
  errors to `OnBusError` with the logical address.
- The CPU (`Processor.BusErrors`, 030 and 040) takes them through the
  same path as MMU faults: the instruction is undone and a format $B
  (030) or format $7 (040) frame is stacked. On the 040 the SSW's ATC
  bit is clear, which marks a bus error rather than a translation fault.
- The motherboard keeps `BusErrors` in step with the bit (also after a
  reset or a state load). `garyTimesOut` follows WinUAE's model of which
  addresses Gary times out: from $B80000 up, except $DF8000-$DFFFFF and
  $E80000-$F7FFFF, and everything above 16 MB. I have no Commodore
  documentation for this.

`TestGaryBusTimeout` checks this on the 030 and 040: the frame, the fault
address, the stacked PC, the Autoconfig area staying quiet, and quiet
reads again once the bit is cleared.

## 2026-10-18 — The A4000

Phase 3 of `docs/a4000-68040-plan.md`, with the A4000 Kickstart 3.2
(exec 47.7) the user supplied as `ROMS/kicka4000.rom`.

**Method.** A new diagnostic hook, `Mapper.OnOpenBus` (`Amiga.OnOpenBus`),
reports every access to an unmapped address. `TestA4000ROMProbe`
(`GOAMIGA_DIAG=1`) boots the ROM and lists them in order, so each
missing piece showed up as the next thing the ROM touched. The code
around each one was then disassembled with `diag/disasm68k.py`.

**1. Gary and Ramsey** (`amiga/motherboard.go`). The ROM's first accesses
are bytes $DE0000-$DE0002: bus timeout mode, timeout enable, and cold
boot.
- On a 68020+ the reset code checks the cold boot bit (bit 7 of
  $DE0002). If it is set, it clears the ExecBase pointer to force a cold
  start, then clears the bit.
- Later exec copies a routine into chip RAM that reads Ramsey's version
  at $DE0043 ($7F would mean no Ramsey). It then writes page mode, burst
  and wrap into the control register at $DE0003 and loops until it reads
  them back. With nothing mapped, the read gave 0 and the boot hung
  there.
- The emulation keeps the three bit-7 registers (cold boot set at power
  on, not at reset), keeps Ramsey's control register, and reports
  version $0F (the A4000's; the A3000's is $0D).

**2. Motherboard RAM.** Exec sizes it a megabyte at a time downwards from
$08000000 (checking whether the ROM appears mirrored at $07F80000), then
upwards from $08000000 for CPU-slot RAM. `--mbram` (default 16 MB) maps
RAM ending at $07FFFFFF. Exec adds it at priority 30, and it becomes the
fastest RAM in the system.

**3. The IDE interface.** The ROM's scsi.device keeps $DD2020 in A4. From
its accesses:
- data is at $DD2020 and register n at $DD2022 + 4n;
- the control block is at +$1000: device control and alternate status at
  $DD303A;
- it also probes for a second channel at $DD3020;
- the interrupt status is at $DD3020 (bit 7), reaching INT2 with no
  enable or latch.

This is Gayle's IDE with different decoding, so it is a mode of the
Gayle code (`a4000`), sharing its ATA drives. Everything else in
$DD2000-$DD3FFF floats high, so with no drive the status reads $FF and
the driver gives up quickly. Before this was mapped it read 0, and the
boot hung waiting for a drive.

**4. A 24-bit mask in host access.** With the motherboard RAM, exec put
ExecBase at $07xxxxxx, and the test helper read AttnFlags as 0.
`peekBytes`/`pokeBytes`, which goamiga.device, bsdsocket.library and the
diagnostics use to reach Amiga memory, masked addresses to 24 bits. They
now pass the full address; the Mapper wraps to its own width. The
A2091's DMA, a 24-bit Zorro II device, keeps its own mask.

**5. Zorro III** (`amiga/zorro3.go`). From the ROM's expansion code
(`ReadExpansionByte` at $F8493E, `WriteExpansionByte`/`Word`):
- An unconfigured Zorro III board answers at $FF000000. Register k's
  high nibble is at $FF000000 + 4k and its low nibble at +$100; all but
  er_Type read inverted, as on Zorro II.
- The base is written as a word to $44 (A31-A16), after a byte to $48.

`--z3ram MB` adds a RAM board (er_Type $A0 + size, er_Flags $30:
extended sizes, Zorro III). The Autoconfig chain now maps its first
unconfigured board at $FF000000 or $E80000, depending on its bus.

**Configuration.**
- New flags: `--motherboard none|a3000|a4000`, `--mbram KB`, `--z3ram MB`.
- The A4000 preset is a 68040, AGA, 2 MB chip, 16 MB on the motherboard
  and the clock. The configuration screen has a motherboard choice
  (Machine tab) and motherboard and Zorro III RAM (Memory tab).
- On the A4000 the IDE interface is always fitted and `--ide` uses it.
  `--gayle on` with it is an error.
- Save states (version 17) record the motherboard parts and refuse to
  load on a different machine.

**Verified:**
- `TestA4000BootsFloppy`: Workbench 3.2 from floppy. AttnFlags show the
  040 and FPU40, there is a 16 MB fast region at $07000000, the cold boot
  bit is cleared, and Ramsey is set up.
- `TestA4000BootsIDE`: Workbench from an RDB disk on the A4000's IDE, in
  309 frames.
- `TestA4000IDERegisters`: IDENTIFY through the A4000 decoding, the
  interrupt status at $DD3020 and INT2, the floating bus, and Gayle's
  addresses not answering.
- `TestA4000Zorro3RAM`: a 64 MB board configured at $40000000 and in the
  free list, alongside a Zorro II board at $200000.
- The `--headless` binary builds the A4000 from the command line, and
  refuses the motherboard on a 68020 and `--z3ram` without a
  motherboard.
- The full suite passes.

**Notes:**
- The floppy boot takes about 2200 frames, the same as the A1200 ROM with
  the same disk: it is the Install disk, not the A4000.
- The A500/A2000 Kickstart 3.2 ignores the A3000 motherboard (no Ramsey
  setup, no motherboard RAM), so it isn't in the A3000 preset.
- Not done: Zorro III I/O boards, CPU-slot RAM above $08000000, the
  A4000's Ricoh clock (the Oki serves), and the A4000T's SCSI.

## 2026-10-18 — The 68040, and a 32-bit address bus

Phases 1 and 2 of `docs/a4000-68040-plan.md`: `--cpu 68040`. Built from
Motorola's M68040 User's Manual (fetched into `Reference/`).

**The 32-bit bus.** The bus was 24 bits everywhere
(`m68kmem.MaxAddress`), even for the 68030 machines. The Mapper now has
a `Wide` option (all 32 bits, no wrap at 16 MB) which the Amiga sets for
the 68030 and 68040. The 68000 and 68EC020 keep 24 bits. The Decoder now
reduces the CPU's addresses to 32 bits before anything else sees them:
the CPU does its address arithmetic in `int`, so an absolute word such as
`$8000.W` arrived as -32768 and was refused as an access violation. It
now reaches $FFFF8000 (the 24-bit bus sees $FF8000, as a 68000 would).
AMIX boots the same on both buses: two runs of 4000 frames from fresh
copies of its disk gave the same PC, MMU and bus-error count every 100
frames.

**The 68040** (`m68k/m68040.go`):
- MOVEC: CACR (bits 31 and 15 only), TC, ITT0/1, DTT0/1, MMUSR, URP
  and SRP. CAAR is illegal on the 040. Exec tells an 040 from the
  others by these registers.
- The 030's MMU instructions (`$F000`) take the F-line exception, which
  is how software tells the MMUs apart. The 040 has its own PFLUSH (An),
  PFLUSHN, PFLUSHA, PFLUSHAN, PTESTR and PTESTW instead.
- The MMU: a fixed three-level walk from URP or SRP (7/7/6 bits of index
  for 4 KB pages, 7/7/5 for 8 KB), four-byte descriptors, indirect page
  descriptors, U and M updates, W at any level, S. There are separate
  instruction and data ATCs. Unlike the 030, loading TC, URP or SRP
  doesn't flush them. The transparent translation registers are checked
  first, ITT for instruction fetches and DTT for data. A write-protected
  TT block faults even with the MMU off.
- PTEST fills MMUSR (the physical page, G, U1, U0, S, CM, M, W, R; T|R
  for a TT hit) and loads the data ATC.
- Access errors take a 30-word format $7 frame (SSW with ATC, RW,
  SIZE, TM, and the fault address). As for the 030, the instruction is
  undone and RTE runs it again, so the frame never has pending
  write-backs. An address error (a jump to an odd address) takes a
  format $2 frame. RTE knows the 040's formats ($0-$4, $7) and refuses
  the 020/030's $9-$B.
- MOVE16 (all five forms), and CINV and CPUSH, which do nothing because
  the caches aren't emulated.
- The FPU (`FPU68040`): FSAVE writes the 040's idle frame ($41000000),
  which is how exec finds an 040 FPU. The FSxxx forms (FSADD, FSMUL ...)
  round to single precision. Every other instruction works, including
  those a real 040 traps to `68040.library` (FSIN, FETOX, FMOVECR,
  packed decimal ...). Hyperion's 3.2 disks don't include that library,
  so programs run without it.

**The machine.** `--cpu 68040` defaults to 1000% speed (a 25 MHz 040:
about 1.5 clocks an instruction). At that speed most instructions take
less than a 7 MHz clock, and the old rule of at least 2 clocks per
instruction would have capped it at 3.5 MIPS. So for the 040 the
remainder is carried to the next instruction instead, with no minimum.
The other CPUs are timed as before. `--fpu` now defaults to `auto`: none
for the 68020/030, and the built-in FPU for the 040, which `--fpu none`
removes (a 68LC040). The configuration screen has the 68040, and save
states (version 16) keep its MMU registers.

**Verified:**
- `m68k/m68040_test.go` covers:
  - the MOVEC registers and their masks, and CAAR being illegal;
  - the 030's PMOVE being F-line on the 040;
  - translation with 4 KB and 8 KB pages, and indirect descriptors;
  - U/M updates at each level;
  - an access error at an invalid page, with the handler mapping the
    page and RTE rerunning the instruction (60-byte frame, (A3)+
    undone);
  - write-protected and supervisor-only pages;
  - DTT0 transparent translation, and TT write protection with the MMU
    off;
  - PTESTR and MMUSR;
  - PFLUSHAN keeping global pages, and PFLUSH (An);
  - MOVE16, CINV/CPUSH privilege, the format $2 address error, the
    FSAVE idle frame, FSADD rounding, and save-state round-trips.
- `m68kmem`: `TestMapperWide` (mapping above 16 MB, open bus at
  $40000000, the top of the 4 GB space, and a 24-bit Mapper refusing
  $07000000) and `TestDecoderNegativeAddress`.
- `TestBootKS32On68040` boots Kickstart 3.2 (the A1200 ROM with AGA, and
  the A500 ROM with ECS) to Workbench on a 68040 with 8 MB of fast RAM:
  - exec sets AttnFlags $804F (68010-68040 and FPU40);
  - running 300 more frames takes no bus errors, address errors or
    F-line exceptions. The only other exceptions are exec's own
    Supervisor() privilege violations.
- The full suite passes.

**Not done yet:**
- The 68LC040's format $4 frame for FPU instructions: they take a plain
  F-line exception.
- An option to trap the instructions the real 040 leaves to software.
- Nothing in AmigaOS 3.2 turns the 040's MMU on, so it is tested only
  by the unit tests until a program that uses it (MMULib, or a Unix)
  runs.

## 2026-10-14 — AMIX: networking (the A2065 and a user-mode network)

`--a2065` fits Commodore's A2065 Ethernet card (`a2065.go`), connected to
a user-mode network (`usernet.go`) that uses the host's sockets, as
QEMU's "user" networking does. No privileges or host setup are needed.

**The A2065, as AMIX's aen.c drives it** (from its disassembly):
- `autocon($02020070)` finds it.
- `get_auto_serial_number` builds the MAC from the board: 00:80:10 and
  the serial number's inverted Autoconfig nibbles at $1C-$26. So the
  board keeps answering with its Autoconfig data at $0000 after it is
  configured.
- The LANCE's data and address ports are at $4000/$4002. `setup_lance`
  sets CSR3 to byte swap (BSWP), puts the init block at $8000 in the
  32 KB of RAM (which the LANCE addresses by board offset), and sets up
  128 receive descriptors of 150 bytes and 8 transmit descriptors of
  1518.
- `aenintr` reads CSR0, writes it back to acknowledge, and handles RINT
  and TINT. `receive_interrupt` follows chained buffers from STP to ENP
  and takes the length from the last descriptor's message count.

**The emulated LANCE:**
- CSR0-3, through RAP/RDP. INIT reads the init block and sets IDON;
  STRT, STOP and INEA work, and the interrupt bits clear when written
  as 1.
- Transmit: on TDMD and on a periodic poll, it sends each STP..ENP chain
  it owns and sets TINT.
- Receive: a frame is spread over as many owned buffers as it needs.
  The last carries ENP and the message count, which includes a zero
  FCS. Then RINT, or MISS if the buffers run out.
- It interrupts on INT2, level-triggered.

**The user-mode network.** The gateway is 10.0.2.2 (the host's
loopback, for connections in), DNS is 10.0.2.3 (the host's name server
from `/etc/resolv.conf`), and the Amiga is 10.0.2.15.
- The gateway answers ARP for the network and replies to pings.
- UDP goes out through a host socket per Amiga port and destination.
- For TCP, the gateway dials the destination from the host when the
  Amiga's SYN arrives, and completes the handshake once that succeeds
  (or resets it). It then acknowledges the Amiga's data and writes it
  to the host. The host's data goes out within the Amiga's window and
  MSS, and is resent after 0.5 s without an acknowledgement. FINs are
  handled each way.
- Forwarded host ports open connections into the Amiga the same way,
  starting with our SYN. `--a2065-forward` defaults to 2323:23.

**The AMIX disk** (`amixdisk -net`, the default) brings `aen0` up as
10.0.2.15 with a default route to 10.0.2.2, in `network-config`, which
only acts when `aen -S` finds the card. It also turns DNS on, with
`resolv.so` in `/etc/netconfig` and `resolv.conf` naming 10.0.2.3.

**Bugs on the way:**
- *TCP checksums.* AMIX kept resending its SYN: it was rejecting our
  SYN|ACK. The pseudo-header sum wrote `a<<8 | b + c<<8 | d`, and Go
  gives `|` and `+` the same precedence. The tests now verify every
  checksum the gateway sends.
- *The FIN.* Once the Amiga had acknowledged our FIN, the in-flight
  count subtracted it twice, went to -1 and panicked.

**Testing:**
- `TestA2065ARPAndPing` sets the LANCE up as `setup_lance` does,
  including reading the MAC the way the driver does, then does ARP and
  ping through the rings.
- `TestUserNetTCPOut`, `TestUserNetForward` and `TestUserNetUDP` use
  real host sockets.
- In AMIX (`TestAMIXBoot`, `AMIX_NET`):
  - the boot prints "add net default: gateway 10.0.2.2";
  - `ifconfig aen0` shows 10.0.2.15 UP, and the routing table is
    right;
  - `ping 10.0.2.2` reports "10.0.2.2 is alive";
  - AMIX's `telnet 10.0.2.2 PORT` reaches a server on the host and
    exchanges text, until "Connection closed by foreign host";
  - a host connection to the forwarded port gets AMIX's telnetd:
    "UNIX(r) System V Release 4.0 (amiga) login:".

## 2026-10-14 — AMIX: a UFS filesystem for the long names

`amixdisk` now adds a fourth slice: a UFS filesystem (`ufs.go`), mounted
at `/usr/ufs` from `/etc/vfstab`. It holds `/usr/src`, `/usr/X`,
`/usr/x11r5` and `/usr/public`, the only trees with names longer than
s5's 14 characters, and the root has links to them (`/usr/src ->
/usr/ufs/src`). The links were relative (`ufs/src`) at first, until
`startx` failed with "ufs/X/bin/startx: cannot open". AMIX hands a script
run through a relative link its target as is, so the links are absolute
now.
`-usr 0` leaves it out and cuts the names, as before.

**Why only those trees.** All of `/usr` can't move: `/etc/sysinit` runs
from `/usr` before `rc2` mounts anything else. It needs `setclk`, the
first-boot setup, `date`, `uname`, and `sioc` for the keymap. The four
trees aren't needed that early.

**The format** is 4.3BSD-Tahoe FFS as AMIX's `sys/fs/ufs_*.h` define
it:
- 8 KB blocks of 1 KB fragments. A cylinder group is 16 cylinders of
  1024 sectors (8 MB), with 2048 inodes per group.
- 128-byte inodes with both old and EFT ($90909090) fields. The size is
  the quad's second long on 68k.
- 4.3BSD directory records in 512-byte chunks.
- Fragment tails for small files, and a `lost+found` with room for
  `fsck`.
- A superblock copy in every group.

The kernel's `mountfs` confirmed the magic at $55C and clean as
`fs_state + fs_time == FSOKAY`.

**Checked with AMIX's own fsck.** The first image passed phases 1-4 but
not phase 5: "SUMMARY INFORMATION BAD". Disassembling the ufs `fsck`'s
`pass5` showed it rebuilds each cylinder group header and compares the
first 724 bytes, and two fields differed:
- **The group's time.** `fsck` sets it to the earlier of the group's
  time and the clock's. Under the Y2K clock shift AMIX's clock says
  1998, earlier than the 2026 the image was built in. Groups are now
  stamped 0.
- **The last group's `cg_ncyl`.** It's `fs_ncyl % fs_cpg`, as mkfs
  computes it, which is 0 when the groups are all whole.

The free-block table by rotational position is computed unconditionally
by `fsck`, as the formula in the header does, even with rotational
layout off.

Now `fsck -F ufs -n` passes all five phases on the 18,452 files. The
only remaining message is that the state isn't OKAY, which it never is
while mounted. `ls` shows the long names, and `diag/s5.py check` finds
the s5 root intact.

**A side effect:** `system.setup` no longer offers the Netnews setup,
because it looks for `/usr/public` before it is mounted.

`TestBuildDiskUFS` builds a small UFS and checks a long-named file
(blocks and a fragment tail, and di_blocks), lost+found, the vfstab
entry and the link.

## 2026-10-14 — AMIX: the tape drive

`--tape ID:DIR` (`scsitape.go`) puts a read-only SCSI tape drive on the
A2091. It plays a tape kept as a directory: the files in `index.tape`
order (or sorted), with a filemark after each. AMIX's distribution tape is
kept exactly that way.

**What AMIX expects** (from `ct.c`'s disassembly and `amixpkg`'s
strings):
- `amixpkg` uses `/dev/rmt/4hn`: SCSI ID 4, the A3070's ID, without
  rewind on close. It reads the index archive, spaces forward a filemark
  at a time to a package's file, and runs `dd bs=256k | [zcat |] cpio`.
- The driver uses fixed 512-byte blocks: READ(6) with the FIXED bit, the
  count in blocks. It also uses SPACE over filemarks, REWIND, WRITE
  FILEMARKS, MODE SELECT for the QIC-120/150 density, and a 14-byte
  REQUEST SENSE.
- `ctopen` reads the Archive drive's QIC-02 status bytes at sense bytes
  8-9. Byte 8 bit 6, no cartridge, fails the open. Unless byte 9 bit 3,
  beginning of medium, is set (or the driver already knows the position),
  the open fails too.

The first attempt failed for that reason: the sense data had zeros
there, and `cpio` got EIO. The drive now reports write protected, end of
medium, filemark, beginning of medium and power-on reset in those bytes.

**Reads at a filemark:** the drive returns the blocks before it with
CHECK CONDITION, FILEMARK and the residue (the VALID information field),
and moves past the filemark. Past the last file it reports BLANK CHECK
at end of medium.

**Testing:**
- `TestSCSITape`: reading over a filemark, the sense data, SPACE,
  REWIND, the end of the tape, and refused writes.
- In `TestAMIXBoot` (`AMIX_TAPE`), `amixpkg` lists the tape's 28
  packages. It then installs `games`: it spaces to file 21 and reads its
  2.3 MB through to the filemark ("Done reading tape"). `games` shows as
  installed.

The configuration screen's Hard disks tab has a Tape row (ID 4).

## 2026-10-14 — AMIX: X Windows

X11R4 runs on the native chipset with no emulator changes. `TestAMIXBoot`
types the session:
1. It answers `system.setup`'s 13 questions, waiting after each one.
   Typing ahead fails because the timezone script swallows a keystroke.
2. It waits for getty, which discards anything typed before `login:`
   appears, and logs in as root.
3. It sets PATH to include `/usr/X/bin` and runs `startx`.

The key script now has `~` to pause 100 frames, and Shift for capitals
and symbols.

`X` is a link to `X2410`, which also drives the native display. The
setup script would offer to relink it, but its `case "1|5"` never
matches, so the shipped link stays. The server comes up with the window
manager, an xterm and xclock (`docs/images/amix-x11.png`), after about
4,000 more page faults.

## 2026-10-14 — The clock's Y2K option

`--rtc-y2k`, which the A3000 preset sets, makes the battery clock show a
year 28 years earlier. The Gregorian calendar repeats every 28 years
between 1901 and 2099, so 2026 shows as 1998 with the same weekdays and
leap years. Setting the clock to a year from 1972-1999 adds the 28 back.

AMIX's 1992 `date` refused 2026, as a two-digit year below 70. With the
option it boots with "Wed Oct 14 06:21:01 EDT 1998": today's day, month,
weekday and time. `TestClockY2K` checks the year, the weekday and the
round trip. The other presets turn the option off again.

## 2026-10-13 — The A3000 preset, SCSI disks on the configuration screen

**The preset.** The configuration screen has an **A3000** model: a 68030
with a 68882, ECS, 1 MB of chip RAM and 8 MB of fast RAM. That's the real
machine's processor and chipset, and what Amiga Unix needs. The model
buttons now wrap onto a second row when they don't fit.

**What it isn't.** The A3000's built-in SCSI (its SDMAC), its 32-bit
motherboard RAM above 16 MB and its Ricoh clock aren't emulated. An
A2091, Zorro II fast RAM and the Oki clock stand in, and AMIX accepts
all three. The built-in SCSI would matter for the A3000's own Kickstart,
which needs an A3000 ROM image.

**SCSI disks on the configuration screen.** The Hard disks tab lists them
with hardfiles and folders. **Add SCSI disk…** gives the first one ID 6
(where AMIX looks for its root), and later ones the lowest free ID.

**One attachment.** `AttachSCSI` also makes each SCSI disk a unit of
goamiga.device, unless the same file is one already. This stands in for
the A2091's boot ROM, which isn't emulated, so AmigaOS mounts and boots
SCSI disks itself. AMIX now needs only `--scsi 6:amix.hdf`; before, it
also needed `--hdf amix.hdf`.
- `TestAMIXBoot` boots with only `AttachSCSI`.
- `TestA2091` configures both boards in the chain: goamiga.device's at
  $E90000, the A2091 at $EA0000.

## 2026-10-12 — Amiga Unix boots

AMIX 2.1, Commodore's System V Release 4, boots to its first-boot setup
and takes keyboard input. The pieces are the 68030 and its MMU, the A2091
and three new ones:

**`cmd/amixdisk`** builds the disk from the distribution's cpio (odc)
archive, the installed tree of all 28 packages. There's no installation
floppy, so this replaces the tape install. Everything about the layout
came from AMIX itself:
- **Slices.** `sdpartition` makes slice *n* the *n*th PART in the RDB's
  list, positioned with `rdb_CylBlocks`, and slice 0 the whole disk.
  The partition types don't matter. The disk has root (s1), swap (s2) and
  boot (s3). Root and swap are marked not to mount, so AmigaOS leaves
  them alone.
- **The root device** is compiled into the kernel: `rootdev` = $480016,
  major 18 minor $16. The minor's low nibble is the SCSI ID (the device
  nodes in the archive show the encoding), so it means `c6d0s1`: ID 6,
  the A3000's internal drive. `/etc/vfstab` is set to match.
- **The s5 filesystem** follows `sys/fs/s5*.h` from the archive's C
  development package:
  - *Alignment:* the superblock needs gcc's 4-byte alignment, which
    `s5statvfs` confirms by reading `s_fsize` at 4 and `s_tfree` at $1B0.
  - *Blocks:* 1 KB. The inodes are 64 bytes with 3-byte block addresses,
    and the free list is chained.
  - *Device numbers:* `iread` shows the expanded form, a flag in
    address 1 with major and minor in 2 and 3.
  - *Names* longer than s5's 14 characters (144, nearly all in the
    source trees) are cut and listed.
- **The boot partition** is made as `stand/Makefile` makes it: boot1, an
  IBLK for boot2, boot2, an IBLK for the kernel, the kernel. The IBLK
  format comes from disassembling AMIX's `makeiblk`: the size at 8, the
  SVR4 sum at 16, and $FFFFFFFF at 20 to let the loader place the kernel.
  The DosEnvec has de_BootBlocks = 2.
- `diag/s5.py` reads it back independently: partitions, superblock,
  directories, files, and a check of every block and link count.

**The boot**, as it happened the first time:
1. Kickstart 3.2's strap finds the boot partition's BootNode on
   goamiga.device and boots its boot blocks like a floppy, with the
   I/O request's offset at the partition (`boot1` relies on this from
   Kickstart 2.0).
2. boot2 loads the 1.4 MB ELF kernel, relocates it into the 8 MB of
   fast RAM, and passes `bootinfo` (the ConfigDev list and memory
   regions).
3. The kernel turns on the MMU (TC $82B02D60), resets the A2091 and
   finds the RDB.
4. It mounts the s5 root from slice 1, marks it active, and runs
   `init`.
5. The startup scripts run, through about 860 page faults handled by
   the restartable bus errors, and AMIX's `system.setup` asks for the
   node name.

**A battery-backed clock** (`rtc.go`): an Oki MSM6242 at $DC0000, the
A500/A2000 chip. AMIX's `get_clock_type` recognises it by register F
reading 4 (24-hour mode), and so does Kickstart's battclock.resource,
so AmigaOS now gets the date at boot. It runs on the host's local time,
and setting it under HOLD keeps the difference. The app fits it by
default (`--rtc`); tests don't, so their results don't depend on the
date. AMIX's 1992 `date` refuses 2026, as a two-digit year before 70,
and keeps the filesystem's time.

**Also:** `wireMemoryMap` replaced the CPU's Decoder whenever a board
was configured, and so would have dropped the MMU's translation hook. It
keeps it now.

**Testing:**
- `TestAMIXBoot` runs when `AMIX_HDF` names an image. `AMIX_KEYS` types
  at the prompts, and `AMIX_PNG` saves screenshots.
- `cmd/amixdisk`'s test builds a small archive and reads it back:
  contents through an indirect block, a hard link, a device node, a
  symbolic link, a cut name, vfstab and the boot partition.
- `TestClock` covers the clock.

## 2026-10-12 — The A2091 SCSI controller

The second step towards Amiga Unix, whose kernel drives an A2090, A2091
or the A3000's SCSI itself. `--scsi ID:FILE` attaches a hardfile as a
SCSI disk on an emulated A2091 (`a2091.go`, `scsi.go`).

**How AMIX's driver uses it.** This comes from the kernel's `a2091.c`,
whose disassembly shows each register:
- `initialize` finds the board with `autocon($02020003)`, Commodore
  product 3. `autocon` searches `bootinfo`: AMIX's boot loader copies
  expansion.library's ConfigDev list into the kernel, so the board only
  has to be configured by Kickstart.
- It then sets the WD33C93's own ID to 7 with advanced features and
  resets it. Status $01 means it uses select-and-transfer with ATN
  ($08).
- `startany` loads the command descriptor block into registers $03-$0E,
  the target into $15 ($40 for data in), the LUN and the transfer count.
  `startdma` sets the DMAC's CNTR ($30 read, $38 write) and ACR (the
  address), and strobes ST_DMA. `startany` then writes the command.
- The interrupt routine checks the DMAC's ISTR for INT_P, reads the
  WD33C93's status, and on $16 takes the target's status byte from
  register $0F, checking the phase register for $60.
- `stopdma` flushes the FIFO, waits for FE_FLG, then clears the
  interrupt and stops DMA.

**The emulation:**
- The DMAC's registers are as above. The board's interrupt (INT_P, with
  CNTR's INTEN) holds INT2 until the WD33C93's status is read.
- The WD33C93 has its register file through SASR/SCMD, with SASR
  stepping on except at the auxiliary status, command and data
  registers. Its commands are reset, abort and select-and-transfer.
- Select-and-transfer runs the whole SCSI exchange at once: data out
  from memory at ACR, the command, data in to memory, all clipped to the
  transfer count. It ends with status $16, the status byte in $0F, the
  phase at $60 and the residual count, or $42 if no target answers.
- The SCSI disk covers:
  - TEST UNIT READY, INQUIRY, READ CAPACITY;
  - READ/WRITE(6) and (10);
  - REQUEST SENSE with real sense data;
  - MODE SENSE pages 3 and 4, MODE SELECT;
  - the seek, start/stop, verify and reserve commands.
- There's no boot ROM. Kickstart configures the board without a driver,
  and AmigaOS boots through goamiga.device instead.
- The status bar has a SCSI light.

`TestA2091` drives the board exactly as `a2091.c` does: reset, INQUIRY,
READ CAPACITY, READ(10) and WRITE(10) by DMA, CHECK CONDITION then
REQUEST SENSE, and a select timeout.

## 2026-10-12 — The 68030 and its MMU

The first step towards Amiga Unix (AMIX 2.1), which needs a 68030 with
its MMU. `--cpu 68030` (the configuration screen has it too) is the
68020 instruction set with the 68030's CACR and on-chip MMU
(`m68k/mmu030.go`).

**Translation:**
- TC divides the logical address: initial shift, up to four table
  indexes, and page size. FCL puts a function-code level first, and SRE
  gives supervisor accesses their own root (SRP).
- The walk handles short and long descriptors, limits, supervisor-only
  and write-protect bits, early termination and indirect descriptors.
  It sets U on every descriptor it uses, and M on the page descriptor
  for a write.
- TT0/TT1 map 16 MB blocks transparently, by function code and read or
  write.
- The ATC is a 1024-entry cache of finished walks. PFLUSH and loading
  TC, CRP or SRP flush it. A write through an entry whose M is clear
  walks again, to set M.
- The instructions are PMOVE (TC, CRP, SRP, TT0, TT1, MMUSR), PFLUSH,
  PFLUSHA, PLOAD and PTEST (which sets MMUSR, and with A the descriptor's
  address). A bad TC or root pointer takes the MMU configuration
  exception (vector 56).

**Where it sits.** `m68kmem.Decoder` has a `Translate` hook, installed
while the MMU is on, so every CPU access passes through it and Go-side
DMA doesn't.
- The function code comes from SR, from the Decoder's fetch flag
  (program or data) and, for MOVES, from SFC/DFC.
- An access crossing a 256-byte boundary is translated a byte at a time.
  A write translates every byte before writing any, so a fault on the
  second page writes nothing.

**Faults and restarting.**
- While the MMU is on, Step snapshots the registers before each
  instruction.
- A refused access panics with a `busFault`. Step catches it, puts the
  registers back and takes the bus error with a format $B frame. The
  frame holds the special status word (DF, R/W and size for data; FB/RB
  for instruction fetches), the data fault address and the stage-B
  address.
- The stacked PC is the instruction's own, so RTE runs it again. The
  real 68030 resumes mid-instruction from the frame's internal state
  instead, but an OS that fixes the page and returns gets the same
  result.
- AMIX's `get_fault` reads exactly these fields: the fault address at
  frame offset $10 when DF is set, else the stage-B address (format $B)
  adjusted by FB/FC. The disassembly of the kernel, which isn't
  stripped, settled the choice.

**Testing** (`m68k/mmu030_test.go`):
- A translated write and read, with U and M set.
- A write to an invalid page: the handler sees the right SSW, fault
  address and the undone `(A3)+`, maps the page and returns. The rerun
  writes once and increments once.
- Write protection, with PTEST reporting W, the level count and the
  descriptor address.
- TT0.
- Two-level long-descriptor walks: limit, supervisor-only from user mode,
  early termination, indirect.
- FCL giving user and supervisor different memory.
- An instruction-fetch fault with stage B.
- A page-crossing write that writes nothing.
- MOVES through DFC.
- The 68030 CACR.

`TestBootKS32On68030` boots Kickstart 3.2 to Workbench on a 68030 with a
68882. Exec's AttnFlags are $8037: 68010, 68020, 68030, 68881, 68882.

## 2026-10-12 — AGA: super-hires, scrolling and sprites

Four gaps in AGA's display (and one in ECS's) are closed.

**Super-hires at full resolution** (`video.go`):
- Before, a super-hires line put two pixels in each hires-wide column and
  showed their average.
- Now a frame that showed super-hires makes the next frame twice as wide,
  with a column per super-hires pixel. Lores and hires pixels in it are 4
  and 2 columns.
  - `Video.cs` is the columns per hires pixel. Every column computation
    (playfield, sprites, register writes during the line, the window
    edges) scales by it.
  - `DisplaySize` stays hires-wide, so the host window keeps its size and
    a 2× window shows each super-hires pixel as one host pixel.
  - *Bug, found in use:* super-hires looked worse than the old averaging.
    Ebitengine's logical screen (`Layout`) is hires-wide, so the 1280-wide
    frame was scaled down to 640 on the way to the window, nearest pixel,
    dropping every other column. The display now implements
    `DrawFinalScreen` and draws a super-hires frame straight from the
    framebuffer at the window's resolution: 1:1 at 2×, linear filtering in
    a smaller window. The status bar and mouse keep the logical layout.
  - The first frame of super-hires still averages (`pf.half`), and the
    frame after the last one returns to hires width.
- AGA shows up to 8 planes in super-hires (ECS 2), with the whole palette,
  HAM and dual playfield.
- Fetch lengths have one formula for every resolution (`fetchWordsRes`).
  A fetch cycle is 8 colour clocks, or as long as the fetch width's words
  take to show if that is longer. Each plane fetches what its cycle
  shows, until DDFSTOP and one cycle more. It gives the same counts as
  before for OCS, ECS and the hires/lores AGA modes, and 84 words for
  FMODE 3 super-hires at DDF $38-$D8.

**Scrolling.** AGA's BPLCON1 has four more bits per playfield: two above
(delays up to 63 lores pixels, which 64-bit fetch modes need for smooth
scrolling) and two below (to a super-hires pixel).

**Sprites** (`sprites.go`):
- BPLCON3 SPRES sets their pixels to lores, hires or super-hires.
  0 is ECS's rule: lores, but hires on a super-hires screen.
- SPRxCTL bits 4 and 3 place them to a hires or super-hires pixel.
- BRDSPRT (with ECSENA) shows them in the border.

**Testing:**
- `TestAGASuperHiresFrame`: an 8-colour, FMODE 3 super-hires line. It
  checks the pointer advance (84 words), the frame width switching both
  ways, and each pixel's colour.
- `TestAGAScroll`, `TestAGASpriteResolution`, `TestAGABorderSprites`.
- `TestWorkbenchSuperHiresAGA` boots the A1200 from a host folder holding
  the Install 3.2 disk. The folder also has IPrefs and a
  `screenmode.prefs` for PAL Super-High Res in 16 colours, so Workbench
  opens in a 1280-wide 16-colour screen. The screenshot shows it sharp.

## 2026-10-12 — Host folders as drives

`--dir [VOLUME=]PATH` mounts a host folder as an Amiga drive, on any
Kickstart, as UAE's "filesystem" does. The configuration screen's Hard
disks tab has "Add folder…".

**How it works** (`hostfs.go`):
- goamiga.device mounts each folder like a partition: a parameter packet
  for `MakeDosNode`, then a BootNode, bootable (priority -10) if the
  folder has `S/Startup-Sequence`. The device is the next free DH name.
- The DeviceNode is patched to run a handler in the board's ROM: dn_SegList
  points to a segment there, dn_GlobalVec is -1, dn_StackSize 4096.
- The handler is a short 68000 loop. At start it finds its process and
  port, and allocates a 16 KB pool for FileLocks. Then it waits on its
  port, passes each message to Go (a trap), and `PutMsg`s it back to the
  port Go names.
- Go handles the packet against the host folder and fills in Res1/Res2
  and the reply fields.
  - The first packet is the startup one. Go matches it to a folder by its
    DeviceNode (Arg3, from 2.0) or dn_Startup (Arg2: Kickstart 1.3 leaves
    Arg3 0).
  - It sets dn_Task, builds the volume's DeviceList node in the pool and
    links it at the head of the DOS list.
- **Packets:**
  - Locks: LOCATE_OBJECT, FREE_LOCK, COPY_DIR, PARENT, SAME_LOCK.
  - Directories: EXAMINE_OBJECT, EXAMINE_NEXT, CREATE_DIR.
  - Files: FINDINPUT/OUTPUT/UPDATE, READ, WRITE, SEEK, END,
    SET_FILE_SIZE.
  - The 2.0 filehandle packets: FH_FROM_LOCK, COPY_DIR_FH, PARENT_FH,
    EXAMINE_FH.
  - Objects: DELETE_OBJECT, RENAME_OBJECT, SET_PROTECT, SET_COMMENT,
    SET_DATE.
  - The volume: INFO, DISK_INFO, CURRENT_VOLUME, RENAME_DISK.
  - IS_FILESYSTEM, FLUSH, INHIBIT and the like simply succeed.
  - EXAMINE_ALL isn't handled: dos.library falls back to Examine/ExNext.
    That also keeps pattern matching on the Amiga side.
- **Locks** are real FileLock structures (DOS reads fl_Volume and fl_Task)
  from the pool. Go keeps each one's path, and checks shared and
  exclusive use. File handles need only an ID in fh_Arg1. A rename
  moves the locks and open files inside what was renamed.
- **Names** are matched case-insensitively in Amiga terms. Each host name
  is converted to Latin-1 and compared, so names with unshowable
  characters still resolve.
- **Protection bits and comments** go in WinUAE/FS-UAE `.uaem` files.
- **Save states** record each handler's locks and open files (path and
  position) and reopen the files on load (`stateVersion` 12).

**Two bugs on the way to the first boot:**
- *Kickstart 1.3's startup packet.* It has no DeviceNode in Arg3, so no
  folder matched and DOS waited forever. Matching dn_Startup in Arg2
  fixed it.
- *Assign prefixes.* DOS sends the name exactly as typed,
  `devs:system-configuration`, along with the lock that DEVS: stands for.
  The handler treated any colon as "from the root" and looked in the
  wrong place. Only a leading `:` means the root; otherwise the prefix is
  dropped and the lock kept.

**Testing:**
- `TestFolderBootKS13` and `TestFolderBootKS32` boot Workbench from
  folders holding the files of the Workbench 1.3 and Install 3.2 disks.
  The 3.2 boot takes 256 frames, against 283 from the same files on a
  hardfile.
- `TestFolderBesideHardfile`: with Workbench on a hardfile, a data
  folder's handler starts at boot on 1.3 and 3.2, and its volume's icon
  is on the Workbench (checked by screenshot, `HOSTFS_PNG`).
- `TestHostfsFiles` and `TestHostfsReadOnly` drive the packet code
  directly. They cover writing, reading and seeking; locks relative to
  locks and `/`; ExNext listings; and rename with a lock held. They also
  cover delete refusing an open file, a full drawer or a delete-protected
  file; the `.uaem` files coming and going; Latin-1 names; and a
  read-only folder.
- `TestSaveStateResumesExactly` has a folder case. It saves mid-boot with
  11 locks and 2 files open, and the loaded machine runs on exactly as
  the original did.

## 2026-10-12 — Filesystems from the RDB

A partition using a filesystem Kickstart doesn't have (PFS3, SFS, or FFS
on Kickstart 1.3, whose ROM has only OFS) now mounts, if the filesystem is
stored in the disk's RDB, as on a real Amiga.

**Reading the RDB** (`hdfilesys.go`, `hardfile.go`):
- The FileSysHeaderBlock (FSHD) chain gives each filesystem's DosType,
  version, PatchFlags and DeviceNode values.
- Each FSHD's LoadSegBlock (LSEG) chain is joined into the executable.
- A broken chain is reported (`Hardfile.FSError`, printed at startup) but
  doesn't stop the disk mounting: its partitions may still use a
  Kickstart filesystem.

**Mounting** (`goamiga.device`): what a real controller's boot ROM does.
- The RomTag init now runs two more traps between `MakeDosNode` and
  `Enqueue` for each partition whose DosType has a filesystem in the RDB.
- If FileSystem.resource has that DosType at least as new, its entry is
  used.
- Otherwise the first trap returns the memory needed, the 68000 code
  calls `AllocMem`, and the second trap loads the executable there.
  Go parses the hunks (CODE, DATA, BSS, RELOC32, RELOC32SHORT, symbols
  and debug skipped) and lays them out as a seglist.
- The loaded filesystem gets a FileSysEntry at the start of its block,
  added to FileSystem.resource (when there is one: Kickstart 1.3 has
  none), so later partitions and the Mount command share it.
- The DeviceNode is patched from the entry: always dn_SegList, the fields
  PatchFlags names, and dn_GlobalVec -1 if PatchFlags leaves it out (no
  RDB filesystem is BCPL).

**IDE drives** needed nothing: Kickstart 3.x's `scsi.device` loads
filesystems from the RDB itself, and the Gayle emulation is good enough
for it to.

**Testing.** The tests build RDB disks around the ADFs. One converts
Workbench 1.3's OFS disk to FFS: each file's data is rewritten into its
first blocks at 512 bytes a block, and the block tables are shortened.
- `TestRDBFileSystemKS13`: Kickstart 1.3 boots Workbench 1.3 from an FFS
  partition, with Workbench 1.3's `L:FastFileSystem` in the RDB. Before
  this, 1.3 couldn't read the partition at all.
- `TestRDBFileSystemOlderThanROM`: on Kickstart 3.2 the RDB's FFS 34 is
  passed over for the ROM's 47.4.
- `TestRDBFileSystemNewerThanROM`: an FFS marked version 99 is loaded on
  Kickstart 3.2 and appears in FileSystem.resource.
- `TestGayleIDEFileSystemKS32A1200`: the same through IDE, loaded by
  `scsi.device`.
- `TestParseHunks`: relocations and segment layout.

One bug, in the test's converter: a `copy` into the disk image wasn't
limited to one block, so each file's data overwrote the blocks after it.

## 2026-10-12 — Gayle: the A600/A1200 IDE interface

`--gayle auto|on|off` fits Gayle. `auto`, the default, fits it with the AGA
chipset, as every A1200 has it. `--ide FILE` attaches up to two hardfiles
as IDE drives, master and slave. The A600 and A1200 presets fit Gayle, and
the configuration screen has IDE master/slave rows and a Gayle setting.

**Gayle** (`gayle.go`):
- The ID register at $DE1000, read a bit at a time ($D0).
- The IDE interrupt status, latch, enable and configuration registers at
  $DA8000–$DAB000.
- The IDE interrupt is level-triggered on INT2: it keeps returning until
  the driver clears Gayle's latch.
- The task file at $DA0000 (register n at +4n, mirrored at $DA2000). The
  device control and alternate status register is at +$1018.
- The PCMCIA slot is not emulated: no card is ever present.

**The drives** (`ide.go`) are ATA disks in PIO mode:
- IDENTIFY;
- READ/WRITE SECTORS and MULTIPLE, and SET MULTIPLE;
- INITIALIZE DEVICE PARAMETERS, READ VERIFY and diagnostics;
- the power, recalibrate, seek, flush and feature commands, accepted;
- CHS and LBA addressing.

A command stays busy for 400 cycles, so the driver is waiting for the
interrupt when it arrives.

**Debugging the boot** took three fixes:
1. **Addresses.** The Mapper gives devices offsets from where they're
   mapped, so the ID register at $DE1000 first looked like IDE register 0,
   and Kickstart never saw Gayle. `gayleView` adds each range's base back.
2. **IDENTIFY's byte order.** `scsi.device` then sent INITIALIZE DEVICE
   PARAMETERS with zero sectors per track: it had decoded IDENTIFY wrongly.
   The Amiga's IDE bus delivers words byte-swapped relative to ATA, and
   drivers swap IDENTIFY's words back. IDENTIFY is now built little-endian,
   as a drive's own words are; sector data is the image's bytes as they
   are.
3. **An empty bus.** With no drive at all, boot stalled 35 seconds while
   `scsi.device` waited for one to become ready. An empty bus floats high,
   so it now reads $FF (a master still answers 0 for a missing slave).
   That stall became 2.5 seconds.

**Results:**
- `TestGayleIDEBootKS32A1200`: Kickstart 3.2 for the A1200 finds an RDB
  hardfile on IDE through its own `scsi.device`. The test builds the disk
  with checksummed RDSK and PART blocks around the Install 3.2 disk.
  Kickstart mounts DH0 and boots Workbench from it in 356 frames.
- **The Chaos Engine** (see below) now plays on the default A1200
  configuration: menu, then World 1. With Gayle fitted, `scsi.device`
  allocates its memory at boot as on a real A1200. The game's stack, set
  just inside its own allocation, no longer lands on trackdisk's DMA
  buffer. Without Gayle the same setup hangs.
- The rest of the game list is unchanged with Gayle fitted.

**Tests:**
- `TestIDEIdentify`, `TestIDEReadWrite` (LBA and CHS reads, multiple-sector
  writes, the interrupt latch and INT2), `TestIDEErrors`, `TestGayleID`.
- The boot test above.

The save state (version 11) records Gayle and the IDE drives.

## 2026-10-12 — The CPU joins the module; renamed github.com/andrewsjg/goamiga

The CPU was a separate repository, `../go-m68k`, on a local branch that
existed nowhere else. goAmiga used it through a `replace` directive.
- Upstream (Ryan Armstrong's go-m68k) has been dormant since 2018.
- About 70% of the code was now goAmiga's own: 68020, FPU, trace,
  prefetch, exception frames, timing.
- Nearly every CPU change was made for, and tested by, an emulator change.
- The arrangement meant two commits for one feature, two repos to keep in
  step, and a build that only worked with the sibling checkout on the
  right branch.

**The merge.** `git subtree add` brought the branch into `m68k/` with its
whole history (Ryan's commits included).
- The nested `go.mod` and the `replace` are gone.
- The module is now `github.com/andrewsjg/goamiga`, and the CPU imports as
  `github.com/andrewsjg/goamiga/m68k`.
- go-m68k's LICENSE (BSD) stays with the code. `m68k/README.md` says
  where it came from.
- The SingleStepTests data (~190 MB) remains git-ignored in
  `m68k/testdata/680x0/` and has to be downloaded (or copied) to run that
  suite.

Both suites pass in the merged module.

## 2026-10-12 — diag/ cleanup

Removed 63 one-off diagnostic files: 61 Python scripts from the initial
import and the `bootdiag` and `romanalysis` Go programs.
- They were probes for the early Kickstart 1.3 boot, blitter and disk
  bugs, all long fixed and written up here.
- Each was tied to a particular ROM address.
- Most were numbered copies of one another.
- 59 of them opened `Kick13.rom` from a path that no longer exists.

They remain in git history (`9bca521`). Kept: `adf.py` (disk image
files) and `disasm68k.py` (disassembler).

## 2026-10-12 — Power, Reset and Config buttons

The status bar has three buttons at its right:
- **Power**: a power cycle. The app builds a new machine from the same
  settings, so memory and every chip start from cold, with the disks
  currently in the drives put back in.
- **Reset**: the keyboard reset, as Cmd+R.
- **Config**: back to the configuration screen, as Cmd+,.

Power and Config discard the running machine, so the first click turns the
button into "Sure?" for three seconds.

Clicks on the status bar go to the buttons, not to the Amiga, and don't
trap the pointer. The buttons work while the pointer is free.

Cmd+Shift+R is Power from the keyboard. `amiga.DiskFile` maps a zip-member
disk path to its archive, so a power cycle can check those disks exist
(test: `TestDiskFile`).

Checked in the window: the buttons draw, a power cycle reboots Workbench
1.3 from the same disk, and Config returns to the screen with the
machine's configuration selected.

## 2026-10-11 — Configuration screen

Started with no arguments, goAmiga opens a configuration screen instead of
printing its usage:
- saved configurations on the left: load, save, new, delete, and
  double-click to start;
- the settings in tabs: Machine (with A500, A500+, A600, A1200 and
  A1200 + fast presets), Memory, Floppies, Hard disks, Ports and Display;
- Start and Quit.

**Settings.** `internal/config.Settings` holds everything a machine and
its window need. It registers the command-line flags (`Register`), so
flags, settings files and saved configurations all fill in the same
struct:
- `Write` saves the values that differ from the defaults, with absolute
  paths, as an ordinary settings file in `~/.config/goamiga/configs/`.
- `Load` reads one back.
- `Saved`, `LastUsed` and `Resolve` back the list, the start-up choice
  and `--config NAME`.

**Startup.** `cmd/goamiga` builds the machine in `buildMachine`, which
returns errors rather than exiting, so the screen can show them.
Ebitengine runs one game per process. A small `app` game is either the
configuration screen or a machine's `Display`:
- `Display.Start`/`Stop` replace `Run` for this. Ebitengine allows one
  audio context per process, so it's now shared between machines.
- `Amiga.Shutdown` releases hardfiles and sockets, and the serial
  listener is closed.
- Cmd+, in the emulator returns to the screen with the machine's settings.

**UI.** The screen is drawn with a small immediate-mode kit (`ui.go`) in
the style of Workbench 2.0:
- bevelled buttons;
- cycle gadgets (click forward, right-click back);
- checkboxes, text fields and a list;
- Go's typeface through text/v2;
- native file dialogs (zenity), as F11 already used.

`GOAMIGA_UISHOT=DIR` renders each tab to a PNG and quits, which is how the
layout was checked and the README's screenshot made.

**Verified:**
- Each tab rendered and inspected.
- A start, return and start-again cycle: the second machine boots, the
  serial port is bound again (the first was released), and the shared
  audio context works.
- Tests: `TestSettingsRoundTrip`, `TestValidName` and
  `TestSettingsAbsolutePaths`.

## 2026-10-11 — bsdsocket.library

`--bsdsocket` adds a network board (`netboard.go`, `bsdsocket.go`): a
Zorro II board whose ROM adds bsdsocket.library v4 and a PORTS interrupt
server. It is the AmiTCP API, implemented on the host's sockets, as UAE's
bsdsocket emulation is.

**Mechanism.** Each of the library's 46 vectors (LVO -30 to -300) traps
into Go, which reads the arguments from the CPU's registers.
- Host sockets are always non-blocking.
- When a call on a blocking Amiga socket would block, Go returns a signal
  mask in D1. The vector's stub `Wait()`s on it, then traps again to retry.
- The board polls the waiting calls' descriptors eight times a frame.
  When one is ready, or a timeout has passed, it raises INT2. The
  library's PORTS server then `Signal()`s the task.
- A break signal (Ctrl-C, or `SetSocketSignals`) ends the wait with EINTR
  and stays set. `WaitSelect`'s own signal mask is honoured.
- Name lookups run in goroutines, so the emulator never blocks.

**Library state.** Each task that opens the library gets:
- its own descriptor table (64, adjustable);
- errno and h_errno, with the pointers `SetErrnoPtr` and the tag list
  set;
- the break mask;
- a buffer on the board for the netdb results (hostent, servent,
  `Inet_NtoA`).

`ObtainSocket`/`ReleaseSocket` pass sockets between tasks.

**Translation.** Constants are AmiTCP's (BSD 4.4) and are translated to
the host's:
- errno values;
- socket options (SOL_SOCKET, TCP_NODELAY, IP_TOS/TTL), with
  SO_RCVTIMEO/SNDTIMEO done in the emulator;
- MSG_ flags and the 4.4BSD sockaddr_in.

Save states record that the board is fitted; host sockets can't be saved,
so they are closed on load.

**Tests.** Two Amiga programs are assembled in the test (`pcAsm`), wrapped
as AmigaDOS executables, and written over `c:DiskDoctor` on a Workbench
1.3 image. The startup-sequence runs them, against a Go TCP peer:
- `TestBSDSocketTCP`: gethostbyname("localhost") (asynchronous), socket,
  connect, send, a recv that sleeps until the reply arrives, and an echo.
- `TestBSDSocketServer`: setsockopt, bind, listen, WaitSelect with a
  timeout, accept, recv/send, and errno EBADF for a bad descriptor.

Also: `TestInetAddr`, `TestSocketBaseTagList`.

## 2026-10-11 — Serial port

Paula's UART is emulated (`serial.go`):
- SERPER sets the bit time and word length.
- SERDAT has a buffer in front of the shift register. Each word sends its
  own stop bits, and transmission takes the real frame time.
- SERDATR reports RBF, OVRUN, TBE, TSRE and RXD.
- The TBE (level 1) and RBF (level 5) interrupts are raised.
- On CIA-B, /DSR and /CTS are asserted and /CD follows the connection.

Received bytes arrive at the baud rate. The next byte waits until RBF is
cleared, so pasted text can't overrun.

The far end is a `SerialHost`. `NetSerial` (`serial_host.go`) listens on
TCP. In telnet mode it:
- asks the client for character mode with remote echo;
- strips option negotiation from the input;
- turns CR LF or CR NUL into CR;
- sends LF as CR LF.

Raw mode passes bytes through untouched. CLI: `--serial telnet:PORT` or
`--serial tcp:PORT`. The save state includes the UART (version 10).

**Tests:**
- Frame timing and TBE/TSRE.
- Reception and RBF pacing.
- The modem lines.
- Telnet over a real loopback connection.
- `TestSerialAUXShell`: Workbench 1.3 with its startup-sequence patched to
  `mount aux:` and `newshell aux:`. A command typed over the serial port
  runs, and its output comes back.

## 2026-10-02 — A1200 chip bus: 68020 cache and 32-bit chip RAM

Alien Breed: Tower Assault hung after loading from disk 3. Its VERTB
handler fades 256 24-bit colours into a copper list, then calls a "wait 8
lines" routine. That routine adds 8 to VPOS and waits for the result, so
it never returns if called after line 304. Here the fade took 290 lines
of a 313-line frame.

The fade runs from chip RAM (no fast RAM on a stock A1200) while 8 lores
planes take every slot during the display window. goAmiga charged two
things the A1200 doesn't pay for:
- **Instruction fetches.** The 68EC020's 256-byte instruction cache holds
  a loop like this, so its fetches don't use the bus.
- **Longwords.** Alice's chip RAM bus is 32 bits wide, so a longword is
  one access, not two.

Now, with a 68020:
- While CACR's enable bit is set, instruction fetches don't count as chip
  bus accesses. go-m68k's `Decoder.Fetch` flags code reads and
  `Processor.ICacheEnabled` reports the cache. Kickstart turns it on.
  The cache itself isn't modelled, so straight-line code is a little
  flattered.
- On AGA, chip RAM accesses count in 32-bit units. Custom registers stay
  16-bit.

With that, and auto-swapping disks in the batch, the A1200 list now
reaches:
- Alien Breed TA: options menu.
- Bubble Bobble: play.
- Frontier: 3D flight over a city.
- James Pond 2: in game.
- Lotus: racing.
- Pinball Mania: table select.
- Prince of Persia: play.
- Speedball 2: menu.
- Worms DC: intro.

Still not working:
- The Chaos Engine (stack overlap, see below).
- Deluxe Galaga.
- SDI (black or CLI on the A1200; both work on the A500).
- Kick Off 96: its disks are separate zips.
- Colonization and SkidMarks: they need icon.library on disk under
  Kickstart 3.2.

## 2026-10-02 — Multi-disk zips; The Chaos Engine

**Multi-disk zips.** Collections often zip all of a game's disks
together. A zip holding several disks now stands for all of them:
- `--df0 game.zip` inserts the boot disk (named "boot", "disk 1", ...;
  an ADF before an IPF).
- The other disks go into DF0:'s list, so Cmd+F11 changes to the next.
  The F11 chooser does the same when you pick such a zip.
- A single disk can be named with `game.zip#Disk2.ipf`, e.g. to put
  disk two in DF1: (`--df1 "game.zip#Disk2.ipf"`). Many games then never
  ask for a swap.

The batch harness gained three options:
- `speed`: the CPU speed.
- `swap=FRAME`: change DF0: to the next disk at that frame, then again
  later, cycling.
- `df1`: put disk two in DF1:.

**68020 address errors.** A jump to an odd address panicked the emulator
on the 68020: the fault was raised, but only the 68000 path caught it.
The 68020 now takes exception 3 with its own short bus fault frame
(format $A, 32 bytes), which RTE pops. Odd data addresses stay legal.
Test: `TestAddressError020`.

**AGA fetch width rounding** (Worms: The Director's Cut's title was
sheared). With FMODE's wide fetches, the words fetched per line were
computed as floor((DDFSTOP−DDFSTRT)/unit) + 1 units. The original chips
round up: the unit under way when DDFSTOP is reached completes, and
`fetchWords` already did that. Two hires FMODE 3 screens pin it down:
- Kickstart 3.2's own: DDF $38-$D8, modulo −8, needs 44 words;
- Worms': DDF $38-$C0, modulo 0, needs 40. Floor gave 36.

Rounding up fits both. On the way I tried 32-clock blocks with two
fetches per plane, which fixed Worms but sheared Kickstart's screen.
Test: `TestFetchWordsFactor`.

**The Chaos Engine (AGA)** hangs at a dark screen, or shows "Software
Failure" in input.device, on the A1200 with Kickstart 3.2. The cause is
in the game's bootblock:
1. It allocates the largest free chip block and copies itself there.
2. It sets SSP 26 bytes into that block.
3. It calls DoIO from supervisor mode to load the game.

Exec's dispatcher and the interrupt handlers then push frames below the
block, into memory the game doesn't own. With Kickstart 3.2's layout
that is the last 50 bytes of trackdisk's DMA buffer, which sits directly
beneath. The track read overwrites the saved registers. The idle loop's
A0 came back as MFM data and it stopped seeing ready tasks.

Whether it bites depends on where the DMA is when an interrupt runs:
- At CPU speeds 290-330% and 370-400% it boots cleanly. Most other
  speeds show the Software Failure first and continue after a click.
- With disk two in DF1: it reaches World 1.

The code is the game's, so the emulator is not at fault. A real A1200
probably has a different layout: its Gayle chip's IDE driver allocates
memory at boot, and goAmiga has no Gayle yet. Kickstart 3.1 may also
differ. This is a reason to emulate Gayle next.

## 2026-10-02 — Game testing: Copylock, prefetch, strobes, NTSC

Triage of the games in `ADF/` (mostly A1200 titles, plus some A500 ones)
found seven emulator bugs. Each is described below with the game that
exposed it.

**Batch harness.** `TestGameBatch` (`amiga/game_batch_test.go`) boots a
list of games in parallel. For each game it:
- clicks the mouse and presses fire at intervals;
- writes contact sheets of screenshots taken at a third, two thirds and
  the end;
- takes options per game: ROM, CPU, chipset, video standard, memory and
  frame count.

```
GOAMIGA_BATCH=games.txt GOAMIGA_OUT=/tmp/sheets go test -tags noebitengine -run TestGameBatch -v ./amiga/
```

A list line is `label | disk | rom=../ROMS/Kick13.rom,cpu=68000,chipset=ocs,chip=512,slow=512`.
When a zip holds several disks, the boot disk is picked by name (`disk1`,
`boot`, ...), and an ADF is preferred to an IPF.

1. **IPF reads: sync on every sync word** (Bubble Bobble, Colonization,
   Pinball Mania: "read error on disk block"). Paula re-aligns the word
   boundary each time the sync word passes during a WORDSYNC read, not
   just at the start. IPF tracks record each sector at its own bit offset,
   so after the first sector everything arrived misaligned. Disk DMA now
   shifts cells through a modelled shift register.
2. **Trace exception** (Bubble Bobble's Rob Northen Copylock). The CPU
   didn't implement the T bit. Copylock decrypts its code one traced
   instruction at a time: each trace exception decrypts the next
   instruction and re-encrypts the last. Implemented in go-m68k: a trace
   exception follows any instruction that starts with T set, except
   illegal, line A/F and privilege violations. It is taken before pending
   interrupts.
3. **Illegal instruction, line A and line F stack the instruction's own
   address**, not the address after it. Copylock's ILLEGAL handler adds 2
   to the stacked PC, so it resumed one word late, in encrypted code.
4. **Bit-level sync on ADF tracks** (Prince of Persia hung after the
   title). The ADF path only found syncs at word boundaries of its
   synthesized MFM. Prince of Persia's check reads 16 words after DSKSYNC
   `$4891`, a word that occurs in ordinary sector data at an odd bit
   offset. ADF tracks now go through the same bit-level reader as IPF
   (`readTrack`).
5. **68000 prefetch** (SDI: "Software error - task held", an address error
   in garbage code). SDI's decryption loop is:

   ```
   loop: MOVE.W (A0)+,D0
         EOR.W  D0,(A1)+   ; the first pass decrypts the DBRA's displacement
         DBRA   D1,loop
   ```

   On a 68000 the DBRA's displacement was prefetched before the EOR wrote
   it, so the loop exits on its first pass into the real decryptor. Ours
   looped 575 times over the wrong data. go-m68k now emulates the
   two-word prefetch queue (`Processor.Prefetch`, on for the Amiga's
   68000): an instruction's writes to the next instruction's first two
   words aren't seen by that instruction. MOVE is the exception: it makes
   its final prefetch after writing, so the second word reads fresh. See
   `prefetch.go`. Both Reset functions also dropped the new
   `OnException` hook; they keep it now.
6. **Strobe registers act on reads** (SDI black after loading). SDI
   starts its copper list with `MOVE.W $DFF088,D0`, a read of COPJMP1. On
   the real chips any access to a strobe address triggers it. Reads of
   COPJMP1/2 now restart the Copper, including long reads that span them.
7. **NTSC** (`--video ntsc`; Bubble Bobble, the US release, stayed black on
   PAL). The game sets BPLCON0 from the CPU at line 255 and leaves it out
   of its own copper list. The system's copper list is still running for
   the rest of that frame, and on PAL it turns the planes off at line 300,
   the end of Kickstart's 256-line screen. On NTSC that screen ends at
   line 244, so the write survives. NTSC means:
   - 263/262-line frames;
   - the colour clock at 3.579545 MHz (audio resampling, warp speed);
   - 60 Hz host pacing;
   - the NTSC Agnus ID bit (VPOSR bit 12), from which Kickstart builds
     200-line screens.

   Lines are all 227 colour clocks, not alternating 227/228. The save
   state records the standard (version 9).

Prince of Persia's red screen was not a bug: its loader paints COLOR00 red
when TypeOfMem finds no memory beyond 512 KB, so it needs `slowram 512`.

**Results.**
- A500 (Kickstart 1.3):
  - Bubble Bobble plays (with `--video ntsc`).
  - Prince of Persia reaches its title (1 MB).
  - SDI, from both the IPF and the crack, reaches the cockpit.
- A1200: Bubble Bobble and Prince of Persia now reach gameplay. Still
  failing or blocked:
  - Chaos Engine, James Pond 2, Worms, Deluxe Galaga and Alien Breed TA
    (black or empty screens).
  - Kick Off 96: asks for disk 2.
  - Colonization and SkidMarks: want icon.library, which Kickstart 3.2
    doesn't have in ROM.

**Tests:**
- go-m68k:
  - `TestTraceException`.
  - `TestPrefetchSelfModifyingBranch`, `TestPrefetchWriteOrder` and
    `TestPrefetchCarriesOver`.
  - `TestIllegalStacksInstructionAddress`.
- goAmiga: `TestDMASyncAtBitOffset`, `TestCopperJumpOnRead` and
  `TestBootDiskName`.

## 2026-10-02 — AGA

`--chipset aga` (A1200/A4000). It defaults to 2 MB of chip RAM; use it
with the A1200 Kickstart (`kick32a1200.rom`) and `--cpu 68020`.

**What's emulated** (`aga.go` and the compositor):
- **Identity.** Alice reports ID $22 in VPOSR, and Lisa answers DENISEID
  $F8. Kickstart 3.2 for the A1200 then sets ChipRevBits0 = $1F (AA Alice
  and Lisa).
- **Palette.** 256 colour registers of 24 bits. COLOR00-31 write the bank
  BPLCON3 bits 15-13 select. A normal write sets both nibbles of each
  component, and a write with LOCT (bit 9) sets the low ones.
- **Bitplanes:** 8 planes (BPLCON0 BPU3), with BPL7PT and BPL8PT.
- **Colour modes:**
  - HAM8: planes 1-2 are the control bits, the six data bits set a palette
    colour (0-63) or a component's top six bits.
  - EHB only with 6 planes; KILLEHB on AGA drops EHB but keeps plane 6.
  - Dual playfield with 4+4 planes, playfield 2 offset by PF2OF (BPLCON3
    bits 12-10).
  - BPLCON4: the bitplane colour XOR, and separate colour banks for even
    and odd sprites.
- **FMODE.**
  - Bitplane fetches 32 or 64 bits wide: fewer, longer fetch units, so
    DDFSTOP comes earlier for the same width; the DMA slot table and the
    CPU's and blitter's share of slots follow.
  - Sprites 32 or 64 pixels wide, whose control words are also fetched
    wide.
- **Reset values:** BPLCON3 $0C00 and BPLCON4 $0011.

**One unified palette.** The compositor now uses a single 24-bit line
palette for every chipset. OCS/ECS fill entries 0-31 from the 12-bit
registers, so their output is bit-for-bit what it was; the existing
display tests confirm it. Colour writes during a line now record the
palette entry and its 24-bit old and new values.

**A fix found with the real ROM.** The first AGA Workbench came up shifted
32 hires pixels to the right. graphics.library uses FMODE 3 for its hires
screens, with DDFSTRT $38 and DIWSTRT $81. That means the
fetch-to-display delay stays 8 colour clocks (2×8+1 = 17 lores pixels)
however wide the fetch, rather than growing with the unit as I'd first
written. With that, the Install 3.2 Workbench and your installed Workbench
3.2 (16-colour interlaced hires, FMODE 3) display correctly.

**Tests:**
- `TestAGAChipIDs`, `TestAGAPaletteBanksAndLOCT`;
- `TestAGAEightPlanes` (colour $C5 from 8 planes), `TestAGAHAM8`;
- `TestAGADualPlayfieldAndXOR` (PF2OF 16, BPLAM $FF);
- `TestAGAFMODEFetch`;
- `TestAGASprites` (sprite banks, a 32-pixel sprite);
- `TestNotAGAIgnores`;
- `TestBootAGA` (A1200 ROM: ChipRevBits0 $0F, Workbench starts);
- an AGA case in `TestSaveStateResumesExactly`.

Save states carry the palette and wide sprite data (version 8).

**Not modelled:** scan doubling and the productivity/BEAMCON0 modes;
sprite resolution and horizontal precision beyond lores (BPLCON3 SPRES);
border sprites; Gayle (the A1200's IDE and PCMCIA). goAmiga's own hard
disk controller works with the A1200 ROM.

## 2026-10-01 — 68881/68882 FPU

`--fpu 68881|68882` (with `--cpu 68020`) adds a floating-point coprocessor.

**go-m68k `fpu.go`.** F-line opcodes for coprocessor 1 ($F2xx-$F3xx) go to
the FPU when one is fitted; otherwise they stay F-line exceptions, which is
how software finds there's none. It has:
- FP0-FP7 (held as float64), plus FPCR, FPSR and FPIAR;
- the N, Z, I and NaN condition codes;
- all seven operand formats: byte, word, long, single, double, 96-bit
  extended (converted exactly, denormals included) and packed decimal;
- the general operations: FMOVE, FINT, FINTRZ, FSQRT, FABS, FNEG, FADD,
  FSUB, FMUL, FDIV, FMOD and FREM (with the quotient byte), FGETEXP,
  FGETMAN, FSCALE, FSGLDIV, FSGLMUL, FCMP, FTST, the transcendental
  functions and FSINCOS;
- FMOVECR's constant ROM;
- control-register moves, and FMOVEM, static or dynamic, in all three
  addressing directions;
- FBcc, FScc, FDBcc and FTRAPcc with all 32 predicates, unordered
  comparisons included;
- FSAVE/FRESTORE: a null frame until the FPU is first used, then an idle
  frame whose size ($18 for the 68881, $38 for the 68882) is how exec tells
  the two apart.

Instructions take typical 68881 times (`timingInfo.fixed`).

**Simplified:**
- results carry float64's 53-bit mantissa, not 64 bits;
- FPCR's rounding precision is ignored (its rounding mode is used for
  integer conversion and FINT);
- arithmetic exceptions are noted in FPSR but never trapped.

**Amiga side.** `SetFPU`, and save states carry the FPU registers (version
7).

**Checks.** `TestBootFPU`: Kickstart 3.2 reports AttnFlags $10 with a
68881 and $30 with a 68882, and boots Workbench. Your installed Workbench
3.2 boots fully on a 68020 with a 68882. go-m68k tests cover:
- the format conversions (1.0's exact extended bits, a denormal, ∞, NaN,
  packed decimal);
- arithmetic, rounding modes and divide-by-zero;
- FMOVEM round trips;
- FBcc/FScc, including NaN-unordered;
- FSAVE/FRESTORE frames for both models;
- the F-line exception with no FPU.

## 2026-10-01 — Timing: disk DMA as the head moves, spin-up, bus slots, line blits

**Disk DMA is progressive** (`diskXfer`).
- A read now delivers each word to chip RAM as it passes the head (227
  CPU cycles per word, after the sync wait), not all at the start.
- A write takes its buffer when the transfer ends, since by then the
  controller has read all of it.
- Clearing DSKLEN's DMA bit aborts a transfer, with no DSKBLK.
- Save states carry the transfer in progress.

**Drive spin-up.** A drive raises /DSKRDY 300 ms after its motor starts,
typical for a 3.5" drive, instead of at once.

**Fixed DMA slots for the blitter** (`fixedSlot`). These now follow the HRM
layout: refresh $01-$07, disk $09-$0D (while a transfer runs), audio
$0F-$15 (one per enabled channel), sprites $17-$35. Before, only refresh and
sprites counted, and the sprite range was shifted.

**The Copper competes for the bus** (`Copper.busyWords`). While running
rather than waiting, the Copper takes the next even slots. The CPU and
blitter now lose those. How many it will take comes from reading ahead in
its list, with no side effects, to the next WAIT or SKIP: 0 while it waits.
`chipBusWait` and `blitterSlots` give the Copper the first free even slots,
after the bitplanes.

**Line blits are incremental** (`lineRun`). A line is drawn a pixel per 4
colour clocks of bus time the blitter gets, with the registers following
it, like area blits. The Bresenham code is unchanged, just made resumable.

New tests:
- `TestDiskDMAProgressive`, `TestDiskDMAAbort`, `TestDiskSpinUp`;
- `TestCopperDelaysCPU`, `TestAudioDMATakesBlitterSlots`;
- `TestLineBlitIsIncremental`.

Disk tests that checked memory straight after starting a transfer now run
it to completion first (`finishDiskDMA`). Lemmings, Supremacy and the full
suite are unchanged.

## 2026-10-01 — IPF disk images; Supremacy boots

`.ipf` images (also inside zips) now load. The format, and how goAmiga
decodes it, are written up in [ipf-format.md](ipf-format.md).

**What was built:**
- `ParseIPF` checks every record's CRC, matches DATA to IMGE by chunk ID,
  and rebuilds each track's MFM cells from its block descriptors. It
  handles:
  - sync, raw, data, gap and flakey stream elements;
  - CAPS (byte) and SPS (bit) counts;
  - gap values, and forward and backward gap streams;
  - the start-bit offset from the index.
- A drive can hold an IPF, which is always write-protected. Read DMA from
  one (`readIPF`) works on cells: it finds DSKSYNC at any bit alignment
  through a rolling 16-bit window, times the transfer at 2 µs per cell,
  and gives weak cells new random values on each read.
- Save states keep the IPF file and re-parse it (state version 4).

**Supremacy** (Virgin 1990, SPS IPF, CAPS encoder). Track 0 decodes to
eleven good AmigaDOS sectors and a `DOS` bootblock. The game tracks are
custom: on cylinder 1, a single sync pair, then a 4,105-byte sector. The
bootblock ran and the game's loader started, but then it read the same
track forever.

**The cause was a blitter bug.** The loader decodes MFM with the blitter:
B shifted by 15, a blit 4 words wide and 513 lines tall, zero modulos.
Each word takes its top bit from the word before. goAmiga cleared the
shifters' history at the start of every line, but the real blitter
carries it from one line's last word into the next line's first word.
That's why software widens shifted blits by a word and masks the extra
one with ALWM = 0. The first word of every line decoded wrongly, so the
loader's checksum failed every time.

Fixed: the history carries across lines; A's history is the word *after*
AFWM/ALWM masking; and the fill carry starts each line at FCI (BLTCON1 bit
2) rather than 0. With that, Supremacy loads to its title picture and the
opponent-selection screen. Lemmings, the Workbench tests and the full
suite are unchanged.

**Tests.** Synthetic IPFs built in code:
- `TestIPFCapsTrack`;
- `TestIPFSPSTrack` (bit-aligned elements, both kinds of gap stream);
- `TestIPFWeakCells`;
- `TestIPFCorrupt`;
- `TestIPFReadDMAUnaligned` (a sync word at an odd bit position).

Also `TestBlitShiftCarriesAcrossLines`, and `TestParseIPFSupremacy` for a
real image (`GOAMIGA_IPF`).

**Not modelled:**
- variable cell timing (density types 3 and up: Copylock, Speedlock
  timing checks);
- per-track revolution length, since the index stays at 6,250 words;
- writing to IPF disks.

## 2026-09-30 — The 68020

`--cpu 68020` fits a 68EC020 accelerator: 24-bit addressing, no MMU or
FPU, like the A1200's CPU. It runs at 250% of the 7 MHz 68000 by default
(`--cpu-speed`).

**go-m68k** (`Processor.Model = Model68020`; `m68020.go`,
`m68020_misc.go`, `bitfield.go`, and `decodeFullExt` in `util.go`):
- **Registers and exceptions:**
  - control registers VBR, CACR, CAAR, SFC, DFC and MSP, through MOVEC;
  - vectors read relative to VBR;
  - exception frames begin with a format/vector word. Format 2 frames
    (CHK, CHK2, TRAPcc/TRAPV, trace, divide by zero) add the instruction's
    address, and RTE pops by format.
- **Behaviour changes:** no address errors for data; MOVE from SR
  privileged; MOVE from CCR.
- **New instructions:**
  - RTD, EXTB.L, LINK.L, TRAPcc, CHK.L;
  - MULU.L/MULS.L (32- and 64-bit results);
  - DIVU.L/DIVS.L/DIVUL.L/DIVSL.L (64/32 and 32/32);
  - Bcc/BRA/BSR with a 32-bit displacement;
  - the eight bit-field instructions (fields wrap in registers, and can
    span five bytes at signed offsets in memory);
  - CAS, CAS2, CHK2/CMP2 (with wrapping bounds), MOVES, PACK and UNPK.
- **Addressing:** scaled indexing (×1/2/4/8), and the full extension word
  format: base and index suppression, word or long base displacement,
  memory indirect pre- and post-indexed with an outer displacement.
- **Not implemented:** the instruction cache (CACR is stored), CALLM/RTM
  (68020 only, never used by AmigaOS), and coprocessor (FPU) instructions,
  which take the F-line exception. An instruction fetch from an odd address
  still uses the 68000's address-error frame.

The 68020 opcodes are matched first only when the model is 68020, so the
68000 path is untouched and its SingleStepTests baseline still passes.
There is no equivalent single-step suite for the 68020, so each
instruction and addressing mode has hand-assembled tests against the
manual's definitions. The tests caught a dispatch mistake in the first
draft: CAS's pattern mask also matched `$08C0`, which is BSET #imm.

**Amiga side:**
- `SetCPU(model, speed)`. An executed instruction's cycles are divided by
  speed/100; chip-bus waits and idle skips stay real-time.
- Save states carry the model, the control registers and the speed
  (state version 3).

**Checks:**

| Kickstart | Disk | Workbench on 68000 | Workbench on 68020 | AttnFlags |
|---|---|---|---|---|
| 3.2 | Install disk | 283 frames | 115 frames | 68010 and 68020 |
| 1.3 | Workbench 1.3 | 827 frames | 471 frames | 68020 |

Your installed Workbench 3.2 boots fully on it too: SetPatch switches the
instruction cache on (CACR = 1), and the backdrop appears. Lemmings plays
on the 68020 as well.

## 2026-09-30 — First games: Lemmings plays; zipped disks

**Zipped disks.** `ReadDiskImage` reads a plain ADF, a zip archive (the
first `.adf` inside), or a gzip-compressed ADF (`.adz`, `.adf.gz`). Writes
to a compressed disk stay in memory. IPF images, which record a disk's raw
magnetic layout to preserve copy protection, are recognised and refused
with an explanation. Supremacy, one of the test games, only comes as IPF.

**Game harness** (`TestGameDiag`, driven by `GOAMIGA_*` environment
variables). It boots a disk headlessly and saves a screenshot every N
frames. It can:
- click the mouse at given frames, and move it;
- swap disks at a given frame;
- trace instructions from a given frame;
- log custom register writes by name (for example `DSK`);
- dump the disk controller's state.

**Lemmings (Skid Row crack, 2 disks), step by step:**
1. The cracktro and trainer menu ran and drew correctly.
2. Then it hung on a black screen. The game's own loader
   (`A6 = $DFF024`) started a read and polled `BTST #1,-5(A6)`, a *byte*
   read of $DFF01F (INTREQR's low byte, DSKBLK). DSKBLK was set: INTREQR
   read $17A2. But `CustomChips.Read` special-cases only each register's
   even address, so a byte read of the odd half returned an unrelated raw
   byte. It's the read-side twin of the byte-write bug found with ECS. A
   1-byte read at an odd address now reads the register as a word and
   returns its low byte.
3. With that fixed: DMA Design logo, intro, the "insert disk 2" prompt,
   the swap, the title menu, and level 1 ("Just dig!") running with
   walking lemmings, the skill panel and the minimap.

**Also fixed on the way:** disk read DMA started while no drive was
selected, or while the motor was off, was silently dropped. The real
controller keeps reading the silent data line until a sync word arrives,
so the read now waits (`dmaWaiting`) and starts once a drive is ready. It
wasn't Lemmings' problem, but loaders do rely on it. The save-state
version went to 2 for the new field.

Tests: `TestReadDiskImage` (plain, zip, gzip, IPF in and outside a zip,
wrong size) and `TestCustomByteReadOddAddress`.

## 2026-09-29 — Save states

Cmd+Shift+1-9 saves the whole machine to a slot, and Cmd+1-9 loads it
(`goamiga-N.state` in `--state-dir`). `--load-state FILE` resumes at
startup.

**Format** (`amiga/savestate.go`). A gzip file containing: a magic string
and version; the ROM's CRC-32 (the Kickstart checksum can't identify a ROM,
since every good one sums to $FFFFFFFF); the configuration (chip, slow and
fast RAM, chipset, hardfile paths); then each component. Loading
reconfigures memory and chipset to match, and refuses a different ROM or a
different set of hardfiles.

**One method per component, both directions.** `state(s *stateCodec)`
calls `num(s, &field)`, `s.bool`, `s.bytes`, and so on. The codec writes
each field when saving and reads it back into place when loading, so save
and load can't drift apart. The state covers:
- CPU registers and run state (new in go-m68k: `Processor.State` and
  `SetState`, since the pending interrupt level and run state are
  unexported);
- the beam and cycle counter, so disk rotation and CIA E-clock phase carry
  over;
- chip, slow and fast RAM, and the Autoconfig boards (the controller's ROM
  is rebuilt, then its memory restored);
- custom registers, both CIAs (timers, TOD, ICR), the Copper mid-instruction,
  the blitter including a blit in progress, the video pointers and
  interlace state, sprites including the current line's writes;
- the disk controller, and each drive with its disk image and any pending
  swap;
- Paula's channels, sample phase and filter memories;
- the keyboard queue and mouse counters.

Hard disk contents aren't included; they're too big.

**Tests.** `TestSaveStateResumesExactly` saves at two points: Kickstart 1.3
mid-boot from floppy, and Kickstart 3.2 at Workbench from a hardfile, whose
bytes are restored between the runs. It runs 300 frames on, loads the state
into a new machine, runs the same 300 frames, and requires identical CPU
state, beam, cycle count, chip RAM and screen. `TestSaveStateResumesSound`
does the same with a looping DMA waveform and compares every sample.

To check the tests themselves, I dropped the Copper's state from the save,
and they failed. Dropping Paula's state wasn't caught until the sound test
existed, which is why it was added. `TestLoadStateChecks` covers the
wrong-ROM and garbage cases.

States are about 180-430 KB (mostly RAM and floppy images, compressed).

## 2026-09-29 — ECS chipset

`--chipset ocs|ecs-agnus|ecs` (`Amiga.SetChipset`):
- `ecs-agnus` is the 8372A Fat Agnus with the original Denise, which is
  what an A500 with 1 MB of chip RAM really had. It is the default with
  `--chipram 1024`, otherwise `ocs`.
- `ecs` adds the 8373 ECS Denise (A500+, A600).

**ECS Agnus:**
- VPOSR reports ID $20 (8372A, PAL).
- BLTSIZV/BLTSIZH start big blits, up to 32,768 lines × 2,048 words
  (`Blitter.ExecuteBig`). The blitter now starts from a width and height
  (`start`) rather than a packed BLTSIZE.
- BLTCON0L sets just the minterm.
- DIWHIGH supplies the window's high bits (H8 and V10-V8 for start and
  stop) until DIWSTRT or DIWSTOP is written again.
- BEAMCON0 is stored only.
- On OCS none of these registers do anything.

**ECS Denise:**
- DENISEID reads $FFFC.
- KILLEHB (BPLCON2 bit 9) turns plane 6 off rather than giving half-brite.
- BRDRBLNK (BPLCON3 bit 5, with BPLCON0 ECSENA) blacks out the border.
- Super-hires (BPLCON0 SHRES): up to 2 planes, twice the hires words, two
  pixels per output column (`pfIdx`/`pfIdxB`), shown as their average.
  The DMA slot table knows super-hires takes every slot of its window.

**Bug found on the way: byte writes to custom registers.** With ECS
Agnus, Kickstart 3.2 drew window borders but no text or icons. It starts
every blit through BLTSIZV/BLTSIZH, and sets the minterm with
`MOVE.B #mt,$DFF05B`, a byte write to BLTCON0L's odd address. On a 68000 a
byte write drives the byte on both halves of the data bus, and the custom
chips decode only word addresses, so they see a word write of the byte
doubled to $05A. `CustomChips.Write` now turns every 1-byte write into
exactly that. Before, a byte write only stored one byte and triggered no
register side effect.

**Checks:**
- `TestChipsetDetectedKS32` boots Kickstart 3.2 with each chipset.
  graphics.library's ChipRevBits0 reads 0, 1 and 3, and the three
  Workbench screens are pixel-identical 400 frames in.
- `TestChipsetECSKS13` boots 1.3 with ECS fitted.
- Unit tests cover big blits, OCS ignoring them, DIWHIGH, the byte-write
  rule, super-hires, KILLEHB and border blanking.

## 2026-09-29 — The blitter works word by word; BZERO

The blitter's timing was already modelled:
- BBUSY lasts the blit's real duration from the HRM cycle table;
- only bus slots free of bitplane, refresh and sprite DMA count;
- the CPU waits for it, in nasty mode or when a blit uses every slot.

But all the data moved at the BLTSIZE write. For as long as BBUSY stayed
set, the CPU could already read a finished destination, and the blit had
read its source before the CPU could change it. Code that races the
blitter (writing just behind it, or preparing the next source while it
runs) saw the wrong thing.

**Now** an area blit is a `blitRun`: its pointers, x/y position, shift
history, fill carry, and the control registers, modulos and masks latched
at BLTSIZE.
- `TickCycles` gives it the bus slots it got (`step`). After the pipeline
  startup, each word costs the HRM's cycles for its USEx combination.
- Each word reads its sources when it is reached and writes D straight
  away, and the BLTxPT registers follow it.
- When BBUSY's timer ends (`finish`), or a new BLTSIZE supersedes a running
  blit, any remaining words are done first.

The word loop is the old `executeBlit` body, unchanged apart from keeping
its state in the run. Line mode is still done at once; lines are short
and rarely raced.

**Blitter DMA off pauses it.** With DMAEN or BLTEN clear, the blitter
now gets no slots, as on the real machine. Before, a blit completed
regardless.

**BZERO** (DMACONR bit 13) is set when every word the last blit produced
(area or line) was zero. Games use it for blitter collision tests. It was
never set before.

**Tests.** The tests of what blits produce now finish the blit first
(`blitNow`); the timing tests keep the real behaviour.
`TestStoppedCPUWakesForBlit` now switches blitter DMA on. New tests:
`TestBlitIsIncremental` (halfway through its time, about half the words
are written and DPT matches), `TestBlitReadsSourceWhenReached`, and
`TestBZERO`. The full suite passes. Your installed Workbench 3.2 renders
identically before and after.

## 2026-09-29 — Reset, pause, fullscreen, screenshot

These are host commands on Cmd:
- **Cmd+R** resets.
- **Cmd+P** pauses. The audio buffer is muted, and Amiga input isn't read,
  so keys don't arrive in a burst on resume.
- **Cmd+F** toggles fullscreen.
- **Cmd+S** saves a PNG named by the time to `--screenshot-dir`.
  Non-interlaced rows are doubled so the image has the on-screen
  proportions.

The status bar shows PAUSED and a note for three seconds after a command.

**Reset had to become a real /RESET.** `Amiga.Reset` was only ever used at
power-on, when everything was already zero. It reset the CPU, the memory
map, the keyboard and Paula. On a running machine that left DMA,
interrupts, CIA timers and the Copper as they were. It now also:
- resets the custom registers and both CIAs;
- replaces the Copper, blitter and sprite engine with new ones;
- clears the disk controller's DMA state and drive motors, leaving disks
  in their drives (`DiskDrive.resetDMA`);
- clears the hard disk light's timer.

Chip RAM keeps its contents, as on the real machine.
`TestResetRunningMachine` boots Workbench from a hardfile under Kickstart
1.3 and 3.2, resets, and checks that Workbench starts again. Under 1.3 it
took 787 frames the second time and 827 the first.

## 2026-09-29 — Settings file; the command line builds without Ebitengine

**`goamiga.conf`.** The format is `key = value` lines whose keys are the
flag names, so there's no new vocabulary and no TOML dependency
(`internal/config`).
- Keys can repeat where the flag can (`df0`, `hdf`).
- A bare key sets a boolean. `#` starts a comment, and quotes are
  optional.
- Relative paths (`rom`, `dfN`, `hdf`) are resolved from the file's folder,
  so a config next to the ROMs works from anywhere.
- Flags given on the command line win: `Apply` skips any flag
  `flag.Visit` reports as set.
- Lookup order: `--config`, `./goamiga.conf`,
  `~/.config/goamiga/goamiga.conf`.
- An unknown key, or a bad value, stops with the file and line number.
- `goamiga.conf.example` documents it. The personal `goamiga.conf` is
  git-ignored.

**A test gotcha.** The parser first lived in `cmd/goamiga`, but that test
binary crashed during Ebitengine's package init on macOS. `currentMouseLocation`
dereferences nil when there is no window-server session, for example with
the screen locked. That's why it moved to its own package. The same crash
happens for `amiga` tests and the binary whenever the screen is locked, so
`go test -tags noebitengine ./amiga/` is the way to run tests then.

**`-tags noebitengine` builds the command line again.** The window code
moved into `cmd/goamiga/display.go`. With the tag, a stub reports that
there is no display and suggests `--headless`.

## 2026-09-29 — BPLCON0 and sprite registers changed mid-line

Until now only colour, BPLCON1 and BPLCON2 writes took effect at their
column. Everything else used the register's end-of-line value for the whole
line.

**BPLCON0.** It is read by two chips, at different times:
- **Agnus** fetches with the value in force when the fetch starts
  (DDFSTRT). `RenderLine` rewinds any BPLCON0 write made at or after that
  colour clock to get it. Before, a Copper list switching the display off
  at the end of a line (after the fetch) stopped that line from being
  fetched, and its pointers from advancing.
- **Denise** shows the planes BPLCON0 names, capped by what was fetched,
  and its dual playfield, HAM and EHB modes from each write's column.
  `drawLine` recomputes a plane mask and the mode flags at each change. A
  change of the hires bit mid-line is not shown; the layout stays as
  fetched.

`regChange` now records the write's colour clock as well as its column.

**Sprites.** There were two problems:
- Sprite DMA ran at the end of each line (just before `RenderLine`), so it
  overwrote anything the Copper wrote to the sprite registers during the
  line. It now runs just after `nextLine`, at the start of the line, where
  the real DMA slots are ($15-$33).
- Each sprite was drawn once at its final position. Now `FetchLine` ends
  with `startLine`, which snapshots the eight sprites, and each
  SPRxPOS/CTL/DATA/DATB write during the line is recorded with its column
  (`SpriteEngine.note`, called from the custom register writes with
  `writeHpos`). `paint` replays the line from the snapshot: an armed
  sprite (DATA written, not since disarmed by a CTL write) shows when the
  beam reaches its HSTART. If the Copper then moves HSTART right and
  rearms it, it shows again there. That is the horizontal sprite reuse
  games use.

With no writes to a sprite in a line, the current registers are used, so
direct pokes (and CPU writes with the display off) behave as before.

New tests: `TestMidLineBPLCON0`, `TestLateBPLCON0KeepsFetch`,
`TestSpriteReusedMidLine`, `TestSpriteDMABeforeCopperWrites`. The full
suite passes.

## 2026-09-29 — Warp mode and activity lights

**Host commands on Cmd.** Alt/Option proved unreliable on macOS (F11 saw it
held), and Cmd (the Windows key on a PC) isn't an Amiga key. So host
commands are Cmd+key, and `pollKeyboard` drops any key pressed while Cmd is
held so nothing reaches the Amiga.

**Warp (Cmd+W, `--warp`).**
- Each tick emulates for 14 ms of host time instead of pacing by the audio
  clock.
- Every frame but the last runs with `Video.DisplayActive` off, which uses
  the fast headless path. Only the last frame is drawn.
- Paula's output buffer is muted and emptied (`AudioBuffer.SetMuted`), so
  nothing plays sped up or late when warp ends.
- Speed is measured each second as a multiple of the PAL frame rate. Boot
  profiling suggests about 20-25× real time.

**Status bar (`--statusbar`, on by default).** Layout adds 18 logical pixels
below the picture, drawn with Ebitengine's debug font and filled rects:
- POWER: CIA-A PRA bit 1, bright or dim like the A500 LED that follows the
  audio filter.
- Each connected drive's motor LED and head cylinder (`Drive.MotorOn`,
  `Drive.Cylinder`).
- The hard disk light (`hdBoard.Activity`): green for a read, orange for a
  write. Transfers are instant, so the light is held for 3 frames.
- The warp speed, while warp is on.

## 2026-09-27 — Sound stutters and skips under load: pace by the audio clock

Sound stuttered and then seemed to speed up whenever there was a lot of
animation. It wasn't emulation speed. With the display on during a busy
Kickstart 3.2 boot, a frame took a median of 2.8 ms of host time and at
most 5.3 ms, against a 20 ms budget.

**Cause.** Each 50 Hz Ebitengine tick ran exactly one emulated frame. A PAL
frame is 49.92 Hz, so Paula produced about 0.16% more sound than the host
played, and the 100 ms buffer sat permanently full.
- Whenever Ebitengine delivered ticks back to back (it catches up in bursts
  under drawing load), a frame's worth of the oldest samples was dropped.
  That is the "speed up".
- A late tick drained the buffer and played silence. That is the stutter.

**Fix.** The audio clock now sets the emulation speed (`framesToRun`,
`paceFrames`). Each tick runs one frame, none if the buffer is more than a
frame above its 60 ms target, or two if it is more than a frame below. A
burst of ticks therefore runs nothing extra. The buffer never has to drop
samples, and emulation runs at exactly the host's audio rate. The 0.16%
drift now shows up as a repeated or skipped video frame about every 12 s
instead of an audible glitch. The buffer's hard cap went up to 500 ms and
is only a safety net. `AudioBuffer.Read` reuses its scratch slice rather
than allocating on every pull. With `--mute` it is one frame per tick as
before.

`TestPaceFramesJitter` simulates two minutes of bursty ticks (gaps of 0 to
60 ms, averaging 20) against a 48 kHz consumer. The buffer stays between
20 and 100 ms, never empties, and emulation runs at ~49.9 frames/s.

## 2026-09-27 — Bottom of a 16-colour Workbench missing: Copper vs bitplane DMA

After installing Workbench 3.2 to a hard disk, the bottom fifth of the screen
showed only the background colour, at every window scale. The window's
bottom border and gadgets were missing, so the display itself was stopping.
The Install disk's Workbench drew fine.

**Cause.** The installed system opens a hires 4-plane (16-colour) screen.
graphics.library ends such a screen with this Copper list:

    WAIT (255,$38)
    WAIT (44,0)
    MOVE BPLCON0 = display off

The Copper compares only 8 bits of the line, so at line 255 the second WAIT
($FF ≥ $2C) is already satisfied. On real hardware it isn't reached there:
hires with 4 planes takes every even DMA slot from DDFSTRT ($38) to the end
of the fetch ($E0). The Copper can't fetch the second WAIT until $E0/$E2,
the last slots of line 255, so the compare happens on line 256 and the
display runs to line 300. Our Copper ran one whole instruction per 8-cycle
slot and ignored bitplane DMA, so it switched the display off at line 255.
The Install screen has fewer planes and never stalls the Copper, which is
why it was fine.

**Fix.** The Copper now works one bus cycle at a time (`Step`, every
`CopperCyclesPerWord` = 4 CPU cycles on even colour clocks):
- it fetches one word per cycle, and an instruction executes when its second
  word arrives;
- a WAIT compares the beam in the cycle after that, and waking takes a
  cycle;
- a word can't be fetched in a slot that bitplane DMA takes (`bitplaneDMA`,
  the table already used for CPU slot stealing). Hires with 4 planes blocks
  the Copper for the whole fetch window, and hires with 3 planes or lores
  with 6 halves its speed.

The $FFDE line-255 trick still works (`TestCopperLine255Wrap`), and so
does the free-bus throughput of about 56 MOVEs a line. New tests:
`TestCopperStallsDuringHires4PlaneFetch` and
`TestCopperHalfSpeedDuringHires3PlaneFetch`.

Also found on the way: Paula masked AUDxLCH to 3 bits, so audio couldn't
reach chip RAM above 512 KB. It is now 5 bits, and chip RAM wraps at its
size.

## 2026-09-27 — Bigger window

The window now opens at twice the emulated screen's size: 1280×1024 for the
standard 640×512 view, 1440×1136 with overscan. `Layout` still returns the
emulated size, so Ebitengine scales the picture and the emulated resolution
doesn't change. `--scale N` sets the multiplier. If the window wouldn't fit
the monitor it is reduced (`windowScale` uses 95% of the width and 90% of
the height). The window can also be resized freely.

## 2026-09-27 — Choosing a disk while running

F11 now opens a file dialog to pick an ADF for the drive, with the existing
modifiers: Alt, Ctrl and Ctrl+Alt for DF1:-DF3:, Shift to eject. Cycling
through the command-line disk list moved to Cmd/Win+F11. Chosen disks join
that list, so the title and later swaps know them.

The dialog is `github.com/ncruces/zenity`. It is pure Go: osascript on
macOS, Win32 on Windows, zenity or kdialog on Linux. It runs on its own
goroutine and posts the result on a channel that `Update` polls, so the
Ebitengine loop and the emulation don't stall. The mouse is released from
capture first so the dialog can be used. Insertion goes through the same
`SwapDiskUnit` path as before, which waits for trackdisk to see the drive
empty.

I considered dropping files onto the window as well. Ebitengine's
`DroppedFiles` only offers an `fs.FS`, which hides the host path, so a
dropped disk couldn't be written back to its ADF. Left out for now.

## 2026-09-27 — 1 MB chip RAM; more memory by default

The Workbench 3.2 installer ran out of memory. The default machine had
512 KB chip and 512 KB slow RAM, and 3.2 wants more than 1 MB.

- `SetChipRAM(1024)` / `--chipram 1024` fits a megabyte of chip RAM, as the
  8372A "Fat Agnus" does in later A500s. The chip RAM device already wrapped
  addresses at its own size. However, the Copper, bitplane, sprite, audio
  and blitter fetches each masked their pointers to 19 bits ($7FFFF), so
  those masks are gone. DMA now reaches the whole megabyte and wraps at
  the fitted size.
- Agnus still reports the OCS ID in VPOSR. Kickstart sizes chip RAM by
  probing, not from the ID. An ECS ID would make 2.0+ use BLTSIZV/BLTSIZH
  and BEAMCON0, which goAmiga doesn't implement.
- The new defaults are `--chipram 1024 --slowram 0 --fastram 2048`. On an
  A500 the trapdoor RAM is what becomes the second chip megabyte, hence no
  slow RAM.

Tests boot both Kickstarts with 1 MB chip (`TestChipRAM1MBKS13`,
`TestChipRAM1MBKS32`). On 1.3, exec's chip region ends at $0FE800 because
its supervisor stack sits at the top, with 835 KB free at Workbench. On 3.2
it ends at $100000, with 621 KB free.

## 2026-09-27 — Hard disks: an autobooting hardfile controller

`--hdf file.hdf` (repeatable) attaches hard disk images. Plain WinUAE-style
hardfiles and RDB disks both work. Kickstart 1.3 and 3.2 boot from them
with no floppy.

**Approach.** I didn't emulate a real controller (A590/A2091 with a WD33C93
SCSI chip, plus its Commodore driver ROM). goAmiga has its own Zorro II board
and device driver, as UAE does with uaehf.device. The board's autoboot ROM
follows Commodore's autoboot example:

1. Autoconfig: er_Type has ERTF_DIAGVALID, and er_InitDiagVec points at a
   DiagArea (`DAC_WORDWIDE|DAC_CONFIGTIME`).
2. expansion.library copies the DiagArea to RAM and calls its DiagPoint,
   which fixes up the RomTag's MatchTag and EndSkip in the copy. The system
   then finds that RomTag and initialises it.
3. The RomTag init (RTF_COLDSTART) does:
   - `AddDevice(goamiga.device)`
   - for each partition, `MakeDosNode(parameter packet)`, then Enqueue a
     BootNode (NT_BOOTNODE, `ln_Name` = the ConfigDev) on
     `eb_MountList`.
4. strap boots from the highest-priority BootNode by calling the
   DiagArea's BootPoint, which does `FindResident("dos.library")` and jumps
   to its init.

**No assembler needed.** Nothing outside Go can assemble 68000 code here, and
the ROM only needs glue. `hdboard.go` has a tiny assembler (about 15
instructions, labels, short branches) and generates the ROM when
expansion.library assigns the board's base, so every address in it is
absolute. Each routine that needs real work writes a trap number to a board
register (`move.w #n,$E90100`). The board's Write handler then runs Go code
with the CPU registers and memory in reach:

| Trap | Go does |
|---|---|
| DiagPoint | patch the RomTag copy, remember the ConfigDev |
| init | build the device base (jump table + Library) in the board's own RAM, so no AllocMem |
| next partition | build the MakeDosNode parameter packet |
| boot node | build the BootNode |
| Open, Close | check the unit, set io_Unit, keep the open count |
| BeginIO | perform the request |

BeginIO's glue calls exec's ReplyMsg only when Go says the request was not
IOF_QUICK. TD_ADDCHANGEINT is kept, never replied, as trackdisk does. The
device supports:
- CMD_READ/WRITE/UPDATE/CLEAR and TD_FORMAT
- TD_MOTOR, TD_SEEK, TD_CHANGENUM/CHANGESTATE, TD_PROTSTATUS
- TD_GETGEOMETRY, TD_GETNUMTRACKS, TD_GETDRIVETYPE
- the TD64 and NSD 64-bit commands, and NSCMD_DEVICEQUERY

**Autoconfig chain.** Adding a second board meant generalising the fast RAM
code (`autoconfig.go`). Only the first board not yet configured answers at
$E80000, and the rest follow once it takes its address. Fast RAM goes to
$200000 and the controller to $E90000.

**Images.** A plain hardfile has 32 sectors per track, one head and two
reserved blocks, in one partition with boot priority -unit. An unformatted
one is mounted as FFS on Kickstart 2.0 and later, OFS before. RDB disks
supply their geometry and PART blocks (name, DosEnvec, bootable, no-mount).
A handy fact for testing: an ADF is a valid plain hardfile. Its 1,760 blocks
make 55 cylinders of 32 sectors, with the root block in the middle.
`TestHardfileBootKS13` and `TestHardfileBootKS32` boot Workbench 1.3 and the
Install 3.2 disk that way:

| Kickstart | Frames to Workbench from hardfile | From floppy |
|---|---|---|
| 1.3 | 827 (16.5 s) | 3,056 |
| 3.2 | 283 | — |

Workbench3.2.adf alone stops at "insert LIBS/icon.library": the A500 3.2 ROM
doesn't have it and that disk doesn't carry it. It is a missing module, not
an emulator fault.

Tests without Kickstart drive the traps directly (`TestHDDeviceIO`,
`TestHDDeviceWriteProtect`), and `TestHardfileRDB` parses a built RDB.

## 2026-09-27 — Joystick in port 2

`joystick.go` gives game port 2 a digital joystick:
- **Directions.** JOY1DAT reads the switch pattern: right sets X1, left
  sets Y1, and down and up are X1^X0 and Y1^Y0.
- **Fire.** Fire pulls /FIR1 (CIA-A PRA bit 7) low.
- **Second button.** The second button grounds port 2's POTY pin
  (POTGOR DATRY).

POTGO is now modelled properly. Each pot pin reads high as an input, reads
its DATxx bit when POTGO makes it an output, and reads low while a button
shorts it. The mouse's right button therefore appears as DATLY, and its
middle button as DATLX. POTGOR used to return $FFFF, and now returns only
the four data bits, as Paula does. JOYTEST loads the mouse counters'
upper six bits.

On the host, any gamepad drives the joystick (D-pad or left stick, A and B).
Ctrl+F12 or `--joy-keys` turns the arrow keys plus Right Ctrl/Alt and Right
Shift into the joystick and takes those keys off the Amiga keyboard. F10 is
an Amiga key, so the toggle couldn't use it.

## 2026-09-27 — Real 68000 instruction timing; CIA /IRQ is a level

**CPU timing.** go-m68k charged every instruction the 4-cycle minimum. On
the SingleStepTests data, emulated time was a median 39% of real time, so the
CPU ran about 2.5× too fast against the chipset. `go-m68k/timing.go` now
applies the 68000 User's Manual tables: effective-address times, MOVE source
plus destination, MOVEM per register, shift count, MULU/MULS bit patterns,
Jorge Cwik's DIVU/DIVS algorithm, branch outcomes and exception frames. A few
corrections came from the test data. Result: 99.78% of the 1,000,060 tests
match to the cycle, and the emulated/real ratio is 1.00. The baseline file
now also caps cycle mismatches per instruction.

Effect on the machine: Kickstart 1.3 to `workbench.task` now takes 3,056
frames (61.1 s emulated, 2.4 s host). With Workbench idle and the display on,
the CPU is busy 13% of the time and the emulator runs at 23.8× real time.

**CIA interrupt lost with slow RAM.** Once timing was realistic,
`TestSlowRAMFound` hung in trackdisk. A CIA's /IRQ output is a level. It stays
asserted while any enabled ICR bit is pending. We modelled it as an edge: the
CIA set INTREQ PORTS/EXTER once, and a handler that cleared INTREQ before
reading the ICR lost the interrupt for good. The timing change moved exactly
such a CIA event into that gap. Now an INTREQ clear of bit 3 or bit 13 is
ignored while CIA-A or CIA-B still asserts /IRQ (`CIA.IRQ()`). Tested by
`TestCIAIRQIsLevel`. The full suite passes (376 s).

## 2026-09-27 — CPU checked against the SingleStepTests 68000 suite

**Suite.** github.com/SingleStepTests/680x0 (formerly Tom Harte's
ProcessorTests): about 8,000 randomly generated tests per 68000 operation,
1,000,060 in all. Each gives full register and memory state before and after
one instruction, from an implementation verified against real hardware.
The data (193 MB) is downloaded into go-m68k's git-ignored
`testdata/680x0/`. `singlestep_test.go` runs it in ~13 s and reports a pass
rate per file plus cycle-count agreement. `testdata/singlestep_baseline.txt`
records the failures allowed per file, so a regression fails the test.

**Result: 76.4% → 99.999%** (1,000,059 / 1,000,060) in go-m68k `e86428c`.
The fixes, largest first:
- **Address errors** (exception 3) were not implemented at all, and
  accounted for most failures. A Decoder hook catches word and long
  accesses at odd addresses, and the CPU builds the 14-byte group-0
  frame: SSW = IR & $FFE0 | R/W | I/N | FC, then address, IR, SR, PC.
  The details were read off the test data:
  - Stacked PC: the PC at the fault − 2, or the target − 4 for an odd
    jump/branch/return target.
  - JSR faults before pushing its return address; BSR after.
  - MOVE prefetches before a −(An) write (+2) and after a memory →
    (xxx).L write (−2), and sets flags before its write.
  - Long writes to −(An), and ADDX/SUBX long operands, go low word first,
    so a fault leaves An decremented by 2.
  - (An)+ reads increment before the access.
  - MOVEM writes An back only at the end.
- **Missing instructions:** CHK, NBCD, RTR, TRAPV.
- **BCD:** ABCD/SBCD/NBCD use flamewing's formulas measured on hardware
  (invalid digits, "undefined" N and V). `-(A7)` byte accesses move by 2,
  and the source is read first.
- **Read before write:** CLR and MOVE from SR read their destination
  first (a 68000 quirk). TAS and CLR now compute their effective address
  once.
- **Bcc/BSR** displacement `$FF` is −1 on the 68000. It was treated as the
  68020's 32-bit form, and BSR `$61FF` was even illegal.
- **UNLK A7** keeps the popped value.
- **DIVU/DIVS:** overflow sets V, clears C, and leaves N, Z and Dn alone.
  Divide by zero raised a host error that would stop the emulator; it now
  takes exception 5.

**Disputed test data** (counted separately in the harness, not
"fixed"):
- ASR by a register count larger than the operand size, on a negative
  value: the data expects C = X = 0. The Programmer's Reference Manual and
  MAME set them from the last bit shifted out, which is the sign (1).
  3,736 tests, all following this one rule.
- Two ASL.b tests reported as wrong in the suite's issue #4.
- One DIVU zero-divide test stacks the instruction's own address as PC.
  Left as the single allowed failure.

**Amiga.** Address errors are live, and the whole goAmiga suite passes.
`TestOpenBusReadFFFFFF`, which noted that address errors were missing, now
expects exception 3. Boot is 2,586 frames (was 2,584); host time for the
boot is 2.1 s (was 1.9, from the per-instruction fault handler).

**Not done: cycle counts.** They match for a minority of tests (0–50% per
instruction). The CPU's timing is only roughly right, which affects
emulated speed but not correctness. A candidate for later.

## 2026-09-27 — Up to four floppy drives

**Hardware model.**
- **Select lines:** each drive has its own `/SELn` on CIA-B PRB (bits
  3–6). It latches `/MTR` when selected (falling edge) and responds to
  `/STEP` only while selected. `/SIDE` and `/DIR` are shared.
- **Status lines:** the selected drives pull their status lines low on
  CIA-A PRA, wired together.
- **DMA:** there is one controller, and DMA and the index pulse come from
  the selected drive.
- **External drive detection:** Kickstart detects DF1:–DF3: by their ID.
  Turning the motor off resets a shift register, and each later select
  with the motor off clocks out one bit on `/RDY` (low = 1). A 3.5" DD
  drive answers `$FFFFFFFF`, an empty port `0`. DF0: is never asked, so it
  keeps its old `/RDY` behaviour.

**Code.** The per-drive state moved into `Drive` (`amiga/drive.go`): image,
file path, write-protect, motor latch, head, select, change latch, pending
swap and ID counter. `DiskDrive` is now the controller. It embeds DF0:'s
`*Drive`, so `a.Disk.InsertDisk`, `a.Disk.motor` etc. still mean DF0: and the
existing tests were unaffected. `ConnectDrive(n)` and `DF(n)` reach the
others, and `active()` picks the selected drive for DMA/status/index.
`Amiga.LoadDiskUnit` and `SwapDiskUnit` take a drive number.

**UI.** `--df0`…`--df3` (repeatable; `--disk` = `--df0`), `--drives N`,
and `--write-protect` for all drives. F11 / Alt / Ctrl / Ctrl+Alt+F11 swap
DF0:–DF3:, and Shift ejects. The title lists each drive's disk.

**The Workbench 3.2 Clock, revisited.** With Install 3.2 in DF0: and
Workbench 3.2 in DF1:, both volumes mount. With
`Assign LIBS: DF1:Libs DF1:Classes` (amigaguide.library needs
`Classes/DataTypes/amigaguide.datatype`), Clock opens and draws its face;
no requesters, no swapping.

**Tests.** `drive_test.go`: the ID protocol (DF1: `$FFFFFFFF`, absent DF2:
`0`), independent motors and heads, DMA from the selected drive, and
Kickstart 1.3 mounting a second (renamed) disk in DF1: at boot.
`TestKS32TwoDrivesClock` covers the scenario above. The diagnostic takes
`GOAMIGA_DF1`. Full suite passes.

## 2026-09-26 — Clock "error #8000000B"; disk swaps while the drive is busy

**Report.** Booting Install 3.2, swapping to Workbench 3.2 and running
Utilities/Clock gave "Program failed (error #8000000B)", CPU exception 11
(Line-F).

**Reproducing it headlessly** needed new `TestKickstartDiag` options, all
applied to in-memory copies of the images:
- `GOAMIGA_PATCH="old|new;…"` replaces startup-sequence lines (same length
  or shorter; FFS data blocks have no checksum);
- `GOAMIGA_SWAP="disk:frame"` swaps disks at a given frame;
- `GOAMIGA_SWAP_ON_REQUEST=disk` swaps whenever a "System Request" is up
  and the motor is off;
- `GOAMIGA_BOOTBLOCK` borrows another disk's boot block;
- `GOAMIGA_FRAMES`, `GOAMIGA_TRACE_VOLUMES`, and `GOAMIGA_TRACE_DRIVE`.

The catcher now covers Line-A/Line-F too. `diag/adf.py` lists and extracts
files from OFS/FFS images.

Workbench3.2.adf ships with an empty boot block (`DOS\1` + zeros, bad
checksum), so it does not boot on its own. Install 3.2 does, and loads
icon/workbench.library with FixRomLibs. That matches Hyperion's floppy
set; see the Workbench disk's startup-sequence.

**Found along the way: `SwapDisk` inserted too early while the drive was
busy.** When the swap came during disk activity, DOS kept treating the new
disk as the old volume ("Install3.2 has a checksum error on disk block
684"). Drive trace: with the motor on, a data-seek step happened while
the drive was empty. `SwapDisk` took it for trackdisk's change poll and
inserted 0.5 s later. The next seek, now with a disk present, cleared
/DSKCHANGE before trackdisk polled, so the swap was never reported. Real
hardware corrupts disks the same way if you swap while the light is on;
trackdisk only polls when idle. Now only a step with the motor *off*
counts as the poll. Without one, the disk goes in after 3 s of idle drive,
or after 10 s regardless. With that, DOS's volume list follows every swap
under Kickstart 3.2. `TestSwapDiskTiming` covers the busy-drive case.

**The crash itself is not an emulator bug.** OpenLibrary trace for Clock:
`amigaguide.library` v39 returns NULL, and Clock then crashes at PC
`$FFFFFFB8`. That is `JSR -72(A6)` with A6 = 0, i.e. a call through the
NULL library base; the word there (top of ROM) is `$FFFF`, a Line-F
opcode. amigaguide.library is in `Workbench3.2:Libs`, but booted from
Install 3.2, `LIBS:` is `Install3.2:Libs`. A real A500 booted the same
way crashes identically. With `Assign LIBS: Workbench3.2:Libs ADD`, the
amigaguide open no longer fails, but with one drive ramlib ping-pongs
between the two volumes for every library (standard single-drive
AmigaDOS behaviour). Two drives are the practical fix for this disk set.

## 2026-09-26 — Workbench 3.2: DENISEID and the Copper's WAIT wake-up

ROMs now live in `ROMS/` and ADFs in `disks/` (all test paths updated;
both directories are still git-ignored by `*.rom`/`*.adf`).

Booting `disks/Install3.2.adf` under Kickstart 3.2 reached Workbench with a
wrecked display: every horizontal line smeared diagonally over several
display lines. Two separate bugs.

**1. SetPatch detected AGA.** The screen was hires, 2 planes, DDF
`$38`/`$D8` (42 words), with BPLxMOD −8: rows of 76 bytes against an
80-byte bitmap. Under the 1.3 disk the same screen had −4, which is right.
graphics.library computes the modulo from what it thinks the chipset
fetches, and `GfxBase->ChipRevBits0` showed `$08` (AA_ALICE) only after
the Install disk's SetPatch 47 ran. SetPatch re-detects the chipset from
`DENISEID` (`$DFF07C`): ECS Denise returns `$xxFC`, AGA Lisa `$xxF8`, and
OCS Denise has no such register, so the bus floats high. The emulator
returned `$0000`, whose bit pattern reads as AGA. `DENISEID` now reads
`$FFFF` (`TestDENISEIDReadsAsOCS`).

**2. The screen stopped 40 lines early.** The bottom border and scroll
gadgets of the Workbench window were missing. BPLCON0 went to 0 planes
by line 260. Disassembling the view's Copper list showed the standard
pattern: `WAIT $FF,$DE` (end of line 255), `WAIT $2C` (line `$12C` = 300
once past 255), `MOVE BPLCON0` (planes off). The emulated Copper ran the
instruction after a satisfied WAIT in the same slot, so `WAIT $2C` was
compared on line 255, where 255 ≥ `$2C`, and the planes went off
immediately. On hardware, waking from a WAIT costs a cycle and the next
instruction still has to be fetched. A WAIT released at `$DE` therefore
has its successor compared on line 256, whose low 8 bits start again from
0; that is the reason software uses `$DE`/`$DF` there. Now the wake-up
consumes its Copper slot (in `Step` and `StepLine`).
`TestCopperLine255Wrap`. `TestCopperWaitHorizontalPosition` moved its WAIT
from `h=$E0` to `$D0`, because a MOVE after a WAIT at `$E0` now correctly
lands on the next line.

Kickstart 1.3's Workbench rendering is unchanged apart from the title's
free-memory figure (360,168 → 360,112, sampled at a slightly different
moment).

**Tests.** `TestKS32InstallDiskWorkbench` boots Install 3.2
(write-protected) to Workbench and checks no alert, `ChipRevBits0 == 0`,
a Workbench window, and 2 planes still on at line 299. Full suite passes.

## 2026-09-26 — Kickstart 3.2 boots; wide bitplane fetches

`Kick32.rom` (Hyperion Kickstart 3.2, 47.96, the A500/A600/A2000 build;
checksum good) was run with `GOAMIGA_DIAG=1 go test -run
TestKickstartDiag ./amiga/`. The diagnostic takes any ROM via
`GOAMIGA_KS` and a disk via `GOAMIGA_ADF`, always write-protected. It
logs state and exec's alert, catches illegal instructions, and with
`GOAMIGA_TRACE_OPEN=1` traces OpenLibrary/InitResident calls and results.

**No disk:** the insert screen (Boing ball, version text, disk and drive)
renders correctly on the first try, with no alert.

**Workbench 1.3 disk: sheared screen.** Intuition 3.2's Workbench screen
is hires, 2 planes, with DDF `$38`/`$D8` and BPLxMOD −4: 42 words per line,
with the modulo bringing each row back to 80 bytes. `RenderLine` capped the
fetch at 40 words (its buffer size), so the pointers advanced 4 bytes short
every line. Fixes:
- the fetch buffer holds the OCS maximum (50 hires words, DDF `$18`–`$D8`);
- `fetchWords` uses the hardware rule instead of formulas plus a
  special case. Agnus fetches in 8-colour-clock units from DDFSTRT and
  finishes the unit that reaches DDFSTOP plus one more; a unit is 1 word
  per plane in lores and 2 in hires. That gives 20 lores words for
  `$38`/`$D0`, 40 hires for both `$3C`/`$D4` and KS1.3's `$3C`/`$D0` (the
  old "39 → 40" special case is gone), and 42 for `$38`/`$D8`.
  Kickstart 1.3 renders pixel-identically.

**Then a requester: "Please insert a volume containing LIBS/icon.library".**
Correct behaviour, not a bug. The OpenLibrary trace showed ramlib's
`InitResident(icon.library)` blocking, and the requester comes from
inside it. The ROM's workbench.library and icon.library RomTags sit
`$68` bytes apart at the end of the ROM. They are loader stubs that name
"LIBS" plus workbench.library 47.33 / icon.library 47.4, and load those
from disk. The 1.3 disk only has icon.library v34, so 3.2 asks for its
own. A Workbench 3.2 disk set is needed to go further.

**Tests.** `TestKS32InsertScreen` (no alert; colour histogram of the
insert screen with slack for the animated ball; skips without the ROM),
`TestFetchWords`, `TestWideFetchAdvancesPointers`. Full suite passes.

## 2026-09-26 — 512 KB ROMs; the repo's Kickstart 3.1 image is damaged

**ROM mapping.** 512 KB (2.x/3.x) ROMs used to be cut to their upper half.
Now `mapROM` maps the ROM across `$F80000`–`$FFFFFF`: a 512 KB ROM once,
a 256 KB ROM twice (at `$F80000` and `$FC0000`, as the A500 decodes it),
and the same way over `$000000` for the reset overlay. `TestROMMapping`.

**First Kickstart 3.1 boot: red screen, then reset, repeating.** Red is
Kickstart's ROM checksum failure. `ROMChecksum` (sum of longwords with
end-around carry; a good image gives `$FFFFFFFF`) confirms the file is
bad: `Kick31.rom` (40.63, the A500/A600/A2000 build, sha1
`c60577608062fbe536d63c5e497ae04ec5b265bb`) sums to `$8DDE2FFB`, while
`Kick13.rom` sums correctly. The emulator ran the ROM's own check
correctly.

What is wrong with it:
- The footer is shifted: the size field `00 08 00 00` sits 5 bytes late and
  the final `00 1D 00 1E 00 1F` is cut off. 5 bytes were inserted
  somewhere.
- All 40 RomTags up to intuition.library (`0x4F0E8`) are intact.
  gadtools.library and workbench.library have name strings but no RomTag
  at all, so the damage is more than one clean insertion.
- Diagnosis with a scratch copy whose checksum was forced to pass
  (`GOAMIGA_KS31=<file> GOAMIGA_DIAG=1 go test -run TestKS31Diag
  ./amiga/`, which also catches illegal-instruction exceptions): Kickstart
  3.1 gets through exec init with ExecBase in slow RAM, then alerts
  `$80000004` (illegal instruction). The faulting opcode is `$4E21`,
  invalid on every 680x0, at ROM offset `$5E4C4` inside
  intuition.library, i.e. in the shifted region. The other trap is exec's
  deliberate MOVEC CPU probe.

The image cannot boot on anything. Repairing it would mean guessing the
missing bytes. The CLI now warns at startup when a ROM fails the
checksum. Kickstart 3.1 support stays untested until a good image
is available.

## 2026-09-26 — Slow RAM and Zorro II fast RAM

**Slow RAM** (`--slowram`, KB: 0/512/1024/1536, default 512 like an A500
with a trapdoor board) is mapped at `$C00000` behind the chip-bus access
counter, so CPU accesses contend with DMA as on hardware. Kickstart 1.3
finds it by probing and moves ExecBase and the supervisor structures into
it (ExecBase `$C00276`). Workbench shows 884,424 free instead of 360,168.

**Fast RAM** (`--fastram`, KB: 0 or a power of two 64–8192) is a Zorro II
Autoconfig board (`amiga/memory.go`). Until configured it presents its ID
at `$E80000`: each register is two nibbles in bits 7–4 of even bytes, and
every register but er_Type reads inverted. er_Type is Zorro II + link into
the memory list + size code, with a made-up manufacturer `$07DB`.
expansion.library writes A19–A16 to `$4A`, then A23–A20 to `$48`, which
configures it. The board then leaves the config space and its RAM appears
at the base (`$200000`). `$4C` shuts it up. Reset unconfigures it.
Kickstart adds it as `MEMF_FAST|MEMF_PUBLIC`.

The Mapper's explicit "NOP" regions were dropped, since open-bus mode
already floats anything unmapped. That leaves room to map expansion
devices in the gaps.

**Gotcha found.** Many tests detect "Workbench started" by PC `$055C`:
exec's AddTask jump-table entry, which is ExecBase − 282 with ExecBase at
`$676`. With slow RAM, ExecBase moves and that address is wrong. New
tests compute ExecBase − 282 instead. The old tests boot without slow RAM
and are unaffected.

**Tests** (`memory_test.go`): the Autoconfig protocol register by register
(ID nibbles and inversion, base address write, RAM visible at both ends,
board gone from config space, reset); Workbench boots with 512 KB slow
RAM (MemList region at `$C005E8`, ~424 KB free), with a 2 MB board (at
`$200000`, MEMF_FAST, ~1.96 MB free), and with 1 MB slow + 4 MB fast
(5,463 KB free in total). Full suite passes.

## 2026-09-26 — Workarounds removed: native Flood, blitter guards, Copper low-PC skip

**Native Flood.** `flood.go` intercepted graphics.library's Flood at the
KS1.3 ROM address `$FC5AD0` and filled natively, because the ROM's Flood
"filled whole bitplanes". Test: `Amiga.NativeFlood` on vs off, rendering
the insert-disk screen (11 Flood calls). The two pictures were
pixel-identical, and the picture is correct. The ROM Flood had been fine
since the descending-modulo blitter fix; the patch was dead weight, and
a ROM-address hook would have misfired on any other Kickstart.
Deleted, with `flood_series_test.go` (which traced the native fill).
`TestInsertScreenFloodFills` now pins the insert screen's colour histogram
(outline, disk body and shutter fills, background) within 5%, so an
unbounded or missing fill fails.

**Blitter guards.** `GuardWrites` with its deny maps and address bands
(`canWriteD`, `buildDeny`, ~480 lines) was off by default. It was deleted
with its tests (`blitter_guard_test.go`, `wb_*_protect_test.go`,
`wb_idcmp_diag_test.go`) and the drag tests' deny instrumentation. The
blit-start pointers (`preApt`–`preDpt`) stay as diagnostics.

**Copper low-PC skip.** The Copper did nothing while its PC was below
`$1000` or zero, attributed to "a KS1.3 LoadView NULL bug". A counter
showed it fired constantly: hundreds of thousands of slots during boot and
on the insert screen. The real sequence: with no view copper list, COP2LC
is 0, and the system list's COPJMP2 sends the Copper to address 0. A real
A500 does the same, and survives because of OCS register protection. The
Copper can never write `$000`–`$03E`, can write `$040`–`$07E` only with
COPCON CDANG, and an illegal MOVE halts it until the next VBLANK or
COPJMP. Address 0's first word is a MOVE to `$000`, so it just stops.
The emulator's check was wrong both ways: it allowed `< $040` with CDANG,
always allowed `$040`–`$07E`, and skipped the write instead of halting. So
the Copper would have crawled through low memory writing registers. The
skip was hiding that. Now `Copper.halted` implements the real rule and the
PC guards are gone. `TestCopperIllegalMoveHalts` covers each register
class and the restart.

**Checks.** Workbench boot screenshot pixel-identical, boot timing
unchanged, full suite passes. One unexplained event: while iterating, two
runs of a test subset panicked with a nil dereference. The panic did not
reproduce in three repeats of the same subset or in a full `-count=1`
run, and no stack trace was captured. If it recurs, capture the full
output.

## 2026-09-26 — Paula audio

**Channels** (`amiga/paula.go`). Four channels, each with AUDxLC, LEN,
PER, VOL and DAT (written from `CustomChips.Write`).
- **DMA:** with DMACON DMAEN + AUDxEN the channel copies LC/LEN into its
  counters, fetches a word, and raises its interrupt (INTREQ bit 7+n,
  level 4). That is software's cue to write the next buffer's LC/LEN.
  Each word is two signed 8-bit samples, high byte first, each held
  AUDxPER colour clocks. When the length runs out the channel reloads and
  interrupts again. Clearing AUDxEN stops it.
- **Manual mode:** with DMA off, a CPU write to AUDxDAT plays the word and
  interrupts to ask for the next.
- **Modulation:** with ADKCON USEnVn+1/USEnPn+1, a channel is silent and
  its words set the next channel's volume and/or period (alternating when
  both are set), one word per period.
- **Timing:** channels are event driven in colour clocks (`Paula.Tick`
  from `Amiga.Step`), not ticked per cycle. Fast-idle wakes for the next
  audio interrupt (`CyclesToNextInterrupt`), so a task sleeping on audio
  is woken on time.

**Output.** Channels 0 and 3 go left, 1 and 2 right, scaled by volume
(0–64). Output is box-filtered into 48 kHz host samples: the channel levels
are integrated between events and sample boundaries. Then comes the
A500's fixed ~4.4 kHz low-pass and, when the power LED is bright (CIA-A
PRA bit 1 low), the 3.3 kHz 12 dB/octave LED filter. Samples go into an
`AudioBuffer` ring. In the window, Ebitengine's audio player
(`NewPlayerF32`) reads it with 50 ms of player buffering. The ring holds
at most 100 ms and drops the oldest samples: the emulated frame is 49.92 Hz
against a 50 Hz tick, so Paula slightly outpaces the host. `--mute`
turns host audio off. Headless runs mix nothing unless a test attaches
an `AudioBuffer`.

**End-to-end test** (`TestSayHello`). The startup-sequence's first echo
is patched to `sys:Utilities/Say hello`, so narrator.device speaks through
the ROM's audio.device during boot. Checked:
- Workbench still starts, so Say finished and narrator got its audio
  interrupts (channels 0 and 1, 34 each);
- narrator programs period 161 (22 kHz);
- 0.34 s of speech at 18.2 s into boot, peak 0.43, the same on both sides.

Offline analysis of the WAV (`GOAMIGA_DIAG=1` writes
`/tmp/goamiga_say.wav`): the strongest autocorrelation peak of the vowel
is 136 Hz (r = 0.92). That is a plausible narrator pitch with stress on
"hel-LO" (default 110 Hz). A timing error would have shifted both pitch
and duration.

**Unit tests** (`paula_test.go`): DMA sample order and hold time,
interrupts at buffer start and repeat, new LC/LEN taking effect at the
next buffer, stopping; manual mode; mixed output rate, volume scaling
and left/right routing; volume modulation; exact next-interrupt
prediction.

**Speed.** Idle Workbench with display and mixing: 1,329 fps (26.6× real
time). Boot unchanged.

## 2026-09-25 — Disk swapping (F11)

**Usage.** `--disk` can be repeated. The first disk goes in at start, F11
swaps DF0: to the next (wrapping), Shift+F11 ejects. The window title shows
the disk in DF0:. Each listed file is checked at startup, and writes go to
whichever file is in the drive.

**Drive behaviour.** `DiskDrive.Eject` empties the drive and pulls
/DSKCHANGE low. It stays low until the head steps with a disk present,
which is how trackdisk detects changes: it polls by stepping the head.

**Why a swap isn't instant.** The first version ejected and inserted in
the same frame. AmigaDOS mounted the new volume but left the old one
marked as still in the drive (`dl_Task` set), because trackdisk never saw
the drive empty. A real swap always has a gap. `TestDiskSwapGapDiag`
(GOAMIGA_DIAG) measured 0.4–1.1 s from eject to DOS taking the volume out,
depending on where trackdisk's poll timer is. Rather than a fixed delay,
`DiskDrive.SwapDisk` ejects, waits for a head step with the drive empty
(trackdisk's poll), allows 0.5 s for that poll to finish, then inserts. If
nothing polls within 3 s (software that doesn't use trackdisk), it
inserts anyway. Fast-idle wakes for the pending insert. `Amiga.SwapDisk`
loads the file and keeps its path for saving writes.

**Tests.** `TestDiskSwap` boots Workbench and reads AmigaDOS's volume list
(dos.library → RootNode → DosInfo → DeviceList). Eject takes
`Workbench1.3` out of the drive. Inserting a copy renamed `SwapTest` (root
block name and checksum patched) mounts it. `SwapDisk` to `SwapTwo`
inserts after 38 frames, with `SwapTwo` in the drive and `SwapTest` out.
`TestSwapDiskTiming` covers the poll shortcut, the 3 s fallback, and
/DSKCHANGE staying low. Full suite passes.

## 2026-09-25 — Floppy writes

**Before.** The drive always reported the disk as write-protected and
ignored write DMA, so AmigaDOS refused to write.

**Now.**
- Write DMA (DSKLEN bit 14) starts at the head position without waiting
  for sync and takes one word time (227 CPU cycles) per word, then raises
  DSKBLK.
- `writeTrack` reads the DMA buffer from chip RAM and decodes every
  sector in it (`decodeMFMSectors`): a run of `$4489` syncs, then info
  (odd/even longs), label, header checksum, data checksum, and 512 bytes as
  128 odd longs then 128 even longs. A sector is committed only if its
  format byte is `$FF` and both checksums match. It goes on the physical
  track under the head. `BadSectorWrites` and `TrackMismatches` count
  anything else. Kickstart's trackdisk writes a whole track per DMA, but
  decoding by sync word handles any number of sectors.
- **Saving:** a disk loaded from a file (`Amiga.LoadDisk`, i.e. `--disk`)
  has each written track saved straight back into that file with
  `WriteAt`. `--write-protect` (`DiskDrive.WriteProtected`) reports the
  disk as protected (/DSKPROT low) and ignores the data; the DMA still
  completes, as on a real drive. Images inserted from memory
  (`InsertDisk`, which all tests use) are never saved anywhere.
- A plain Workbench boot and idle writes nothing (checked in
  `TestTimingProfile`), so an ADF changes only when something on the
  Amiga saves.

**Tests.** `disk_write_test.go`: encode→write round trip of a whole track,
a corrupt sector rejected, write protection (status bit and ignored data),
saving only the written track back to a file.
`TestAmigaDOSWritesFile` patches the first `echo` of the in-memory
`s/startup-sequence` to `echo >df0:written "goAmiga write test"` (fixing
the OFS block checksum), boots, and reads the file back from the image by
walking the root directory's hash chains. It checks every block's
checksum. Boot wrote 66 sectors (6 tracks), none bad. Full suite passes;
boot timing unchanged.

## 2026-09-25 — Overscan view (`--overscan`)

The framebuffer's view of the beam is now a per-`Video` setting instead of
constants (`left`, `top`, `rows`, `Width`; `SetOverscan`):

| View | Size | Beam area |
|---|---|---|
| Standard (default) | 640×256 | lores H `$81`–`$1C1`, lines 44–299 |
| Overscan (`--overscan`) | 720×284 | lores H `$6D`–`$1D5`, lines 28–311 |

Interlace doubles the rows in either. The host window follows the
framebuffer: `Video.DisplaySize` (full width, two host rows per line)
feeds Ebitengine's `Layout`, the initial window size, and the host-mouse
scaling, which used fixed 640×512 constants. The sprite and line buffers
are sized for the widest view (`MaxOutputWidth`).

In overscan, Workbench's standard screen sits 40 columns in and 16 rows
down, framed by the COLOR00 border (`TestOverscanScreenshot`, a
`GOAMIGA_DIAG` test, writes `/tmp/workbench_overscan.png`). The standard
view is still pixel-identical to before. `TestOverscanView` covers
geometry, placement of playfield and sprites, a display window opened over
the whole overscan area, and interlace. Full suite passes.

## 2026-09-25 — Mid-line register changes, interlace, collisions

**Mid-line changes.** Copper colour bars and splits change COLOR registers
partway along a line, but the renderer drew each line with the values at
its end. Now writes to COLOR00–31, BPLCON1 and BPLCON2 during a line are
recorded with their beam position (`Video.noteWrite`, called from
`CustomChips.Write`). The Copper passes its own slot position
(`CustomChips.copperHpos`), since in the per-line fast path the beam
tracker is still at the start of the line; CPU writes use the beam at the
start of the instruction. `drawLine` rewinds those writes to get the
line's starting values and replays each one from its column. A write at
colour clock c takes effect at lores H `2c + 1`, the same offset as
bitplane data (DDFSTRT $38 finishes its first fetch at colour clock $40
and shows its first pixel at H $81). The exact Denise latency is not
verified against hardware. A BPLCON1 change refills the playfield from its
column.

**Interlace.** When BPLCON0 LACE is set at VBLANK the framebuffer becomes
640×512 (`Video.laced`, `outputRow`). The long field (LOF=1) fills even
rows and the short field odd rows. Each field's rows stay until
overwritten, so the two weave. The host display already rescales to the
framebuffer size.

**Collisions.** `collisions()` computes CLXDAT bits per pixel from CLXCON
(ENBP/MVBP on odd planes = PF1 and even planes = PF2; even sprites always,
odd sprites with ENSP). CLXDAT reads return the accumulated bits with bit
15 set, then clear. `SpriteEngine.lineBuffer` now paints each sprite
separately so every sprite's presence per column is known.

**Speed.** The per-column loop now takes playfield indices from
`fillPlayfield`, which expands the fetched words once per line and skips
empty bytes. Register-derived values are recomputed only when a change
applies, and sprite-free collision bits come from a table cached per
CLXCON. Idle Workbench with display: 1,423 fps (28.5× real time); it was
1,490 before the compositor and 823 before this tuning.
`BenchmarkDrawLine` (dense data, worst case) is about 4–5 µs per line.

**Tests.** `TestMidLineColourChange` (exact column), `TestCopperColourSplit`
(a WAIT/MOVE Copper list through the beam), `TestInterlaceWeavesFields`,
`TestCollisionBits`, `TestCLXDATSpritePlayfield`. Workbench boot
screenshot still pixel-identical to the old renderer. Full suite passes.

## 2026-09-25 — Denise compositor: DIW/DDF positioning, scroll, DPF, EHB, HAM, sprite priority

**Problem.** `RenderLine` drew fetched bitplane words from the left edge of
a framebuffer whose width followed the line's resolution (320 or 640). It
ignored horizontal DIW and DDF placement, BPLCON1 scroll and BPLCON2
priorities, and had no EHB, HAM or dual playfield. Sprites were painted
over the finished RGBA line, always in front. Bitplane DMA ran on the fixed
lines 44–300, not the vertical display window.

**Design.**
- The framebuffer is a fixed view of the beam: 640 hires columns starting
  at lores beam position `OutputLeft = $81`, lines `DisplayTop`–
  `DisplayBottom` (44–300). That is exactly the standard PAL display window,
  so a standard screen looks as before (Workbench boot screenshot is
  pixel-identical). Lores pixels are two columns wide. A screen is placed
  inside the view by its own registers; anything beyond it is cropped
  (overscan output is future work).
- Per output column, `drawLine`:
  - is COLOR00 outside DIWSTRT/DIWSTOP (stop H8 = 1, V8 = NOT V7);
  - takes plane bits from the fetched words, with the first pixel at lores
    `2*DDFSTRT + 17` (lores) or `+ 9` (hires), the HRM relationship that
    puts the standard $38/$3C at DIWSTRT $81;
  - delays odd planes by BPLCON1 PF1H and even planes by PF2H, in lores
    pixels in both resolutions;
  - maps the colour through dual playfield (PF1 = odd planes → COLOR00–07,
    PF2 = even planes → COLOR08–15, PF2PRI), EHB (6 planes: bit 5 halves
    COLOR[0–31]) or HAM (5/6 lores planes; the hold register restarts at
    COLOR00 at the window's left edge);
  - puts a sprite pixel in front only if every non-transparent playfield at
    that pixel has a priority code (BPLCON2 PF1P/PF2P) above the sprite's
    pair. PF1 is the odd planes and PF2 the even planes in single playfield
    mode too. With Workbench's $24 all sprites are in front.
- `SpriteEngine.lineBuffer` builds the line's sprite pixels (palette index
  and pair). Lower-numbered sprites win. An odd sprite with ATTACH joins its
  even partner as a 15-colour sprite (COLOR16–31). Sprites are not drawn
  outside the display window (OCS has no border sprites).
- Bitplane DMA (fetch, pointer advance, and the chip bus slots in
  `dma.go`) now happens on lines inside the vertical display window
  (`Video.bitplaneFetchLine`).

**Unverified points.** The manual in the repo could not be read
(no PDF tools; its fonts have no Unicode mapping). Two details come from
memory of the HRM and other emulators, not the text: sprite priority in
single playfield mode using both PF1P (odd planes) and PF2P (even
planes); and scroll granularity of one lores pixel in hires.

**Speed.** Idle Workbench with the display on: 1,490 → 1,171 fps (23×
real time).

**Tests.** `denise_test.go`: DIW clipping on both edges, DDFSTRT
placement, hires pixel width, BPLCON1 per playfield, dual playfield both
priorities, EHB, a HAM sequence (set/blue/red/green/set), sprite vs
playfield priority codes, sprite number priority, attached sprites,
vertical window gating the fetch. Existing video, sprite and DMA tests were
updated to set a standard display window and to read lores pixels at
doubled columns. Full suite passes.

## 2026-09-25 — Floppy rotation timing; STOP/interrupt CPU bug

**Floppy timing.** Disk DMA copied from the track's first sync word and
completed at 2 CPU cycles per word, with DSKSYN and DSKBLK both at the end.
A DD drive spins at 300 RPM with 2 µs MFM bit cells: 6,250 words per
revolution, one word every 32 µs = exactly 227 CPU cycles (two per
scanline).

- The encoded MFM track is padded with `$AAAA` gap to 6,250 words
  (11 sectors fill 5,984).
- The head position is `(cycles / 227) mod 6250`; word 0 is the index.
- A transfer starts at the word under the head. With WORDSYNC it waits for
  the next sync word, and DSKSYN fires when that word passes the head.
  Reads wrap around the track. DSKBLK fires after the last word. A
  WORDSYNC read on a track with no sync word never starts, as on hardware
  (it used to "complete" after 64 cycles).
- The index pulse sets CIA-B /FLAG (ICR bit 4) once per revolution while
  the selected drive spins. Fast-idle wakes for sync, block-done and (when
  enabled) index events (`DiskDrive.CyclesToNextEvent`).

KS 1.3's trackdisk reads 7,358 words (about 1.2 revolutions) without
WORDSYNC and finds the sectors in the buffer, so each track read takes
~12 frames. Boot does 133 track reads of 61 distinct tracks.

**The 18-minute stall.** The first profile after this change booted in
1,124 emulated seconds. `TestDiskTimingDiag` (new, logs every disk DMA)
showed normal reads, then 53,624 idle frames between two of them with the
motor on. 53,624 × 313 lines ≈ 2²⁴: one wrap of CIA-B's 24-bit TOD counter,
which counts scanlines. A state dump in the gap showed:

- CIA-B: only the TOD ALARM interrupt enabled, alarm `$000000`, counter
  already past it. Nothing else could raise a level-6 interrupt.
- CPU: `cpuStopped` set, SR `$2300`, PC `$FC0D14`, the entry of the
  level-3 interrupt handler.

A CPU that is "stopped" with its PC at a handler entry and the mask raised
to that handler's level had already taken the interrupt. In go-m68k,
`Step` calls `CheckInterrupts` after every instruction, including STOP.
When STOP lowered the mask below an already-pending interrupt, the
interrupt was taken in that same step, but the run state stayed
`Stopped`. The Amiga then idled, waiting for a level above 3, while the
level-3 handler never ran. The next VERTB (level 3) could not wake it.
Only the TOD alarm wrapping around did.

On a 68000, any exception ends STOP. Fixed in go-m68k
(`raiseInterruptException` sets the run state to running), with
`TestStopWithPendingInterrupt`. This bug predates the floppy work. It
needs an interrupt to become pending in exactly the instructions before a
STOP, and the new disk timing made that happen in this boot.

**Result.** Boot to `workbench.task`: 1,013 → 2,584 frames (51.7 s
emulated, in line with a real A500 booting 1.3 from floppy); host time
unchanged at 1.7 s.

**Tests.** `disk_timing_test.go`: track length and revolution time,
DSKSYN/DSKBLK timing from a known head position, wrap-around read,
no-sync read never starts, index pulse only from a selected, spinning
drive. `TestDiskDMA` now ticks the actual completion time. Full suites
pass (goAmiga and go-m68k).

## 2026-09-25 — CIA address decoding

The CIAs were mapped as two 4 KB blocks answering at every address. On
the Amiga they share `$BF0000–$BFFFFF`, partially decoded: CIA-A is
selected by A12 low and drives the odd byte (D7–D0), CIA-B by A13 low
and drives the even byte (D15–D8), and A11–A8 pick the register. A new
`ciaSpace` device does this for the CPU's view. A byte lane neither chip
drives reads as open bus (0), writes to it are dropped, and a word access
at `$BFC000` reaches both chips. Emulator code still calls
`CIA.Read`/`Write` directly with any offset. `TestCIAAddressDecoding`.

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

**Still simplified.** There is no 2-cycle start delay on timers.
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
