package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cavaliercoder/goAmiga/amiga"
	"github.com/cavaliercoder/goAmiga/internal/config"
)

func main() {
	romPath := flag.String("rom", "", "Path to Kickstart ROM file")
	// Per-drive disk lists: the first goes in at start, the swap key
	// cycles through the rest.
	var drives [4]diskList
	flag.Var(&drives[0], "df0", "ADF for DF0:; repeat to list more disks (Cmd+F11 swaps to the next, F11 chooses a file)")
	flag.Var(&drives[0], "disk", "same as --df0")
	flag.Var(&drives[1], "df1", "ADF for DF1: (connects the drive); repeat for more")
	flag.Var(&drives[2], "df2", "ADF for DF2: (connects the drive); repeat for more")
	flag.Var(&drives[3], "df3", "ADF for DF3: (connects the drive); repeat for more")
	var hardfiles diskList
	flag.Var(&hardfiles, "hdf", "Hard disk image (.hdf, plain or RDB) as the next unit of goamiga.device; repeat for more")
	hdfCreate := flag.Int("hdf-create", 0, "Create any --hdf file that does not exist, blank, this many MB (format it from the Amiga)")
	numDrives := flag.Int("drives", 0, "Number of drives to connect (1-4), even without disks for them")
	maxCycles := flag.Uint64("cycles", 0, "Maximum CPU cycles to execute (headless mode)")
	headless := flag.Bool("headless", false, "Run without display window")
	writeProtect := flag.Bool("write-protect", false, "Write-protect every drive and hardfile: the Amiga cannot change the image files")
	mute := flag.Bool("mute", false, "No sound")
	chipRAM := flag.Int("chipram", 0, "Chip RAM in KB: 512 (original Agnus), 1024 (8372A Fat Agnus) or 2048 (AGA); default 1024, or 2048 with --chipset aga")
	cpuModel := flag.String("cpu", "68000", "Processor: 68000 (A500) or 68020 (a 68EC020 accelerator)")
	cpuSpeed := flag.Int("cpu-speed", 0, "CPU speed in percent of a 7 MHz 68000 (default 100 for the 68000, 250 for the 68020)")
	fpuModel := flag.String("fpu", "none", "Floating-point coprocessor with --cpu 68020: none, 68881 or 68882")
	chipset := flag.String("chipset", "", "Custom chips: ocs, ecs-agnus (Fat Agnus, original Denise), ecs (A500+/A600) or aga (A1200/A4000); default ecs-agnus with 1 MB chip RAM, else ocs")
	video := flag.String("video", "pal", "Video standard: pal (50 Hz) or ntsc (60 Hz, for American games that need it)")
	slowRAM := flag.Int("slowram", 0, "Slow (trapdoor) RAM at $C00000 in KB: 0, 512, 1024 or 1536")
	fastRAM := flag.Int("fastram", 2048, "Zorro II fast RAM in KB: 0 or a power of two from 64 to 8192")
	overscan := flag.Bool("overscan", false, "Show the overscan area (720x284) instead of the standard 640x256 window")
	scale := flag.Int("scale", 2, "Window size multiplier (the emulated resolution is unchanged); reduced if it doesn't fit the screen")
	statusBar := flag.Bool("statusbar", true, "Show the activity lights (power, drives, hard disk) and warp speed below the picture")
	warp := flag.Bool("warp", false, "Start in warp mode (as fast as possible, no sound); Cmd+W toggles")
	shotDir := flag.String("screenshot-dir", ".", "Folder for Cmd+S screenshots")
	stateDir := flag.String("state-dir", ".", "Folder for save state slots (Cmd+Shift+1-9 save, Cmd+1-9 load)")
	loadState := flag.String("load-state", "", "Resume from a save state file")
	joyKeys := flag.Bool("joy-keys", false, "Start with the arrow keys and Right Ctrl/Alt (fire) as the port 2 joystick; Ctrl+F12 toggles")
	verbose := flag.Bool("v", false, "Enable verbose trace output")
	configPath := flag.String("config", "", "Settings file (key = value per line, keys are these flag names); default ./goamiga.conf or ~/.config/goamiga/goamiga.conf")
	flag.Parse()
	if conf := config.Find(*configPath); conf != "" {
		if err := config.Apply(flag.CommandLine, conf); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Settings from %s\n", conf)
	}

	if *romPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: goamiga --rom <kickstart.rom> [--df0 a.adf ...] [--df1 b.adf ...] [flags]\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Load ROM
	romData, err := amiga.LoadROM(*romPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded ROM: %s (%d bytes)\n", *romPath, len(romData))
	if sum := amiga.ROMChecksum(romData); sum != 0xFFFFFFFF {
		fmt.Fprintf(os.Stderr, "Warning: %s fails the Kickstart checksum ($%08X, want $FFFFFFFF);\n"+
			"  the image is damaged or modified and Kickstart will stop with a red screen.\n", *romPath, sum)
	}

	// Create Amiga
	var logger *os.File
	if *verbose {
		logger = os.Stdout
	}
	a, err := amiga.New(romData, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Amiga: %v\n", err)
		os.Exit(1)
	}

	if *chipRAM == 0 {
		*chipRAM = 1024
		if *chipset == "aga" {
			*chipRAM = 2048
		}
	}
	if err := a.SetChipRAM(*chipRAM); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if *chipset == "" {
		*chipset = "ocs"
		if *chipRAM == 1024 {
			*chipset = "ecs-agnus" // 1 MB of chip RAM needs the 8372A
		}
	}
	if err := a.SetCPU(*cpuModel, *cpuSpeed); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := a.SetFPU(*fpuModel); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := a.SetChipset(*chipset); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := a.SetVideoStandard(*video); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := a.ConfigureMemory(*slowRAM, *fastRAM); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Memory: %d KB chip, %d KB slow, %d KB fast; chipset %s, %s; CPU %s at %d%%\n",
		*chipRAM, *slowRAM, *fastRAM, a.Chipset(), strings.ToUpper(a.VideoStandard()), a.CPU.Model, a.CPUSpeed)

	// Connect drives and load each one's first disk; the rest are for the
	// swap keys. Check every file now so a typo fails at startup.
	for n := 1; n < *numDrives && n < 4; n++ {
		a.Disk.ConnectDrive(n)
	}
	for n, list := range drives {
		for _, p := range list {
			if _, err := os.Stat(p); err != nil {
				fmt.Fprintf(os.Stderr, "Error: DF%d: disk %v\n", n, err)
				os.Exit(1)
			}
		}
		if len(list) == 0 {
			continue
		}
		if err := a.LoadDiskUnit(n, list[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading DF%d: %v\n", n, err)
			os.Exit(1)
		}
		fmt.Printf("DF%d: %s\n", n, list[0])
	}
	a.Disk.SetWriteProtected(*writeProtect)
	if *writeProtect {
		fmt.Println("All drives write-protected")
	} else {
		fmt.Println("Disk writes are saved to the ADF files")
	}

	for _, p := range hardfiles {
		if *hdfCreate > 0 {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := createHardfile(p, *hdfCreate); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				fmt.Printf("Created blank %d MB hardfile %s\n", *hdfCreate, p)
			}
		}
		if err := a.AttachHardfile(p, *writeProtect); err != nil {
			fmt.Fprintf(os.Stderr, "Error: hardfile %v\n", err)
			os.Exit(1)
		}
	}
	if a.HardDisk != nil {
		for u, f := range a.HardDisk.Files {
			var names []string
			for _, p := range f.Partitions {
				names = append(names, p.Name+":")
			}
			fmt.Printf("goamiga.device unit %d: %s (%d MB) as %s\n", u, f.Path, f.Size()>>20, strings.Join(names, " "))
		}
	}

	// Reset
	if err := a.Reset(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during reset: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])
	if *loadState != "" {
		f, err := os.Open(*loadState)
		if err == nil {
			err = a.LoadState(f)
			f.Close()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: load state: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Resumed from %s\n", *loadState)
	}

	// Headless mode: run for a fixed number of cycles (or default 1000)
	if *headless || *maxCycles > 0 {
		cycles := *maxCycles
		if cycles == 0 {
			cycles = 1000
		}
		fmt.Printf("Headless: executing up to %d cycles...\n", cycles)
		if err := a.Run(cycles); err != nil {
			fmt.Printf("Stopped after error: %v\n", err)
		} else {
			fmt.Printf("Execution complete. PC=$%08X\n", a.CPU.PC)
		}
		return
	}

	// Windowed mode: open the Ebitengine display
	fmt.Println("Starting display (50fps PAL)...")
	a.Video.SetOverscan(*overscan)
	err = runDisplay(a, displayOptions{
		mute: *mute, scale: *scale, statusBar: *statusBar, warp: *warp, joyKeys: *joyKeys, drives: drives,
		screenshotDir: *shotDir, stateDir: *stateDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Display error: %v\n", err)
		os.Exit(1)
	}
}

// displayOptions are the window's settings from the command line.
type displayOptions struct {
	mute, statusBar, warp, joyKeys bool
	scale                          int
	drives                         [4]diskList
	screenshotDir, stateDir        string
}

// diskList collects repeated --disk flags.
type diskList []string

func (l *diskList) String() string { return strings.Join(*l, ",") }

func (l *diskList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// createHardfile makes a blank (sparse) image of mb megabytes.
func createHardfile(path string, mb int) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(mb) << 20); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
