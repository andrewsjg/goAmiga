//go:build !noebitengine

package main

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/ncruces/zenity"

	"github.com/andrewsjg/goamiga/amiga"
	"github.com/andrewsjg/goamiga/internal/config"
	"github.com/andrewsjg/goamiga/internal/whdload"
)

// The configuration screen: the saved configurations on the left, the
// settings in tabs on the right, Start and Quit at the bottom.

const (
	launcherW = 820
	launcherH = 560
)

var launcherTabs = []string{"Machine", "Memory", "Floppies", "Hard disks", "WHDLoad", "Ports", "Display"}

type launcher struct {
	ui *ui

	kicksFor string   // the ROM kicks was found for (WHDLoad tab)
	kicks    []string // WHDLoad's names for the Kickstart images beside it
	s        config.Settings
	name     string // the configuration's name, for saving
	saved    []string
	sel      int // selected saved configuration, or -1
	tab      int

	status    string
	statusErr bool

	chooser chan fileChoice // an open file dialog's result
	confirm string          // "delete": waiting for a second click

	// The Kickstart ROM chosen, identified (romInfo for romPath).
	romPath string
	romInfo amiga.ROMInfo
	romErr  error
}

// rom identifies the chosen Kickstart ROM, reading it again only when the
// path changes.
func (l *launcher) rom() (amiga.ROMInfo, error) {
	if l.s.ROM != l.romPath {
		l.romPath = l.s.ROM
		l.romInfo, l.romErr = amiga.ROMInfo{}, nil
		if l.s.ROM != "" {
			data, err := amiga.LoadROM(l.s.ROM)
			if err != nil {
				l.romErr = err
			} else {
				l.romInfo = amiga.IdentifyROM(data)
			}
		}
	}
	return l.romInfo, l.romErr
}

// romMismatch says why the settings can't run the chosen ROM, or "".
func (l *launcher) romMismatch() string {
	r, err := l.rom()
	if err != nil {
		return ""
	}
	return r.Mismatch(l.s.CPU, l.s.Motherboard)
}

// applyPreset applies the named model's settings (the ROM and disks stay).
func (l *launcher) applyPreset(name string) {
	for _, p := range presets {
		if p.name == name {
			p.apply(&l.s)
		}
	}
}

type fileChoice struct {
	target string // what the file is for: "rom", "df0".."df3", "hdf", "shots", "states"
	path   string
	err    error
}

func newLauncher(s config.Settings, name string) *launcher {
	l := &launcher{ui: newUI(), s: s, name: name, sel: -1}
	l.refreshSaved()
	return l
}

// startupLauncher opens with the configuration used last, or the
// settings file if there is one, or the defaults.
func startupLauncher() *launcher {
	if last := config.LastUsed(); last != "" {
		if s, err := config.Load(config.SavedPath(last)); err == nil {
			l := newLauncher(*s, last)
			l.say(false, "Loaded %s, the configuration used last", last)
			return l
		}
	}
	if conf := config.Find(""); conf != "" {
		if s, err := config.Load(conf); err == nil {
			l := newLauncher(*s, "")
			l.say(false, "Settings from %s", conf)
			return l
		}
	}
	l := newLauncher(config.Defaults(), "")
	if len(l.saved) == 0 {
		l.say(false, "Choose a Kickstart ROM on the Machine tab, then Start (and Save, to keep it)")
	}
	return l
}

func (l *launcher) refreshSaved() {
	l.saved = config.Saved()
	l.sel = slices.Index(l.saved, l.name)
}

func (l *launcher) say(isErr bool, format string, args ...any) {
	l.status, l.statusErr = fmt.Sprintf(format, args...), isErr
}

// update lays out the screen and handles input. It returns the settings
// to start with, or quit.
func (l *launcher) update() (start *config.Settings, quit bool) {
	u := l.ui
	u.begin()
	l.pollChooser()

	u.fill(0, 0, launcherW, launcherH, uiGrey)
	// Title bar.
	u.fill(0, 0, launcherW, 26, uiBlue)
	u.textFace(12, 4, "goAmiga — Configuration", uiWhite, u.bold)
	u.fill(0, 26, launcherW, 1, uiBlack)

	if l.leftPanel() {
		start = &l.s
	}
	l.tabs()

	// Status line and the big buttons.
	u.fill(10, 486, launcherW-20, 1, uiDim)
	u.fill(10, 487, launcherW-20, 1, uiWhite)
	col := uiBlack
	if l.statusErr {
		col = uiRed
	}
	lines := u.wrap(l.status, 560)
	for i, ln := range lines[:min(len(lines), 3)] {
		u.text(16, 496+i*18, ln, col)
	}
	if u.button(launcherW-230, 500, 110, 40, "Start") || (u.enter && u.focus == "") {
		start = &l.s
	}
	if u.button(launcherW-110, 500, 100, 40, "Quit") {
		quit = true
	}
	if start != nil {
		if m := l.romMismatch(); m != "" {
			l.say(true, "%s. Click its model on the Machine tab, then Start.", m)
			l.tab = 0
			return nil, quit
		}
		if l.name != "" && slices.Contains(l.saved, l.name) {
			config.SetLastUsed(l.name)
		}
	}
	return start, quit
}

