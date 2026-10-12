//go:build !noebitengine

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/ncruces/zenity"

	"github.com/andrewsjg/goamiga/internal/config"
)

// The configuration screen: the saved configurations on the left, the
// settings in tabs on the right, Start and Quit at the bottom.

const (
	launcherW = 820
	launcherH = 560
)

var launcherTabs = []string{"Machine", "Memory", "Floppies", "Hard disks", "Ports", "Display"}

type launcher struct {
	ui    *ui
	s     config.Settings
	name  string // the configuration's name, for saving
	saved []string
	sel   int // selected saved configuration, or -1
	tab   int

	status    string
	statusErr bool

	chooser chan fileChoice // an open file dialog's result
	confirm string          // "delete": waiting for a second click
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
	for i, ln := range lines[:min(len(lines), 2)] {
		u.text(16, 496+i*18, ln, col)
	}
	if u.button(launcherW-230, 500, 110, 40, "Start") || (u.enter && u.focus == "") {
		start = &l.s
	}
	if u.button(launcherW-110, 500, 100, 40, "Quit") {
		quit = true
	}
	if start != nil {
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
	case shown == "" && dir, shown == ".":
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
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "none", "ocs", 512, 512, 0
	}},
	{"A500+", func(s *config.Settings) {
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "none", "ecs", 1024, 0, 0
	}},
	{"A600", func(s *config.Settings) {
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68000", 0, "none", "ecs", 1024, 0, 0
	}},
	{"A1200", func(s *config.Settings) {
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "none", "aga", 2048, 0, 0
	}},
	{"A1200 + fast", func(s *config.Settings) {
		s.CPU, s.CPUSpeed, s.FPU, s.Chipset, s.ChipRAM, s.SlowRAM, s.FastRAM = "68020", 0, "68882", "aga", 2048, 0, 8192
	}},
}

func (l *launcher) machineTab(x, y int) {
	u := l.ui
	u.label(x-10, y, "Model")
	bx := x
	for _, p := range presets {
		w := u.textWidth(p.name) + 20
		if u.button(bx, y, w, 24, p.name) {
			p.apply(&l.s)
			l.say(false, "%s settings (choose a matching Kickstart ROM)", p.name)
		}
		bx += w + 6
	}
	y += 40
	u.label(x-10, y, "Kickstart ROM")
	l.fileRow(x, y, 260, "rom", &l.s.ROM, romFilter, false)
	y += 40
	u.label(x-10, y, "CPU")
	cycleOf(u, x, y, 200, []string{"68000 (A500, A600)", "68020 (A1200)"}, []string{"68000", "68020"}, &l.s.CPU)
	y += 34
	u.label(x-10, y, "CPU speed")
	cycleOf(u, x, y, 200, []string{"Model's own", "100% (7 MHz)", "150%", "200%", "250% (A1200)", "300%", "400%", "800%"},
		[]int{0, 100, 150, 200, 250, 300, 400, 800}, &l.s.CPUSpeed)
	y += 34
	u.label(x-10, y, "FPU")
	cycleOf(u, x, y, 200, []string{"None", "68881", "68882"}, []string{"none", "68881", "68882"}, &l.s.FPU)
	if l.s.FPU != "none" && l.s.CPU != "68020" {
		u.text(x+210, y+3, "needs the 68020", uiRed)
	}
	y += 34
	u.label(x-10, y, "Chipset")
	cycleOf(u, x, y, 260, []string{"Automatic", "OCS (A500)", "ECS Agnus (A500 1 MB)", "ECS (A500+, A600)", "AGA (A1200)"},
		[]string{"", "ocs", "ecs-agnus", "ecs", "aga"}, &l.s.Chipset)
	y += 34
	u.label(x-10, y, "Video")
	cycleOf(u, x, y, 200, []string{"PAL (50 Hz)", "NTSC (60 Hz)"}, []string{"pal", "ntsc"}, &l.s.Video)
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
	y += 50
	for _, ln := range u.wrap("Chip RAM is shared with the custom chips; slow RAM is the A500 trapdoor "+
		"expansion at $C00000; fast RAM is a Zorro II board. Some old games refuse to run with fast "+
		"RAM under Kickstart 1.3 — turn it off for them.", 400) {
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
	x -= 130
	u.text(x, y, "Hardfiles, mounted in order as goamiga.device units 0, 1, ...", uiBlack)
	y += 28
	for i := 0; i < len(l.s.Hardfiles); i++ {
		u.fill(x, y, 400, 22, uiDarker)
		u.bevel(x, y, 400, 22, false)
		u.text(x+6, y+3, u.fit(fmt.Sprintf("%d: %s", i, l.s.Hardfiles[i]), 388), uiBlack)
		if u.button(x+406, y, 70, 22, "Remove") {
			l.s.Hardfiles = slices.Delete(l.s.Hardfiles, i, i+1)
			i--
		}
		y += 28
	}
	if len(l.s.Hardfiles) < 8 && u.button(x, y, 110, 24, "Add hardfile…") {
		l.choose("hdf", "", hdfFilter, false)
	}
	y += 40
	for _, ln := range u.wrap("Plain hardfiles and RDB images both work. A new, empty hardfile can be "+
		"made with --hdf-create from the command line and formatted from Workbench.", 520) {
		u.text(x, y, ln, uiDim)
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
	y += 36
	u.label(x-10, y, "Joystick")
	u.checkbox(x, y, "Arrow keys and Right Ctrl/Alt (Ctrl+F12 toggles)", &l.s.JoyKeys)
	y += 22
	u.text(x, y, "A gamepad, if connected, is always the joystick.", uiDim)
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
			"shots": "Folder for screenshots", "states": "Folder for save states"}[target]
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
	case c.target == "shots":
		l.s.ScreenshotDir = c.path
	case c.target == "states":
		l.s.StateDir = c.path
	}
}

func (l *launcher) draw(screen *ebiten.Image) { l.ui.draw(screen) }
