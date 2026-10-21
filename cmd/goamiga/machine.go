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
	romInfo := amiga.IdentifyROM(romData)
	fmt.Fprintf(out, "Loaded ROM: %s (%d bytes): %s\n", s.ROM, len(romData), romInfo)
	if m := romInfo.Mismatch(s.CPU, s.Motherboard); m != "" {
		fmt.Fprintf(out, "Warning: %s; with these settings it will not start (try --cpu 68040 --chipset aga --motherboard %s).\n", m, romInfo.Machine)
	}
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
		func() error {
			if s.Motherboard == "" || s.Motherboard == "none" {
				if s.Z3RAM != 0 {
					return fmt.Errorf("--z3ram needs --motherboard a3000 or a4000")
				}
				if s.CPUSlotRAM != 0 {
					return fmt.Errorf("--cpuslotram needs --motherboard a3000 or a4000")
				}
				return nil
			}
			if err := a.EnableMotherboard(s.Motherboard, s.MBRAM); err != nil {
				return err
			}
			if err := a.EnableCPUSlotRAM(s.CPUSlotRAM); err != nil {
				return err
			}
			return a.EnableZorro3RAM(s.Z3RAM)
		},
		func() error { return a.EnableRTG(s.RTG) },
	} {
		if err := step(); err != nil {
			return fail(err)
		}
	}
	fmt.Fprintf(out, "Memory: %d KB chip, %d KB slow, %d KB fast; chipset %s, %s; CPU %s at %d%%\n",
		chipRAM, s.SlowRAM, s.FastRAM, a.Chipset(), strings.ToUpper(a.VideoStandard()), a.CPU.Model, a.CPUSpeed)
	if a.Motherboard != nil {
		fmt.Fprintf(out, "Motherboard: %s with %d KB of motherboard RAM", strings.ToUpper(s.Motherboard), s.MBRAM)
		if s.CPUSlotRAM > 0 {
			fmt.Fprintf(out, ", %d MB of CPU-slot RAM", s.CPUSlotRAM)
		}
		if s.Z3RAM > 0 {
			fmt.Fprintf(out, ", %d MB of Zorro III RAM", s.Z3RAM)
		}
		fmt.Fprintln(out)
	}
	if a.RTG != nil {
		fmt.Fprintf(out, "RTG: the uaegfx card with %d MB on Zorro %s, for Picasso96\n", s.RTG, map[bool]string{false: "II", true: "III"}[a.Motherboard != nil])
	}

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
	if s.A2065 {
		fw, err := parseForwards(s.A2065Fwd)
		if err != nil {
			return fail(err)
		}
		if err := a.EnableEthernet(fw); err != nil {
			return fail(fmt.Errorf("A2065: %v", err))
		}
		fmt.Fprintf(out, "A2065 Ethernet: 10.0.2.15 on a private network through this computer")
		for hp, gp := range fw {
			fmt.Fprintf(out, "; 127.0.0.1:%d -> port %d", hp, gp)
		}
		fmt.Fprintln(out)
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
	if v := s.Tape; v != "" {
		if len(v) < 3 || v[1] != ':' || v[0] < '0' || v[0] > '6' {
			return fail(fmt.Errorf("--tape %q: give it as ID:DIR, ID 0-6", v))
		}
		if err := a.AttachTape(int(v[0]-'0'), v[2:]); err != nil {
			return fail(fmt.Errorf("tape %v", err))
		}
		fmt.Fprintf(out, "A2091 SCSI ID %c: tape drive with %s\n", v[0], v[2:])
	}
	if s.Clock {
		a.EnableClock()
		a.RTC.Y2K = s.ClockY2K
	}
	for _, v := range s.SCSI {
		if len(v) < 3 || v[1] != ':' || v[0] < '0' || v[0] > '6' {
			return fail(fmt.Errorf("--scsi %q: give it as ID:PATH, ID 0-6", v))
		}
		if err := a.AttachSCSI(int(v[0]-'0'), v[2:], s.WriteProtect); err != nil {
			return fail(fmt.Errorf("SCSI disk %v", err))
		}
		fmt.Fprintf(out, "A2091 SCSI ID %c: %s (and a goamiga.device unit, for AmigaOS)\n", v[0], v[2:])
	}
	for _, v := range s.Dirs {
		vol, p := config.SplitDir(v)
		if err := a.AttachDirectory(p, vol, s.WriteProtect); err != nil {
			return fail(fmt.Errorf("folder %v", err))
		}
	}
	gayle := false
	switch s.Gayle {
	case "on":
		gayle = true
	case "auto", "":
		gayle = chipset == "aga" // the A1200 has it
	case "off":
	default:
		return fail(fmt.Errorf("--gayle must be auto, on or off, not %q", s.Gayle))
	}
	a4000 := s.Motherboard == "a4000"
	if a4000 && s.Gayle == "on" {
		return fail(fmt.Errorf("the A4000 has its own IDE interface, not Gayle: leave out --gayle on"))
	}
	if gayle || len(s.IDE) > 0 || a4000 {
		ideName := "Gayle"
		if a4000 {
			a.EnableA4000IDE() // always there on the A4000
			ideName = "A4000"
		} else {
			a.EnableGayle()
		}
		for i, p := range s.IDE {
			if err := a.AttachIDE(p, s.WriteProtect); err != nil {
				return fail(fmt.Errorf("IDE drive %s: %v", p, err))
			}
			hf := a.Gayle.IDEDrives()[i]
			kind := "RDB"
			if !hf.HasRDB() {
				kind = "no RDB, so scsi.device won't mount it; use --hdf for plain hardfiles"
			}
			fmt.Fprintf(out, "IDE %s: %s (%d MB, %s)\n", []string{"master", "slave"}[i], p, hf.Size()>>20, kind)
			printFileSystems(out, hf)
		}
		if len(s.IDE) == 0 {
			fmt.Fprintf(out, "%s: IDE interface fitted, no drives\n", ideName)
		}
	}
	if a.HardDisk != nil {
		for u, f := range a.HardDisk.Files {
			var names []string
			for _, p := range f.Partitions {
				names = append(names, p.Name+":")
			}
			fmt.Fprintf(out, "goamiga.device unit %d: %s (%d MB) as %s\n", u, f.Path, f.Size()>>20, strings.Join(names, " "))
			printFileSystems(out, f)
		}
		for _, d := range a.HardDisk.Dirs {
			boot := ""
			if d.Bootable {
				boot = ", bootable"
			}
			fmt.Fprintf(out, "Folder %s as volume %s:%s\n", d.Path, d.Volume, boot)
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

// printFileSystems lists the filesystems stored in a hardfile's RDB.
func printFileSystems(out io.Writer, f *amiga.Hardfile) {
	var fs []string
	for _, x := range f.FileSystems {
		fs = append(fs, amiga.DosTypeString(x.DosType)+" "+amiga.VersionString(x.Version))
	}
	if len(fs) > 0 {
		fmt.Fprintf(out, "  filesystems in the RDB: %s\n", strings.Join(fs, ", "))
	}
	if f.FSError != nil {
		fmt.Fprintf(out, "  warning: %v\n", f.FSError)
	}
}

// parseForwards reads --a2065-forward: HOSTPORT:AMIGAPORT,...
func parseForwards(v string) (map[int]int, error) {
	fw := map[int]int{}
	for _, f := range strings.Split(v, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		var hp, gp int
		if n, err := fmt.Sscanf(f, "%d:%d", &hp, &gp); n != 2 || err != nil || hp <= 0 || hp > 65535 || gp <= 0 || gp > 65535 {
			return nil, fmt.Errorf("--a2065-forward %q: give HOSTPORT:AMIGAPORT", f)
		}
		fw[hp] = gp
	}
	return fw, nil
}
