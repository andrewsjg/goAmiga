package amiga

import (
	"bytes"
	"encoding/binary"
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
