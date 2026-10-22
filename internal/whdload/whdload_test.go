package whdload

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The first run downloads WHDLoad and the RTBs (here from a local server
// with the same archives) and prepares a game folder: the slave and
// options from its icon, the kickstart images named for WHDLoad, the
// Startup-Sequence.
func TestPrepareDownloads(t *testing.T) {
	usr, rtb := "../../WHDLoad/WHDLoad_usr.lha", "../../WHDLoad/skick346.lha"
	for _, f := range []string{usr, rtb} {
		if _, err := os.Stat(f); err != nil {
			t.Skip(err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/WHDLoad_usr.lha":
			http.ServeFile(w, r, usr)
		case "/skick346.lha":
			http.ServeFile(w, r, rtb)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	WHDLoadURL, RTBURL = srv.URL+"/WHDLoad_usr.lha", srv.URL+"/skick346.lha"

	// A game folder as packs have them: the slave and its icon in a folder.
	game := filepath.Join(t.TempDir(), "SDI")
	os.MkdirAll(filepath.Join(game, "SDI"), 0o755)
	os.WriteFile(filepath.Join(game, "SDI", "SDI.slave"), []byte("slave"), 0o644)
	icon, err := os.ReadFile("../../WHDLoad/installs/x/SDI/SDIHD/SDI.colicon")
	if err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(game, "SDI", "SDI.info"), icon, 0o644)

	home := t.TempDir()
	var log []string
	g, err := Prepare(home, game, []string{"../../ROMS/Kick13.rom", "../../ROMS/kick32a1200.rom"},
		func(f string, a ...any) { log = append(log, f) })
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Errorf("log %q: want the two downloads", log)
	}
	if _, err := os.Stat(filepath.Join(home, "WHDLoad")); err != nil {
		t.Error("WHDLoad wasn't kept")
	}
	if _, err := os.Stat(filepath.Join(g.Boot, "Devs", "Kickstarts", "kick34005.A500.RTB")); err != nil {
		t.Error("no RTB in the boot folder")
	}
	if g.Path != "SDI" || g.Slave != "SDI.slave" || strings.Join(g.Args, " ") != "Preload" {
		t.Errorf("path %q slave %q args %q", g.Path, g.Slave, g.Args)
	}
	if strings.Join(g.Kicks, ",") != "kick34005.A500" {
		t.Errorf("kickstarts %v: want only the 1.3 image (3.2 isn't one WHDLoad emulates)", g.Kicks)
	}
	ss, _ := os.ReadFile(filepath.Join(g.Boot, "S", "Startup-Sequence"))
	if want := "CD \"Game:SDI\"\nC:WHDLoad SLAVE=\"SDI.slave\" Preload\n"; !strings.HasPrefix(string(ss), want) {
		t.Errorf("Startup-Sequence %q, want %q", ss, want)
	}

	// A second run downloads nothing.
	log = nil
	if _, err := Prepare(home, game, nil, func(f string, a ...any) { log = append(log, f) }); err != nil || len(log) != 0 {
		t.Errorf("second run: %v, log %q", err, log)
	}
}

// A game given as an archive is unpacked once into games/, and its slave
// found one folder down; the next run uses what was unpacked.
func TestPrepareArchive(t *testing.T) {
	archive := "../../WHDLoad/installs/SDI.lha"
	if _, err := os.Stat(archive); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "WHDLoad"), []byte("whdload"), 0o644)
	os.MkdirAll(filepath.Join(home, "Kickstarts"), 0o755)
	os.WriteFile(filepath.Join(home, "Kickstarts", "kick34005.A500.RTB"), []byte("rtb"), 0o644)
	g, err := Prepare(home, archive, nil, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if g.Dir != filepath.Join(home, "games", "SDI") || g.Path != "SDIHD" || g.Slave != "SDI.slave" {
		t.Errorf("dir %q path %q slave %q", g.Dir, g.Path, g.Slave)
	}
	os.WriteFile(filepath.Join(g.Dir, "SDIHD", "highs"), []byte("saved"), 0o644)
	if _, err := Prepare(home, archive, nil, t.Logf); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(g.Dir, "SDIHD", "highs")); string(b) != "saved" {
		t.Error("the second run unpacked the archive again over the saved file")
	}
}
