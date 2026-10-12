// Package config reads goamiga settings files.
package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A config file holds settings as "key = value" lines, one per line. The
// keys are the command-line flag names (without dashes); a key may repeat
// where the flag can (df0, hdf, ...); a bare key sets a boolean flag;
// # starts a comment; values may be quoted. Relative paths are taken from
// the file's folder. Settings on the command line win over the file.
//
//	rom     = ROMS/Kick32.rom
//	hdf     = dh0.hdf
//	df0     = disks/Install3.2.adf
//	df0     = disks/Extras3.2.adf
//	fastram = 8192
//	mute

// pathKeys are the settings that name files.
var pathKeys = map[string]bool{"scsi": true, "dir": true, "ide": true, "rom": true, "df0": true, "disk": true, "df1": true, "df2": true, "df3": true, "hdf": true, "screenshot-dir": true, "state-dir": true, "load-state": true}

// Find returns the config file to use: the one named, else
// ./goamiga.conf, else ~/.config/goamiga/goamiga.conf, else "".
func Find(named string) string {
	if named != "" {
		return named
	}
	candidates := []string{"goamiga.conf"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "goamiga", "goamiga.conf"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// Apply sets the flags in fs from the config file at path, except
// those already set on the command line.
func Apply(fs *flag.FlagSet, path string) error {
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	// --disk and --df0 are one list.
	if explicit["disk"] || explicit["df0"] {
		explicit["disk"], explicit["df0"] = true, true
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dir := filepath.Dir(path)
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, hasVal := strings.Cut(line, "=")
		key = strings.TrimLeft(strings.TrimSpace(key), "-")
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		fl := fs.Lookup(key)
		if fl == nil {
			return fmt.Errorf("%s:%d: unknown setting %q", path, n, key)
		}
		if explicit[key] {
			continue
		}
		if !hasVal {
			if b, ok := fl.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
				return fmt.Errorf("%s:%d: %s needs a value", path, n, key)
			}
			val = "true"
		}
		val = mapPath(key, val, func(p string) string { return filepath.Join(dir, p) })
		if err := fs.Set(key, val); err != nil {
			return fmt.Errorf("%s:%d: %s: %v", path, n, key, err)
		}
	}
	return sc.Err()
}
