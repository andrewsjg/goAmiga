# goAmiga

An Amiga 500 emulator in Go, built on the `go-m68k` 68000 core in `../go-m68k`.

Kickstart 1.3 boots Workbench 1.3 from an ADF image in a window.

## Running

You need a Kickstart ROM and, optionally, ADF disk images. Neither is
included. Kickstart 1.3 (256KB) runs Workbench 1.3; Kickstart 3.2 for the
A500/A600/A2000 (512KB) runs too, but wants Workbench 3.2 disks.

```bash
go run ./cmd/goamiga --rom ROMS/Kick13.rom --disk "disks/Workbench1.3.adf"
go run ./cmd/goamiga --rom ROMS/Kick32.rom --df0 disks/Install3.2.adf --df1 disks/Workbench3.2.adf
go run ./cmd/goamiga --rom ROMS/Kick13.rom --headless --cycles 10000000
```

Up to four drives: `--df0` (or `--disk`) to `--df3`, each repeatable to
give that drive a list of disks. The first goes in at start; F11 swaps DF0:
to the next, Alt+F11 DF1:, Ctrl+F11 DF2:, Ctrl+Alt+F11 DF3:, and holding
Shift ejects instead. A swap waits until the Amiga has seen the drive empty,
as with a real disk change. `--drives N` connects drives with no disk yet.
The window title shows what is in each drive.

```bash
go run ./cmd/goamiga --rom ROMS/Kick13.rom --df0 disks/wb13.adf --df1 disks/extras13.adf --df1 disks/blank.adf
```

The machine has 512 KB chip RAM plus 512 KB slow RAM by default, like an
A500 with a trapdoor expansion. `--slowram 0|512|1024|1536` and
`--fastram <KB>` (Zorro II, up to 8192) change that.

Sound plays through the host's audio output (`--mute` to turn it off).

The floppy is writable: anything the Amiga saves to DF0: is written back
into the ADF file. Keep a copy of disks you care about, or pass
`--write-protect`.

### Hard disks

`--hdf file.hdf` attaches a hard disk image as the next unit of
`goamiga.device`, the emulator's own autobooting controller; repeat it for
more. Images can be plain hardfiles (a single partition, as WinUAE creates
them, mounted as `DH0:`, `DH1:` ...) or whole disks with a Rigid Disk Block
(each partition mounted under its own name). With no bootable floppy in DF0:
the Amiga boots from the hard disk.

```bash
go run ./cmd/goamiga --rom ROMS/Kick32.rom --hdf dh0.hdf --hdf-create 100 --df0 disks/Install3.2.adf
```

`--hdf-create MB` creates any `--hdf` file that doesn't exist yet, blank.
Format it from the Amiga (for example `Format DRIVE DH0: NAME Work FFS
QUICK`), then install onto it. Writes go straight into the image file;
`--write-protect` covers hardfiles too. Partitions must use a filesystem the
Kickstart ROM has: OFS on 1.3, OFS or FFS on 2.0 and later. Filesystems
stored in an RDB are not loaded.

### Joystick

A joystick is always plugged into port 2 (the mouse stays in port 1). Any
gamepad drives it: D-pad or left stick, A (bottom button) to fire, B for
the second button. Ctrl+F12 (or `--joy-keys`) turns the arrow keys into the
joystick as well, with Right Ctrl or Right Alt/Option to fire and Right
Shift for the second button. While that's on, those keys don't reach the
Amiga keyboard.

### Display

The window shows the standard PAL display area (640×256, doubled
vertically). Add `--overscan` to see the full overscan area (720×284),
for software that draws into the border.

## Testing

Put the ROMs in `ROMS/` (`Kick13.rom`, optionally `Kick32.rom`) and the ADFs
in `disks/` (the Workbench 1.3 disks, optionally `Install3.2.adf`), then:

```bash
go test ./amiga/          # ~6 minutes; tests skip if the ROM is missing
```

## Documentation

- [docs/architecture.md](docs/architecture.md) — how the emulator is put together
- [docs/progress.md](docs/progress.md) — status, known gaps and change log
- [docs/debugging-window-drag-crash.md](docs/debugging-window-drag-crash.md),
  [docs/debugging-prefs-hang.md](docs/debugging-prefs-hang.md) — bug hunts
