package amiga

import (
	"fmt"
	"testing"
)

// Multi-directional window drags (esp. upward — descending-blit overlap path)
// after the descending-modulo fix. Watches for exceptions, alerts, freelist
// corruption, and pixel garbage in uncovered regions.
func TestWBMultiDragStress(t *testing.T) {
	a := bootWB(t)
	ib := findIntuitionBase(a)
	if ib == 0 {
		t.Fatal("no intuition")
	}
	eb := int(ibRd32(a, 4))
	vec2, vec3, vec4 := ibRd32(a, 8), ibRd32(a, 12), ibRd32(a, 16)

	prevPC := a.CPU.PC
	crashed := ""
	checkExc := func() bool {
		pc := a.CPU.PC
		if pc >= 0xFF4834 && pc < 0xFF4900 {
			crashed = fmt.Sprintf("task-held pc=$%08X D7=$%08X", pc, a.CPU.D[7])
			return true
		}
		if pc == 0xFC3120 || pc == 0xFC3118 || pc == 0xFC3060 || pc == 0x060A {
			crashed = fmt.Sprintf("Alert pc=$%08X D7=$%08X", pc, a.CPU.D[7])
			return true
		}
		for _, h := range []struct {
			v    uint32
			name string
		}{{vec2, "BUS"}, {vec3, "ADDR"}, {vec4, "ILL"}} {
			if h.v != 0 && pc == h.v && prevPC != pc {
				if h.name == "ILL" || prevPC < 0x80000 || (prevPC >= 0xFC0000 && prevPC < 0xFF0000 && prevPC != 0xFC08E6) {
					epc, _ := a.CPU.M.Long(int(a.CPU.A[7]) + 2)
					crashed = fmt.Sprintf("EXC %s from=$%08X epc=$%08X", h.name, prevPC, epc)
					return true
				}
			}
		}
		return false
	}
	runFrames := func(n int) bool {
		for f := 0; f < n; f++ {
			startC := a.cycles
			for int(a.cycles-startC) < CyclesPerFrame {
				prevPC = a.CPU.PC
				if checkExc() {
					return false
				}
				if err := a.Step(); err != nil {
					crashed = fmt.Sprintf("Step err: %v pc=$%08X", err, a.CPU.PC)
					return false
				}
			}
		}
		return true
	}

	// Open the RAM DISK window.
	if !movePointerToScreen(t, a, ib, 600, 22, runFrames) {
		t.Fatalf("pointer positioning failed: %s", crashed)
	}
	click := func() bool {
		a.Mouse.LeftButton = true
		if !runFrames(2) {
			return false
		}
		a.Mouse.LeftButton = false
		return runFrames(3)
	}
	before := len(windowList(a, ib))
	if !click() {
		t.Fatalf("click crash: %s", crashed)
	}
	if !click() {
		t.Fatalf("click crash: %s", crashed)
	}
	opened := false
	for i := 0; i < 100 && !opened; i++ {
		if !runFrames(5) {
			t.Fatalf("crash waiting for window: %s", crashed)
		}
		opened = len(windowList(a, ib)) > before
	}
	if !opened {
		t.Fatal("window did not open")
	}
	if !runFrames(50) {
		t.Fatalf("crash settling: %s", crashed)
	}
	nw := windowList(a, ib)[0]

	geom := func() (le, te, ww, wh int) {
		return int(int16(ibRd16(a, nw+4))), int(int16(ibRd16(a, nw+6))),
			int(ibRd16(a, nw+8)), int(ibRd16(a, nw+10))
	}

	// Drag sequence: down-left, up-right (descending path), down-right, up-left…
	moves := [][2]int{{-2, 2}, {2, -2}, {2, 2}, {-2, -2}, {3, 1}, {-1, -3}}
	for mi, mv := range moves {
		le, te, ww, wh := geom()
		tbx, tby := le+ww/2, te+4
		if tbx < 10 {
			tbx = 10
		}
		if tbx > 630 {
			tbx = 630
		}
		if !movePointerToScreen(t, a, ib, tbx, tby, runFrames) {
			t.Fatalf("drag %d: cannot reach title bar (%s)", mi, crashed)
		}
		a.Mouse.LeftButton = true
		if !runFrames(3) {
			t.Fatalf("drag %d press crash: %s", mi, crashed)
		}
		for i := 0; i < 25; i++ {
			a.Mouse.Move(mv[0], mv[1])
			if !runFrames(1) {
				t.Fatalf("drag %d move crash (i=%d): %s", mi, i, crashed)
			}
		}
		a.Mouse.LeftButton = false
		if !runFrames(30) {
			t.Fatalf("drag %d release crash: %s", mi, crashed)
		}
		ok, det := freelistOK(a)
		le2, te2, _, _ := geom()
		t.Logf("drag %d dir=%v: window (%d,%d)→(%d,%d) freelist=%v(%s)",
			mi, mv, le, te, le2, te2, ok, det)
		if !ok {
			snap(t, a, "wb_diag_stress_corrupt.png")
			t.Fatalf("freelist corrupted after drag %d", mi)
		}
		_ = wh
	}

	if !runFrames(100) {
		t.Fatalf("post-stress crash: %s", crashed)
	}
	snap(t, a, "wb_diag_stress_final.png")
	t.Logf("final LastAlert=$%08X", ibRd32(a, eb+0x202))
}
