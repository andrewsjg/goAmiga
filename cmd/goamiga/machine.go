package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/andrewsjg/goamiga/amiga"
	"github.com/andrewsjg/goamiga/internal/config"
)

// machine is an Amiga built from settings, with what it holds open.
type machine struct {
	a      *amiga.Amiga
	drives [4]config.List // the disks for each drive, zips expanded
	serial *amiga.NetSerial
}

// close releases the host resources the machine holds.
func (m *machine) close() {
	if m.serial != nil {
		m.serial.Close()
	}
	if m.a != nil {
		m.a.Shutdown()
	}
}

// buildMachine makes and resets an Amiga as the settings describe. It
// reports what it does on out.
func buildMachine(s *config.Settings, loadState string, logger io.Writer, out io.Writer) (*machine, error) {
	if s.ROM == "" {
		return nil, fmt.Errorf("no Kickstart ROM chosen")
	}
	romData, err := amiga.LoadROM(s.ROM)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "Loaded ROM: %s (%d bytes)\n", s.ROM, len(romData))
	if sum := amiga.ROMChecksum(romData); sum != 0xFFFFFFFF {
		fmt.Fprintf(out, "Warning: %s fails the Kickstart checksum ($%08X, want $FFFFFFFF);\n"+
			"  the image is damaged or modified and Kickstart will stop with a red screen.\n", s.ROM, sum)
	}
	a, err := amiga.New(romData, logger)
	if err != nil {
		return nil, fmt.Errorf("creating the Amiga: %w", err)
	}
	m := &machine{a: a}
	fail := func(err error) (*machine, error) {
		m.close()
		return nil, err
	}

	chipRAM, chipset := s.ChipRAM, s.Chipset
	if chipRAM == 0 {
		chipRAM = 1024
		if chipset == "aga" {
			chipRAM = 2048
		}
	}
	if chipset == "" {
		chipset = "ocs"
		if chipRAM == 1024 {
			chipset = "ecs-agnus" // 1 MB of chip RAM needs the 8372A
		}
	}
	for _, step := range []func() error{
		func() error { return a.SetChipRAM(chipRAM) },
		func() error { return a.SetCPU(s.CPU, s.CPUSpeed) },
		func() error { return a.SetFPU(s.FPU) },
		func() error { return a.SetChipset(chipset) },
		func() error { return a.SetVideoStandard(s.Video) },
		func() error { return a.ConfigureMemory(s.SlowRAM, s.FastRAM) },
	} {
		if err := step(); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintf(out, "Memory: %d KB chip, %d KB slow, %d KB fast; chipset %s, %s; CPU %s at %d%%\n",
		chipRAM, s.SlowRAM, s.FastRAM, a.Chipset(), strings.ToUpper(a.VideoStandard()), a.CPU.Model, a.CPUSpeed)

	// Connect drives and load each one's first disk; the rest are for the
	// swap keys. A zip holding several disks stands for all of them, boot
	// disk first.
	for n := 1; n < s.NumDrives && n < 4; n++ {
		a.Disk.ConnectDrive(n)
	}
	for n, list := range s.Drives {
		var all config.List
		for _, p := range list {
			if _, err := os.Stat(amiga.DiskFile(p)); err != nil {
				return fail(fmt.Errorf("DF%d: disk %v", n, err))
			}
			disks, err := amiga.DiskImagesIn(p)
			if err != nil {
				return fail(fmt.Errorf("DF%d: %v", n, err))
			}
			all = append(all, disks...)
		}
		m.drives[n] = all
		if len(all) == 0 {
			continue
		}
		if len(all) > 1 {
			fmt.Fprintf(out, "DF%d: %d disks; Cmd+F11 changes to the next\n", n, len(all))
		}
		if err := a.LoadDiskUnit(n, all[0]); err != nil {
			return fail(fmt.Errorf("loading DF%d: %v", n, err))
		}
		fmt.Fprintf(out, "DF%d: %s\n", n, all[0])
	}
	if s.BSDSocket {
		a.EnableBSDSocket()
		fmt.Fprintln(out, "bsdsocket.library: on (host network)")
	}
	if s.Serial != "" {
		ns, err := amiga.ListenSerial(s.Serial)
		if err != nil {
			return fail(err)
		}
		m.serial = ns
		a.Serial.Host = ns
		how := "nc"
		if ns.Telnet {
			how = "telnet"
		}
		host, port, _ := net.SplitHostPort(ns.Addr().String())
		fmt.Fprintf(out, "Serial port: %s %s %s\n", how, host, port)
	}
	a.Disk.SetWriteProtected(s.WriteProtect)
	if s.WriteProtect {
		fmt.Fprintln(out, "All drives write-protected")
	}

	for _, p := range s.Hardfiles {
		if s.HDFCreate > 0 {
			if _, err := os.Stat(p); os.IsNotExist(err) {
				if err := createHardfile(p, s.HDFCreate); err != nil {
					return fail(err)
				}
				fmt.Fprintf(out, "Created blank %d MB hardfile %s\n", s.HDFCreate, p)
			}
		}
		if err := a.AttachHardfile(p, s.WriteProtect); err != nil {
			return fail(fmt.Errorf("hardfile %v", err))
		}
	}
	if a.HardDisk != nil {
		for u, f := range a.HardDisk.Files {
			var names []string
			for _, p := range f.Partitions {
				names = append(names, p.Name+":")
			}
			fmt.Fprintf(out, "goamiga.device unit %d: %s (%d MB) as %s\n", u, f.Path, f.Size()>>20, strings.Join(names, " "))
		}
	}

	if err := a.Reset(); err != nil {
		return fail(fmt.Errorf("reset: %w", err))
	}
	if loadState != "" {
		f, err := os.Open(loadState)
		if err == nil {
			err = a.LoadState(f)
			f.Close()
		}
		if err != nil {
			return fail(fmt.Errorf("load state: %v", err))
		}
		fmt.Fprintf(out, "Resumed from %s\n", loadState)
	}
	a.Video.SetOverscan(s.Overscan)
	return m, nil
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
