package amiga

import (
	"os"
	"testing"
)

func TestWorkbenchIcons(t *testing.T) {
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
	a.Video.DisplayActive = true

	readName := func(addr uint32) string {
		if addr == 0 {
			return ""
		}
		b := make([]byte, 40)
		for i := 0; i < 40; i++ {
			v, e := a.CPU.M.Byte(int(addr) + i)
			if e != nil || v == 0 {
				return string(b[:i])
			}
			b[i] = byte(v)
		}
		return string(b)
	}

	wb := 0
	dmas, dmaAfterWB := 0, 0
	prevBusy := false
	openDev := map[string]int{}
	ioErrs := 0
	blitStarts := 0
	lastBlit := uint16(0)
	// track DoIO/WaitIO via LVO stubs
	doIO, waitIO := 0, 0
	// Capture DIW/DDF at end
	// Also look for ".info" string reads - hard; instead track file open via dos

	// dos packet/Open at common ROM
	for i := 0; i < 25_000_000; i++ {
		pc := a.CPU.PC
		if a.Disk.completionCycles > 0 && !prevBusy {
			dmas++
			if wb > 0 {
				dmaAfterWB++
				if dmaAfterWB <= 8 {
					t.Logf("step=%d DMA after WB #%d cyl=%d side=%d", i, dmaAfterWB, a.Disk.cylinder, a.Disk.side)
				}
			}
		}
		prevBusy = a.Disk.completionCycles > 0

		// blitter start
		if a.Blitter != nil {
			// count when BLTSIZE written? use completion
		}
		bs := a.Custom.ReadReg16(0x058)
		if bs != 0 && bs != lastBlit {
			blitStarts++
			lastBlit = bs
		}

		switch pc {
		case 0x055C:
			namePtr, _ := a.CPU.M.Long(int(a.CPU.A[1]) + 0x0A)
			name := readName(uint32(namePtr))
			if name == "workbench.task" {
				wb = i
				t.Logf("step=%d workbench.task", i)
			}
		case 0x04BA:
			name := readName(a.CPU.A[0])
			openDev[name]++
		case 0x04AE: // DoIO per some maps - verify
			doIO++
		case 0x049C:
			waitIO++
		case 0xFEA652: // decode result
			if wb > 0 {
				d0 := a.CPU.D[0] & 0xFF
				if d0 >= 11 || dmaAfterWB < 10 {
					t.Logf("step=%d decode D0=$%02X cyl=%d", i, d0, a.Disk.cylinder)
				}
			}
		case 0xFEA366: // trackread return
			if a.CPU.D[2] != 0 {
				ioErrs++
				if ioErrs <= 10 {
					t.Logf("step=%d td err D2=$%08X", i, a.CPU.D[2])
				}
			}
		}

		// sample chip strings for .info after WB
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
		if i > 0 && i%5_000_000 == 0 {
			t.Logf("--- %dM dmas=%d afterWB=%d blit~%d openDev_timer=%d doIO=%d waitIO=%d DIWSTRT=$%04X DIWSTOP=$%04X DDFSTRT=$%04X DDFSTOP=$%04X BPL1PT=$%08X SPR0PT=$%08X",
				i/1_000_000, dmas, dmaAfterWB, blitStarts, openDev["timer.device"], doIO, waitIO,
				a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
				a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094),
				uint32(a.Custom.ReadReg16(0x0E0))<<16|uint32(a.Custom.ReadReg16(0x0E2)),
				uint32(a.Custom.ReadReg16(0x120))<<16|uint32(a.Custom.ReadReg16(0x122)))
		}
	}

	// scan chip RAM for .info and disk icon names
	buf := make([]byte, 0x80000)
	a.ChipRAM.Read(0, buf)
	for _, s := range []string{"Workbench.info", "RAM.info", ".info", "disk.info", "Trashcan"} {
		// find
		b := []byte(s)
		count := 0
		for i := 0; i+len(b) < len(buf); i++ {
			ok := true
			for j := 0; j < len(b); j++ {
				if buf[i+j] != b[j] {
					ok = false
					break
				}
			}
			if ok {
				count++
				if count <= 2 {
					t.Logf("found %q at $%06X", s, i)
				}
			}
		}
		t.Logf("count %q = %d", s, count)
	}

	t.Logf("FINAL dmas=%d afterWB=%d ioErrs=%d openDev=%v DIWSTRT=$%04X DIWSTOP=$%04X DDF=$%04X/$%04X BPLCON0=$%04X",
		dmas, dmaAfterWB, ioErrs, openDev,
		a.Custom.ReadReg16(0x08E), a.Custom.ReadReg16(0x090),
		a.Custom.ReadReg16(0x092), a.Custom.ReadReg16(0x094),
		a.Custom.ReadReg16(0x100))
	// sprite 0
	t.Logf("SPR0PT=$%08X POS=$%04X CTL=$%04X",
		a.Sprites.sprites[0].pt, a.Sprites.sprites[0].pos, a.Sprites.sprites[0].ctl)
}