// leftPanel is the list of saved configurations. It reports a double
// click (load and start).
func (l *launcher) leftPanel() bool {
	u := l.ui
	x, w := 12, 220
	u.heading(x, 36, w, "Saved configurations")
	prev := l.sel
	activated := u.list(x, 62, w, 290, l.saved, &l.sel)
	if l.sel != prev || activated {
		l.load(l.saved[l.sel])
	}
	u.text(x, 362, "Name", uiBlack)
	u.textField("name", x+46, 359, w-46, &l.name)
	if u.button(x, 392, 70, 26, "Save") {
		l.save()
	}
	if u.button(x+75, 392, 70, 26, "New") {
		l.s, l.name, l.sel = config.Defaults(), "", -1
		l.say(false, "New configuration: the default A500 settings")
	}
	delLabel := "Delete"
	if l.confirm == "delete" {
		delLabel = "Sure?"
	}
	if u.buttonOn(x+150, 392, 70, 26, delLabel, l.sel >= 0) {
		if l.confirm == "delete" {
			l.delete()
		} else {
			l.confirm = "delete"
		}
	} else if u.clicked {
		l.confirm = ""
	}
	u.text(x, 428, "Double-click a configuration", uiDim)
	u.text(x, 446, "to start it.", uiDim)
	return activated
}

func (l *launcher) load(name string) {
	s, err := config.Load(config.SavedPath(name))
	if err != nil {
		l.say(true, "%v", err)
		return
	}
	l.s, l.name = *s, name
	l.say(false, "Loaded %s", name)
}

func (l *launcher) save() {
	if err := config.ValidName(l.name); err != nil {
		l.say(true, "%v", err)
		return
	}
	if err := l.s.Write(config.SavedPath(l.name)); err != nil {
		l.say(true, "Saving: %v", err)
		return
	}
	l.refreshSaved()
	l.say(false, "Saved %s (start it from the command line with --config %q)", l.name, l.name)
}

func (l *launcher) delete() {
	name := l.saved[l.sel]
	if err := os.Remove(config.SavedPath(name)); err != nil {
		l.say(true, "Deleting: %v", err)
		return
	}
	l.confirm = ""
	if l.name == name {
		l.name = ""
	}
	l.refreshSaved()
	l.say(false, "Deleted %s", name)
}

// ---- Tabs ----

func (l *launcher) tabs() {
	u := l.ui
	x0, y0 := 250, 36
	x := x0
	for i, name := range launcherTabs {
		w := u.textWidth(name) + 24
		if i == l.tab {
			u.fill(x, y0, w, 28, uiGrey)
			u.fill(x, y0, w, 1, uiWhite)
			u.fill(x, y0, 1, 28, uiWhite)
			u.fill(x+w-1, y0, 1, 28, uiBlack)
			u.textFace(x+12, y0+5, name, uiBlack, u.bold)
		} else {
			if u.hit(x, y0+3, w, 25) && u.clicked {
				l.tab = i
			}
			u.fill(x, y0+3, w, 25, uiDarker)
			u.bevel(x, y0+3, w, 25, true)
			u.text(x+12, y0+7, name, uiBlack)
		}
		x += w + 2
	}
	u.fill(x0, y0+28, launcherW-x0-10, 1, uiWhite)
	box := func() { u.bevel(x0, y0+28, launcherW-x0-10, 410, true) }
	box()
	cx, cy := x0+150, y0+48
	switch launcherTabs[l.tab] {
	case "Machine":
		l.machineTab(cx, cy)
	case "Memory":
		l.memoryTab(cx, cy)
	case "Floppies":
		l.floppyTab(cx, cy)
	case "Hard disks":
		l.hardDiskTab(cx, cy)
	case "Ports":
		l.portsTab(cx, cy)
	case "WHDLoad":
		l.whdloadTab(cx, cy)
	case "Display":
		l.displayTab(cx, cy)
	}
}

