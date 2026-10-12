package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Settings is everything that describes a machine and its window: the
// command-line flags, a settings file and a saved configuration are three
// ways of filling it in.
type Settings struct {
	ROM          string
	Drives       [4]List // per drive: the first disk goes in, the rest are for Cmd+F11
	NumDrives    int
	Hardfiles    List
	Dirs         List   // host folders as drives: PATH or VOLUME=PATH
	IDE          List   // IDE drives on Gayle: the master, then the slave
	SCSI         List   // SCSI disks on an A2091: ID:PATH
	Gayle        string // auto (with AGA, as in the A1200), on or off
	HDFCreate    int
	WriteProtect bool

	CPU      string
	CPUSpeed int
	FPU      string
	Chipset  string
	Video    string
	ChipRAM  int
	SlowRAM  int
	FastRAM  int

	Serial    string
	BSDSocket bool
	JoyKeys   bool

	Mute          bool
	Overscan      bool
	Scale         int
	StatusBar     bool
	Warp          bool
	ScreenshotDir string
	StateDir      string
}

// Defaults are the settings with nothing given: an A500 with 1 MB of chip
// RAM and 2 MB of fast RAM.
func Defaults() Settings {
	return Settings{CPU: "68000", FPU: "none", Video: "pal", Gayle: "auto", FastRAM: 2048, Scale: 2, StatusBar: true,
		ScreenshotDir: ".", StateDir: "."}
}

// List is a setting that may be given more than once (df0, hdf).
type List []string

func (l *List) String() string { return strings.Join(*l, ",") }

func (l *List) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// Values returns the entries, for writing one line each.
func (l *List) Values() []string { return *l }

