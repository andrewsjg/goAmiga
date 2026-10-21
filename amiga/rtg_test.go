package amiga

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// p96Archive is Picasso96 2.0 (Aminet, freely distributable).
const p96Archive = "../Picasso96/Picasso96.lha"

// p96Files unpacks Picasso96 into a temporary folder (with lha) and returns
// its Picasso96Install folder.
func p96Files(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(p96Archive); err != nil {
		t.Skip(err)
	}
	lha, err := exec.LookPath("lha")
	if err != nil {
		t.Skip("lha not installed")
	}
	dir := t.TempDir()
	abs, _ := filepath.Abs(p96Archive)
	// lha exits 1 here even when it unpacks everything; what matters is
	// whether the files are there.
	out, err := exec.Command(lha, "xqw="+dir, abs).CombinedOutput()
	inst := filepath.Join(dir, "Picasso96Install")
	if _, serr := os.Stat(filepath.Join(inst, "Libs/Picasso96/uaegfx.card")); serr != nil {
		t.Fatalf("lha: %v %s", err, out)
	}
	return inst
}

// addToolTypes returns an icon (a DiskObject with no tooltypes) with the
// given tooltypes added: do_ToolTypes set, and the strings appended, a
// longword array size then each string with its length.
func addToolTypes(info []byte, tts []string) []byte {
	be := binary.BigEndian
	out := append([]byte(nil), info...)
	be.PutUint32(out[54:], 1) // do_ToolTypes: present
	out = be.AppendUint32(out, uint32(4*(len(tts)+1)))
	for _, s := range tts {
		out = be.AppendUint32(out, uint32(len(s)+1))
		out = append(out, s...)
		out = append(out, 0)
	}
	return out
}

// installP96 installs Picasso96 for the uaegfx card into a boot folder, as
// its installer would: the libraries, the card driver, the monitor (with
// BoardType and SettingsFile tooltypes) and a settings file.
func installP96(t *testing.T, p96, boot, settings string) {
	t.Helper()
	cp := func(from, to string) {
		b, err := os.ReadFile(filepath.Join(p96, from))
		if err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Dir(filepath.Join(boot, to)), 0o755)
		if err := os.WriteFile(filepath.Join(boot, to), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cp("Libs/Picasso96/rtg.library", "Libs/Picasso96/rtg.library")
	cp("Libs/Picasso96/uaegfx.card", "Libs/Picasso96/uaegfx.card")
	cp("Libs/Picasso96/emulation.library", "Libs/Picasso96/emulation.library")
	cp("Libs/Picasso96API.library", "Libs/Picasso96API.library")
	cp("Devs/Monitors/Picasso96", "Devs/Monitors/uaegfx")
	cp("Devs/Picasso96Settings."+settings, "Devs/Picasso96Settings")
	// The presets are for no board in particular; Picasso96Mode would make
	// them uaegfx's.
	p := filepath.Join(boot, "Devs/Picasso96Settings")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, renameSettingsBoard(b, "uaegfx"), 0o644)
	info, err := os.ReadFile(filepath.Join(p96, "Devs/Monitors/Picasso96.info"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(boot, "Devs/Monitors/uaegfx.info"),
		addToolTypes(info, []string{"BoardType=uaegfx", "SettingsFile=SYS:Devs/Picasso96Settings", "IgnoreMask=Yes"}), 0o644)
}

// bootRTG boots Workbench 3.2 on an A1200 with the uaegfx card and
// Picasso96 installed, its monitor loaded at boot (LoadMonDrvs, from the
// installed system on installedHDF). With a mode, Workbench opens in it
// (screenmode.prefs, applied by IPrefs). With a4000, the machine is an
// A4000 with the card on Zorro III.
func bootRTG(t *testing.T, a4000 bool, mode uint32, w, h, depth int) *Amiga {
	t.Helper()
	p96 := p96Files(t)
	var a *Amiga
	if a4000 {
		a = newA4000(t)
		if err := a.EnableRTG(16); err != nil {
			t.Fatal(err)
		}
	} else {
		rom, err := LoadROM("../ROMS/kick32a1200.rom")
		if err != nil {
			t.Skip(err)
		}
		a, _ = New(rom, nil)
		a.SetCPU("68020", 0)
		a.SetChipset("aga")
		a.SetChipRAM(2048)
		a.ConfigureMemory(0, 4096)
		if err := a.EnableRTG(4); err != nil {
			t.Fatal(err)
		}
	}
	hdf, err := os.ReadFile(installedHDF)
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(t.TempDir(), "Boot")
	os.MkdirAll(dir, 0o755)
	adfExtract(t, readDisk(t, "../Workbench/Install3.2.adf"), dir)
	installP96(t, p96, dir, "31")
	os.WriteFile(filepath.Join(dir, "C", "LoadMonDrvs"), adfRead(hdf, "C/LoadMonDrvs"), 0o644)
	boot := "C:LoadMonDrvs >SYS:monitors.log\nEcho \"rc $RC\" >>SYS:monitors.log\n"
	if mode != 0 {
		os.WriteFile(filepath.Join(dir, "C", "IPrefs"), adfRead(hdf, "C/IPrefs"), 0o644)
		os.MkdirAll(filepath.Join(dir, "Libs"), 0o755)
		os.WriteFile(filepath.Join(dir, "Libs", "iffparse.library"), adfRead(hdf, "Libs/iffparse.library"), 0o644)
		os.WriteFile(filepath.Join(dir, "Prefs/Env-Archive/Sys/screenmode.prefs"), screenModePrefs(mode, uint16(w), uint16(h), uint16(depth)), 0o644)
		boot += "C:IPrefs\n"
	}
	ss := filepath.Join(dir, "S", "Startup-Sequence")
	b, _ := os.ReadFile(ss)
	os.WriteFile(ss, []byte(strings.Replace(string(b), "LoadWB", boot+"LoadWB", 1)), 0o644)

	if os.Getenv("RTG_TRACE") != "" {
		a.RTG.Trace = logWriter{t}
	}
	a.RTG.Verify = os.Getenv("RTG_VERIFY") != ""
	a.RTG.SoftPointer = os.Getenv("RTG_SOFTPOINTER") != ""
	if err := a.AttachDirectory(dir, "", false); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HOSTFS_TRACE") != "" {
		a.HardDisk.Trace = logWriter{t}
	}
	a.Reset()
	if !waitWorkbench(t, a) {
		t.Fatal("Workbench did not start")
	}
	for range 300 {
		a.RunFrame()
	}
	if log, err := os.ReadFile(filepath.Join(dir, "monitors.log")); err == nil {
		t.Logf("monitors.log: %q", log)
	}
	return a
}

