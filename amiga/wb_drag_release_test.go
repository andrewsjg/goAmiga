package amiga

import (
	"encoding/binary"
	"os"
	"testing"
)

func bootWB(t *testing.T) *Amiga {
	t.Helper()
	rom, err := LoadROM("../ROMS/Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	data, err := os.ReadFile("../disks/Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
	if err != nil {
		t.Skip(err)
	}
	a.Disk.InsertDisk(data)
	a.Reset()
	for i := 0; i < 25_000_000; i++ {
		if a.CPU.PC == 0x055C {
			np, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(np), 24) == "workbench.task" {
				break
			}
		}
		if err := a.Step(); err != nil {
			t.Fatalf("boot: %v", err)
		}
	}
	a.Video.DisplayActive = true
	for i := 0; i < 4_000_000; i++ {
		_ = a.Step()
	}
	for i := 0; i < 50; i++ {
		_ = a.RunFrame()
	}
	return a
}

func freelistOK(a *Amiga) (ok bool, detail string) {
	rd32 := func(addr int) uint32 {
		if addr < 0 || addr+3 >= ChipRAMSize {
			return 0xFFFFFFFF
		}
		b := make([]byte, 4)
		a.ChipRAM.Read(addr, b)
		return binary.BigEndian.Uint32(b)
	}
	eb := int(rd32(4))
	if eb < 0x100 || eb >= ChipRAMSize-0x200 {
		return false, "bad ExecBase"
	}
	memList := eb + 0x142
	mh := rd32(memList)
	for n := 0; n < 8 && mh != 0; n++ {
		if int(mh) < 0x100 || int(mh)+4 >= ChipRAMSize {
			return false, "bad mh"
		}
		succ := rd32(int(mh))
		if succ == 0 {
			break
		}
		first := rd32(int(mh) + 0x10)
		lower := rd32(int(mh) + 0x14)
		upper := rd32(int(mh) + 0x18)
		for c, mc := 0, first; mc != 0 && c < 80; c++ {
			if mc == 0xFFFFFFFF || mc > 0x7FFFF {
				return false, "mc_Next poisoned"
			}
			mci := int(mc)
			if mci < int(lower) || mci+8 > int(upper) {
				return false, "mc out of bounds"
			}
			sz := rd32(mci + 4)
			if sz == 0 || sz > 0x80000 || sz&1 != 0 {
				return false, "bad mc_Bytes"
			}
			next := rd32(mci)
			if next == 0xFFFFFFFF {
				return false, "next=$FFFFFFFF"
			}
			mc = next
		}
		mh = succ
	}
	return true, "ok"
}

