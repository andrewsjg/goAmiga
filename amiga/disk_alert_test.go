package amiga

import (
	"fmt"
	"os"
	"testing"
)

// Catch the Software Failure / Alert that stalls boot after startup-sequence.
func TestDiskAlertCause(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()

	// Track exception vectors 2-11
	type excEvt struct {
		step, vec int
		pc, ppc   uint32
		a7, d0    uint32
	}
	var excs []excEvt
	prevPC := uint32(0)

	// Alert entry FC3120 / LVO
	alerts := 0

	// Track task switches / PC in chip RAM (loaded programs)
	chipPCSamples := map[uint32]int{}

	for i := 0; i < 6_000_000; i++ {
		pc := a.CPU.PC

		// Exception: PC jumps to handler stored in vector
		if i > 2_000_000 {
			for v := 2; v <= 11; v++ {
				handler, _ := a.CPU.M.Long(v * 4)
				if handler != 0 && pc == uint32(handler) && prevPC != pc {
					// verify we came from outside
					if prevPC < uint32(handler) || prevPC > uint32(handler)+0x100 {
						excs = append(excs, excEvt{i, v, pc, prevPC, a.CPU.A[7], a.CPU.D[0]})
						if len(excs) <= 12 {
							// exception frame: SR, PC
							sr, _ := a.CPU.M.Word(int(a.CPU.A[7]))
							epc, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 2)
							t.Logf("step=%d EXC vec=%d handler=$%08X fromPC=$%08X frame SR=$%04X EPC=$%08X A7=$%08X",
								i, v, pc, prevPC, sr, epc, a.CPU.A[7])
						}
					}
				}
			}
		}

		switch pc {
		case 0xFC3120, 0xFC3118, 0xFC30F0, 0x060A:
			alerts++
			if alerts <= 15 {
				// Read LastAlert and stack
				eb := a.CPU.A[6]
				la, _ := a.CPU.M.Long(int(eb) + 0x202)
				t.Logf("step=%d ALERT pc=$%08X D7=$%08X D0=$%08X A5=$%08X A6=$%08X LastAlert=$%08X SP=$%08X",
					i, pc, a.CPU.D[7], a.CPU.D[0], a.CPU.A[5], a.CPU.A[6], la, a.CPU.A[7])
				// dump return address
				ra, _ := a.CPU.M.Long(int(a.CPU.A[7]))
				t.Logf("  ret=$%08X", ra)
			}

		case 0xFD423C:
			if alerts > 0 || i > 4_000_000 {
				// one-shot dump of guru text buffer near A5
				t.Logf("step=%d mouse_wait; dump $7FE00-$7FF00", i)
				b := make([]byte, 0x100)
				a.ChipRAM.Read(0x7FE00, b)
				// extract strings
				s := ""
				for _, c := range b {
					if c >= 0x20 && c <= 0x7e {
						s += string(c)
					} else if s != "" {
						if len(s) >= 6 {
							t.Logf("  text: %q", s)
						}
						s = ""
					}
				}
				// Also ExecBase LastAlert
				eb, _ := a.CPU.M.Long(4)
				la, _ := a.CPU.M.Long(int(eb) + 0x202)
				task, _ := a.CPU.M.Long(int(eb) + 0x114)
				t.Logf("  LastAlert=$%08X ThisTask=$%08X", la, task)
				// disassemble around $18BC8 if code
				t.Logf("  mem@$18BC0: %02X", mustReadBytes(a, 0x18BC0, 32))
				t.Logf("  mem@$19580: %02X", mustReadBytes(a, 0x19580, 32))
				goto done
			}

		case 0xFC323A: // "Guru Meditation" format string load site - nearby
			// skip
		}

		// Sample chip-RAM PCs after bootblock
		if i > 2_500_000 && pc < 0x80000 && pc > 0x1000 {
			if i%200 == 0 {
				chipPCSamples[pc]++
			}
		}

		prevPC = pc
		if err := a.Step(); err != nil {
			t.Logf("step error %d: %v PC=$%08X prev=$%08X", i, err, pc, prevPC)
			break
		}
	}
done:
	t.Logf("alerts=%d exceptions=%d", alerts, len(excs))
	// top chip PCs
	type kv struct {
		pc uint32
		n  int
	}
	var top []kv
	for pc, n := range chipPCSamples {
		top = append(top, kv{pc, n})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].n > top[i].n {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	t.Log("top chip-RAM PCs:")
	for i := 0; i < 20 && i < len(top); i++ {
		t.Logf("  $%08X x%d", top[i].pc, top[i].n)
	}
	_ = fmt.Sprintf
}

func mustReadBytes(a *Amiga, addr, n int) []byte {
	b := make([]byte, n)
	a.Mapper.Read(addr, b) // CPU view, so ROM addresses work too
	return b
}