// cycleOf shows a cycle gadget over values with their labels.
func cycleOf[T comparable](u *ui, x, y, w int, labels []string, values []T, v *T) {
	cur := slices.Index(values, *v)
	if cur < 0 {
		cur = 0
	}
	if i := u.cycle(x, y, w, labels, cur); i != cur || slices.Index(values, *v) < 0 {
		*v = values[i]
	}
}

// fileRow shows a path with Choose and Clear buttons.
func (l *launcher) fileRow(x, y, w int, target string, path *string, filters zenity.FileFilters, dir bool) {
	u := l.ui
	u.fill(x, y, w, 22, uiDarker)
	u.bevel(x, y, w, 22, false)
	shown := *path
	switch {
	case shown == "" && dir && target != "tape", shown == ".":
		shown = "(the folder goAmiga starts in)"
	case shown == "":
		shown = "(none)"
	}
	u.text(x+6, y+3, u.fit(shown, w-12), uiBlack)
	if u.button(x+w+6, y, 74, 22, "Choose…") {
		l.choose(target, *path, filters, dir)
	}
	if u.buttonOn(x+w+84, y, 56, 22, "Clear", *path != "") {
		*path = ""
	}
}

var (
	romFilter  = zenity.FileFilters{{Name: "Kickstart ROMs", Patterns: []string{"*.rom", "*.bin"}, CaseFold: true}, {Name: "All files", Patterns: []string{"*"}}}
	diskFilter = zenity.FileFilters{{Name: "Amiga disk images", Patterns: []string{"*.adf", "*.ipf", "*.zip", "*.adz", "*.gz"}, CaseFold: true}, {Name: "All files", Patterns: []string{"*"}}}
	hdfFilter  = zenity.FileFilters{{Name: "Hardfiles", Patterns: []string{"*.hdf", "*.hdz"}, CaseFold: true}, {Name: "All files", Patterns: []string{"*"}}}
)

// presets set up the common models (the ROM stays as chosen).
var presets = []struct {
	name  string
	apply func(s *config.Settings)
}{
	{"A500", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ocs", 512, 512, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "off", false, "none", 0
	}},
	{"A500+", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ecs", 1024, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "off", false, "none", 0
	}},
	{"A600", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "auto", "ecs", 1024, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	{"A1200", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "auto", "aga", 2048, 0, 0
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	{"A1200 + fast", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "68882", "aga", 2048, 0, 8192
		s.Gayle, s.ClockY2K, s.Motherboard, s.Z3RAM = "on", false, "none", 0
	}},
	// The A3000's processor and chipset: what Amiga Unix needs (its disk
	// goes on the A2091 at SCSI ID 6). The real machine's built-in SCSI
	// and 32-bit memory aren't emulated.
	{"A3000", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68030", 0, "68882", "ecs", 1024, 0, 8192
		s.Gayle, s.Motherboard, s.Z3RAM = "off", "none", 0
		s.Clock, s.ClockY2K = true, true // AMIX's date wants a year before 2000
	}},
	// The A4000/040: its motherboard (Ramsey, 16 MB of 32-bit RAM, the
	// IDE interface) and Zorro III. It needs the A4000 Kickstart.
	{"A4000", func(s *config.Settings) {
		s.RTG, s.CPUSlotRAM = 0, 0
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68040", 0, "auto", "aga", 2048, 0, 0
		s.Gayle, s.Motherboard, s.MBRAM, s.Z3RAM = "auto", "a4000", 16384, 0
		s.Clock, s.ClockY2K = true, false
	}},
}