// Drag press/release cycles — the interactive pattern that triggers
// Software error - task held after freelist-header protection.
func TestWBDragReleaseStress(t *testing.T) {
	a := bootWB(t)

	rd32 := func(addr int) uint32 {
		b := make([]byte, 4)
		a.ChipRAM.Read(addr, b)
		return binary.BigEndian.Uint32(b)
	}
	eb := int(rd32(4))
	vec4 := rd32(4 * 4)
	vec2 := rd32(2 * 4)
	vec3 := rd32(3 * 4)

	overrun := 0
	a.Blitter.DebugWriteHook = func(addr int, val, aW, bW, cW uint16, mt byte) {
		start := int(a.Blitter.preDpt & 0x7FFFE)
		// Count attempted writes into freelist-ish band from plane starts
		if start >= 0xD000 && (addr < 0xD510 || addr >= 0x17510) {
			overrun++
		}
	}
	prevPC := a.CPU.PC
	faults := 0
	alerts := 0

	checkExc := func(label string) bool {
		pc := a.CPU.PC
		// task-held / alert strings path
		if pc >= 0xFF4834 && pc < 0xFF4900 {
			alerts++
			t.Logf("%s task-held ROM pc=$%08X D7=$%08X LastAlert=$%08X",
				label, pc, a.CPU.D[7], rd32(eb+0x202))
			return true
		}
		if pc == 0xFC3120 || pc == 0xFC3118 || pc == 0xFC3060 || pc == 0x060A {
			alerts++
			t.Logf("%s Alert pc=$%08X D7=$%08X", label, pc, a.CPU.D[7])
			return true
		}
		for _, h := range []struct {
			v    uint32
			name string
		}{
			{vec2, "BUS"}, {vec3, "ADDR"}, {vec4, "ILL"},
		} {
			if h.v != 0 && pc == h.v && prevPC != pc {
				// skip if from SuperState
				if h.name == "ILL" || prevPC < 0x80000 || (prevPC >= 0xFC0000 && prevPC < 0xFF0000 && prevPC != 0xFC08E6) {
					sr, _ := a.CPU.M.Word(int(a.CPU.A[7]))
					epc, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 2)
					tt := rd32(eb + 0x114)
					t.Logf("%s EXC %s from=$%08X epc=$%08X SR=$%04X ThisTask=$%08X(%q)",
						label, h.name, prevPC, epc, sr, tt, readCStrMem(a, rd32(int(tt)+10), 24))
					if epc < 0x80000 {
						t.Logf("  mem@epc: %02X", mustReadBytes(a, int(epc-4), 16))
					}
					faults++
					return true
				}
			}
		}
		return false
	}

	ok, det := freelistOK(a)
	t.Logf("freelist initial: %v %s", ok, det)

	// Sessions: home, move, click-drag, release, idle — like real use
	for session := 0; session < 30; session++ {
		// Home upper-left then to title-bar-ish area
		for i := 0; i < 8; i++ {
			a.Mouse.Move(-40, -30)
			_ = a.RunFrame()
		}
		a.Mouse.Move(120+session*3, 40)
		for i := 0; i < 5; i++ {
			_ = a.RunFrame()
		}
		// Press (start drag)
		a.Mouse.LeftButton = true
		for i := 0; i < 3; i++ {
			_ = a.RunFrame()
		}
		// Drag
		for i := 0; i < 40; i++ {
			a.Mouse.Move(2-((i/5)%4), (i%3)-1)
			// step with exception watch for a portion of the frame
			startC := a.cycles
			for int(a.cycles-startC) < CyclesPerFrame/2 {
				prevPC = a.CPU.PC
				if checkExc("drag") {
					goto done
				}
				if err := a.Step(); err != nil {
					t.Logf("Step err: %v pc=$%08X", err, a.CPU.PC)
					goto done
				}
			}
			_ = a.RunFrame()
		}
		// Release (window redraw — freelist stress)
		a.Mouse.LeftButton = false
		for i := 0; i < 30; i++ {
			startC := a.cycles
			for int(a.cycles-startC) < CyclesPerFrame {
				prevPC = a.CPU.PC
				if checkExc("release") {
					goto done
				}
				if err := a.Step(); err != nil {
					t.Logf("Step err on release: %v pc=$%08X", err, a.CPU.PC)
					// access violation style
					goto done
				}
			}
		}
		ok, det = freelistOK(a)
		if !ok {
			t.Logf("session %d freelist BAD: %s", session, det)
			goto done
		}
		if session%5 == 4 {
			t.Logf("session %d ok fl=%s overrunHints=%d pc=$%08X",
				session, det, overrun, a.CPU.PC)
		}
	}
done:
	ok, det = freelistOK(a)
	t.Logf("final freelist=%v (%s) faults=%d alerts=%d overrunHints=%d LastAlert=$%08X COLOR00=$%04X pc=$%08X",
		ok, det, faults, alerts, overrun, rd32(eb+0x202),
		a.Custom.ReadReg16(0x180), a.CPU.PC)
	if faults > 0 || alerts > 0 {
		t.Errorf("saw faults=%d alerts=%d during drag/release", faults, alerts)
	}
}
