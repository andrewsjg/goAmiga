# goAmiga

An Amiga 500 emulator in Go, built on the `go-m68k` 68000 core in `../go-m68k`.

Kickstart 1.3 boots Workbench 1.3 from an ADF image in a window.

## Running

You need a Kickstart 1.3 ROM (256KB) and, optionally, an ADF disk image.
Neither is included.

```bash
go run ./cmd/goamiga --rom Kick13.rom --disk "Workbench1.3.adf"
go run ./cmd/goamiga --rom Kick13.rom --headless --cycles 10000000
```

The floppy is writable: anything the Amiga saves to DF0: is written back
into the ADF file. Keep a copy of disks you care about, or pass
`--write-protect`.

The window shows the standard PAL display area (640×256, doubled
vertically). Add `--overscan` to see the full overscan area (720×284),
for software that draws into the border.

## Testing

Put `Kick13.rom` and the Workbench 1.3 ADFs in this directory, then:

```bash
go test ./amiga/          # ~6 minutes; tests skip if the ROM is missing
```

## Documentation

- [docs/architecture.md](docs/architecture.md) — how the emulator is put together
- [docs/progress.md](docs/progress.md) — status, known gaps and change log
- [docs/debugging-window-drag-crash.md](docs/debugging-window-drag-crash.md),
  [docs/debugging-prefs-hang.md](docs/debugging-prefs-hang.md) — bug hunts
