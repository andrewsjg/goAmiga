package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cavaliercoder/goAmiga/amiga"
)

func main() {
	romPath := flag.String("rom", "", "Path to Kickstart ROM file")
	diskPath := flag.String("disk", "", "Path to ADF floppy disk image")
	maxCycles := flag.Uint64("cycles", 0, "Maximum CPU cycles to execute (headless mode)")
	headless := flag.Bool("headless", false, "Run without display window")
	overscan := flag.Bool("overscan", false, "Show the overscan area (720x284) instead of the standard 640x256 window")
	verbose := flag.Bool("v", false, "Enable verbose trace output")
	flag.Parse()

	if *romPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: goamiga --rom <kickstart.rom> [--cycles N] [--headless] [-v]\n")
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

	// Load disk if specified
	if *diskPath != "" {
		if err := a.LoadDisk(*diskPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error loading disk: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Loaded disk: %s\n", *diskPath)
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
	if err := display.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Display error: %v\n", err)
		os.Exit(1)
	}
}