func (l *launcher) machineTab(x, y int) {
	u := l.ui
	u.label(x-10, y, "Model")
	bx := x
	for _, p := range presets {
		w := u.textWidth(p.name) + 20
		if bx+w > launcherW-20 { // wrap onto a second row
			bx, y = x, y+30
		}
		if u.button(bx, y, w, 24, p.name) {
			p.apply(&l.s)
			if p.name == "A3000" {
				l.say(false, "A3000 settings: for Amiga Unix, add its disk as a SCSI disk (ID 6) on the Hard disks tab")
			} else if p.name == "A4000" {
				l.say(false, "A4000 settings: choose the A4000 Kickstart ROM; IDE drives go on its own interface")
			} else {
				l.say(false, "%s settings (choose a matching Kickstart ROM)", p.name)
			}
		}
		bx += w + 6
	}
	y += 40
	u.label(x-10, y, "Kickstart ROM")
	l.fileRow(x, y, 260, "rom", &l.s.ROM, romFilter, false)
	// What the ROM is, and a warning (with the fix) if these settings
	// can't run it.
	if r, err := l.rom(); err != nil {
		u.text(x, y+28, u.fit(err.Error(), 390), uiRed)
	} else if l.s.ROM != "" {
		if r.Mismatch(l.s.CPU, l.s.Motherboard) != "" {
			model := map[string]string{"a3000": "A3000", "a4000": "A4000"}[r.Machine]
			msg := "Kickstart " + r.Release() + " for the " + model + ":"
			u.text(x, y+29, msg, uiRed)
			label := "Use " + model + " settings"
			if u.button(x+u.textWidth(msg)+8, y+26, u.textWidth(label)+20, 22, label) {
				l.applyPreset(model)
				l.say(false, "%s settings, for the %s Kickstart", model, model)
			}
		} else {
			u.text(x, y+28, r.String(), uiDim)
		}
	}
	y += 58
	u.label(x-10, y, "CPU")
	cycleOf(u, x, y, 200, []string{"68000 (A500, A600)", "68020 (A1200)", "68030 (A3000)", "68040 (A4000)"}, []string{"68000", "68020", "68030", "68040"}, &l.s.CPU)
	y += 34
	u.label(x-10, y, "CPU speed")
	cycleOf(u, x, y, 200, []string{"Model's own", "100% (7 MHz)", "150%", "200%", "250% (A1200)", "300%", "400%", "800%", "1000% (A4000)", "1600%"},
		[]int{0, 100, 150, 200, 250, 300, 400, 800, 1000, 1600}, &l.s.CPUSpeed)
	// Exact chip bus timing (--exact-bus): only a 68000 at its own speed
	// uses it.
	if u.checkbox(x+214, y+3, "Exact bus (68000)", &l.s.ExactBus) {
		if l.s.ExactBus {
			l.say(false, "Exact bus timing: a 68000 at 7 MHz has its chip accesses timed one by one against the Copper and DMA")
		} else {
			l.say(false, "Bus timing forecast: faster, but a 68000's accesses are timed roughly")
		}
	}
	y += 34
	u.label(x-10, y, "FPU")
	cycleOf(u, x, y, 200, []string{"Auto", "None", "68881", "68882"}, []string{"auto", "none", "68881", "68882"}, &l.s.FPU)
	if l.s.FPU != "none" && l.s.FPU != "auto" && l.s.CPU == "68000" {
		u.text(x+210, y+3, "needs a 68020 or 68030", uiRed)
	} else if l.s.CPU == "68040" {
		u.text(x+210, y+3, map[bool]string{true: "68LC040: no FPU", false: "the 68040's own"}[l.s.FPU == "none"], uiDim)
	}
	y += 34
	u.label(x-10, y, "Chipset")
	cycleOf(u, x, y, 260, []string{"Automatic", "OCS (A500)", "ECS Agnus (A500 1 MB)", "ECS (A500+, A600)", "AGA (A1200)"},
		[]string{"", "ocs", "ecs-agnus", "ecs", "aga"}, &l.s.Chipset)
	y += 34
	u.label(x-10, y, "Video")
	cycleOf(u, x, y, 200, []string{"PAL (50 Hz)", "NTSC (60 Hz)"}, []string{"pal", "ntsc"}, &l.s.Video)
	y += 34
	u.label(x-10, y, "Gayle (IDE)")
	cycleOf(u, x, y, 260, []string{"Automatic (with AGA)", "Fitted (A600, A1200)", "Not fitted"},
		[]string{"auto", "on", "off"}, &l.s.Gayle)
	y += 34
	u.label(x-10, y, "Motherboard")
	cycleOf(u, x, y, 260, []string{"None", "A3000 (Ramsey, 32-bit RAM)", "A4000 (with its IDE)"},
		[]string{"none", "a3000", "a4000"}, &l.s.Motherboard)
	if l.s.Motherboard != "none" && l.s.Motherboard != "" && (l.s.CPU == "68000" || l.s.CPU == "68020") {
		u.text(x+270, y+3, "needs a 68030/040", uiRed)
	}
}

