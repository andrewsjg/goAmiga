package amiga

import (
	"testing"
)

// BBUSY lasts as long as the real blit: HRM cycles per word by channel
// combination, in colour clocks (2 CPU cycles each), plus pipeline startup.
func TestBlitterBusyDuration(t *testing.T) {
	cases := []struct {
		name       string
		con0, con1 uint16
		size       uint16
		want       int // CPU cycles
	}{
		{"ABCD 40x256", 0x0FF0, 0, 256<<6 | 40, (256*40*4 + 2) * 2},
		{"D-only clear 40x256", 0x0100, 0, 256<<6 | 40, (256*40*2 + 2) * 2},
		{"ACD 10x10", 0x0BCA, 0, 10<<6 | 10, (100*3 + 2) * 2},
		{"line 100px", 0x0BCA, 0x0001, 100<<6 | 2, (100*4 + 2) * 2},
	}
	for _, c := range cases {
		a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
		a.Custom = NewCustomChips(nil, a)
		a.Blitter = NewBlitter(a)
		a.Blitter.con0, a.Blitter.con1 = c.con0, c.con1
		a.Blitter.afwm, a.Blitter.alwm = 0xFFFF, 0xFFFF
		a.Blitter.Execute(c.size)

		cycles := 0
		for a.Blitter.busy && cycles < 1<<20 {
			a.Blitter.TickCycles(4)
			cycles += 4
		}
		if cycles < c.want || cycles >= c.want+4 {
			t.Errorf("%s: BBUSY cleared after %d cycles, want %d", c.name, cycles, c.want)
		}
		if a.Custom.ReadReg16(0x09C)&0x0040 == 0 {
			t.Errorf("%s: no BLIT INTREQ at completion", c.name)
		}
	}
}

// Superseding a busy blit must not drop QBlit completions, and must not
// raise BLIT INTREQ re-entrantly inside Execute (that caused Guru A1000001).
func TestBlitterSupersedeDefersBlitIRQ(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)
	buf := []byte{0x7F, 0xFF}
	a.Custom.Write(0x09C, buf)

	a.Blitter.con0 = 0x01F0
	a.Blitter.con1 = 0
	a.Blitter.adat = 0
	a.Blitter.Execute(1<<6 | 1)
	if !a.Blitter.busy {
		t.Fatal("expected busy after first Execute")
	}
	// Second BLTSIZE before timer — queues IRQ for first, starts second
	a.Blitter.Execute(1<<6 | 1)
	if a.Custom.ReadReg16(0x09C)&0x0040 != 0 {
		t.Fatal("must not raise BLIT INTREQ inside Execute (re-entrancy)")
	}
	if a.Blitter.pendingIRQs < 1 {
		t.Fatal("supersede must queue pendingIRQs for the previous blit")
	}
	if !a.Blitter.busy {
		t.Fatal("second blit should be busy")
	}
	// Drain second blit; should deliver IRQs only when idle
	for a.Blitter.busy {
		a.Blitter.TickCycles(64)
	}
	// Deliver queued IRQs one per TickCycles while idle
	for i := 0; i < 8 && a.Blitter.pendingIRQs > 0; i++ {
		a.Blitter.TickCycles(4)
	}
	if a.Custom.ReadReg16(0x09C)&0x0040 == 0 {
		t.Fatal("expected BLIT INTREQ after idle delivery")
	}
	if a.Blitter.pendingIRQs != 0 {
		t.Fatalf("pendingIRQs=%d want 0 after delivery", a.Blitter.pendingIRQs)
	}
}

