# goAmiga

An Amiga emulator written in Go, from the 68000 up. It runs Kickstart and
Workbench, games and demos (copy-protected originals included), from the
A500 to the A1200. It has its own 680x0 CPU core, in [`m68k/`](m68k/).

![The configuration screen](docs/images/config-screen.png)

## What it emulates

**Machines:**
- 68000 or 68020 CPU, with an optional 68881/68882 FPU.
- OCS, ECS or AGA chipset, PAL or NTSC.
- 512 KB to 2 MB of chip RAM, plus trapdoor slow RAM and Zorro II fast
  RAM.
- Presets for the A500, A500+, A600 and A1200.
- Kickstart 1.3 to 3.2. Workbench 1.3 and 3.2 run from floppy or hard
  disk.

**Chipset:**
- Copper and blitter, with cycle-based DMA contention.
- Bitplanes up to AGA's 8 planes, HAM6 and HAM8, sprites.
- Paula's four sound channels with the A500's filters.
- Disk DMA, with bit-level reads for copy protection.
- CIAs, keyboard, mouse and joystick.

**Disks:**
- Four floppy drives, writable and swappable.
- ADF images, IPF (the Software Preservation Society's format, which keeps
  copy protection) and zipped or gzipped images.
- Hardfiles, plain or RDB, on an autobooting controller, or as IDE drives
  on the A600/A1200's Gayle, booted by Kickstart's own `scsi.device`.
  Filesystems stored in the RDB (PFS3, SFS, FFS for Kickstart 1.3) are
  loaded at boot.

**Connections:**
- The serial port over telnet or TCP.
- bsdsocket.library, so Amiga Internet software uses your network.

**Conveniences:**
- A configuration screen with saved configurations.
- Save states, warp mode, screenshots.
- Activity lights, and Power, Reset and Config buttons.

What isn't done yet is listed under "Known gaps" in
[docs/progress.md](docs/progress.md). The PCMCIA slot and an MMU are among
them.

## Building

