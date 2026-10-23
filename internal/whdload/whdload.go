// Package whdload prepares a WHDLoad game to run: a small boot folder with
// WHDLoad, the kickstart images it may need and a Startup-Sequence that
// starts the game, and the game's own folder, unpacked from its archive
// once and kept (saves and high scores stay with it).
//
// Everything lives in a home folder (by default ~/.config/goamiga/whdload):
//
//	WHDLoad          the WHDLoad command (from WHDLoad_usr.lha)
//	Kickstarts/      the .RTB relocation tables (from Aminet's skick346.lha)
//	games/NAME/      each game, unpacked
//	boot/            the boot folder, written again for each run
//
// WHDLoad and the RTBs are downloaded the first time they are needed.
package whdload

import (
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrewsjg/goamiga/internal/lha"
)

// Where WHDLoad and the kickstart relocation tables come from.
var (
	WHDLoadURL = "https://whdload.de/whdload/WHDLoad_usr.lha"
	RTBURL     = "http://aminet.net/util/boot/skick346.lha"
)

// Game is a prepared game.
type Game struct {
	Boot  string   // the boot folder
	Dir   string   // the game's folder, mounted as Game:
	Path  string   // the slave's folder, within Dir ("" for Dir itself)
	Slave string   // the slave's file name
	Args  []string // WHDLoad options (from the icon's tooltypes)
	Kicks []string // kickstart images provided, by WHDLoad name
}

// Prepare gets the game at path (an LhA archive, a folder, or a .slave)
// ready to run, with the kickstart images among roms (files) made
// available to kickemu slaves. Progress goes to logf.
func Prepare(home, path string, roms []string, logf func(string, ...any)) (*Game, error) {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	if err := ensureWHDLoad(home, logf); err != nil {
		return nil, err
	}
	g := &Game{}
	dir, err := gameDir(home, path, logf)
	if err != nil {
		return nil, err
	}
	g.Dir = dir
	if strings.EqualFold(filepath.Ext(path), ".slave") {
		g.Slave = filepath.Base(path)
	} else if err := g.findSlave(); err != nil {
		return nil, err
	}
	if installerOnly(filepath.Join(g.Dir, filepath.FromSlash(g.Path))) {
		return nil, fmt.Errorf("%s is WHDLoad's installer for the game, not the installed game: its files come from "+
			"the original disk. Install it from Workbench (its Install icon, with the original disk in DF0:) "+
			"and give the installed folder, or use an installed version", filepath.Base(path))
	}
	if err := g.writeBoot(home, roms); err != nil {
		return nil, err
	}
	return g, nil
}

// ensureWHDLoad downloads WHDLoad and the RTBs if they aren't there.
func ensureWHDLoad(home string, logf func(string, ...any)) error {
	if _, err := os.Stat(filepath.Join(home, "WHDLoad")); err != nil {
		logf("Downloading WHDLoad from %s", WHDLoadURL)
		tmp, err := fetchAndUnpack(WHDLoadURL)
		if err != nil {
			return fmt.Errorf("getting WHDLoad: %w (put the WHDLoad command in %s)", err, home)
		}
		defer os.RemoveAll(tmp)
		if err := copyFile(filepath.Join(tmp, "WHDLoad", "C", "WHDLoad"), filepath.Join(home, "WHDLoad")); err != nil {
			return fmt.Errorf("WHDLoad's archive: %w", err)
		}
	}
	kicks := filepath.Join(home, "Kickstarts")
	if rtbs, _ := filepath.Glob(filepath.Join(kicks, "*.RTB")); len(rtbs) == 0 {
		logf("Downloading the kickstart relocation tables from %s", RTBURL)
		tmp, err := fetchAndUnpack(RTBURL)
		if err != nil {
			// Only kickemu slaves need them: carry on without.
			logf("Couldn't get them (%v): games that emulate Kickstart won't start", err)
			return nil
		}
		defer os.RemoveAll(tmp)
		os.MkdirAll(kicks, 0o755)
		files, _ := filepath.Glob(filepath.Join(tmp, "Kickstarts", "*.RTB"))
		for _, f := range files {
			copyFile(f, filepath.Join(kicks, filepath.Base(f)))
		}
	}
	return nil
}