// A CPU sitting in STOP waiting for the BLIT interrupt must not fast-idle
// past the blit's completion (to the next VBLANK or CIA event).
func TestStoppedCPUWakesForBlit(t *testing.T) {
	a := createTestAmiga(t)
	a.vpos, a.hpos = 100, 0
	a.cpuStopped = true
	a.Custom.Write(0x096, []byte{0x82, 0x40}) // DMAEN + BLTEN
	a.Blitter.con0 = 0x0100                   // D only
	a.Blitter.Execute(10<<6 | 10)
	want := a.Blitter.completionCycles

	// Fast-idle skips to the earliest the blit could end; refresh slots
	// can push it a few cycles later, needing another short idle step.
	start := a.cycles
	for i := 0; i < 10 && a.Blitter.busy; i++ {
		if err := a.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if got := int(a.cycles - start); got > want+CPUClocksPerLine {
		t.Fatalf("fast-idle advanced %d cycles, blit ends after ~%d", got, want)
	}
	if a.Blitter.busy || a.Custom.ReadReg16(0x09C)&0x0040 == 0 {
		t.Fatal("blit not complete / BLIT INTREQ not raised after idle step")
	}
}

// An area blit moves its words as it gets bus slots: halfway through its
// time, about half the destination is written and DPT has advanced that
// far; the rest is written by the time BBUSY clears.
func TestBlitIsIncremental(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	a.Custom.Write(0x096, []byte{0x82, 0x40}) // DMAEN + BLTEN
	b := a.Blitter
	b.con0 = 0x01FF // D only, minterm $FF: all ones
	b.dptH, b.dptL = 0, 0x4000
	b.Execute(10<<6 | 10) // 100 words
	total := b.completionCycles
	written := func() int {
		n := 0
		for i := 0; i < 100; i++ {
			if a.peek16(0x4000+uint32(2*i)) == 0xFFFF {
				n++
			}
		}
		return n
	}
	if n := written(); n != 0 {
		t.Errorf("%d words written at the BLTSIZE write, want 0", n)
	}
	b.TickCycles(total / 2)
	n := written()
	if n < 40 || n > 55 {
		t.Errorf("%d of 100 words written halfway through, want ~50", n)
	}
	if dpt := b.ptr(b.dptH, b.dptL); dpt != 0x4000+uint32(2*n) {
		t.Errorf("DPT = $%06X mid-blit, want $%06X", dpt, 0x4000+2*n)
	}
	for b.busy {
		b.TickCycles(CPUClocksPerLine)
	}
	if n := written(); n != 100 {
		t.Errorf("%d words written after the blit, want 100", n)
	}
	var buf [2]byte
	a.Custom.Read(0x002, buf[:])
	if buf[0]&0x20 != 0 {
		t.Error("BZERO set after a blit that produced ones")
	}
}

// The blitter reads each source word when it gets to it: the CPU changing
// source data ahead of a running blit changes what is copied.
func TestBlitReadsSourceWhenReached(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	a.Custom.Write(0x096, []byte{0x82, 0x40})
	b := a.Blitter
	b.con0 = 0x09F0 // A -> D, minterm A
	b.aptH, b.aptL = 0, 0x5000
	b.dptH, b.dptL = 0, 0x6000
	b.Execute(1<<6 | 40)
	b.TickCycles(b.completionCycles / 4)
	a.poke16(0x5000+2*39, 0x1234) // last source word, not yet read
	for b.busy {
		b.TickCycles(CPUClocksPerLine)
	}
	if got := a.peek16(0x6000 + 2*39); got != 0x1234 {
		t.Errorf("last word copied $%04X, want the CPU's later $1234", got)
	}
	var buf [2]byte
	a.Custom.Read(0x002, buf[:])
	if buf[0]&0x20 != 0 {
		t.Error("BZERO set though a word was non-zero")
	}
}

func TestBZERO(t *testing.T) {
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	b := a.Blitter
	b.con0 = 0x0100 // D only, minterm 0
	b.dptH, b.dptL = 0, 0x4000
	blitNow(a, 1<<6|4)
	var buf [2]byte
	a.Custom.Read(0x002, buf[:])
	if buf[0]&0x20 == 0 {
		t.Error("BZERO clear after an all-zero blit")
	}
}
