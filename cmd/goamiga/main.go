package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/andrewsjg/goamiga/internal/config"
)

func main() {
	s := config.Defaults()
	s.Register(flag.CommandLine)
	maxCycles := flag.Uint64("cycles", 0, "Maximum CPU cycles to execute (headless mode)")
	headless := flag.Bool("headless", false, "Run without display window")
	loadState := flag.String("load-state", "", "Resume from a save state file")
	verbose := flag.Bool("v", false, "Enable verbose trace output")
	launcher := flag.Bool("launcher", false, "Open the configuration screen (as with no arguments at all)")
	configPath := flag.String("config", "", "Settings file (key = value per line, keys are these flag names) or the name of a saved configuration; default ./goamiga.conf or ~/.config/goamiga/goamiga.conf")
	flag.Parse()

	// No arguments: the configuration screen, to pick or make one.
	if len(os.Args) == 1 || *launcher {
		if err := runLauncher(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if conf := config.Find(config.Resolve(*configPath)); conf != "" {
		if err := config.Apply(flag.CommandLine, conf); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Settings from %s\n", conf)
	}
	if s.ROM == "" {
		fmt.Fprintf(os.Stderr, "Usage: goamiga [--rom <kickstart.rom> [--df0 a.adf ...] [flags] | --config NAME]\n"+
			"With no arguments goamiga opens its configuration screen.\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	var logger io.Writer
	if *verbose {
		logger = os.Stdout
	}
	m, err := buildMachine(&s, *loadState, logger, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	a := m.a
	fmt.Printf("CPU reset: PC=$%08X SSP=$%08X\n", a.CPU.PC, a.CPU.A[7])

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
		m.close()
		return
	}

	if err := runMachine(m, &s); err != nil {
		fmt.Fprintf(os.Stderr, "Display error: %v\n", err)
		os.Exit(1)
	}
}