// fetchAndUnpack downloads an LhA archive and unpacks it into a temporary
// folder.
func fetchAndUnpack(url string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp("", "goamiga-*.lha")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	dir, err := os.MkdirTemp("", "goamiga-whdload")
	if err != nil {
		return "", err
	}
	if err := lha.ExtractAll(f.Name(), dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// gameDir is the folder a game runs from: an archive is unpacked under
// games/ the first time and used from there after.
func gameDir(home, path string, logf func(string, ...any)) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	switch {
	case st.IsDir():
		return path, nil
	case strings.EqualFold(filepath.Ext(path), ".slave"):
		return filepath.Dir(path), nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".lha", ".lzh":
	case ".lzx":
		return "", fmt.Errorf("%s: LZX archives aren't supported; unpack it and give the folder", filepath.Base(path))
	default:
		return "", fmt.Errorf("%s: give a WHDLoad game as an .lha archive, a folder or a .slave", filepath.Base(path))
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Join(home, "games", name)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil // unpacked before: keep its saves
	}
	logf("Unpacking %s into %s", filepath.Base(path), dir)
	tmp := dir + ".part"
	os.RemoveAll(tmp)
	if err := lha.ExtractAll(path, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return dir, os.Rename(tmp, dir)
}

// findSlave finds the slave to run and its options. A WHDLoad game's icon
// has them as tooltypes (SLAVE=Game.slave, PRELOAD ...); without one, the
// slave is the .slave file found.
func (g *Game) findSlave() error {
	var icons, slaves []string
	filepath.Walk(g.Dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(g.Dir, p)
		if info.IsDir() {
			if strings.Count(rel, string(filepath.Separator)) >= 2 {
				return filepath.SkipDir // slaves sit at the top, or one folder down
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".info":
			icons = append(icons, rel)
		case ".slave":
			slaves = append(slaves, rel)
		}
		return nil
	})
	sort.Strings(icons)
	for _, icon := range icons {
		b, err := os.ReadFile(filepath.Join(g.Dir, icon))
		if err != nil {
			continue
		}
		tts := ToolTypes(b)
		var slave string
		var args []string
		for _, tt := range tts {
			k, v, _ := strings.Cut(tt, "=")
			switch {
			case strings.EqualFold(k, "SLAVE"):
				slave = v
			case validOption(k):
				if v == "" {
					args = append(args, k)
				} else {
					args = append(args, fmt.Sprintf("%s=%q", k, v))
				}
			}
		}
		if slave == "" {
			continue
		}
		dir := filepath.Dir(icon)
		if _, err := os.Stat(filepath.Join(g.Dir, dir, slave)); err != nil {
			continue
		}
		g.Path, g.Slave, g.Args = cleanRel(dir), slave, args
		return nil
	}
	if len(slaves) == 0 {
		return fmt.Errorf("no WHDLoad slave (.slave) in %s", g.Dir)
	}
	sort.Strings(slaves)
	g.Path, g.Slave = cleanRel(filepath.Dir(slaves[0])), filepath.Base(slaves[0])
	g.Args = []string{"PRELOAD"}
	return nil
}

// installerOnly reports whether a slave's folder is a WHDLoad install
// package as whdload.de has them: an Installer script beside the slave,
// and none of what installing adds (a data folder, Disk.N images). An
// installed game doesn't keep the Install script.
func installerOnly(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	script := false
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		switch {
		case e.IsDir():
			if n != "source" && n != "src" && n != "sources" {
				return false // data, or the game in a folder: installed
			}
		case strings.HasPrefix(n, "disk.") || strings.HasPrefix(n, "disk-"):
			return false // disk images: installed
		case !script && !strings.HasSuffix(n, ".info") && strings.Contains(n, "install"):
			script = installerScript(filepath.Join(dir, e.Name()))
		}
	}
	return script
}

// installerScript reports whether a file is a script for the Amiga's
// Installer: text in its Lisp-like language.
func installerScript(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 1<<20 {
		return false
	}
	t := string(b)
	return strings.Contains(t, "(welcome") || strings.Contains(t, "@app-name") ||
		strings.Contains(t, "(copyfiles") || strings.Contains(t, "(set @default-dest")
}

func cleanRel(dir string) string {
	if dir == "." {
		return ""
	}
	return filepath.ToSlash(dir)
}

// validOption reports whether a tooltype is a WHDLoad option to pass on:
// a word, not a comment "(...)" or icon image data (NewIcons' IM1= ...).
func validOption(k string) bool {
	if k == "" || len(k) > 20 || strings.HasPrefix(k, "IM") && len(k) == 3 {
		return false
	}
	for _, c := range k {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// kickNames are the kickstart images WHDLoad's kickemu loads, by CRC-32.
var kickNames = map[uint32]string{
	0xA6CE1636: "kick33180.A500",
	0xC4F0F55F: "kick34005.A500",
	0xC3BDB240: "kick37175.A500",
	0x6C9B07D2: "kick39106.A1200",
	0xFC24AE0D: "kick40063.A600",
	0x1483A091: "kick40068.A1200",
	0xD6BAE334: "kick40068.A4000",
}

// ROMsBeside lists the files in a ROM's folder that could be Kickstart
// images (1 MB or less), for the games that emulate one.
func ROMsBeside(rom string) []string {
	var roms []string
	dir := filepath.Dir(rom)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.Mode().IsRegular() && info.Size() <= 1<<20 {
			roms = append(roms, filepath.Join(dir, e.Name()))
		}
	}
	return roms
}

// KickImages names the Kickstart images among files that WHDLoad's
// kickemu can use, as WHDLoad names them.
func KickImages(files []string) map[string]string {
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if name, ok := kickNames[crc32.ChecksumIEEE(b)]; ok {
			out[name] = f
		}
	}
	return out
}

// Installed reports whether WHDLoad is in home (else Prepare downloads it).
func Installed(home string) bool {
	_, err := os.Stat(filepath.Join(home, "WHDLoad"))
	return err == nil
}

// writeBoot writes the boot folder: WHDLoad, the kickstarts and RTBs in
// Devs:Kickstarts, and the Startup-Sequence.
func (g *Game) writeBoot(home string, roms []string) error {
	g.Boot = filepath.Join(home, "boot")
	os.RemoveAll(g.Boot)
	kicks := filepath.Join(g.Boot, "Devs", "Kickstarts")
	for _, d := range []string{filepath.Join(g.Boot, "C"), filepath.Join(g.Boot, "S"), kicks} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(home, "WHDLoad"), filepath.Join(g.Boot, "C", "WHDLoad")); err != nil {
		return err
	}
	rtbs, _ := filepath.Glob(filepath.Join(home, "Kickstarts", "*.RTB"))
	for _, f := range rtbs {
		copyFile(f, filepath.Join(kicks, filepath.Base(f)))
	}
	for name, f := range KickImages(roms) {
		if err := copyFile(f, filepath.Join(kicks, name)); err == nil {
			g.Kicks = append(g.Kicks, name)
		}
	}
	sort.Strings(g.Kicks)
	cd := "Game:"
	if g.Path != "" {
		cd += g.Path
	}
	// CD and Echo are built into Kickstart 2.0's shell: no Workbench needed.
	ss := fmt.Sprintf("CD %q\nC:WHDLoad SLAVE=%q %s\nEcho \"The game has quit. Cmd+R starts it again.\"\n",
		cd, g.Slave, strings.Join(g.Args, " "))
	return os.WriteFile(filepath.Join(g.Boot, "S", "Startup-Sequence"), []byte(ss), 0o644)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}
