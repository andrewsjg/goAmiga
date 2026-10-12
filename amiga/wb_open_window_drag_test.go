package amiga

import (
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/andrewsjg/goamiga/m68k/m68kmem"
)

// Headless reproduction of the interactive crash: open the Workbench1.3 disk
// window with a real double-click, then drag it. Closed-loop pointer control
// uses IntuitionBase MouseX/MouseY so quadrature scaling does not matter.

func ibRd32(a *Amiga, addr int) uint32 {
	if addr < 0 || addr+4 > ChipRAMSize {
		return 0
	}
	b := make([]byte, 4)
	a.ChipRAM.Read(addr, b)
	return binary.BigEndian.Uint32(b)
}

func ibRd16(a *Amiga, addr int) uint16 {
	if addr < 0 || addr+2 > ChipRAMSize {
		return 0
	}
	b := make([]byte, 2)
	a.ChipRAM.Read(addr, b)
	return binary.BigEndian.Uint16(b)
}

// findIntuitionBase walks the Exec LibList for intuition.library.
func findIntuitionBase(a *Amiga) int {
	eb := int(ibRd32(a, 4))
	if eb < 0x100 || eb >= ChipRAMSize {
		return 0
	}
	node := ibRd32(a, eb+0x17A) // LibList lh_Head
	for n := 0; n < 64 && node != 0; n++ {
		ni := int(node)
		if ni < 0x100 || ni+0x20 > ChipRAMSize {
			return 0
		}
		namePtr, _ := a.CPU.M.Long(ni + 0x0A)
		if readCStrMem(a, uint32(namePtr), 24) == "intuition.library" {
			return ni
		}
		node = ibRd32(a, ni)
	}
	return 0
}

// intuiMouse reads intuition's idea of the pointer position (screen coords).
func intuiMouse(a *Amiga, ib int) (x, y int) {
	y = int(int16(ibRd16(a, ib+0x44)))
	x = int(int16(ibRd16(a, ib+0x46)))
	return
}

// movePointerToScreen targets true screen coordinates. Intuition tracks Y at
// double (interlace-capable) resolution: IB.MouseY == 2 * screen Y, while
// IB.MouseX == screen X on a hires screen. Verified via Screen.MouseX/Y.
func movePointerToScreen(t *testing.T, a *Amiga, ib, sx, sy int, run func(int) bool) bool {
	return movePointerTo(t, a, ib, sx, sy*2, run)
}

// movePointerTo drives the mouse counters until intuition reports (tx,ty).
func movePointerTo(t *testing.T, a *Amiga, ib, tx, ty int, run func(int) bool) bool {
	for iter := 0; iter < 120; iter++ {
		x, y := intuiMouse(a, ib)
		if x == tx && y == ty {
			return true
		}
		dx, dy := tx-x, ty-y
		// Hires screen: X counts move 2 screen px; be conservative & clamp.
		mdx := dx / 2
		if mdx == 0 && dx != 0 {
			mdx = dx // fall back for odd remainder
		}
		clamp := func(v int) int {
			if v > 40 {
				return 40
			}
			if v < -40 {
				return -40
			}
			return v
		}
		a.Mouse.Move(clamp(mdx), clamp(dy))
		if !run(1) {
			return false
		}
	}
	x, y := intuiMouse(a, ib)
	t.Logf("movePointerTo(%d,%d) stuck at (%d,%d)", tx, ty, x, y)
	return false
}

func windowList(a *Amiga, ib int) []int {
	scr := int(ibRd32(a, ib+0x3C)) // FirstScreen
	if scr < 0x100 || scr >= ChipRAMSize {
		return nil
	}
	var ws []int
	w := int(ibRd32(a, scr+4))
	for n := 0; n < 16 && w >= 0x100 && w < ChipRAMSize; n++ {
		ws = append(ws, w)
		w = int(ibRd32(a, w))
	}
	return ws
}

