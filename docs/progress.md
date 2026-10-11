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
  autobooting controller.
- The CPU passes the SingleStepTests 68000 suite with 68000 cycle timing,
  and can be a 68020 (`--cpu 68020`).

**Not yet implemented / known gaps** (roughly in planned order):

1. **Timing (remaining).** The CPU's bus cycles are counted per
   instruction rather than placed within it. The Copper's bus use is
   predicted from its list rather than taken cycle by cycle. IPF tracks'
   variable cell timing isn't modelled. The CPU's bus cycles are counted per
   instruction rather than placed within it.
2. **Display.** A resolution change (hires bit) partway through a line
   isn't shown, and neither are sprite data rewrites while a sprite is
   being shifted out. Collisions are only detected on lines that are drawn
   (not in headless mode).
3. **Paula (other).** The POTxDAT proportional
   counters (analogue joysticks, paddles) are not implemented. Audio DMA
   slots are not charged against the blitter.
4. **Machine.** 512 KB, 1 MB or (AGA) 2 MB chip RAM, optional slow and
   Zorro II fast RAM. OCS, ECS or AGA (`--chipset`). AGA lacks scan
   doubling, sprite resolutions beyond lores, and the A1200's Gayle
   (IDE/PCMCIA). On ECS, BEAMCON0 is stored
   but ignored (PAL or NTSC is chosen with `--video`; no programmable beam), productivity and
   scan-doubled modes aren't emulated, and super-hires is averaged into
   the hires-wide picture. Hardfiles use Kickstart's filesystem;
   filesystems stored in an RDB (PFS, SFS, FFS on 1.3) are not loaded.
5. **Safety nets.** CPU access violations are dropped rather than raised
   (`busErrorCount`); with the open-bus Mapper they should not occur.

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
