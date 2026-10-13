package amiga

import (
	"os"
	"testing"
)

func TestWBBltSizeWrites(t *testing.T) {
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
	for i := 0; i < 20_000_000; i++ {
		if a.CPU.PC == 0x055C {
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			if readCStrMem(a, uint32(namePtr), 24) == "workbench.task" {
				break
			}
		}
		a.Step()
	}
	a.Video.DisplayActive = true
	// Count BLTSIZE during remaining boot settle
	blts := 0
	execs := 0
	// Hook custom write by wrapping - count Execute via OnExecute
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		execs++
	}
	for i := 0; i < 3_000_000; i++ {
		a.Step()
	}
	t.Logf("execs during 3M steps: %d DMACON=$%04X", execs, a.Custom.ReadReg16(0x096))
	for i := 0; i < 20; i++ {
		a.RunFrame()
	}
	t.Logf("execs after frames: %d", execs)

	// Force a blit via writing BLTSIZE like software
	a.Custom.WriteReg16(0x096, 0x8240) // SET DMAEN|BLTEN
	a.Blitter.con0 = 0x01F0
	a.Blitter.con1 = 0
	a.Blitter.dptH = 0
	a.Blitter.dptL = 0x2000
	// Write BLTSIZE through custom
	a.Custom.Write(0x058, []byte{0x00, 0x42}) // h=1 w=2
	t.Logf("after forced BLTSIZE execs=%d busy=%v lastSize=$%04X", execs, a.Blitter.busy, a.Blitter.lastSize)
	_ = blts
}