func snap(t *testing.T, a *Amiga, name string) {
	img := a.Video.ScreenImage()
	f, err := os.Create("../" + name)
	if err != nil {
		t.Logf("snap: %v", err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
	t.Logf("saved %s", name)
}

// watchRAM wraps chip RAM and reports writes overlapping [lo,hi).
type watchRAM struct {
	m68kmem.Memory
	lo, hi  int
	onWrite func(addr int, p []byte)
}

func (w *watchRAM) Write(addr int, p []byte) (int, error) {
	if addr < w.hi && addr+len(p) > w.lo {
		w.onWrite(addr, p)
	}
	return w.Memory.Write(addr, p)
}

func TestWBOpenWindowAndDrag(t *testing.T) {
	a := bootWB(t)

	eb := int(ibRd32(a, 4))
	vec2 := ibRd32(a, 2*4)
	vec3 := ibRd32(a, 3*4)
	vec4 := ibRd32(a, 4*4)

	// Ring buffer of recent blits for post-mortem.
	type blitRec struct {
		size, con0, con1       uint16
		apt, bpt, cpt, dpt     uint32
		amod, bmod, cmod, dmod int16
	}
	var blits []blitRec
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		blits = append(blits, blitRec{size, con0, con1,
			a.Blitter.preApt, a.Blitter.preBpt, a.Blitter.preCpt, dpt,
			a.Blitter.amod, a.Blitter.bmod, a.Blitter.cmod, a.Blitter.dmod})
		if len(blits) > 64 {
			blits = blits[1:]
		}
	}
	dumpBlits := func(n int) {
		if n > len(blits) {
			n = len(blits)
		}
		for _, b := range blits[len(blits)-n:] {
			h, w := int(b.size>>6), int(b.size&0x3F)
			t.Logf("  blit %dx%d con0=$%04X con1=$%04X A=$%06X B=$%06X C=$%06X D=$%06X mods a=%d b=%d c=%d d=%d",
				h, w, b.con0, b.con1, b.apt, b.bpt, b.cpt, b.dpt, b.amod, b.bmod, b.cmod, b.dmod)
		}
	}

	prevPC := a.CPU.PC
	crashed := ""
	checkExc := func() bool {
		pc := a.CPU.PC
		if pc >= 0xFF4834 && pc < 0xFF4900 {
			crashed = fmt.Sprintf("task-held ROM pc=$%08X D7=$%08X LastAlert=$%08X", pc, a.CPU.D[7], ibRd32(a, eb+0x202))
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
					tt := ibRd32(a, eb+0x114)
					crashed = fmt.Sprintf("EXC %s from=$%08X epc=$%08X ThisTask=$%08X(%q)",
						h.name, prevPC, epc, tt, readCStrMem(a, ibRd32(a, int(tt)+10), 24))
					return true
				}
			}
		}
		return false
	}

	// run n frames, stepping instruction-by-instruction with exception watch.
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

	ib := findIntuitionBase(a)
	if ib == 0 {
		t.Fatal("intuition.library not found")
	}
	t.Logf("IntuitionBase=$%06X", ib)

	ws := windowList(a, ib)
	t.Logf("windows before: %d %v", len(ws), ws)
	x, y := intuiMouse(a, ib)
	t.Logf("pointer at (%d,%d)", x, y)

	// Move over the RAM DISK icon (screen coords ~600,22).
	if !movePointerToScreen(t, a, ib, 600, 22, runFrames) {
		if crashed != "" {
			t.Fatalf("crash during pointer move: %s", crashed)
		}
		snap(t, a, "wb_diag_pointer_stuck.png")
		t.Fatal("could not position pointer")
	}

	// Double-click.
	click := func() bool {
		a.Mouse.LeftButton = true
		if !runFrames(2) {
			return false
		}
		a.Mouse.LeftButton = false
		return runFrames(3)
	}
	if !click() || !click() {
		t.Fatalf("crash during double-click: %s", crashed)
	}

	// Wait for the drawer window to open (disk I/O + layer creation).
	opened := false
	for i := 0; i < 100; i++ {
		if !runFrames(5) {
			dumpBlits(30)
			t.Fatalf("crash while window opening (iter %d): %s", i, crashed)
		}
		ws2 := windowList(a, ib)
		if len(ws2) > len(ws) {
			t.Logf("window opened after ~%d frames: %v", (i+1)*5, ws2)
			opened = true
			break
		}
	}
	snap(t, a, "wb_diag_after_open.png")
	if !opened {
		t.Fatal("window did not open (double-click missed?)")
	}
	if !runFrames(50) {
		dumpBlits(30)
		t.Fatalf("crash after window open settled: %s", crashed)
	}

	// Find the new window geometry.
	ws2 := windowList(a, ib)
	nw := ws2[0]
	le := int(int16(ibRd16(a, nw+4)))
	te := int(int16(ibRd16(a, nw+6)))
	ww := int(ibRd16(a, nw+8))
	wh := int(ibRd16(a, nw+10))
	t.Logf("new window @$%06X geom=%d,%d %dx%d title=%q",
		nw, le, te, ww, wh, readCStrMem(a, ibRd32(a, nw+32), 40))

	// Watch CPU/blitter writes into plane0 rows 70..76 to find who paints the
	// remnant dashes. Records the CPU PC at the time of each write.
	type memwr struct {
		pc   uint32
		addr int
		n    int
		val  uint16
	}
	var memLog []memwr
	watch := &watchRAM{Memory: a.ChipRAM, lo: 0xD510 + 70*80, hi: 0xD510 + 77*80,
		onWrite: func(addr int, p []byte) {
			v := uint16(p[0])
			if len(p) > 1 {
				v = v<<8 | uint16(p[1])
			}
			memLog = append(memLog, memwr{a.CPU.PC, addr, len(p), v})
		}}
	a.ChipRAM = watch
	a.wireMemoryMap(false)

	// Grab the title bar and drag (screen coords).
	tbx, tby := le+ww/2, te+4
	if !movePointerToScreen(t, a, ib, tbx, tby, runFrames) {
		if crashed != "" {
			dumpBlits(30)
			t.Fatalf("crash moving to title bar: %s", crashed)
		}
		t.Fatal("could not reach title bar")
	}
	t.Logf("before press: ActiveWindow=$%06X wFlags=$%08X ibFlags=$%08X",
		ibRd32(a, ib+0x34), ibRd32(a, nw+24), ibRd32(a, ib+0x40))

	// Record every blit's registers and the write history of the "must be
	// restored" band (old window rows that the moved window uncovers).
	type blitRec2 struct {
		size, con0, con1       uint16
		apt, bpt, cpt, dpt     uint32
		amod, bmod, cmod, dmod int16
		adat, bdat, afwm, alwm uint16
	}
	var allBlits []blitRec2
	type wr struct {
		blit int
		val  uint16
	}
	writeLog := map[int][]wr{}
	const bandLo, bandHi = 0xD510 + 40*80, 0xD510 + 86*80
	blitCount, lineCount := 0, 0
	a.Blitter.OnExecute = func(size, con0, con1 uint16, dpt uint32) {
		blitCount++
		if con1&1 != 0 {
			lineCount++
		}
		allBlits = append(allBlits, blitRec2{size, con0, con1,
			a.Blitter.preApt, a.Blitter.preBpt, a.Blitter.preCpt, dpt,
			a.Blitter.amod, a.Blitter.bmod, a.Blitter.cmod, a.Blitter.dmod,
			a.Blitter.adat, a.Blitter.bdat, a.Blitter.afwm, a.Blitter.alwm})
	}
	a.Blitter.DebugWriteHook = func(addr int, val, aW, bW, cW uint16, mt byte) {
		if addr >= bandLo && addr < bandHi {
			writeLog[addr] = append(writeLog[addr], wr{len(allBlits) - 1, val})
		}
	}
	dumpHistory := func(addr int) {
		t.Logf("  history of $%06X (row %d, byte %d):", addr, (addr-0xD510)/80, (addr-0xD510)%80)
		for _, w := range writeLog[addr] {
			b := allBlits[w.blit]
			h, wd := int(b.size>>6), int(b.size&0x3F)
			mode := "area"
			if b.con1&1 != 0 {
				mode = "line"
			}
			t.Logf("    blit#%d %s %dx%d con0=$%04X con1=$%04X A=$%08X B=$%06X C=$%06X D=$%06X mods(a=%d b=%d c=%d d=%d) adat=$%04X bdat=$%04X fwm=$%04X lwm=$%04X wrote=$%04X",
				w.blit, mode, h, wd, b.con0, b.con1, b.apt, b.bpt, b.cpt, b.dpt,
				b.amod, b.bmod, b.cmod, b.dmod, b.adat, b.bdat, b.afwm, b.alwm, w.val)
		}
	}
	checkBand := func() {
		// Rows 52..79, bytes 0..49 (x<400) of plane0 must be zero after the
		// move (old window area uncovered; no icons there).
		buf := make([]byte, 80)
		bad := 0
		for row := 52; row < 80; row++ {
			a.ChipRAM.Read(0xD510+row*80, buf)
			for x := 0; x < 50; x++ {
				if buf[x] != 0 {
					bad++
					if bad <= 6 {
						addr := 0xD510 + row*80 + (x &^ 1)
						t.Logf("REMNANT plane0 row=%d byte=%d val=$%02X", row, x, buf[x])
						dumpHistory(addr)
					}
				}
			}
		}
		t.Logf("remnant bytes in uncovered band: %d", bad)
	}
	a.Mouse.LeftButton = true
	if !runFrames(3) {
		dumpBlits(30)
		t.Fatalf("crash on drag press: %s", crashed)
	}
	t.Logf("after press: ActiveWindow=$%06X wFlags=$%08X ibFlags=$%08X",
		ibRd32(a, ib+0x34), ibRd32(a, nw+24), ibRd32(a, ib+0x40))
	// PC histogram during drag to find where the OS sits.
	pcHist := map[uint32]int{}
	runFramesHist := func(n int) bool {
		for f := 0; f < n; f++ {
			startC := a.cycles
			for int(a.cycles-startC) < CyclesPerFrame {
				prevPC = a.CPU.PC
				pcHist[a.CPU.PC]++
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
	for i := 0; i < 30; i++ {
		a.Mouse.Move(-2, 2) // drag left+down
		if !runFramesHist(1) {
			dumpBlits(30)
			t.Fatalf("crash during drag (i=%d): %s", i, crashed)
		}
		mx, my := intuiMouse(a, ib)
		if i%5 == 0 {
			t.Logf("drag i=%d intuiMouse=(%d,%d) JOY0DAT=$%04X pc=$%08X", i, mx, my, a.Mouse.JOY0DAT(), a.CPU.PC)
		}
	}
	// Top PCs during drag
	type kv struct {
		pc uint32
		n  int
	}
	var top []kv
	for pc, n := range pcHist {
		top = append(top, kv{pc, n})
	}
	for i := 0; i < len(top); i++ {
		for j := i + 1; j < len(top); j++ {
			if top[j].n > top[i].n {
				top[i], top[j] = top[j], top[i]
			}
		}
	}
	if len(top) > 15 {
		top = top[:15]
	}
	for _, e := range top {
		t.Logf("  hot pc=$%08X count=%d", e.pc, e.n)
	}
	t.Logf("during drag: blits=%d lineBlits=%d", blitCount, lineCount)

	snap(t, a, "wb_diag_dragging.png")
	a.Mouse.LeftButton = false
	// "Crashes shortly after": watch a good while post-release.
	for i := 0; i < 200; i++ {
		if !runFrames(1) {
			snap(t, a, "wb_diag_crash.png")
			dumpBlits(40)
			t.Fatalf("crash after drag release (frame %d): %s", i, crashed)
		}
		if i%50 == 49 {
			ok, det := freelistOK(a)
			t.Logf("post-release frame %d freelist=%v(%s) pc=$%08X", i+1, ok, det, a.CPU.PC)
		}
	}
	snap(t, a, "wb_diag_after_drag.png")
	t.Log("after release:")
	checkBand()
	row73 := 0xD510 + 73*80
	for _, m := range memLog {
		if m.addr >= row73 && m.addr < row73+40 {
			t.Logf("  WR pc=$%08X addr=$%06X (row73 byte %d) n=%d val=$%04X",
				m.pc, m.addr, m.addr-row73, m.n, m.val)
		}
	}
	t.Logf("total watched writes rows70-76: %d", len(memLog))

	le2 := int(int16(ibRd16(a, nw+4)))
	te2 := int(int16(ibRd16(a, nw+6)))
	t.Logf("window moved %d,%d → %d,%d", le, te, le2, te2)
	ok, det := freelistOK(a)
	t.Logf("final freelist=%v (%s) LastAlert=$%08X", ok, det, ibRd32(a, eb+0x202))
	if !ok {
		t.Errorf("freelist corrupted: %s", det)
	}
}
