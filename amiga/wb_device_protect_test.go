package amiga

import (
	"os"
	"testing"
)

func TestWBDeviceAndLibLVOLive(t *testing.T) {
	rom, err := LoadROM("../Kick13.rom")
	if err != nil {
		t.Skip(err)
	}
	a, _ := New(rom, nil)
	a.Blitter.GuardWrites = true
	data, err := os.ReadFile("../Workbench v1.3.3 rev 34.34-Disk 1 of 2.adf")
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
		_ = a.Step()
	}
	a.Video.DisplayActive = true
	for i := 0; i < 3_000_000; i++ {
		_ = a.Step()
	}
	for i := 0; i < 40; i++ {
		_ = a.RunFrame()
	}

	rd32 := func(addr int) uint32 {
		b := make([]byte, 4)
		if addr >= 0 && addr+3 < ChipRAMSize {
			a.ChipRAM.Read(addr, b)
		} else if a.CPU != nil {
			for i := 0; i < 4; i++ {
				v, _ := a.CPU.M.Byte(addr + i)
				b[i] = byte(v)
			}
		}
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	rd16 := func(addr int) int {
		return int(rd32(addr) >> 16)
	}
	// fix rd16
	rd16 = func(addr int) int {
		b := make([]byte, 2)
		a.ChipRAM.Read(addr, b)
		return int(b[0])<<8 | int(b[1])
	}

	eb := int(rd32(4))
	a.Blitter.lastSize = 1<<6 | 20
	a.Blitter.buildDeny()

	// DeviceList $15E
	head := rd32(eb + 0x15E)
	t.Logf("DeviceList head=$%08X", head)
	for n, node := 0, head; n < 20 && node != 0; n++ {
		succ := rd32(int(node))
		if succ == 0 {
			break
		}
		np := rd32(int(node) + 10)
		name := readCStrMem(a, np, 28)
		neg := rd16(int(node) + 0x10)
		pos := rd16(int(node) + 0x12)
		lvo := int(node) - neg
		okLVO := !a.Blitter.canWriteD(lvo+6, uint32(lvo))
		okBase := !a.Blitter.canWriteD(int(node)+0x20, uint32(lvo))
		t.Logf("dev @$%06X neg=$%04X pos=$%04X %q LVO@%06X deniedLVO=%v deniedBase=%v",
			node, neg, pos, name, lvo, okLVO, okBase)

		// Try smash LVO if not denied
		if !okLVO && lvo > 0x100 && neg > 0 && neg < 0x400 {
			snap := make([]byte, 12)
			a.ChipRAM.Read(lvo, snap)
			a.Blitter.con0 = 0x01FF
			a.Blitter.con1 = 0
			a.Blitter.adat = 0x0808
			a.Blitter.afwm = 0xFFFF
			a.Blitter.alwm = 0xFFFF
			a.Blitter.dptH, a.Blitter.dptL = 0, uint16(lvo)
			a.Blitter.dmod = 0
			a.Blitter.Execute(1<<6 | 6)
			after := make([]byte, 12)
			a.ChipRAM.Read(lvo, after)
			diff := 0
			for i := range snap {
				if snap[i] != after[i] {
					diff++
				}
			}
			if diff > 0 {
				t.Errorf("device %q LVO smashed %d bytes with pattern 0808", name, diff)
			}
		}
		node = succ
	}

	// Also re-check graphics LVO with 0x0808 fill (matches guru high bytes)
	libHead := rd32(eb + 0x17A)
	for n, node := 0, libHead; n < 20 && node != 0; n++ {
		succ := rd32(int(node))
		if succ == 0 {
			break
		}
		name := readCStrMem(a, rd32(int(node)+10), 28)
		if name == "graphics.library" || name == "intuition.library" {
			neg := rd16(int(node) + 0x10)
			lvo := int(node) - neg
			snap := make([]byte, neg)
			a.ChipRAM.Read(lvo, snap)
			a.Blitter.con0 = 0x01FF
			a.Blitter.adat = 0x0808
			a.Blitter.afwm, a.Blitter.alwm = 0xFFFF, 0xFFFF
			a.Blitter.dptH, a.Blitter.dptL = 0, uint16(lvo)
			a.Blitter.dmod = 0
			w := neg / 2
			if w > 63 {
				a.Blitter.Execute(uint16(((w+62)/63)<<6 | 63))
			} else {
				a.Blitter.Execute(uint16(1<<6 | w))
			}
			after := make([]byte, neg)
			a.ChipRAM.Read(lvo, after)
			diff := 0
			for i := range snap {
				if snap[i] != after[i] {
					diff++
				}
			}
			t.Logf("%s LVO smash attempt diff=%d (want 0)", name, diff)
			if diff > 0 {
				t.Errorf("%s LVO not protected", name)
			}
		}
		node = succ
	}

	// ThisTask / ExecBase
	tt := rd32(eb + 0x114)
	t.Logf("ThisTask=$%08X ExecBase=$%08X", tt, eb)
	if a.Blitter.canWriteD(eb+0x114, 0x2000) {
		t.Error("ThisTask must be denied")
	}
}
