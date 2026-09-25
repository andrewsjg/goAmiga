package amiga

import (
	"os"
	"testing"
)

// IntuitionBase past +$200 must be denied (semaphores / AN_SemCorrupt).
func TestWBIntuitionBaseFullyDenied(t *testing.T) {
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
		a.ChipRAM.Read(addr, b)
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	rd16 := func(addr int) int {
		b := make([]byte, 2)
		a.ChipRAM.Read(addr, b)
		return int(b[0])<<8 | int(b[1])
	}
	eb := int(rd32(4))
	head := rd32(eb + 0x17A)
	var ibase uint32
	var pos int
	for n, node := 0, head; n < 32 && node != 0; n++ {
		succ := rd32(int(node))
		if succ == 0 {
			break
		}
		if readCStrMem(a, rd32(int(node)+10), 28) == "intuition.library" {
			ibase = node
			pos = rd16(int(node) + 0x12)
			break
		}
		node = succ
	}
	if ibase == 0 {
		t.Fatal("no intuition.library")
	}
	a.Blitter.lastSize = 1<<6 | 40
	a.Blitter.buildDeny()

	// Probe deep into PosSize (old $200 cap left this open)
	for _, off := range []int{0x100, 0x200, 0x300, 0x400, pos - 4} {
		if off < 0 {
			continue
		}
		addr := int(ibase) + off
		if a.Blitter.canWriteD(addr, 0x20000) {
			t.Errorf("intuition.library +$%X ($%06X) still writable", off, addr)
		}
	}

	// Smash attempt with $0808 (SemCorrupt guru pattern)
	start := int(ibase) + 0x280
	snap := make([]byte, 64)
	a.ChipRAM.Read(start, snap)
	a.Blitter.con0 = 0x01FF
	a.Blitter.adat = 0x0808
	a.Blitter.afwm, a.Blitter.alwm = 0xFFFF, 0xFFFF
	a.Blitter.dptH, a.Blitter.dptL = 0, uint16(start)
	a.Blitter.dmod = 0
	a.Blitter.Execute(1<<6 | 32)
	after := make([]byte, 64)
	a.ChipRAM.Read(start, after)
	diff := 0
	for i := range snap {
		if snap[i] != after[i] {
			diff++
		}
	}
	t.Logf("IBase=$%06X pos=$%04X smash@$%06X diff=%d", ibase, pos, start, diff)
	if diff > 0 {
		t.Errorf("intuition data smashed (%d bytes)", diff)
	}
}