// Register defines the flags in fs, bound to s, with s's current values as
// their defaults.
func (s *Settings) Register(fs *flag.FlagSet) {
	fs.StringVar(&s.ROM, "rom", s.ROM, "Path to Kickstart ROM file")
	fs.Var(&s.Drives[0], "df0", "ADF for DF0:; repeat to list more disks (Cmd+F11 swaps to the next, F11 chooses a file)")
	fs.Var(&s.Drives[0], "disk", "same as --df0")
	fs.Var(&s.Drives[1], "df1", "ADF for DF1: (connects the drive); repeat for more")
	fs.Var(&s.Drives[2], "df2", "ADF for DF2: (connects the drive); repeat for more")
	fs.Var(&s.Drives[3], "df3", "ADF for DF3: (connects the drive); repeat for more")
	fs.Var(&s.Hardfiles, "hdf", "Hard disk image (.hdf, plain or RDB) as the next unit of goamiga.device; repeat for more")
	fs.Var(&s.Dirs, "dir", "Host folder as an Amiga drive: PATH, or VOLUME=PATH to name its volume (else the folder's name); bootable if it has S/Startup-Sequence; repeat for more")
	fs.Var(&s.SCSI, "scsi", "Hardfile as a SCSI disk on an A2091 controller, as ID:PATH (ID 0-6); for Amiga Unix, which drives the A2091 itself (AmigaOS has no driver for it here); repeat for more")
	fs.Var(&s.IDE, "ide", "Hardfile (with a Rigid Disk Block) as an IDE drive on Gayle, booted by Kickstart's own scsi.device: the master, then the slave")
	fs.StringVar(&s.Gayle, "gayle", s.Gayle, "Gayle, the A600/A1200 IDE interface: auto (fitted with the AGA chipset), on or off; --ide fits it")
	fs.IntVar(&s.HDFCreate, "hdf-create", s.HDFCreate, "Create any --hdf file that does not exist, blank, this many MB (format it from the Amiga)")
	fs.IntVar(&s.NumDrives, "drives", s.NumDrives, "Number of drives to connect (1-4), even without disks for them")
	fs.BoolVar(&s.WriteProtect, "write-protect", s.WriteProtect, "Write-protect every drive and hardfile: the Amiga cannot change the image files")
	fs.BoolVar(&s.Mute, "mute", s.Mute, "No sound")
	fs.IntVar(&s.ChipRAM, "chipram", s.ChipRAM, "Chip RAM in KB: 512 (original Agnus), 1024 (8372A Fat Agnus) or 2048 (AGA); default 1024, or 2048 with --chipset aga")
	fs.StringVar(&s.CPU, "cpu", s.CPU, "Processor: 68000 (A500), 68020 (a 68EC020, as in the A1200) or 68030 (with its MMU, as in the A3000)")
	fs.IntVar(&s.CPUSpeed, "cpu-speed", s.CPUSpeed, "CPU speed in percent of a 7 MHz 68000 (default 100 for the 68000, 250 for the 68020, 350 for the 68030)")
	fs.StringVar(&s.FPU, "fpu", s.FPU, "Floating-point coprocessor with --cpu 68020 or 68030: none, 68881 or 68882")
	fs.StringVar(&s.Chipset, "chipset", s.Chipset, "Custom chips: ocs, ecs-agnus (Fat Agnus, original Denise), ecs (A500+/A600) or aga (A1200/A4000); default ecs-agnus with 1 MB chip RAM, else ocs")
	fs.BoolVar(&s.BSDSocket, "bsdsocket", s.BSDSocket, "Fit a network board with bsdsocket.library: Amiga TCP/IP programs use the host's network")
	fs.StringVar(&s.Serial, "serial", s.Serial, "Connect the serial port to a TCP port: telnet:PORT (a terminal; try NewShell AUX: on the Amiga) or tcp:PORT (raw bytes); HOST:PORT to listen beyond this machine")
	fs.StringVar(&s.Video, "video", s.Video, "Video standard: pal (50 Hz) or ntsc (60 Hz, for American games that need it)")
	fs.IntVar(&s.SlowRAM, "slowram", s.SlowRAM, "Slow (trapdoor) RAM at $C00000 in KB: 0, 512, 1024 or 1536")
	fs.IntVar(&s.FastRAM, "fastram", s.FastRAM, "Zorro II fast RAM in KB: 0 or a power of two from 64 to 8192")
	fs.BoolVar(&s.Overscan, "overscan", s.Overscan, "Show the overscan area (720x284) instead of the standard 640x256 window")
	fs.IntVar(&s.Scale, "scale", s.Scale, "Window size multiplier (the emulated resolution is unchanged); reduced if it doesn't fit the screen")
	fs.BoolVar(&s.StatusBar, "statusbar", s.StatusBar, "Show the activity lights (power, drives, hard disk) and warp speed below the picture")
	fs.BoolVar(&s.Warp, "warp", s.Warp, "Start in warp mode (as fast as possible, no sound); Cmd+W toggles")
	fs.StringVar(&s.ScreenshotDir, "screenshot-dir", s.ScreenshotDir, "Folder for Cmd+S screenshots")
	fs.StringVar(&s.StateDir, "state-dir", s.StateDir, "Folder for save state slots (Cmd+Shift+1-9 save, Cmd+1-9 load)")
	fs.BoolVar(&s.JoyKeys, "joy-keys", s.JoyKeys, "Start with the arrow keys and Right Ctrl/Alt (fire) as the port 2 joystick; Ctrl+F12 toggles")
}

// notSaved are flags that describe a run rather than a machine.
var notSaved = map[string]bool{"disk": true, "hdf-create": true}

