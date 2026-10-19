package amiga

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// ROMInfo is what a Kickstart image says about itself: its version (from
// the ROM header, $0C) and, for the ROMs that only run on one machine, the
// machine. The A3000 and A4000 Kickstarts address hardware no other model
// has: both set up Gary's registers at $DE0000 at reset, and the A4000's
// drives its IDE interface at $DD2020. The others (A500/A600/A2000,
// A1200) run on a range of machines and aren't told apart.
type ROMInfo struct {
	Version, Revision int
	Machine           string // "a3000", "a4000", or "" for any
}

// IdentifyROM reads a Kickstart image's version and machine.
func IdentifyROM(data []byte) ROMInfo {
	var r ROMInfo
	if len(data) >= 16 {
		r.Version = int(binary.BigEndian.Uint16(data[12:]))
		r.Revision = int(binary.BigEndian.Uint16(data[14:]))
	}
	gary := bytes.Contains(data, []byte{0x00, 0xDE, 0x00, 0x00})
	switch {
	case gary && bytes.Contains(data, []byte{0x00, 0xDD, 0x20, 0x20}):
		r.Machine = "a4000"
	case gary:
		r.Machine = "a3000"
	}
	return r
}

// Release is the Kickstart release a version number belongs to.
func (r ROMInfo) Release() string {
	switch {
	case r.Version >= 47:
		return "3.2"
	case r.Version >= 45:
		return "3.1.4"
	case r.Version == 40:
		return "3.1"
	case r.Version == 39:
		return "3.0"
	case r.Version == 37:
		return "2.04"
	case r.Version == 36:
		return "2.0"
	case r.Version == 34:
		return "1.3"
	case r.Version == 33:
		return "1.2"
	}
	return ""
}

func (r ROMInfo) String() string {
	s := fmt.Sprintf("Kickstart %d.%d", r.Version, r.Revision)
	if rel := r.Release(); rel != "" {
		s = fmt.Sprintf("Kickstart %s (%d.%d)", rel, r.Version, r.Revision)
	}
	if r.Machine != "" {
		s += " for the " + map[string]string{"a3000": "A3000", "a4000": "A4000"}[r.Machine]
	}
	return s
}

// Mismatch says why a machine with this CPU and motherboard can't run the
// ROM, or "" if it can.
func (r ROMInfo) Mismatch(cpu, motherboard string) string {
	if r.Machine == "" {
		return ""
	}
	name := map[string]string{"a3000": "A3000", "a4000": "A4000"}[r.Machine]
	if motherboard != r.Machine || (cpu != "68030" && cpu != "68040") {
		return fmt.Sprintf("This is the %s Kickstart: it only runs on the %s (its motherboard and a 68030 or 68040)", name, name)
	}
	return ""
}