// Picasso96 finds the uaegfx card and sets it up.
func TestRTGCardFound(t *testing.T) {
	a := bootRTG(t, false, 0, 0, 0, 0)
	if base := a.RTG.Configured(); base == 0 {
		t.Fatal("the RTG board wasn't configured")
	}
	t.Logf("RTG board at $%08X", a.RTG.Configured())
	gfx := findLibrary(a, "graphics.library")
	for id := uint32(0xFFFFFFFF); ; {
		id = callLib(t, a, gfx, -732, [2]uint32{id, 0}, [2]uint32{})
		if id == 0xFFFFFFFF {
			break
		}
		if id&0xFFF00000 == 0xFFF00000 || id>>28 == 5 {
			w, h, depth, name := modeInfo(t, a, id)
			t.Logf("  mode $%08X: %dx%d, %d bits, %q", id, w, h, depth, name)
		}
	}
}

// modeInfo asks graphics.library about a display mode: its nominal size,
// its depth and its name.
func modeInfo(t *testing.T, a *Amiga, id uint32) (w, h, depth int, name string) {
	t.Helper()
	exec := a.peek32(4)
	gfx := findLibrary(a, "graphics.library")
	buf := callLib(t, a, exec, -198, [2]uint32{256, 0x10001}, [2]uint32{}) // AllocMem: public, clear
	defer callLib(t, a, exec, -210, [2]uint32{256, 0}, [2]uint32{buf, 0})  // FreeMem
	// GetDisplayInfoData(handle A0, buffer A1, size D0, tag D1, ID D2).
	if callLibRegs(t, a, gfx, -756, []uint32{256, 0x80001000, id}, []uint32{0, buf}) > 0 { // DTAG_DIMS
		w = int(a.peek16(buf+30)) - int(a.peek16(buf+26)) + 1
		h = int(a.peek16(buf+32)) - int(a.peek16(buf+28)) + 1
		depth = int(a.peek16(buf + 16))
	}
	if callLibRegs(t, a, gfx, -756, []uint32{256, 0x80003000, id}, []uint32{0, buf}) > 0 { // DTAG_NAME
		name = readCStrMem(a, buf+16, 32)
	}
	return
}

