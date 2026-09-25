package amiga

import (
	"os"
	"testing"
)

func TestWBPortsAndMessagesDenied(t *testing.T) {
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
	for i := 0; i < 4_000_000; i++ {
		_ = a.Step()
	}
	for i := 0; i < 50; i++ {
		_ = a.RunFrame()
	}

	rd32 := func(addr int) uint32 {
		b := make([]byte, 4)
		a.ChipRAM.Read(addr, b)
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	eb := int(rd32(4))
	a.Blitter.lastSize = 256<<6 | 40
	a.Blitter.dmod = 0
	a.Blitter.buildDeny()

	// Drag a bit so IDCMP messages may be queued
	a.Mouse.LeftButton = true
	for i := 0; i < 30; i++ {
		a.Mouse.Move(2, 1)
		_ = a.RunFrame()
	}
	a.Blitter.buildDeny()

	head := rd32(eb + 0x188)
	ports, msgs, writable := 0, 0, 0
	for n, port := 0, head; n < 48 && port != 0; n++ {
		succ := rd32(int(port))
		if succ == 0 {
			break
		}
		ports++
		if a.Blitter.canWriteD(int(port)+8, uint32(port)&0xFFFE) {
			writable++
			t.Logf("writable PORT @$%06X", port)
		}
		msgHead := rd32(int(port) + 0x16)
		for m, msg := 0, msgHead; m < 32 && msg != 0; m++ {
			msucc := rd32(int(msg))
			if msucc == 0 {
				break
			}
			msgs++
			if a.Blitter.canWriteD(int(msg)+8, 0x20000) {
				writable++
				t.Logf("writable MSG @$%06X on port $%06X", msg, port)
			}
			msg = msucc
		}
		port = succ
	}
	t.Logf("ports=%d msgsQueued=%d stillWritable=%d denyRanges=%d", ports, msgs, writable, len(a.Blitter.denyLo))
	if writable > 0 {
		t.Errorf("%d port/message addresses still blittable", writable)
	}
}
