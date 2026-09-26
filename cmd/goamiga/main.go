package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cavaliercoder/goAmiga/amiga"
)

func main() {
	romPath := flag.String("rom", "", "Path to Kickstart ROM file")
	var disks diskList
	flag.Var(&disks, "disk", "ADF floppy image for DF0:; repeat to list more disks, F11 swaps to the next")
	maxCycles := flag.Uint64("cycles", 0, "Maximum CPU cycles to execute (headless mode)")
	headless := flag.Bool("headless", false, "Run without display window")
	writeProtect := flag.Bool("write-protect", false, "Write-protect the floppy: the Amiga cannot change the ADF file")
	mute := flag.Bool("mute", false, "No sound")
	overscan := flag.Bool("overscan", false, "Show the overscan area (720x284) instead of the standard 640x256 window")
	verbose := flag.Bool("v", false, "Enable verbose trace output")
	flag.Parse()

	if *romPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: goamiga --rom <kickstart.rom> [--disk a.adf [--disk b.adf ...]] [flags]\n")
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

	// Load the first disk; the others are for F11 to swap in. Check them
	// all now so a typo fails at startup, not at the swap.
	a.Disk.WriteProtected = *writeProtect
	for _, p := range disks {
		if _, err := os.Stat(p); err != nil {
			fmt.Fprintf(os.Stderr, "Error: disk %v\n", err)
			os.Exit(1)
		}
	}
	if len(disks) > 0 {
		diskPath := disks[0]
		if err := a.LoadDisk(diskPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading disk: %v\n", err)
			os.Exit(1)
		}
		if *writeProtect {
			fmt.Printf("Loaded disk: %s (write-protected)\n", diskPath)
		} else {
			fmt.Printf("Loaded disk: %s (writes are saved to this file)\n", diskPath)
		}
		if len(disks) > 1 {
			fmt.Printf("F11 swaps DF0: to the next of %d disks; Shift+F11 ejects\n", len(disks))
		}
	}

	// Reset
	if err := a.Reset(); err != nil {
		fmt.Fprintf(os.Stderr, "Error during reset: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])

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

	// Windowed mode: open Ebitengine display
	fmt.Println("Starting display (50fps PAL)...")
	a.Video.SetOverscan(*overscan)
	display := amiga.NewDisplay(a)
	display.Disks = disks
	display.Mute = *mute
	if len(disks) > 0 {
		display.DiskIndex = 0
	}
	if err := display.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Display error: %v\n", err)
		os.Exit(1)
	}
}

// diskList collects repeated --disk flags.
type diskList []string

func (l *diskList) String() string { return strings.Join(*l, ",") }

func (l *diskList) Set(v string) error {
	*l = append(*l, v)
	return nil
}