// renameSettingsBoard renames the board a Picasso96Settings file's
// resolutions are for: the BDNM chunk (32 bytes) and the "board:WxH"
// names in each RSHD chunk (22 bytes from offset 14).
func renameSettingsBoard(b []byte, board string) []byte {
	out := append([]byte(nil), b...)
	for p := 12; p+8 <= len(out); {
		id := string(out[p : p+4])
		size := int(binary.BigEndian.Uint32(out[p+4:]))
		body := out[p+8 : p+8+size]
		switch id {
		case "STHD": // the board type the resolutions are for: uaegfx's
			binary.BigEndian.PutUint32(body, 1)
		case "BDNM":
			clear(body)
			copy(body, board)
		case "RSHD":
			name := body[14:]
			old := string(name[:strings.IndexByte(string(name), 0)])
			if _, res, ok := strings.Cut(old, ":"); ok {
				clear(name)
				copy(name, board+":"+res)
			}
		}
		p += 8 + size + size&1
	}
	return out
}

// Workbench opens on the card, in 256 colours, 16-bit and 32-bit modes,
// and on the A4000 with the card on Zorro III:
// Picasso96 switches the display to it and points it at a framebuffer in
// its memory, which becomes the picture.
func TestRTGWorkbench(t *testing.T) {
	for _, c := range []struct {
		name   string
		a4000  bool
		mode   uint32
		depth  int
		format int
	}{
		{"clut", false, 0x50011000, 8, rgbfCLUT},
		{"r5g6b5", false, 0x50011102, 16, rgbfR5G6B5},
		{"argb", false, 0x50011300, 24, rgbfA8R8G8B8},
		{"a4000-z3", true, 0x50011300, 24, rgbfA8R8G8B8},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := bootRTG(t, c.a4000, c.mode, 640, 480, c.depth)
			r := a.RTG
			t.Logf("on %v, %dx%d format %d, framebuffer $%08X width %d", r.on, r.width, r.height, r.format, r.fb, r.rowPixels)
			if !r.on || r.width != 640 || r.height != 480 || r.format != c.format {
				t.Fatalf("Workbench isn't on the card in format %d", c.format)
			}
			if w, h := a.PictureSize(); w != 640 || h != 480 {
				t.Errorf("picture %dx%d, want 640x480", w, h)
			}
			img := a.PictureImage()
			savePNG(t, a, "/tmp/rtg_workbench_"+c.name+".png")
			// Workbench's background is its colour 0, grey.
			if px := img.RGBAAt(600, 300); px.R>>3 != 0xAA>>3 || px.G>>3 != 0xAA>>3 || px.B>>3 != 0xAA>>3 {
				t.Errorf("the background is %v, want Workbench grey", px)
			}
		})
	}
}

// A save state keeps the card: its mode, palette and video memory come
// back, and with them the picture. Reset gives the display back to the
// chipset.
func TestRTGStateAndReset(t *testing.T) {
	a := bootRTG(t, false, 0x50011000, 640, 480, 8)
	want := a.PictureImage()
	var buf bytes.Buffer
	if err := a.SaveState(&buf); err != nil {
		t.Fatal(err)
	}
	r := a.RTG
	r.on, r.width, r.palette = false, 0, [256][3]byte{}
	clear(r.vram.b)
	if err := a.LoadState(&buf); err != nil {
		t.Fatal(err)
	}
	if !r.showing() || !bytes.Equal(a.PictureImage().Pix, want.Pix) {
		t.Fatal("the picture didn't come back with the state")
	}
	a.Reset()
	if r.showing() {
		t.Error("the card still has the display after reset")
	}
	if w, h := a.PictureSize(); w == 640 && h == 480 {
		t.Errorf("picture %dx%d after reset: still the card's", w, h)
	}
}

// rtgDesk drives Workbench on the card with the mouse, through the
// front screen's own pointer position (Screen.MouseX/Y), for tests.
type rtgDesk struct {
	t  *testing.T
	a  *Amiga
	ib uint32
}

func newRTGDesk(t *testing.T, a *Amiga) *rtgDesk {
	return &rtgDesk{t, a, findLibrary(a, "intuition.library")}
}

func (d *rtgDesk) frames(n int) {
	for range n {
		d.a.RunFrame()
	}
}

func (d *rtgDesk) screen() uint32 { return d.a.peek32(d.ib + 0x3C) }

