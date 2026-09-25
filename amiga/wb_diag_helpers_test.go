package amiga

import (
	"testing"
)

// Shared helpers for the Workbench interaction regression tests.
//
// Coordinate note: IntuitionBase.MouseY is tracked at double (interlace)
// resolution, so screen Y must be doubled — see movePointerToScreen in
// wb_open_window_drag_test.go.

// findLibBase walks the Exec LibList for a named library.
func findLibBase(a *Amiga, name string) int {
	eb := int(ibRd32(a, 4))
	if eb < 0x100 {
		return 0
	}
	node := ibRd32(a, eb+0x17A)
	for n := 0; n < 64 && node != 0; n++ {
		ni := int(node)
		if ni < 0x100 || ni+0x20 > ChipRAMSize {
			return 0
		}
		np, _ := a.CPU.M.Long(ni + 0x0A)
		if readCStrMem(a, uint32(np), 24) == name {
			return ni
		}
		node = ibRd32(a, ni)
	}
	return 0
}

// bootToPrefsWindow boots Workbench, opens the Workbench1.3 disk drawer and
// then the Prefs drawer inside it, returning the machine, IntuitionBase and
// the Prefs window pointer. Icon positions come from the default WB1.3 layout.
func bootToPrefsWindow(t *testing.T) (*Amiga, int, int) {
	t.Helper()
	a := bootWB(t)
	ib := findIntuitionBase(a)
	if ib == 0 {
		t.Fatal("intuition.library not found")
	}
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
	dbl := func(sx, sy int) {
		if !movePointerToScreen(t, a, ib, sx, sy, run) {
			t.Fatalf("cannot reach (%d,%d)", sx, sy)
		}
		click()
		click()
	}
	waitWin := func(n int) []int {
		for i := 0; i < 500; i++ {
			run(5)
			if ws := windowList(a, ib); len(ws) >= n {
				return ws
			}
		}
		t.Fatalf("window %d never opened", n)
		return nil
	}
	// Wait for the drive to stop seeking (icons finished loading).
	settle := func() {
		s := 0
		for p := 0; p < 25 && s < 3; p++ {
			c0 := a.Disk.cylinder
			run(60)
			if a.Disk.cylinder == c0 {
				s++
			} else {
				s = 0
			}
		}
	}

	dbl(600, 52) // Workbench1.3 disk icon
	waitWin(2)
	settle()
	// The first click only activates the drawer window; the drawer needs a
	// separate double-click afterwards.
	if !movePointerToScreen(t, a, ib, 152, 118, run) {
		t.Fatal("cannot reach the Prefs drawer icon")
	}
	click()
	run(15)
	dbl(152, 118)
	ws := waitWin(3)
	settle()
	return a, ib, ws[0]
}