func (l *launcher) memoryTab(x, y int) {
	u := l.ui
	u.label(x-10, y, "Chip RAM")
	cycleOf(u, x, y, 220, []string{"Automatic", "512 KB", "1 MB", "2 MB (AGA)"}, []int{0, 512, 1024, 2048}, &l.s.ChipRAM)
	y += 34
	u.label(x-10, y, "Slow RAM")
	cycleOf(u, x, y, 220, []string{"None", "512 KB", "1 MB", "1.5 MB"}, []int{0, 512, 1024, 1536}, &l.s.SlowRAM)
	y += 34
	u.label(x-10, y, "Fast RAM")
	cycleOf(u, x, y, 220, []string{"None", "64 KB", "128 KB", "256 KB", "512 KB", "1 MB", "2 MB", "4 MB", "8 MB"},
		[]int{0, 64, 128, 256, 512, 1024, 2048, 4096, 8192}, &l.s.FastRAM)
	if l.s.Motherboard != "none" && l.s.Motherboard != "" {
		y += 34
		u.label(x-10, y, "Motherboard RAM")
		cycleOf(u, x, y, 220, []string{"None", "1 MB", "2 MB", "4 MB", "8 MB", "16 MB"},
			[]int{0, 1024, 2048, 4096, 8192, 16384}, &l.s.MBRAM)
		y += 34
		u.label(x-10, y, "CPU-slot RAM")
		cycleOf(u, x, y, 220, []string{"None", "16 MB", "32 MB", "64 MB", "128 MB"},
			[]int{0, 16, 32, 64, 128}, &l.s.CPUSlotRAM)
		y += 34
		u.label(x-10, y, "Zorro III RAM")
		cycleOf(u, x, y, 220, []string{"None", "16 MB", "32 MB", "64 MB", "128 MB", "256 MB"},
			[]int{0, 16, 32, 64, 128, 256}, &l.s.Z3RAM)
	}
	y += 34
	u.label(x-10, y, "RTG card")
	if l.s.Motherboard != "none" && l.s.Motherboard != "" {
		if l.s.RTG > 0 && l.s.RTG < 16 {
			l.s.RTG = 16
		}
		cycleOf(u, x, y, 220, []string{"None", "16 MB (Zorro III)", "32 MB (Zorro III)", "64 MB (Zorro III)"},
			[]int{0, 16, 32, 64}, &l.s.RTG)
	} else {
		if l.s.RTG > 4 {
			l.s.RTG = 4
		}
		cycleOf(u, x, y, 220, []string{"None", "1 MB (Zorro II)", "2 MB (Zorro II)", "4 MB (Zorro II)"},
			[]int{0, 1, 2, 4}, &l.s.RTG)
		if l.s.RTG > 0 && l.s.FastRAM+l.s.RTG*1024 > 8192 {
			u.text(x+230, y+3, "too much with the fast RAM", uiRed)
		}
	}
	y += 50
	for _, ln := range u.wrap("Chip RAM is shared with the custom chips; slow RAM is the A500 trapdoor "+
		"expansion at $C00000; fast RAM is a Zorro II board. Some old games refuse to run with fast "+
		"RAM under Kickstart 1.3 — turn it off for them. The RTG card is the uaegfx graphics card "+
		"for Picasso96: install Picasso96 with its uaegfx driver.", 400) {
		u.text(x-130, y, ln, uiDim)
		y += 18
	}
}

func (l *launcher) floppyTab(x, y int) {
	u := l.ui
	for n := 0; n < 4; n++ {
		u.label(x-10, y, fmt.Sprintf("DF%d:", n))
		first := ""
		if len(l.s.Drives[n]) > 0 {
			first = l.s.Drives[n][0]
		}
		path := first
		l.fileRow(x, y, 260, "df"+strconv.Itoa(n), &path, diskFilter, false)
		if path != first { // cleared
			l.s.Drives[n] = nil
		}
		if k := len(l.s.Drives[n]); k > 1 {
			u.text(x, y+24, fmt.Sprintf("and %d more disk(s) for Cmd+F11", k-1), uiDim)
		}
		y += 46
	}
	u.label(x-10, y, "Drives")
	cycleOf(u, x, y, 220, []string{"As many as have disks", "1 (DF0:)", "2", "3", "4"}, []int{0, 1, 2, 3, 4}, &l.s.NumDrives)
	y += 36
	u.checkbox(x, y, "Write-protect all disks (the image files are never changed)", &l.s.WriteProtect)
	y += 36
	u.text(x-130, y, "A zip of several disks inserts the boot disk; the rest go to Cmd+F11.", uiDim)
}