You need:
- **Go 1.25 or later.**
- **A C compiler**, for the windowing and sound library
  ([Ebitengine](https://ebitengine.org)): the Xcode command-line tools on
  macOS.
- **On Linux:** the X11, OpenGL and ALSA development packages, plus
  `zenity` or `kdialog` for file dialogs.

```bash
git clone https://github.com/andrewsjg/goamiga
cd goamiga
go build ./cmd/goamiga
```

Or install it with `go install github.com/andrewsjg/goamiga/cmd/goamiga@latest`.

You also need a **Kickstart ROM**; none is included. Amiga Forever (Cloanto)
supplies the classic ROMs, and Hyperion sells Kickstart 3.2.
- Kickstart 1.3 (256 KB) runs Workbench 1.3.
- Kickstart 3.2 runs Workbench 3.2: the A500/A600/A2000 version, or the
  A1200 one with the AGA chipset.

## Quick start

Run it with no arguments:

```bash
./goamiga
```

It opens the configuration screen:
- **Saved configurations** are on the left. Click one to load it,
  double-click to start it, and use Save, New and Delete to manage them.
- **The settings** are in tabs on the right:
  - Machine: model presets, Kickstart ROM, CPU, FPU, chipset, PAL/NTSC.
  - Memory.
  - Floppies (DF0:–DF3:).
  - Hard disks.
  - Ports: serial, bsdsocket.library, the keyboard joystick.
  - Display and sound.

The settings use cycle gadgets: click to step through the choices, or
right-click to step back. Choose… opens a file dialog.

**Start** boots the machine. A problem, such as a missing ROM, shows at the
bottom of the screen instead.

goAmiga opens with the configuration you started last. While the Amiga
runs, the **Config** button (or Cmd+,) switches it off and returns here.

## Command line

Flags skip the configuration screen and boot straight away:

```bash
goamiga --rom ROMS/Kick13.rom --df0 "disks/Workbench1.3.adf"
goamiga --rom ROMS/Kick32.rom --df0 disks/Install3.2.adf --df1 disks/Workbench3.2.adf
goamiga --rom ROMS/kick32a1200.rom --chipset aga --cpu 68020 --hdf dh0.hdf
goamiga --config "My A1200"        # a configuration saved from the screen
goamiga --rom ROMS/Kick13.rom --headless --cycles 10000000
```

`goamiga --help` lists every flag. `--launcher` opens the configuration
screen even when other flags are given.

### Machine

- **CPU.** `--cpu 68020` fits a 68EC020, as in the A1200, with 24-bit
  addressing and no MMU. It runs 2.5× as fast as the A500's 7 MHz 68000;
  `--cpu-speed PERCENT` changes that. `--fpu 68881` or `--fpu 68882` adds
  a floating-point unit to the 68020. Kickstart and Workbench detect all
  of this themselves.
- **Chipset.** `--chipset` chooses:
  - `ocs`: the original A500.
  - `ecs-agnus`: the Fat Agnus with the original Denise, as in A500s with
    1 MB of chip RAM. This is the default with 1 MB; otherwise it's `ocs`.
  - `ecs`: adds the ECS Denise (A500+, A600), with the super-hires
    modes.
  - `aga`: the A1200/A4000 chipset. 256 colours from 16.8 million, 8
    bitplanes, HAM8, wide fetch modes and wide sprites; 2 MB of chip RAM by
    default. Use it with the A1200 Kickstart and the 68020.
- **Gayle.** `--gayle auto|on|off` fits Gayle, the A600/A1200 chip with
  the IDE interface. `auto`, the default, fits it with the AGA chipset, as
  in every A1200. It also matters with no drive attached: Kickstart's
  `scsi.device` then sets up as on the real machine, and at least one game
  (The Chaos Engine) depends on the memory layout that leaves. `--ide`
  fits it too (see Hard disks).
- **Video.** `--video ntsc` makes an American machine: 60 Hz, 263-line
  frames and 200-line Workbench screens. Some US games only display
  properly on NTSC, Bubble Bobble among them. The default is `pal`.
- **Memory.** The default is 1 MB of chip RAM and 2 MB of fast RAM: an
  A500 with the 8372A Fat Agnus and a side expansion, enough for the
  Workbench 3.2 installer.
  - `--chipram 512|1024|2048` sets the chip RAM.
  - `--slowram 0|512|1024|1536` adds trapdoor RAM at $C00000.
  - `--fastram KB` sets the fast RAM: 0, or a power of two up to 8192.

  For an original 512 KB A500, pass `--chipram 512 --fastram 0`. Old games
  often want exactly that: some crash under Kickstart 1.3 when fast RAM is
  fitted.

### Floppy disks

- **Drives.** `--df0` (or `--disk`) to `--df3` put disks in up to four
  drives. `--drives N` connects drives that have no disk yet.
- **Formats.** Disk images can be:
  - plain `.adf`;
  - `.ipf`, which keeps the copy protection, so original games boot;
  - zipped (`.zip` holding ADFs or IPFs, as game collections come);
  - gzipped (`.adz`, `.adf.gz`).

  See [docs/ipf-format.md](docs/ipf-format.md) for how IPF is decoded.
- **Disk lists.** Repeating `--dfN` gives that drive a list of disks: the
  first goes in, and **Cmd+F11** changes to the next. A zip holding several
  disks of one game works the same way, with the boot disk first.
  `"game.zip#Disk2.adf"` names one disk in a zip, e.g.
  `--df1 "game.zip#Disk2.adf"` to give a game its second disk in DF1:.
- **Changing disks.** While it runs, **F11** opens a file dialog to change
  disk. Shift+F11 ejects. The new disk goes in once the Amiga has seen the
  drive empty, as with a real disk change.

  | Keys | Drive |
  |---|---|
  | F11 | DF0: |
  | Alt+F11 | DF1: |
  | Ctrl+F11 | DF2: |
  | Ctrl+Alt+F11 | DF3: |

- **Writing.** Disks are writable: what the Amiga saves goes back into
  the ADF file. Keep copies of disks you care about, or pass
  `--write-protect`. Writes to compressed images stay in memory, and IPF
  images are read-only.

### Hard disks

`--hdf file.hdf` attaches a hard disk image as the next unit of
`goamiga.device`, the emulator's own autobooting controller. Repeat it for
more.
- **Plain hardfiles** (one partition, as WinUAE makes them) mount as
  `DH0:`, `DH1:` and so on.
- **Images with a Rigid Disk Block** mount each partition under its own
  name.

With no bootable floppy in DF0:, the Amiga boots from the hard disk.

```bash
goamiga --rom ROMS/Kick32.rom --hdf dh0.hdf --hdf-create 100 --df0 disks/Install3.2.adf
```

`--hdf-create MB` creates any `--hdf` file that doesn't exist yet, blank.
Format it from the Amiga (e.g. `Format DRIVE DH0: NAME Work FFS QUICK`),
then install onto it.

**IDE drives.** `--ide file.hdf` connects a hardfile as an IDE drive on
Gayle: the first is the master, a second the slave. Kickstart 3.x for the
A600/A1200 finds it with its own `scsi.device` and boots from it, as on the
real machine.
- HDToolBox and the Workbench installer see it as they would a real drive.
- The image needs a Rigid Disk Block, as on a real IDE disk. Plain
  hardfiles go on `goamiga.device` with `--hdf`.
- IDE drives are interchangeable with WinUAE's A600/A1200 IDE images.

```bash
goamiga --rom ROMS/kick32a1200.rom --chipset aga --cpu 68020 --ide a1200hd.hdf
```

Writes go straight into the image file; `--write-protect` covers hardfiles
and IDE drives too.

**Filesystems.** Kickstart has OFS (1.3), or OFS and FFS (2.0 and later).
A partition using anything else, such as PFS3, SFS, or FFS on 1.3, needs
the filesystem stored in the disk's RDB, as on a real Amiga. HDToolBox
puts it there ("Add/Update" in its filesystem list), and disks prepared
for WinUAE or a real machine often have it already. At boot the
filesystem is loaded from the RDB, unless Kickstart has the same one at
least as new (most RDBs carry an older FFS than Kickstart 3.x's). goAmiga
lists the filesystems it finds in an RDB when it starts. A plain hardfile
has no RDB, so it can only use Kickstart's own filesystems.

### Settings files

Instead of typing flags each time, put them in a settings file, one
`key = value` per line:
- the keys are the flag names, and `df0` or `hdf` may repeat;
- a bare key turns a switch on;
- relative paths are taken from the file's folder.

Copy `goamiga.conf.example` to start.

```
rom     = ROMS/Kick32.rom
hdf     = dh0.hdf
df0     = disks/Install3.2.adf
fastram = 8192
```

goAmiga reads `./goamiga.conf`, else `~/.config/goamiga/goamiga.conf`, or
the file given with `--config FILE`. Flags on the command line override
the file.

Configurations saved from the configuration screen are files of the same
kind, in `~/.config/goamiga/configs/`.

## While it runs

### The status bar

Under the picture are the activity lights:
- **POWER:** bright or dimmed, as the A500's LED is with the audio filter.
- **Each drive:** its motor LED and the head's cylinder.
- **HD** (goamiga.device) and **IDE**: green for reads, orange for
  writes.
- **The warp speed**, while warp is on.

At the right are three buttons. Click them with the mouse pointer free;
press Esc or F12 first if the Amiga has it.
- **Power** switches the Amiga off and on: a cold start. The disks stay
  in, as on a real machine.
- **Reset** is Ctrl+Amiga+Amiga.
- **Config** switches the Amiga off and returns to the configuration
  screen.

Power and Config ask for a second click, since they lose what's running.
`--statusbar=false` hides the bar; the keys below still work.

### Keys

Clicking in the window gives the mouse to the Amiga; Esc or F12 releases
it. Host commands use Cmd (the Windows key on a PC), which isn't an Amiga
key, so they never type anything on the Amiga.

| Keys | Action |
|---|---|
| Cmd+W | Warp: as fast as the host allows (about 20× real time), without sound; `--warp` starts in it |
| Cmd+R | Reset (Ctrl+Amiga+Amiga) |
| Cmd+Shift+R | Power off and on |
| Cmd+, | Switch off and go to the configuration screen |
| Cmd+P | Pause |
| Cmd+F | Fullscreen |
| Cmd+S | Screenshot as a PNG (`--screenshot-dir`) |
| Cmd+Shift+1…9 | Save state to slot 1–9 (`--state-dir`) |
| Cmd+1…9 | Load state from slot 1–9 |
| F11, Cmd+F11 | Change disk (see Floppy disks) |
| Ctrl+F12 | Arrow keys as the joystick, on or off |

A save state holds the whole machine, floppy contents included, and resumes
exactly. `--load-state FILE` starts from one.
- It needs the same Kickstart ROM, and the same hard disk images attached.
- Hard disk contents aren't in the state. Loading an old state after the
  hard disk has changed can confuse its filesystem.

### Joystick

A joystick is always plugged into port 2, with the mouse in port 1.
- **Gamepad:** any connected gamepad drives it. Use the D-pad or left stick
  to move, A (the bottom button) to fire, and B for the second button.
- **Keyboard:** Ctrl+F12 (or `--joy-keys`) makes the arrow keys the
  joystick, with Right Ctrl or Right Alt/Option to fire and Right Shift for
  the second button. While that's on, those keys don't reach the Amiga.

### Display and sound

- **Window size.** The window opens at twice the emulated screen's size;
  `--scale N` changes that. It's reduced if it wouldn't fit the monitor,
  and you can also resize it freely.
- **Overscan.** It shows the standard PAL area (640×256, doubled
  vertically). `--overscan` shows the full overscan area (720×284), for
  software that draws into the border.
- **Sound** plays through the host's audio output. `--mute` turns it off.

## Networking

`--bsdsocket` fits a network board carrying bsdsocket.library, the AmiTCP
socket interface, as UAE emulates it. Amiga Internet software then uses the
host's network directly, with no TCP/IP stack to install: telnet and FTP
clients, IRC, mail, browsers like AWeb and IBrowse, and servers.

Supported:
- TCP and UDP over IPv4;
- blocking and non-blocking sockets, and `WaitSelect`;
- name lookups through the host's resolver;
- the usual socket options;
- the netdb calls and `SocketBaseTagList`.

Limits:
- Servers bind on the host, so ports below 1024 need the host's
  permission.
- It works on macOS and Linux.

## Serial port

`--serial telnet:2323` connects the Amiga's serial port to TCP port 2323,
listening on this machine only. Connect with `telnet localhost 2323`, then
start a shell on the serial port from the Amiga:
- Workbench 1.3: `mount aux:` then `newshell aux:`.
- Workbench 2.0 and later: `newshell aux:`.

Terminal programs on the Amiga reach the telnet client the same way.
- `--serial tcp:2323` passes bytes through untouched, for file transfers
  or `nc`.
- `HOST:PORT` listens on another interface.

The line runs at whatever baud rate the Amiga sets. The Amiga sees carrier
while a client is connected.

## Testing

```bash
go test -tags noebitengine ./...   # about 8 minutes
```

`-tags noebitengine` leaves out the window, so the tests run anywhere,
including over SSH or with the screen locked.

Many tests boot real software. They skip when the files aren't there:
- **ROMs** in `ROMS/`: `Kick13.rom`, `Kick32.rom`, `kick32a1200.rom`.
- **Disks** in `disks/`: the two Workbench 1.3 disks, `Workbench3.2.adf`
  and `Install3.2.adf`.

The CPU's tests include the SingleStepTests 68000 suite, which needs its
data (about 190 MB) in `m68k/testdata/680x0/`. See
`m68k/singlestep_test.go` for where to get it.

`TestGameBatch` boots a list of games and writes contact sheets of
screenshots, for compatibility testing. Its comment explains the list
format.

## Documentation

- [docs/architecture.md](docs/architecture.md): how the emulator is put
  together.
- [docs/progress.md](docs/progress.md): status, known gaps, and the log of
  how it was built and debugged.
- [docs/ipf-format.md](docs/ipf-format.md): the IPF disk image format and
  how it's decoded.
- [m68k/README.md](m68k/README.md): the CPU.
- [docs/debugging-window-drag-crash.md](docs/debugging-window-drag-crash.md),
  [docs/debugging-prefs-hang.md](docs/debugging-prefs-hang.md): two of
  the bug hunts, written up.
- `diag/`: `adf.py` lists and extracts files from disk images;
  `disasm68k.py` disassembles ROMs and memory dumps.

## Licence

goAmiga is under the BSD 3-Clause licence (see [LICENSE](LICENSE)).

## Credits

The CPU core began as Ryan Armstrong's
[go-m68k](https://github.com/cavaliercoder/go-m68k) and keeps its own
BSD 3-Clause licence (see [m68k/LICENSE](m68k/LICENSE)). The CPU tests use Tom Harte's
[SingleStepTests](https://github.com/SingleStepTests/680x0).
