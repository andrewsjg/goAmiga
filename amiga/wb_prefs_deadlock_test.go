package amiga

import (
	"testing"
)

// findTaskByName searches TaskReady + TaskWait + ThisTask for a named task.
func findTaskByName(a *Amiga, name string) int {
	eb := int(ibRd32(a, 4))
	if eb < 0x100 || eb >= ChipRAMSize {
		return 0
	}
	check := func(ni int) bool {
		if ni < 0x100 || ni+64 > ChipRAMSize {
			return false
		}
		namePtr, _ := a.CPU.M.Long(ni + 10)
		return readCStrMem(a, uint32(namePtr), 24) == name
	}
	if tt := int(ibRd32(a, eb+0x114)); check(tt) {
		return tt
	}
	for _, off := range []int{0x196, 0x1A4} {
		node := ibRd32(a, eb+off)
		for n := 0; n < 32 && node != 0; n++ {
			ni := int(node)
			succ := ibRd32(a, ni)
			if succ == 0 {
				break
			}
			if check(ni) {
				return ni
			}
			node = succ
		}
	}
	return 0
}

type taskState struct {
	addr     int
	state    byte
	sigWait  uint32
	sigRecvd uint32
}

func readTaskState(a *Amiga, ni int) taskState {
	return taskState{
		addr:     ni,
		state:    mustReadBytes(a, ni+15, 1)[0],
		sigWait:  ibRd32(a, ni+22),
		sigRecvd: ibRd32(a, ni+26),
	}
}

// TestWBPrefsClickDeadlock: after opening the Prefs drawer, a single click on
// the Preferences icon must not wedge the Workbench task. Reports whether the
// task ever runs again and whether the screen resumes updating.
func TestWBPrefsClickDeadlock(t *testing.T) {
	a, ib, pw := bootToPrefsWindow(t)
	run := func(n int) bool {
		for i := 0; i < n; i++ {
			if err := a.RunFrame(); err != nil {
				t.Fatalf("frame err: %v pc=$%08X", err, a.CPU.PC)
			}
		}
		return true
	}
	click := func() {
		a.Mouse.LeftButton = true
		run(2)
		a.Mouse.LeftButton = false
		run(3)
	}

	wb := findTaskByName(a, "Workbench")
	if wb == 0 {
		t.Fatal("Workbench task not found")
	}
	t.Logf("prefs window @$%06X, Workbench task @$%06X, state before: %+v",
		pw, wb, readTaskState(a, wb))

	// Click the Preferences program icon.
	if !movePointerToScreen(t, a, ib, 302, 132, run) {
		t.Fatal("cannot reach Preferences icon")
	}
	run(5)
	click()
	run(20)
	t.Logf("after click: %+v ActiveWindow=$%06X", readTaskState(a, wb), ibRd32(a, ib+0x34))

	// Sample the Workbench task state over ~10 seconds of emulated time.
	// An idle event-driven task legitimately sits in TS_WAIT, so what matters
	// is *which* signal it waits on: $80000000 is its normal IDCMP wait,
	// while $10 is the beam-synchronised blit completion that used to never
	// arrive (see CIA.TickTOD).
	const beamSyncSignal = 0x10
	running, waiting, stuckOnBeamSync := 0, 0, 0
	sigMasks := map[uint32]int{}
	for i := 0; i < 500; i++ {
		st := readTaskState(a, wb)
		if st.state == 4 {
			waiting++
			sigMasks[st.sigWait]++
			if st.sigWait == beamSyncSignal {
				stuckOnBeamSync++
			}
		} else {
			running++
		}
		run(1)
	}
	t.Logf("over 500 frames: running/ready=%d waiting=%d beamSyncWaits=%d",
		running, waiting, stuckOnBeamSync)
	for m, n := range sigMasks {
		t.Logf("  sigWait=$%08X for %d samples", m, n)
	}
	final := readTaskState(a, wb)
	t.Logf("final Workbench state: %+v", final)

	// Does the machine respond to further input? Click elsewhere and see if
	// anything on screen changes.
	regionSum := func() uint32 {
		var s uint32
		for _, b := range a.Video.Pixels {
			s = s*131 + uint32(b)
		}
		return s
	}
	if !movePointerToScreen(t, a, ib, 200, 170, run) {
		t.Fatal("cannot reach Pointer icon")
	}
	run(5)
	s0 := regionSum()
	click()
	run(20)
	s1 := regionSum()
	t.Logf("second click elsewhere: screen changed=%v ($%08X → $%08X)", s0 != s1, s0, s1)
	snap(t, a, "wb_diag_prefs_deadlock.png")

	if stuckOnBeamSync == 500 {
		t.Errorf("Workbench stayed blocked on the beam-synchronised blit signal ($%08X) "+
			"for every sample — the QBSBlit queue is not being drained", final.sigWait)
	}
	if final.sigWait == beamSyncSignal {
		t.Errorf("Workbench ended blocked on the beam-sync blit signal (sigWait=$%08X)", final.sigWait)
	}
	if s0 == s1 {
		t.Errorf("screen stopped updating after clicking the Preferences icon")
	}
}
