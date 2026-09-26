package amiga

import (
	"fmt"
	"os"
	"testing"
)

// TestKickstartDiag boots a Kickstart ROM (default ../Kick32.rom, or
// $GOAMIGA_KS) with no disk, logs CPU/chipset state and exec's last alert
// every 2 s, catches illegal-instruction exceptions, and saves the screen
// to /tmp/ks_<frame>.png. With $GOAMIGA_ADF it boots that disk
// (write-protected) for 60 s:
//
//	GOAMIGA_DIAG=1 go test -run TestKickstartDiag -v ./amiga/
func TestKickstartDiag(t *testing.T) {
	requireDiag(t)
	path := "../Kick32.rom"
	if p := os.Getenv("GOAMIGA_KS"); p != "" {
		path = p
	}
	rom, err := LoadROM(path)
	if err != nil {
		t.Skip(err)
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(512, 0)
	if adf := os.Getenv("GOAMIGA_ADF"); adf != "" {
		if err := a.LoadDisk(adf); err != nil {
			t.Fatal(err)
		}
		a.Disk.WriteProtected = true // never modify the image
	}
	a.Reset()
	a.Video.DisplayActive = true
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	// Catch illegal-instruction exceptions: execution arriving at the
	// vector 4 handler right after running another instruction.
	illegals := 0
	// Trace OpenLibrary (exec LVO -552): name, version, and what it returns.
	type pendingOpen struct {
		ret  uint32
		name string
		ver  uint32
	}
	var opens []pendingOpen
	traceOpen := os.Getenv("GOAMIGA_TRACE_OPEN") != ""
	step := func() error {
		prev := a.CPU.PC
		if traceOpen {
			if eb := rd(4); eb > 0x100 && eb < 0x1000000 {
				switch prev {
				case eb - 552: // OpenLibrary(A1 name, D0 version)
					opens = append(opens, pendingOpen{rd(a.CPU.A[7]), readCStrMem(a, a.CPU.A[1], 40), a.CPU.D[0]})
				case eb - 102: // InitResident(A1 romtag, D1 seglist)
					opens = append(opens, pendingOpen{rd(a.CPU.A[7]), "InitResident " + readCStrMem(a, rd(a.CPU.A[1]+14), 40), 0})
				}
			}
			for i := len(opens) - 1; i >= 0; i-- {
				if prev == opens[i].ret {
					t.Logf("OpenLibrary(%q, %d) from $%06X = $%08X", opens[i].name, opens[i].ver, opens[i].ret, a.CPU.D[0])
					opens = append(opens[:i], opens[i+1:]...)
					break
				}
			}
		}
		if err := a.Step(); err != nil {
			return err
		}
		if h := rd(0x10); a.CPU.PC == h && prev != h && illegals < 5 && rd(4) > 0x100 {
			illegals++
			op, _ := a.CPU.M.Word(int(prev))
			eb := rd(4)
			task := rd(eb + 276)
			name := readCStrMem(a, rd(task+10), 32)
			t.Logf("ILLEGAL at $%06X opcode $%04X (ROM offset $%05X) task $%06X %q",
				prev, op, prev-0xF80000, task, name)
		}
		return nil
	}
	frames := 600
	if os.Getenv("GOAMIGA_ADF") != "" {
		frames = 3000
	}
	for f := 0; f <= frames; f++ {
		start := a.cycles
		for a.cycles-start < CyclesPerFrame {
			if err := step(); err != nil {
				t.Fatalf("frame %d: %v", f, err)
			}
		}
		if f%100 == 0 && (frames <= 600 || f%500 == 0) {
			eb := rd(4)
			t.Logf("frame %d: PC=$%06X SR=$%04X stopped=%v ExecBase=$%06X BPLCON0=$%04X DMACON=$%04X COLOR00=$%03X busErrors=%d",
				f, a.CPU.PC, a.CPU.SR, a.cpuStopped, eb, a.Custom.ReadReg16(0x100),
				a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x180), a.busErrorCount)
			saveScreenshot(a.Video, fmt.Sprintf("/tmp/ks_%03d.png", f))
			for a.vpos != 150 {
				a.advanceBeamPerCycle(1)
			}
			c := a.Custom
			t.Logf("  line 150: BPLCON0=$%04X DDF=$%04X/$%04X BPL1MOD=%d BPL2MOD=%d DIW=$%04X/$%04X BPLCON1=$%04X",
				c.ReadReg16(0x100), c.ReadReg16(0x092), c.ReadReg16(0x094),
				int16(c.ReadReg16(0x108)), int16(c.ReadReg16(0x10A)),
				c.ReadReg16(0x08E), c.ReadReg16(0x090), c.ReadReg16(0x102))
			if eb >= 0x100 && eb < 0x1000000 {
				t.Logf("  LastAlert=$%08X $%08X  [$100]=$%08X [$104]=$%08X  D7=$%08X A5=$%08X",
					rd(eb+0x202), rd(eb+0x206), rd(0x100), rd(0x104), a.CPU.D[7], a.CPU.A[5])
			}
		}
	}
	for _, o := range opens {
		t.Logf("still waiting: OpenLibrary(%q, %d) from $%06X", o.name, o.ver, o.ret)
	}
	// exec's resident module table (ExecBase->ResModules, +$12C): a
	// NULL-terminated array of RomTag pointers (bit 31 set = link).
	if eb := rd(4); eb > 0x100 && eb < 0x1000000 {
		for p := rd(eb + 0x12C); p != 0; p += 4 {
			tag := rd(p)
			if tag == 0 {
				break
			}
			if tag&0x80000000 != 0 {
				p = tag&0x7FFFFFFF - 4
				continue
			}
			name := readCStrMem(a, rd(tag+14), 32)
			if name == "icon.library" || name == "workbench.library" || name == "ramlib" {
				flags, _ := a.CPU.M.Byte(int(tag + 10))
				ver, _ := a.CPU.M.Byte(int(tag + 11))
				typ, _ := a.CPU.M.Byte(int(tag + 12))
				pri, _ := a.CPU.M.Byte(int(tag + 13))
				t.Logf("resident %q at $%06X flags=$%02X ver=%d type=%d pri=%d", name, tag, flags, ver, typ, int8(pri))
			}
		}
	}
}
