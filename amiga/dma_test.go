package amiga

import "testing"

// dmaAmiga returns an Amiga with bitplane DMA on, the given BPLCON0 and
// DDF window, and the beam on display line 100.
func dmaAmiga(t *testing.T, bplcon0, ddfstrt, ddfstop uint16) *Amiga {
	t.Helper()
	a := createTestAmiga(t)
	a.wireMemoryMap(false)
	a.Custom.Write(0x096, []byte{0x83, 0x00}) // DMAEN + BPLEN
	a.Custom.WriteReg16(0x100, bplcon0)
	a.Custom.WriteReg16(0x092, ddfstrt)
	a.Custom.WriteReg16(0x094, ddfstop)
	a.vpos, a.hpos = 100, 0
	return a
}

func TestBitplaneDMASlots(t *testing.T) {
	cases := []struct {
		name             string
		bplcon0          uint16
		ddfstrt, ddfstop uint16
		wantStart        int
		wantEnd          int
		wantUsed         int // slots used in the window
		wantEven         int // even slots used
	}{
		{"hires 4 planes", 0xC200, 0x3C, 0xD4, 0x3C, 0xDC, 160, 80},
		{"hires 2 planes", 0xA200, 0x3C, 0xD4, 0x3C, 0xDC, 80, 0},
		{"lores 6 planes", 0x6200, 0x38, 0xD0, 0x38, 0xD8, 120, 40},
		{"lores 4 planes", 0x4200, 0x38, 0xD0, 0x38, 0xD8, 80, 0},
	}
	for _, c := range cases {
		a := dmaAmiga(t, c.bplcon0, c.ddfstrt, c.ddfstop)
		f := a.bitplaneDMA(a.vpos)
		if f.start != c.wantStart || f.end != c.wantEnd {
			t.Errorf("%s: window [$%X,$%X), want [$%X,$%X)", c.name, f.start, f.end, c.wantStart, c.wantEnd)
		}
		used, even := 0, 0
		for cck := 0; cck < ColorClocksPerLine; cck++ {
			if f.uses(cck) {
				used++
				if cck&1 == 0 {
					even++
				}
			}
		}
		if used != c.wantUsed || even != c.wantEven {
			t.Errorf("%s: %d slots (%d even), want %d (%d even)", c.name, used, even, c.wantUsed, c.wantEven)
		}
		if got := a.bitplaneDMA(20); got.planes != 0 {
			t.Errorf("%s: bitplane DMA active in vertical blank", c.name)
		}
	}
}

func TestChipBusWait(t *testing.T) {
	cck := func(c int) int { return c * CPUCyclesPerColorClock }

	// Hires 4 planes takes every even slot in the fetch window: an access
	// there waits for the window to end.
	a := dmaAmiga(t, 0xC200, 0x3C, 0xD4)
	a.hpos = cck(0x40)
	if got, want := a.chipBusWait(1), cck(0xDC-0x40); got != want {
		t.Errorf("hires 4 planes in window: wait %d, want %d", got, want)
	}
	a.hpos = cck(0x10)
	if got := a.chipBusWait(1); got != 0 {
		t.Errorf("hires 4 planes before window: wait %d, want 0", got)
	}
	a.vpos = 20
	a.hpos = cck(0x40)
	if got := a.chipBusWait(1); got != 0 {
		t.Errorf("hires 4 planes in vertical blank: wait %d, want 0", got)
	}

	// Hires 2 planes only uses odd slots.
	a = dmaAmiga(t, 0xA200, 0x3C, 0xD4)
	a.hpos = cck(0x40)
	if got := a.chipBusWait(8); got != 0 {
		t.Errorf("hires 2 planes: wait %d, want 0", got)
	}

	// Lores 6 planes takes every other even slot: 4 back-to-back accesses
	// from $38 get $38, $3C, $40, $44 — 3 extra slots of 2 colour clocks.
	a = dmaAmiga(t, 0x6200, 0x38, 0xD0)
	a.hpos = cck(0x38)
	if got, want := a.chipBusWait(4), cck(6); got != want {
		t.Errorf("lores 6 planes: wait %d, want %d", got, want)
	}
}

// Only the CPU's chip RAM and custom register accesses are counted; ROM
// accesses do not use the chip bus.
func TestChipBusCountsCPUAccesses(t *testing.T) {
	a := dmaAmiga(t, 0, 0, 0)
	buf := make([]byte, 4)
	a.cpuChipAccesses = 0
	a.CPU.M.Read(0x1000, buf)
	if a.cpuChipAccesses != 2 {
		t.Errorf("long chip RAM read: %d accesses, want 2", a.cpuChipAccesses)
	}
	a.CPU.M.Read(0xDFF006, buf[:2])
	if a.cpuChipAccesses != 3 {
		t.Errorf("after custom read: %d accesses, want 3", a.cpuChipAccesses)
	}
	a.CPU.M.Read(0xFC0000, buf)
	if a.cpuChipAccesses != 3 {
		t.Errorf("ROM read counted as chip access: %d", a.cpuChipAccesses)
	}
}

// The blitter only gets slots bitplane and fixed DMA leave free.
func TestBlitterSlots(t *testing.T) {
	a := dmaAmiga(t, 0xC200, 0x3C, 0xD4)
	// 227 - 160 bitplane - 4 refresh
	if got := a.blitterSlots(CPUClocksPerLine); got != 63 {
		t.Errorf("hires 4 planes: %d blitter slots per line, want 63", got)
	}
	a.Custom.Write(0x096, []byte{0x80, 0x20}) // + SPREN
	if got := a.blitterSlots(CPUClocksPerLine); got != 63-16 {
		t.Errorf("hires 4 planes + sprites: %d blitter slots, want %d", got, 63-16)
	}
	a.vpos = 20
	if got := a.blitterSlots(CPUClocksPerLine); got != 227-4-16 {
		t.Errorf("vertical blank: %d blitter slots, want %d", got, 227-4-16)
	}
}

func TestBlitterBusContention(t *testing.T) {
	a := dmaAmiga(t, 0, 0, 0)
	a.Custom.Write(0x096, []byte{0x82, 0x40}) // DMAEN + BLTEN
	a.Blitter.con0 = 0x0FF0                   // ABCD: no idle cycles
	a.Blitter.Execute(10<<6 | 10)
	remaining := a.Blitter.completionCycles

	// Blitter hogs the bus: each CPU access waits 3 colour clocks and
	// costs the blitter one.
	if got := a.chipBusWait(2); got != 2*3*CPUCyclesPerColorClock {
		t.Errorf("ABCD blit: wait %d, want %d", got, 2*3*CPUCyclesPerColorClock)
	}
	if a.Blitter.completionCycles != remaining+2*CPUCyclesPerColorClock {
		t.Errorf("blit not extended by yielded slots: %d, was %d", a.Blitter.completionCycles, remaining)
	}

	// A D-only blit has idle cycles the CPU can use.
	a.Blitter.con0 = 0x0100
	a.Blitter.Execute(10<<6 | 10)
	if got := a.chipBusWait(2); got != 0 {
		t.Errorf("D-only blit: wait %d, want 0", got)
	}

	// Nasty mode: the first chip access waits for the blit; none if the
	// instruction stays off the chip bus.
	a.Custom.Write(0x096, []byte{0x84, 0x00}) // + BLTPRI
	if got := a.chipBusWait(1); got != a.Blitter.completionCycles {
		t.Errorf("nasty blit: wait %d, want %d", got, a.Blitter.completionCycles)
	}
	if got := a.chipBusWait(0); got != 0 {
		t.Errorf("nasty blit, no chip access: wait %d, want 0", got)
	}
}