// Write saves the settings that differ from the defaults as a settings
// file, with absolute paths so that it works from anywhere.
func (s *Settings) Write(path string) error {
	defs := Defaults()
	dfs := flag.NewFlagSet("defaults", flag.ContinueOnError)
	defs.Register(dfs)
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	cp := s.clone()
	cp.Register(fs)
	var b strings.Builder
	b.WriteString("# goAmiga configuration\n")
	fs.VisitAll(func(f *flag.Flag) {
		if notSaved[f.Name] {
			return
		}
		if l, ok := f.Value.(*List); ok {
			for _, v := range l.Values() {
				fmt.Fprintf(&b, "%s = %s\n", f.Name, quote(absPath(f.Name, v)))
			}
			return
		}
		v := f.Value.String()
		if v == dfs.Lookup(f.Name).Value.String() {
			return
		}
		if bv, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bv.IsBoolFlag() && v == "true" {
			fmt.Fprintf(&b, "%s\n", f.Name)
			return
		}
		fmt.Fprintf(&b, "%s = %s\n", f.Name, quote(absPath(f.Name, v)))
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (s *Settings) clone() *Settings {
	c := *s
	for i := range c.Drives {
		c.Drives[i] = append(List(nil), s.Drives[i]...)
	}
	c.Hardfiles = append(List(nil), s.Hardfiles...)
	c.IDE = append(List(nil), s.IDE...)
	c.Dirs = append(List(nil), s.Dirs...)
	c.SCSI = append(List(nil), s.SCSI...)
	return &c
}

func absPath(key, v string) string {
	return mapPath(key, v, func(p string) string {
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return p
	})
}

// mapPath applies fn to the path in a setting's value, if it names a file
// and the path is relative.
func mapPath(key, v string, fn func(string) string) string {
	if !pathKeys[key] {
		return v
	}
	vol, p := "", v
	switch key {
	case "dir":
		vol, p = SplitDir(v)
	case "scsi":
		if len(v) > 2 && v[1] == ':' && v[0] >= '0' && v[0] <= '9' {
			if filepath.IsAbs(v[2:]) {
				return v
			}
			return v[:2] + fn(v[2:])
		}
	}
	if p == "" || filepath.IsAbs(p) {
		return v
	}
	if vol != "" {
		return vol + "=" + fn(p)
	}
	return fn(p)
}

// SplitDir splits a --dir value into the volume name (or "") and the
// folder.
func SplitDir(v string) (volume, path string) {
	if i := strings.IndexByte(v, '='); i > 0 && i <= 30 && !strings.ContainsAny(v[:i], `/\:`) {
		return v[:i], v[i+1:]
	}
	return "", v
}

func quote(v string) string {
	if v == "" || strings.ContainsAny(v, " #\t\"'") {
		return strconv.Quote(v)
	}
	return v
}

// Load reads a settings file over the defaults.
func Load(path string) (*Settings, error) {
	s := Defaults()
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	s.Register(fs)
	if err := Apply(fs, path); err != nil {
		return nil, err
	}
	return &s, nil
}

// Dir is the folder for goAmiga's own files: ~/.config/goamiga.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".goamiga"
	}
	return filepath.Join(home, ".config", "goamiga")
}

// SavedDir holds the saved configurations, one NAME.conf file each.
func SavedDir() string { return filepath.Join(Dir(), "configs") }

// SavedPath is the file for a saved configuration.
func SavedPath(name string) string { return filepath.Join(SavedDir(), name+".conf") }

// Saved lists the saved configurations by name.
func Saved() []string {
	ents, _ := os.ReadDir(SavedDir())
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".conf"); ok && !e.IsDir() {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}

// ValidName checks a name for a saved configuration (it becomes a file
// name).
func ValidName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("the configuration needs a name")
	case strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, "."):
		return fmt.Errorf("%q can't be used as a name", name)
	}
	return nil
}

// LastUsed is the saved configuration started last, or "".
func LastUsed() string {
	b, err := os.ReadFile(filepath.Join(Dir(), "last"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetLastUsed remembers the saved configuration being started.
func SetLastUsed(name string) {
	os.MkdirAll(Dir(), 0o755)
	os.WriteFile(filepath.Join(Dir(), "last"), []byte(name+"\n"), 0o644)
}

// Resolve turns --config's argument into a file: a path as given, or the
// name of a saved configuration.
func Resolve(named string) string {
	if named == "" || strings.ContainsAny(named, `/\`) || strings.HasSuffix(named, ".conf") {
		return named
	}
	if _, err := os.Stat(named); err == nil {
		return named
	}
	if p := SavedPath(named); fileExists(p) {
		return p
	}
	return named
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
