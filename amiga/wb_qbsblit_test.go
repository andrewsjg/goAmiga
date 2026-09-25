package amiga

import (
	"testing"
)

// GfxBase field offsets used to inspect the blitter queues (V33).
const (
	gfxBlthd    = 58 // bltnode *blthd   — QBlit queue
	gfxBlttl    = 62
	gfxBsblthd  = 66 // bltnode *bsblthd — QBSBlit (beam synchronised) queue
	gfxBsblttl  = 70
	gfxBlitLock = 170
	gfxBlitNest = 172
)

// Clicking a Workbench icon makes graphics.library queue the icon's Bob via
// QBSBlit — a beam-synchronised blit started from a CIA-B TOD alarm, because
// CIA-B's TOD input is wired to the horizontal sync and so counts scanlines.
// If that alarm never fires the queue never drains and the calling task blocks
// forever holding the layer and IntuitionBase locks, freezing the whole UI.
func TestWBBeamSyncBlitQueueDrains(t *testing.T) {
	a, ib, _ := bootToPrefsWindow(t)
	run := func(n int) bool {
		for i := 0; i < n; i++ {
			if err := a.RunFrame(); err != nil {
				t.Fatalf("frame err: %v pc=$%08X", err, a.CPU.PC)
			}
		}
		return true
	}

	gfx := findLibBase(a, "graphics.library")
	if gfx == 0 {
		t.Fatal("graphics.library not found")
	}

	blits := 0
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) { blits++ }

	// Click the Preferences icon — this is what fills the queue.
	if !movePointerToScreen(t, a, ib, 302, 132, run) {
		t.Fatal("cannot reach the Preferences icon")
	}
	run(5)
	a.Mouse.LeftButton = true
	run(2)
	a.Mouse.LeftButton = false
	run(20)

	// Give the queue plenty of frames to drain.
	drained := false
	for i := 0; i < 120 && !drained; i++ {
		run(1)
		if ibRd32(a, gfx+gfxBsblthd) == 0 {
			drained = true
		}
	}
	head := ibRd32(a, gfx+gfxBsblthd)
	t.Logf("bsblthd=$%08X bsblttl=$%08X blits=%d BlitLock=%d BlitNest=%d",
		head, ibRd32(a, gfx+gfxBsblttl), blits,
		int16(ibRd16(a, gfx+gfxBlitLock)), int16(ibRd16(a, gfx+gfxBlitNest)))

	if !drained {
		t.Errorf("beam-synchronised blit queue never drained (bsblthd=$%08X); "+
			"check the CIA-B TOD alarm (CIA.TickTOD)", head)
	}

	wb := findTaskByName(a, "Workbench")
	if st := readTaskState(a, wb); st.sigWait == 0x10 {
		t.Errorf("Workbench blocked on the beam-sync blit signal: %+v", st)
	}
}