// moveTo moves the pointer to x, y on the front screen.
func (d *rtgDesk) moveTo(x, y int) {
	s := d.screen()
	for range 200 {
		my, mx := int(int16(d.a.peek16(s+16))), int(int16(d.a.peek16(s+18)))
		dx, dy := x-mx, y-my
		if dx == 0 && dy == 0 {
			return
		}
		step := func(v int) int { return max(-20, min(20, v)) }
		d.a.Mouse.Move(step(dx), step(dy))
		d.frames(1)
	}
	d.t.Logf("pointer didn't reach %d,%d: at %d,%d", x, y, int16(d.a.peek16(s+18)), int16(d.a.peek16(s+16)))
}

func (d *rtgDesk) button(right, down bool) {
	if right {
		d.a.Mouse.RightButton = down
	} else {
		d.a.Mouse.LeftButton = down
	}
	d.frames(3)
}

func (d *rtgDesk) click(x, y int) {
	d.moveTo(x, y)
	d.button(false, true)
	d.button(false, false)
}

func (d *rtgDesk) doubleClick(x, y int) {
	d.click(x, y)
	d.button(false, true)
	d.button(false, false)
	d.frames(150)
}

// drag holds a button from one place to another.
func (d *rtgDesk) drag(right bool, x0, y0, x1, y1 int) {
	d.moveTo(x0, y0)
	d.button(right, true)
	d.moveTo(x1, y1)
	d.frames(5)
	d.button(right, false)
	d.frames(20)
}

// window finds the front screen's window whose title starts with this
// (a drawer's title goes on with how full it is): its left, top, width
// and height.
func (d *rtgDesk) window(title string) (x, y, w, h int, ok bool) {
	a := d.a
	for win := a.peek32(d.screen() + 4); win != 0; win = a.peek32(win) {
		if strings.HasPrefix(readCStrMem(a, a.peek32(win+32), 40), title) {
			return int(int16(a.peek16(win + 4))), int(int16(a.peek16(win + 6))),
				int(int16(a.peek16(win + 8))), int(int16(a.peek16(win + 10))), true
		}
	}
	return
}

// The card's blitter draws what Picasso96 would: Workbench is driven on
// the card (a drawer opened, moved, resized and closed, an icon selected,
// the menus) in each kind of format, with every call checked against
// Picasso96's own drawing (Verify). All twelve formats were checked so;
// RTG_ALLFORMATS checks them again.
func TestRTGBlitterMatchesSoftware(t *testing.T) {
	if os.Getenv("RTG_NOVERIFY") == "" {
		t.Setenv("RTG_VERIFY", "1")
	}
	for _, c := range []struct {
		name  string
		mode  uint32
		depth int
	}{
		{"clut", 0x50011000, 8},
		{"r5g6b5pc", 0x50011100, 16},
		{"r5g5b5pc", 0x50011101, 15},
		{"rgb24", 0x50011200, 24},
		{"argb", 0x50011300, 24},
		{"bgra", 0x50011303, 24},
		// The rest, with RTG_ALLFORMATS set.
		{"rgba", 0x50011302, 24},
		{"bgr24", 0x50011201, 24},
		{"r5g6b5", 0x50011102, 16},
		{"b5g6r5pc", 0x50011202, 16},
		{"r5g5b5", 0x50011103, 15},
		{"b5g5r5pc", 0x50011203, 15},
	}[:map[bool]int{false: 6, true: 12}[os.Getenv("RTG_ALLFORMATS") != ""]] {
		t.Run(c.name, func(t *testing.T) {
			a := bootRTG(t, false, c.mode, 640, 480, c.depth)
			d := newRTGDesk(t, a)
			d.doubleClick(50, 70) // open Boot
			x, y, w, h, ok := d.window("Boot")
			if !ok {
				savePNG(t, a, "/tmp/rtg_blit_fail.png")
				s := d.screen()
				t.Logf("screen $%08X %q mouse %d,%d", s, readCStrMem(a, a.peek32(s+26), 40), int16(a.peek16(s+18)), int16(a.peek16(s+16)))
				for win := a.peek32(s + 4); win != 0; win = a.peek32(win) {
					t.Logf("window %q", readCStrMem(a, a.peek32(win+32), 40))
				}
				t.Fatal("the Boot window didn't open")
			}
			d.drag(false, x+60, y+5, x+160, y+85) // move it
			x, y, _, _, _ = d.window("Boot")
			d.drag(false, x+w-5, y+h-5, x+w+80, y+h+60) // resize it
			d.click(45, 112)                            // select Ram Disk
			d.drag(true, 20, 5, 30, 60)                 // a menu, let go off it
			d.drag(true, 120, 5, 120, 4)
			x, y, _, _, _ = d.window("Boot")
			d.click(x+5, y+5) // close
			d.frames(50)
			savePNG(t, a, "/tmp/rtg_blit_"+c.name+".png")
			r := a.RTG
			var calls []string
			for fn, n := range r.Calls {
				if n > 0 && fn != rtgSetColorArray {
					calls = append(calls, fmt.Sprintf("%d:%d", fn, n))
				}
			}
			t.Logf("verified %d calls, %d mismatches; calls by number %v", r.Verified, r.Mismatches, calls)
			if r.Mismatches > 0 {
				t.Errorf("%d of %d calls drew differently from Picasso96", r.Mismatches, r.Verified)
			}
		})
	}
}

