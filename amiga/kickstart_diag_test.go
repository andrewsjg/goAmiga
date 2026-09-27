package amiga

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestKickstartDiag boots a Kickstart ROM (default ../ROMS/Kick32.rom, or
// $GOAMIGA_KS) with no disk, logs CPU/chipset state and exec's last alert
// every 2 s, catches illegal-instruction exceptions, and saves the screen
// to /tmp/ks_<frame>.png. With $GOAMIGA_ADF it boots that disk
// (write-protected) for 60 s:
//
//	GOAMIGA_DIAG=1 go test -run TestKickstartDiag -v ./amiga/
func TestKickstartDiag(t *testing.T) {
	requireDiag(t)
	path := "../ROMS/Kick32.rom"
	if p := os.Getenv("GOAMIGA_KS"); p != "" {
		path = p
	}
	rom, err := LoadROM(path)
	if err != nil {
		t.Skip(err)
	}
	a, err := New(rom, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.ConfigureMemory(512, 0)
	if adf := os.Getenv("GOAMIGA_ADF"); adf != "" {
		data, err := os.ReadFile(adf)
		if err != nil {
			t.Fatal(err)
		}
		// $GOAMIGA_RUN runs a command early in the startup-sequence, in
		// place of its AddBuffers line (same length; FFS data blocks have
		// no checksum). The image on disk is never touched.
		if cmd := os.Getenv("GOAMIGA_RUN"); cmd != "" {
			old := []byte("AddBuffers >NIL: DF0: 15")
			i := bytes.Index(data, old)
			line := []byte("Run " + cmd)
			if i < 0 || len(line) > len(old) {
				t.Fatalf("cannot patch startup-sequence for %q", cmd)
			}
			copy(data[i:], append(line, bytes.Repeat([]byte(" "), len(old)-len(line))...))
		}
		// $GOAMIGA_PATCH="old|new" replaces a line of the startup-sequence
		// with a command of at most the same length (padded with spaces).
		// Several patches may be given, separated by ";".
		for _, pt := range strings.Split(os.Getenv("GOAMIGA_PATCH"), ";") {
			if pt == "" {
				continue
			}
			old, repl, _ := strings.Cut(pt, "|")
			i := bytes.Index(data, []byte(old))
			if i < 0 || len(repl) > len(old) {
				t.Fatalf("cannot patch %q", old)
			}
			copy(data[i:], append([]byte(repl), bytes.Repeat([]byte(" "), len(old)-len(repl))...))
		}
		// $GOAMIGA_BOOTBLOCK copies another disk's boot block, to boot a
		// disk that ships non-bootable (Workbench3.2.adf).
		if bb := os.Getenv("GOAMIGA_BOOTBLOCK"); bb != "" {
			other, err := os.ReadFile(bb)
			if err != nil {
				t.Fatal(err)
			}
			copy(data[:1024], other[:1024])
		}
		if err := a.Disk.InsertDisk(data); err != nil {
			t.Fatal(err)
		}
		a.Disk.WriteProtected = true
		// $GOAMIGA_DF1: a disk for DF1: (connects the drive).
		if df1 := os.Getenv("GOAMIGA_DF1"); df1 != "" {
			d1, err := os.ReadFile(df1)
			if err != nil {
				t.Fatal(err)
			}
			a.Disk.ConnectDrive(1).InsertDisk(d1)
		}
		a.Disk.SetWriteProtected(true)
	}
	a.Reset()
	a.Video.DisplayActive = true
	rd := func(addr uint32) uint32 { v, _ := a.CPU.M.Long(int(addr)); return v }
	// Catch illegal-instruction exceptions: execution arriving at the
	// vector 4 handler right after running another instruction.
	illegals := 0
	// Trace OpenLibrary (exec LVO -552): name, version, and what it returns.
	type pendingOpen struct {
		ret  uint32
		name string
		ver  uint32
	}
	var opens []pendingOpen
	traceOpen := os.Getenv("GOAMIGA_TRACE_OPEN") != ""
	step := func() error {
		prev := a.CPU.PC
		if traceOpen {
			if eb := rd(4); eb > 0x100 && eb < 0x1000000 {
				switch prev {
				case eb - 552: // OpenLibrary(A1 name, D0 version)
					opens = append(opens, pendingOpen{rd(a.CPU.A[7]), readCStrMem(a, a.CPU.A[1], 40), a.CPU.D[0]})
				case eb - 102: // InitResident(A1 romtag, D1 seglist)
					opens = append(opens, pendingOpen{rd(a.CPU.A[7]), "InitResident " + readCStrMem(a, rd(a.CPU.A[1]+14), 40), 0})
				}
			}
			for i := len(opens) - 1; i >= 0; i-- {
				if prev == opens[i].ret {
					t.Logf("OpenLibrary(%q, %d) from $%06X = $%08X", opens[i].name, opens[i].ver, opens[i].ret, a.CPU.D[0])
					opens = append(opens[:i], opens[i+1:]...)
					break
				}
			}
		}
		if err := a.Step(); err != nil {
			return err
		}
		for _, vec := range []uint32{4, 10, 11} { // illegal, Line-A, Line-F
			if h := rd(4 * vec); a.CPU.PC == h && prev != h && illegals < 8 && rd(4) > 0x100 {
				illegals++
				op, _ := a.CPU.M.Word(int(prev))
				eb := rd(4)
				task := rd(eb + 276)
				name := readCStrMem(a, rd(task+10), 32)
				code := make([]byte, 16)
				a.CPU.M.Read(int(prev)-6, code)
				t.Logf("EXCEPTION vector %d at $%06X opcode $%04X task $%06X %q; bytes from PC-6: % X",
					vec, prev, op, task, name, code)
			}
		}
		return nil
	}
	frames := 600
	if os.Getenv("GOAMIGA_ADF") != "" {
		frames = 3000
	}
	if n, err := strconv.Atoi(os.Getenv("GOAMIGA_FRAMES")); err == nil {
		frames = n
	}
	// $GOAMIGA_SWAP="disk.adf:frame" swaps DF0: to another disk at that
	// frame (write-protected, via SwapDisk's eject/insert).
	swapFrame, swapData := -1, []byte(nil)
	if sw := os.Getenv("GOAMIGA_SWAP"); sw != "" {
		i := strings.LastIndex(sw, ":")
		swapFrame, _ = strconv.Atoi(sw[i+1:])
		if swapData, err = os.ReadFile(sw[:i]); err != nil {
			t.Fatal(err)
		}
	}
	// $GOAMIGA_SWAP_ON_REQUEST="other.adf": act like a user with two disks.
	// Whenever a "System Request" window is up (DOS asking for a volume),
	// swap to the other disk; DOS closes the requester itself once the
	// right volume is in.
	var disks [2][]byte
	cur := 0
	if other := os.Getenv("GOAMIGA_SWAP_ON_REQUEST"); other != "" {
		disks[0] = append([]byte(nil), a.Disk.adf...)
		if disks[1], err = os.ReadFile(other); err != nil {
			t.Fatal(err)
		}
	}
	requestOpen := func() bool {
		ib := findLibrary(a, "intuition.library")
		if ib == 0 {
			return false
		}
		for scr := rd(ib + 60); scr != 0; scr = rd(scr) {
			for w := rd(scr + 4); w != 0; w = rd(w) {
				if readCStrMem(a, rd(w+32), 20) == "System Request" {
					return true
				}
			}
		}
		return false
	}
	lastSwap := -1000
	lastVols := ""
	for f := 0; f <= frames; f++ {
		// Like a sensible user: only swap once the drive has stopped.
		if disks[1] != nil && f%25 == 0 && f-lastSwap > 400 && !a.Disk.Swapping() && !a.Disk.motor && requestOpen() {
			cur ^= 1
			a.Disk.SwapDisk(disks[cur], "")
			lastSwap = f
			t.Logf("frame %d: requester up, swapped to disk %d", f, cur)
		}
		if os.Getenv("GOAMIGA_TRACE_VOLUMES") != "" && f%5 == 0 {
			if v := fmt.Sprint(dosVolumes(a)); v != lastVols {
				t.Logf("frame %d: volumes %s (drive: disk=%v change=%v motor=%v)", f, v, a.Disk.Inserted(), a.Disk.diskChange, a.Disk.motor)
				lastVols = v
			}
		}
		if os.Getenv("GOAMIGA_TRACE_DRIVE") != "" && lastSwap > 0 && f%25 == 0 && f < lastSwap+160 {
			d := a.Disk
			t.Logf("frame %d: drive disk=%v swapping=%v change=%v motor=%v sel=%v cyl=%d statusReadsSelected=%d pendingWait=%.2fs",
				f, d.Inserted(), d.Swapping(), d.diskChange, d.motor, d.selected, d.cylinder, d.StatusSelectedTrue,
				float64(d.pendingWait)/7093790)
		}
		if f == swapFrame {
			a.Disk.SwapDisk(swapData, "")
			t.Logf("frame %d: swapped disk", f)
		}
		start := a.cycles
		for a.cycles-start < CyclesPerFrame {
			if err := step(); err != nil {
				t.Fatalf("frame %d: %v", f, err)
			}
		}
		if f%100 == 0 && (frames <= 600 || f%500 == 0) {
			eb := rd(4)
			t.Logf("frame %d: PC=$%06X SR=$%04X stopped=%v ExecBase=$%06X BPLCON0=$%04X DMACON=$%04X COLOR00=$%03X busErrors=%d",
				f, a.CPU.PC, a.CPU.SR, a.cpuStopped, eb, a.Custom.ReadReg16(0x100),
				a.Custom.ReadReg16(0x096), a.Custom.ReadReg16(0x180), a.busErrorCount)
			saveScreenshot(a.Video, fmt.Sprintf("/tmp/ks_%03d.png", f))
			for a.vpos != 150 {
				a.advanceBeamPerCycle(1)
			}
			c := a.Custom
			t.Logf("  line 150: BPLCON0=$%04X DDF=$%04X/$%04X BPL1MOD=%d BPL2MOD=%d DIW=$%04X/$%04X BPLCON1=$%04X",
				c.ReadReg16(0x100), c.ReadReg16(0x092), c.ReadReg16(0x094),
				int16(c.ReadReg16(0x108)), int16(c.ReadReg16(0x10A)),
				c.ReadReg16(0x08E), c.ReadReg16(0x090), c.ReadReg16(0x102))
			if eb >= 0x100 && eb < 0x1000000 {
				t.Logf("  LastAlert=$%08X $%08X  [$100]=$%08X [$104]=$%08X  D7=$%08X A5=$%08X",
					rd(eb+0x202), rd(eb+0x206), rd(0x100), rd(0x104), a.CPU.D[7], a.CPU.A[5])
			}
		}
	}
	for _, o := range opens {
		t.Logf("still waiting: OpenLibrary(%q, %d) from $%06X", o.name, o.ver, o.ret)
	}
	// exec's resident module table (ExecBase->ResModules, +$12C): a
	// NULL-terminated array of RomTag pointers (bit 31 set = link).
	if eb := rd(4); eb > 0x100 && eb < 0x1000000 {
		for p := rd(eb + 0x12C); p != 0; p += 4 {
			tag := rd(p)
			if tag == 0 {
				break
			}
			if tag&0x80000000 != 0 {
				p = tag&0x7FFFFFFF - 4
				continue
			}
			name := readCStrMem(a, rd(tag+14), 32)
			if name == "icon.library" || name == "workbench.library" || name == "ramlib" {
				flags, _ := a.CPU.M.Byte(int(tag + 10))
				ver, _ := a.CPU.M.Byte(int(tag + 11))
				typ, _ := a.CPU.M.Byte(int(tag + 12))
				pri, _ := a.CPU.M.Byte(int(tag + 13))
				t.Logf("resident %q at $%06X flags=$%02X ver=%d type=%d pri=%d", name, tag, flags, ver, typ, int8(pri))
			}
		}
	}
	// The front screen's BitMap: IntuitionBase->FirstScreen (+60), Screen
	// Width/Height (+12/+14), BitMap at +184: BytesPerRow.w, Rows.w,
	// Flags.b, Depth.b, pad.w, Planes[8].
	if ib := findLibrary(a, "intuition.library"); ib != 0 {
		scr := rd(ib + 60)
		w, _ := a.CPU.M.Word(int(scr + 12))
		h, _ := a.CPU.M.Word(int(scr + 14))
		bm := scr + 184
		bpr, _ := a.CPU.M.Word(int(bm))
		rows, _ := a.CPU.M.Word(int(bm + 2))
		flags, _ := a.CPU.M.Byte(int(bm + 4))
		depth, _ := a.CPU.M.Byte(int(bm + 5))
		t.Logf("front screen $%06X %q: %dx%d; BitMap BytesPerRow=%d Rows=%d Flags=$%02X Depth=%d Planes=$%06X $%06X",
			scr, readCStrMem(a, rd(scr+22), 40), w, h, bpr, rows, flags, depth, rd(bm+8), rd(bm+12))
		// Every screen: Screen.NextScreen (+0), and its RastPort's BitMap
		// pointer (RastPort at +84, rp_BitMap at +4), which is the bitmap
		// actually drawn into (may differ from the embedded one in 3.x).
		for sc := scr; sc != 0; sc = rd(sc) {
			bmp := rd(sc + 84 + 4)
			bpr, _ := a.CPU.M.Word(int(bmp))
			rows, _ := a.CPU.M.Word(int(bmp + 2))
			fl, _ := a.CPU.M.Byte(int(bmp + 4))
			dp, _ := a.CPU.M.Byte(int(bmp + 5))
			t.Logf("  screen $%06X %q: RastPort BitMap $%06X BytesPerRow=%d Rows=%d Flags=$%02X Depth=%d Planes=$%06X $%06X",
				sc, readCStrMem(a, rd(sc+22), 30), bmp, bpr, rows, fl, dp, rd(bmp+8), rd(bmp+12))
		}
		t.Logf("BPL1PT=$%04X%04X BPL2PT=$%04X%04X", a.Custom.ReadReg16(0x0E0), a.Custom.ReadReg16(0x0E2),
			a.Custom.ReadReg16(0x0E4), a.Custom.ReadReg16(0x0E6))
	}
	// Plane pointers and fetch registers over a few consecutive lines.
	for a.vpos != 99 {
		a.advanceBeamPerCycle(1)
	}
	for l := 0; l < 5; l++ {
		p0 := a.Video.bplpt[0]
		c := a.Custom
		t.Logf("line %d: BPL1 ptr $%06X  DDF=$%02X/$%02X MOD=%d/%d BPLCON0=$%04X BPLCON1=$%04X FMODE?=$%04X",
			a.vpos, p0, c.ReadReg16(0x092), c.ReadReg16(0x094), int16(c.ReadReg16(0x108)), int16(c.ReadReg16(0x10A)),
			c.ReadReg16(0x100), c.ReadReg16(0x102), c.ReadReg16(0x1FC))
		a.advanceBeamPerCycle(CPUClocksPerLine)
	}
	if gfx := findLibrary(a, "graphics.library"); gfx != 0 {
		crb, _ := a.CPU.M.Byte(int(gfx + 0xEC))
		dispflags, _ := a.CPU.M.Word(int(gfx + 0xCE))
		t.Logf("GfxBase $%06X ChipRevBits0=$%02X DisplayFlags=$%04X DENISEID read=$%04X",
			gfx, crb, dispflags, a.Custom.ReadReg16(0x07C))
	}
	// Bottom of the display.
	for _, l := range []int{240, 250, 260, 290, 299, 305} {
		for a.vpos != l {
			a.advanceBeamPerCycle(1)
		}
		a.advanceBeamPerCycle(200)
		c := a.Custom
		t.Logf("line %d: BPLCON0=$%04X DIW=$%04X/$%04X BPL1 ptr $%06X", l, c.ReadReg16(0x100),
			c.ReadReg16(0x08E), c.ReadReg16(0x090), a.Video.bplpt[0])
	}
	if ib := findLibrary(a, "intuition.library"); ib != 0 {
		scr := rd(ib + 60)
		for w := rd(scr + 4); w != 0; w = rd(w) { // Screen.FirstWindow, Window.NextWindow
			l, _ := a.CPU.M.Word(int(w + 4))
			tp, _ := a.CPU.M.Word(int(w + 6))
			wd, _ := a.CPU.M.Word(int(w + 8))
			ht, _ := a.CPU.M.Word(int(w + 10))
			t.Logf("window %q at %d,%d size %dx%d", readCStrMem(a, rd(w+32), 30), l, tp, wd, ht)
		}
	}
	// Disassemble the Copper lists (COP1LC, then COP2LC).
	for _, lc := range []uint32{a.Copper.cop1lc, a.Copper.cop2lc} {
		t.Logf("copper list at $%06X:", lc)
		for pc, n := lc, 0; n < 120; pc, n = pc+4, n+1 {
			w1, _ := a.CPU.M.Word(int(pc))
			w2, _ := a.CPU.M.Word(int(pc + 2))
			switch {
			case w1&1 == 0:
				name := customRegNames[int(w1&0x1FE)]
				if name == "" {
					name = fmt.Sprintf("$%03X", w1&0x1FE)
				}
				t.Logf("  $%06X MOVE %-8s = $%04X", pc, name, w2)
			case w2&1 == 0:
				t.Logf("  $%06X WAIT v=$%02X h=$%02X (mask $%04X)", pc, w1>>8, w1&0xFE, w2)
				if w1 == 0xFFFF && w2 == 0xFFFE {
					n = 999
				}
			default:
				t.Logf("  $%06X SKIP v=$%02X h=$%02X", pc, w1>>8, w1&0xFE)
			}
		}
	}
	if eb := rd(4); eb > 0x100 {
		attn, _ := a.CPU.M.Word(int(eb + 0x128))
		t.Logf("ExecBase->AttnFlags=$%04X (bit0 68010, bit1 68020, bit4 68881, bit5 68882)", attn)
	}
}
