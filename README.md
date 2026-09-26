# goAmiga

An Amiga 500 emulator in Go, built on the `go-m68k` 68000 core in `../go-m68k`.

Kickstart 1.3 boots Workbench 1.3 from an ADF image in a window.

## Running

You need a Kickstart ROM and, optionally, ADF disk images. Neither is
included. Kickstart 1.3 (256KB) runs Workbench 1.3; Kickstart 3.2 for the
A500/A600/A2000 (512KB) runs too, but wants Workbench 3.2 disks.

```bash
go run ./cmd/goamiga --rom ROMS/Kick13.rom --disk "disks/Workbench1.3.adf"
go run ./cmd/goamiga --rom ROMS/Kick32.rom --disk disks/Install3.2.adf --disk disks/Workbench3.2.adf
go run ./cmd/goamiga --rom ROMS/Kick13.rom --headless --cycles 10000000
```

Give `--disk` more than once to swap disks while running: F11 puts the
next disk in DF0: (the Amiga sees the drive empty for about a second, as
with a real swap), Shift+F11 ejects. The window title shows what's in the
drive.

```bash
go run ./cmd/goamiga --rom ROMS/Kick13.rom --disk disks/wb13.adf --disk disks/extras13.adf --disk disks/blank.adf
```

The machine has 512 KB chip RAM plus 512 KB slow RAM by default, like an
A500 with a trapdoor expansion. `--slowram 0|512|1024|1536` and
`--fastram <KB>` (Zorro II, up to 8192) change that.

Sound plays through the host's audio output (`--mute` to turn it off).

The floppy is writable: anything the Amiga saves to DF0: is written back
into the ADF file. Keep a copy of disks you care about, or pass
`--write-protect`.

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