// The card's blitter takes the drawing off the CPU: graphics.library's
// RectFill and ScrollRaster on Workbench's screen take a fraction of the
// emulated time they do drawn by Picasso96 in software.
func TestRTGBlitterSpeed(t *testing.T) {
	cost := map[bool][2]uint64{}
	for _, off := range []bool{true, false} {
		a := bootRTG(t, false, 0x50011300, 640, 480, 24)
		a.RTG.NoBlitter = off
		gfx := findLibrary(a, "graphics.library")
		ib := findLibrary(a, "intuition.library")
		rp := a.peek32(ib+0x3C) + 84 // the front screen's RastPort
		start := a.cycles
		for i := range 50 {
			callLibRegs(t, a, gfx, -306, []uint32{uint32(i), uint32(i), 600, 440}, []uint32{0, rp}) // RectFill
		}
		mid := a.cycles
		for range 50 {
			callLibRegs(t, a, gfx, -396, []uint32{0, 4, 0, 20, 639, 460}, []uint32{0, rp}) // ScrollRaster
		}
		cost[off] = [2]uint64{mid - start, a.cycles - mid}
	}
	t.Logf("RectFill: %d cycles in software, %d with the blitter; ScrollRaster: %d, %d",
		cost[true][0], cost[false][0], cost[true][1], cost[false][1])
	for i, what := range []string{"RectFill", "ScrollRaster"} {
		if cost[false][i]*4 > cost[true][i] {
			t.Errorf("%s with the blitter took more than a quarter of the software's time", what)
		}
	}
}

// The card's pointer is a hardware sprite drawn over the picture, and
// looks just as Picasso96's own software pointer drawn into the screen
// does: same place, same image, same colours.
func TestRTGPointer(t *testing.T) {
	for _, c := range []struct {
		name  string
		mode  uint32
		depth int
	}{
		{"clut", 0x50011000, 8},
		{"argb", 0x50011300, 24},
	} {
		t.Run(c.name, func(t *testing.T) {
			var pics [2]*image.RGBA
			for i, soft := range []bool{true, false} {
				if soft {
					t.Setenv("RTG_SOFTPOINTER", "1")
				} else {
					t.Setenv("RTG_SOFTPOINTER", "")
				}
				a := bootRTG(t, false, c.mode, 640, 480, c.depth)
				d := newRTGDesk(t, a)
				d.moveTo(300, 200)
				d.frames(10)
				pics[i] = a.PictureImage()
				if !soft && !a.RTG.sprite.on {
					t.Fatal("the hardware pointer is off")
				}
				grey := pics[i].RGBAAt(400, 300)
				n := 0
				for y := 195; y < 220; y++ {
					for x := 295; x < 320; x++ {
						if pics[i].RGBAAt(x, y) != grey {
							n++
						}
					}
				}
				t.Logf("soft %v: %d pointer pixels by 300,200", soft, n)
				if n < 20 {
					t.Fatal("no pointer at 300,200")
				}
			}
			diff, first := 0, image.Point{}
			for y := 0; y < 480; y++ {
				for x := 0; x < 640; x++ {
					if pics[0].RGBAAt(x, y) != pics[1].RGBAAt(x, y) {
						if diff == 0 {
							first = image.Pt(x, y)
						}
						diff++
					}
				}
			}
			if diff > 0 {
				savePNG2(t, pics[0], "/tmp/rtg_pointer_soft.png")
				savePNG2(t, pics[1], "/tmp/rtg_pointer_hard.png")
				t.Errorf("%d pixels differ from the software pointer's picture, the first at %v: %v, want %v",
					diff, first, pics[1].RGBAAt(first.X, first.Y), pics[0].RGBAAt(first.X, first.Y))
			}
		})
	}
}

func savePNG2(t *testing.T, img image.Image, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Log(err)
		return
	}
	defer f.Close()
	png.Encode(f, img)
}
