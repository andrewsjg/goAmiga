package amiga

import (
	"fmt"
	"os"
	"testing"
)

// Trace post-bootblock: return code, strap→dos, alerts, IO errors, Open/LoadSeg.
func TestDiskPostBoot(t *testing.T) {
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	readStr := func(addr uint32, n int) string {
		if addr == 0 || addr >= 0x80000 {
			return ""
		}
		b := make([]byte, n)
		a.ChipRAM.Read(int(addr), b)
		for i, c := range b {
			if c == 0 {
				return string(b[:i])
			}
		}
		return string(b)
	}
	readStrAny := func(addr uint32, n int) string {
		if addr == 0 {
			return ""
		}
		// try chip then ROM via CPU memory
		b := make([]byte, n)
		for i := 0; i < n; i++ {
			v, err := a.CPU.M.Byte(int(addr) + i)
			if err != nil {
				return string(b[:i])
			}
			if v == 0 {
				return string(b[:i])
			}
			b[i] = byte(v)
		}
		return string(b)
	}

	type hit struct {
		name string
		n    int
	}
	counts := map[string]int{}
	logHit := func(name string, i int, extra string) {
		counts[name]++
		if counts[name] <= 12 {
			t.Logf("step=%d %s %s", i, name, extra)
		}
	}

	// Exec LVOs relative to A6=ExecBase when called via JSR -$xx(A6)
	// We intercept at known ROM stubs when possible.

	for i := 0; i < 12_000_000; i++ {
		pc := a.CPU.PC
		switch pc {
		case 0xFE85C6:
			logHit("bootblock_jsr", i, fmt.Sprintf("A4=$%08X target=$%08X A1=$%08X",
				a.CPU.A[4], a.CPU.A[4]+0x0C, a.CPU.A[1]))

		case 0xFE85CA: // after bootblock returns
			logHit("bootblock_ret", i, fmt.Sprintf("D0=$%08X", a.CPU.D[0]))

		case 0xFE85CE:
			logHit("bootblock_ALERT", i, fmt.Sprintf("D0=$%08X", a.CPU.D[0]))

		case 0xFE85F0:
			logHit("bootblock_OK", i, fmt.Sprintf("A0=$%08X", a.CPU.A[0]))

		case 0xFE86EC:
			logHit("strap_jmp_dos", i, fmt.Sprintf("A0=$%08X D0=$%08X", a.CPU.A[0], a.CPU.D[0]))

		case 0xFE8600:
			logHit("strap_next_dev", i, fmt.Sprintf("A2=$%08X", a.CPU.A[2]))

		case 0xFE867C:
			logHit("strap_io_err", i, fmt.Sprintf("err_byte=$%02X", a.CPU.D[0]&0xFF))

		case 0xFC3130:
			// only first few — Alert delay
			if counts["alert_delay"] < 3 {
				logHit("alert_delay", i, fmt.Sprintf("A6=$%08X D2=$%08X", a.CPU.A[6], a.CPU.D[2]))
			} else {
				counts["alert_delay"]++
			}

		case 0xFD423C:
			if counts["input_timeout"] < 2 {
				logHit("input_timeout", i, fmt.Sprintf("D3=$%08X D4=%d D6=$%08X A3=$%08X A5=$%08X",
					a.CPU.D[3], a.CPU.D[4], a.CPU.D[6], a.CPU.A[3], a.CPU.A[5]))
			} else {
				counts["input_timeout"]++
			}

		case 0xFEA652:
			d0 := a.CPU.D[0] & 0xFF
			if d0 >= 11 || counts["decode"] < 5 {
				logHit("decode", i, fmt.Sprintf("D0=$%02X cyl=%d side=%d", d0, a.Disk.cylinder, a.Disk.side))
			} else {
				counts["decode"]++
			}

		// OpenLibrary: common ROM entry via exec
		// Track when A1 points to a name string during OpenLibrary-ish calls
		case 0xFC1F4C: // often OpenLibrary in KS1.3 — verify via string
			// fallback: try to read A1 as name
			name := readStrAny(a.CPU.A[1], 32)
			if name != "" && counts["openlib"] < 20 {
				logHit("openlib?", i, fmt.Sprintf("A1=%q D0=$%08X", name, a.CPU.D[0]))
			}

		// Alert LVO path
		case 0xFC31B8, 0xFC3142:
			if counts["alert_body"] < 5 {
				logHit("alert_body", i, fmt.Sprintf("pc=$%08X D2=$%08X", pc, a.CPU.D[2]))
			} else {
				counts["alert_body"]++
			}
		}

		// Detect DoIO completions with errors: watch store to io_Error
		// Track DSKDMA count
		if a.Disk.completionCycles > 0 {
			// nop
		}

		if err := a.Step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		if i > 0 && i%2_000_000 == 0 {
			t.Logf("--- %dM PC=$%08X cyl=%d side=%d motor=%v counts=%v ---",
				i/1_000_000, pc, a.Disk.cylinder, a.Disk.side, a.Disk.motor, counts)
		}
	}

	// Final memory probes: bootblock buffer, potential dos base
	bb := make([]byte, 16)
	a.ChipRAM.Read(0x5C40, bb)
	t.Logf("bootblock@$5C40: %q %02X", bb[:4], bb)
	t.Logf("final counts=%v PC=$%08X", counts, a.CPU.PC)

	// Dump alert code at ExecBase+0x202 if present
	eb, _ := a.CPU.M.Long(4)
	if eb > 0 && eb < 0x80000 {
		alert, _ := a.CPU.M.Long(int(eb) + 0x202)
		t.Logf("ExecBase=$%08X LastAlert=$%08X", eb, alert)
	}
	_ = readStr
}