func (l *launcher) hardDiskTab(x, y int) {
	u := l.ui
	// IDE drives on Gayle, booted by Kickstart's own scsi.device.
	for i, name := range []string{"IDE master", "IDE slave"} {
		u.label(x-10, y, name)
		path := ""
		if i < len(l.s.IDE) {
			path = l.s.IDE[i]
		}
		was := path
		l.fileRow(x, y, 260, "ide"+strconv.Itoa(i), &path, hdfFilter, false)
		if path != was { // cleared
			l.s.IDE = slices.Delete(slices.Clone(l.s.IDE), i, i+1)
		}
		y += 30
		if i == 0 && len(l.s.IDE) == 0 {
			break // a slave only once there's a master
		}
	}
	u.text(x, y, "IDE drives need a Rigid Disk Block; they fit Gayle.", uiDim)
	y += 26
	// A tape drive on the A2091 at SCSI ID 4, where Amiga Unix looks.
	u.label(x-10, y, "Tape (SCSI 4)")
	tape := ""
	if len(l.s.Tape) > 2 && l.s.Tape[1] == ':' {
		tape = l.s.Tape[2:]
	}
	was := tape
	l.fileRow(x, y, 260, "tape", &tape, nil, true)
	if tape != was { // cleared
		l.s.Tape = ""
	}
	y += 34
	x -= 130
	u.text(x, y, "Hardfiles, host folders and SCSI disks", uiBlack)
	y += 26
	row := func(label string, remove func()) {
		u.fill(x, y, 400, 22, uiDarker)
		u.bevel(x, y, 400, 22, false)
		u.text(x+6, y+3, u.fit(label, 388), uiBlack)
		if u.button(x+406, y, 70, 22, "Remove") {
			remove()
		}
		y += 28
	}
	for i := 0; i < len(l.s.Hardfiles); i++ {
		row(fmt.Sprintf("Unit %d: %s", i, l.s.Hardfiles[i]), func() {
			l.s.Hardfiles = slices.Delete(l.s.Hardfiles, i, i+1)
			i--
		})
	}
	for i := 0; i < len(l.s.Dirs); i++ {
		vol, p := config.SplitDir(l.s.Dirs[i])
		if vol == "" {
			vol = filepath.Base(p)
		}
		row(fmt.Sprintf("Folder %s: %s", vol, p), func() {
			l.s.Dirs = slices.Delete(l.s.Dirs, i, i+1)
			i--
		})
	}
	for i := 0; i < len(l.s.SCSI); i++ {
		v := l.s.SCSI[i]
		label := v
		if len(v) > 2 && v[1] == ':' {
			label = fmt.Sprintf("SCSI ID %c: %s", v[0], v[2:])
		}
		row(label, func() {
			l.s.SCSI = slices.Delete(l.s.SCSI, i, i+1)
			i--
		})
	}
	rows := len(l.s.Hardfiles) + len(l.s.Dirs) + len(l.s.SCSI)
	if rows < 7 {
		if u.button(x, y, 120, 24, "Add hardfile…") {
			l.choose("hdf", "", hdfFilter, false)
		}
		if u.button(x+128, y, 120, 24, "Add folder…") {
			l.choose("dir", "", nil, true)
		}
		if u.button(x+256, y, 130, 24, "Add SCSI disk…") {
			l.choose("scsi", "", hdfFilter, false)
		}
	}
	y += 34
	if rows > 5 {
		return
	}
	for _, ln := range u.wrap("Plain hardfiles and RDB images both work here. A new, empty hardfile "+
		"can be made with --hdf-create from the command line and formatted from Workbench. "+
		"A folder appears as a volume named after it, and boots if it has S/Startup-Sequence. "+
		"SCSI disks go on an A2091 for Amiga Unix (the first at ID 6, where it looks for its "+
		"root); AmigaOS sees them too.", 520) {
		u.text(x, y, ln, uiDim)
		y += 18
	}
}

var whdloadFilter = zenity.FileFilters{{Name: "WHDLoad games", Patterns: []string{"*.lha", "*.lzh", "*.slave"}, CaseFold: true}, {Name: "All files", Patterns: []string{"*"}}}

