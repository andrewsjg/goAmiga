package amiga

import (
	"testing"
)

// WaitBlit-style polling must clear BBUSY within a small number of CPU cycles
// even for large blits, or Workbench freezes during window drag.
func TestBlitterBusyClearsQuickly(t *testing.T) {
	a := &Amiga{ChipRAM: newChipRAM(ChipRAMSize)}
	a.Custom = NewCustomChips(nil, a)
	a.Blitter = NewBlitter(a)

	// Full-plane style blit: height 256, width 40 words, all channels
	a.Blitter.con0 = 0x0FF0 // USEA|USEB|USEC|USED, minterm copy
	a.Blitter.con1 = 0
	a.Blitter.afwm = 0xFFFF
	a.Blitter.alwm = 0xFFFF
	a.Blitter.Execute(256<<6 | 40) // h=256 w=40 → 10k words

	if !a.Blitter.busy {
		t.Fatal("expected busy after Execute")
	}
	if a.Blitter.completionCycles > 512 {
		t.Fatalf("completionCycles=%d, want <= 512 (capped)", a.Blitter.completionCycles)
	}
	// Uncapped would be 256*40*4*2 = 81920
	if a.Blitter.completionCycles >= 81920 {
		t.Fatalf("completionCycles not capped: %d", a.Blitter.completionCycles)
	}

	// Simulate WaitBlit: poll until free, 4 cycles per "instruction"
	steps := 0
	for a.Blitter.busy && steps < 5000 {
		a.Blitter.TickCycles(4)
		steps++
	}
	if a.Blitter.busy {
		t.Fatalf("still busy after %d poll steps", steps)
	}
	// 512/4 = 128 steps max
	if steps > 150 {
		t.Fatalf("took %d poll steps to clear busy, want <= 150", steps)
	}
	t.Logf("large blit WaitBlit-equivalent cleared in %d steps (%d cycles)", steps, steps*4)
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
