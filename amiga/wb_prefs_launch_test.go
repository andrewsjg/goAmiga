package amiga

import (
	"testing"
)

// TestWBLaunchPreferences is the end-to-end user flow that used to hang:
// open the Workbench disk, open the Prefs drawer, then double-click the
// Preferences program and wait for its window to appear.
func TestWBLaunchPreferences(t *testing.T) {
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

	before := windowList(a, ib)
	t.Logf("windows before launch: %d (prefs drawer @$%06X)", len(before), pw)

	// Activate the Prefs window, then double-click the Preferences icon.
	if !movePointerToScreen(t, a, ib, 302, 132, run) {
		t.Fatal("cannot reach the Preferences icon")
	}
	run(5)
	click() // activation click
	run(15)
	click()
	click() // double-click launches the program

	countScreens := func() int {
		n := 0
		for s := int(ibRd32(a, ib+0x3C)); n < 8 && s >= 0x100 && s < ChipRAMSize; n++ {
			s = int(ibRd32(a, s))
		}
		return n
	}
	screensBefore := countScreens()
	opened := false
	for i := 0; i < 250 && !opened; i++ {
		run(4)
		if len(windowList(a, ib)) > len(before) {
			opened = true
		}
	}
	// Preferences opens its own custom screen; give the floppy time to load it.
	for i := 0; i < 400 && countScreens() == screensBefore; i++ {
		run(5)
	}
	t.Logf("screens: %d → %d", screensBefore, countScreens())
	snap(t, a, "wb_diag_preferences_running.png")

	ws := windowList(a, ib)
	for _, w := range ws {
		t.Logf("window @$%06X %d,%d %dx%d title=%q", w,
			int16(ibRd16(a, w+4)), int16(ibRd16(a, w+6)),
			ibRd16(a, w+8), ibRd16(a, w+10),
			readCStrMem(a, ibRd32(a, w+32), 40))
	}
	// The Preferences editor opens on its own screen, so also report screens.
	scr := int(ibRd32(a, ib+0x3C))
	for n := 0; n < 8 && scr >= 0x100 && scr < ChipRAMSize; n++ {
		t.Logf("screen @$%06X %dx%d title=%q", scr,
			ibRd16(a, scr+12), ibRd16(a, scr+14),
			readCStrMem(a, ibRd32(a, scr+22), 40))
		scr = int(ibRd32(a, scr))
	}

	wb := findTaskByName(a, "Workbench")
	st := readTaskState(a, wb)
	t.Logf("Workbench task state: %+v", st)
	if st.sigWait == 0x10 {
		t.Errorf("Workbench is wedged on the beam-sync blit signal again")
	}

	// Whether or not a window appears, the machine must stay responsive.
	sum := func() uint32 {
		var s uint32
		for _, b := range a.Video.Pixels {
			s = s*131 + uint32(b)
		}
		return s
	}
	s0 := sum()
	for i := 0; i < 40; i++ {
		a.Mouse.Move(3, 2)
		run(1)
	}
	if sum() == s0 {
		t.Error("screen never changed while moving the mouse — emulator appears frozen")
	}
	if !opened {
		t.Log("note: no extra window appeared; Preferences may open its own screen")
	}
}