// whdloadTab sets a WHDLoad game to boot straight into.
func (l *launcher) whdloadTab(x, y int) {
	u := l.ui
	u.label(x-10, y, "Game")
	u.fill(x, y, 300, 22, uiDarker)
	u.bevel(x, y, 300, 22, false)
	shown := l.s.WHDLoad
	if shown == "" {
		shown = "(none: the machine boots as usual)"
	}
	u.text(x+6, y+3, u.fit(shown, 288), uiBlack)
	y += 28
	if u.button(x, y, 100, 22, "Archive…") {
		l.choose("whdload", l.s.WHDLoad, whdloadFilter, false)
	}
	if u.button(x+106, y, 100, 22, "Folder…") {
		l.choose("whdloaddir", l.s.WHDLoad, nil, true)
	}
	if u.buttonOn(x+212, y, 70, 22, "Clear", l.s.WHDLoad != "") {
		l.s.WHDLoad = ""
	}
	y += 38

	// What the machine has for WHDLoad.
	if r, err := l.rom(); err == nil && l.s.ROM != "" && r.Version < 37 {
		u.text(x-130, y, u.fit("WHDLoad needs Kickstart 2.0 or later: this ROM is "+r.String()+".", 520), uiRed)
	} else if err == nil && l.s.ROM != "" {
		u.text(x-130, y, u.fit("Kickstart: "+r.String()+", which WHDLoad runs on.", 520), uiBlack)
	}
	y += 22
	if l.kicksFor != l.s.ROM { // read the ROM folder once, not every frame
		l.kicksFor = l.s.ROM
		l.kicks = slices.Sorted(maps.Keys(whdload.KickImages(whdload.ROMsBeside(l.s.ROM))))
	}
	names := l.kicks
	if len(names) > 0 {
		u.text(x-130, y, u.fit("Kickstart images for games that need one: "+strings.Join(names, ", "), 520), uiBlack)
	} else {
		u.text(x-130, y, u.fit("No Kickstart 1.3 or 3.1 images beside the ROM: games that emulate one won't start.", 520), uiDim)
	}
	y += 22
	home := filepath.Join(config.Dir(), "whdload")
	if whdload.Installed(home) {
		u.text(x-130, y, u.fit("WHDLoad: in "+home, 520), uiBlack)
	} else {
		u.text(x-130, y, u.fit("WHDLoad: downloaded from whdload.de the first time a game starts.", 520), uiBlack)
	}
	y += 36
	for _, ln := range u.wrap("A WHDLoad game boots straight in, with no Workbench needed: its .lha "+
		"archive (unpacked once into "+filepath.Join(home, "games")+", where its saves and high "+
		"scores stay), its folder or its .slave. Dropping an .lha on the running Amiga starts it too. "+
		"F10 quits the game. Most games want fast RAM and many AGA: the A1200 + fast preset suits "+
		"them, with a Kickstart 3.x ROM.", 520) {
		u.text(x-130, y, ln, uiDim)
		y += 18
	}
}

func (l *launcher) portsTab(x, y int) {
	u := l.ui
	mode, addr := "", ""
	if m, a, ok := strings.Cut(l.s.Serial, ":"); ok {
		mode, addr = m, a
	}
	u.label(x-10, y, "Serial port")
	newMode := mode
	cycleOf(u, x, y, 220, []string{"Not connected", "Telnet (a terminal)", "Raw TCP"}, []string{"", "telnet", "tcp"}, &newMode)
	if newMode != mode && addr == "" {
		addr = "2323"
	}
	if newMode != "" {
		u.text(x+232, y+3, "port", uiBlack)
		u.textField("serialport", x+270, y, 120, &addr)
		y += 26
		host := addr
		if !strings.Contains(host, ":") {
			host = "localhost " + host
		} else {
			host = strings.Replace(host, ":", " ", 1)
		}
		cmd := "telnet " + host
		if newMode == "tcp" {
			cmd = "nc " + host
		}
		u.text(x, y+2, "Connect with: "+cmd, uiDim)
		y += 18
		u.text(x, y+2, "On the Amiga, NewShell AUX: starts a shell there.", uiDim)
	}
	l.s.Serial = ""
	if newMode != "" {
		l.s.Serial = newMode + ":" + addr
	}
	y += 40
	u.label(x-10, y, "Network")
	u.checkbox(x, y, "bsdsocket.library", &l.s.BSDSocket)
	y += 22
	u.text(x, y, "Amiga TCP/IP software uses this computer's network.", uiDim)
	y += 26
	u.checkbox(x, y, "A2065 Ethernet (Amiga Unix)", &l.s.A2065)
	y += 22
	for _, ln := range u.wrap("10.0.2.15, through this computer; telnet to localhost 2323 reaches it.", 400) {
		u.text(x, y, ln, uiDim)
		y += 18
	}
	y += 18
	u.label(x-10, y, "Joystick")
	u.checkbox(x, y, "Arrow keys and Right Ctrl/Alt (Ctrl+F12 toggles)", &l.s.JoyKeys)
	y += 22
	u.text(x, y, "A gamepad, if connected, is always the joystick.", uiDim)
	y += 26
	u.checkbox(x, y, "Gamepad as an analogue joystick", &l.s.JoyAnalog)
	y += 22
	u.text(x, y, "Proportional, for the few games that read one.", uiDim)
	y += 34
	u.label(x-10, y, "Clipboard")
	u.checkbox(x, y, "Share with this computer", &l.s.Clipboard)
	y += 22
	for _, ln := range u.wrap("Copy on either side, paste on the other. Needs Kickstart 2.0+ and a hard "+
		"disk or folder (goAmiga's helper lives on its board).", 400) {
		u.text(x, y, ln, uiDim)
		y += 18
	}
}

