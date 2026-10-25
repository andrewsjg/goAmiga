package main

import (
	"fmt"
	"strings"

	"github.com/andrewsjg/goamiga/internal/config"
)

// presets set up the common models (the ROM stays as chosen).
type preset struct {
	name  string
	apply func(s *config.Settings)
}

var presets = []preset{
	{"A500", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ocs", 512, 512, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "off", false, "none", 0
	}},
	{"A500+", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ecs", 1024, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "off", false, "none", 0
	}},
	{"A600", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ecs", 1024, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	{"A1200", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "auto", "aga", 2048, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	{"A1200 + fast", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "68882", "aga", 2048, 0, 8192
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	// The A3000's processor and chipset: what Amiga Unix needs (its disk
	// goes on the A2091 at SCSI ID 6). The real machine's built-in SCSI
	// and 32-bit memory aren't emulated.
	{"A3000", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68030", 0, "68882", "ecs", 1024, 0, 8192
		s.Gayle, s.Motherboard, s.Z3RAM = "off", "none", 0
		s.Clock, s.ClockY2K = true, true // AMIX's date wants a year before 2000
	}},
	// The A4000/040: its motherboard (Ramsey, 16 MB of 32-bit RAM, the
	// IDE interface) and Zorro III. It needs the A4000 Kickstart.
	{"A4000", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68040", 0, "auto", "aga", 2048, 0, 0
		s.Gayle, s.Motherboard, s.MBRAM, s.Z3RAM = "auto", "a4000", 16384, 0
		s.Clock, s.ClockY2K = true, false
	}},
}

// freeSCSIID is the ID for a new SCSI disk: 6 (the A3000's internal
// drive, where Amiga Unix looks for its root) if free, else the lowest
// free one.
func freeSCSIID(disks []string) int {
	used := map[byte]bool{}
	for _, d := range disks {
		if len(d) > 1 {
			used[d[0]] = true
		}
	}
	if !used['6'] {
		return 6
	}
	for id := byte('0'); id <= '5'; id++ {
		if !used[id] {
			return int(id - '0')
		}
	}
	return 6
}

// applyChoice puts a chosen file or folder where target says: "rom",
// "df0".."df3", "hdf", "dir", "tape", "scsi", "ide0"/"ide1", "shots",
// "states", "whdload" or "whdloaddir".
func applyChoice(s *config.Settings, target, path string) {
	switch {
	case target == "rom":
		s.ROM = path
	case strings.HasPrefix(target, "df"):
		n := int(target[2] - '0')
		s.Drives[n] = config.List{path}
	case target == "hdf":
		s.Hardfiles = append(s.Hardfiles, path)
	case target == "dir":
		s.Dirs = append(s.Dirs, path)
	case target == "tape":
		s.Tape = "4:" + path
	case target == "scsi":
		s.SCSI = append(s.SCSI, fmt.Sprintf("%d:%s", freeSCSIID(s.SCSI), path))
	case strings.HasPrefix(target, "ide"):
		if i := int(target[3] - '0'); i < len(s.IDE) {
			s.IDE[i] = path
		} else {
			s.IDE = append(s.IDE, path)
		}
	case target == "shots":
		s.ScreenshotDir = path
	case target == "states":
		s.StateDir = path
	case target == "whdload", target == "whdloaddir":
		s.WHDLoad = path
	}
}

// presetMessage is the status line after choosing a model.
func presetMessage(name string) string {
	switch name {
	case "A3000":
		return "A3000 settings: for Amiga Unix, add its disk as a SCSI disk (ID 6) on the Hard disks tab"
	case "A4000":
		return "A4000 settings: choose the A4000 Kickstart ROM; IDE drives go on its own interface"
	}
	return name + " settings (choose a matching Kickstart ROM)"
}