func (l *launcher) displayTab(x, y int) {
	u := l.ui
	u.label(x-10, y, "Window size")
	cycleOf(u, x, y, 200, []string{"1×", "2×", "3×", "4×"}, []int{1, 2, 3, 4}, &l.s.Scale)
	y += 36
	u.checkbox(x, y, "Show the overscan area (720×284)", &l.s.Overscan)
	y += 30
	u.checkbox(x, y, "Status bar with the activity lights", &l.s.StatusBar)
	y += 30
	u.checkbox(x, y, "Start in warp mode (Cmd+W toggles)", &l.s.Warp)
	y += 30
	u.checkbox(x, y, "No sound", &l.s.Mute)
	y += 40
	u.label(x-10, y, "Screenshots")
	l.fileRow(x, y, 260, "shots", &l.s.ScreenshotDir, nil, true)
	y += 34
	u.label(x-10, y, "Save states")
	l.fileRow(x, y, 260, "states", &l.s.StateDir, nil, true)
}

// ---- File dialogs ----

func (l *launcher) choose(target, current string, filters zenity.FileFilters, dir bool) {
	if l.chooser != nil {
		return
	}
	start := ""
	if current != "" {
		start = current
		if !dir {
			start = filepath.Dir(current) + string(filepath.Separator)
		}
	}
	ch := make(chan fileChoice, 1)
	l.chooser = ch
	go func() {
		opts := []zenity.Option{zenity.Filename(start)}
		title := map[string]string{"rom": "Choose a Kickstart ROM", "hdf": "Choose a hardfile",
			"dir":  "Choose a folder to use as an Amiga drive",
			"scsi": "Choose a hardfile for the SCSI disk",
			"tape": "Choose the folder holding the tape's files",
			"ide0": "Hardfile for the IDE master", "ide1": "Hardfile for the IDE slave",
			"shots": "Folder for screenshots", "states": "Folder for save states",
			"whdload": "Choose a WHDLoad game (.lha or .slave)", "whdloaddir": "Choose a WHDLoad game's folder"}[target]
		if strings.HasPrefix(target, "df") {
			title = "Disk for " + strings.ToUpper(target) + ":"
		}
		opts = append(opts, zenity.Title(title))
		if dir {
			opts = append(opts, zenity.Directory())
		} else if filters != nil {
			opts = append(opts, filters)
		}
		p, err := zenity.SelectFile(opts...)
		ch <- fileChoice{target, p, err}
	}()
}

func (l *launcher) pollChooser() {
	if l.chooser == nil {
		return
	}
	var c fileChoice
	select {
	case c = <-l.chooser:
	default:
		return
	}
	l.chooser = nil
	switch {
	case c.err == zenity.ErrCanceled:
		return
	case c.err != nil:
		l.say(true, "File dialog: %v", c.err)
		return
	}
	switch {
	case c.target == "rom":
		l.s.ROM = c.path
	case strings.HasPrefix(c.target, "df"):
		n := int(c.target[2] - '0')
		l.s.Drives[n] = config.List{c.path}
	case c.target == "hdf":
		l.s.Hardfiles = append(l.s.Hardfiles, c.path)
	case c.target == "dir":
		l.s.Dirs = append(l.s.Dirs, c.path)
	case c.target == "tape":
		l.s.Tape = "4:" + c.path
	case c.target == "scsi":
		l.s.SCSI = append(l.s.SCSI, fmt.Sprintf("%d:%s", freeSCSIID(l.s.SCSI), c.path))
	case strings.HasPrefix(c.target, "ide"):
		if i := int(c.target[3] - '0'); i < len(l.s.IDE) {
			l.s.IDE[i] = c.path
		} else {
			l.s.IDE = append(l.s.IDE, c.path)
		}
	case c.target == "shots":
		l.s.ScreenshotDir = c.path
	case c.target == "states":
		l.s.StateDir = c.path
	case c.target == "whdload", c.target == "whdloaddir":
		l.s.WHDLoad = c.path
	}
}

func (l *launcher) draw(screen *ebiten.Image) {
	screen.Fill(uiGrey) // the margins round the design, when the window's shape differs
	l.ui.draw(screen)
}

// freeSCSIID is the ID for a new SCSI disk: 6 (the A3000's internal
// drive, where Amiga Unix looks for its root) if free, else the lowest
// free one.
func freeSCSIID(disks []string) int {
	used := map[byte]bool{}
	for _, d := range disks {
		if len(d) > 1 {
			used[d[0]] = true
		}
	}
	if !used['6'] {
		return 6
	}
	for id := byte('0'); id <= '5'; id++ {
		if !used[id] {
			return int(id - '0')
		}
	}
	return 6
}
