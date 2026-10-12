package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Settings survive a save and load, and only what differs from the
// defaults is written.
func TestSettingsRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := Defaults()
	s.ROM = "/roms/kick13.rom"
	s.Drives[0] = List{"/adf/a.adf", "/adf/b disk.adf"}
	s.Hardfiles = List{"/hdf/dh0.hdf"}
	s.CPU, s.Chipset, s.ChipRAM, s.FastRAM = "68020", "aga", 2048, 0
	s.Serial, s.BSDSocket, s.Mute = "telnet:2323", true, true
	s.StatusBar = false

	if err := s.Write(SavedPath("A1200")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(SavedPath("A1200"))
	text := string(data)
	for _, want := range []string{"rom = /roms/kick13.rom\n", "df0 = \"/adf/b disk.adf\"\n", "fastram = 0\n",
		"bsdsocket\n", "statusbar = false\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("saved file lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"video", "scale", "cpu-speed", "disk ="} {
		if strings.Contains(text, unwanted) {
			t.Errorf("saved file has default or alias %q:\n%s", unwanted, text)
		}
	}
	got, err := Load(SavedPath("A1200"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, s) {
		t.Errorf("loaded %+v\nwant  %+v", *got, s)
	}
	if names := Saved(); !reflect.DeepEqual(names, []string{"A1200"}) {
		t.Errorf("Saved() = %q", names)
	}
	if r := Resolve("A1200"); r != SavedPath("A1200") {
		t.Errorf("Resolve(A1200) = %q", r)
	}
	if r := Resolve("other/x.conf"); r != "other/x.conf" {
		t.Errorf("Resolve(path) = %q", r)
	}
	SetLastUsed("A1200")
	if LastUsed() != "A1200" {
		t.Errorf("LastUsed() = %q", LastUsed())
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"A500", "My A1200 (AGA)"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "a/b", ".hidden", "x:y"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// Relative paths are saved absolute.
func TestSettingsAbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	s := Defaults()
	s.ROM = "kick.rom"
	p := filepath.Join(dir, "x.conf")
	if err := s.Write(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.ROM) || filepath.Base(got.ROM) != "kick.rom" {
		t.Errorf("ROM %q, want an absolute path to kick.rom", got.ROM)
	}
}

// A --dir value may name the volume; relative folders are made absolute
// either way, and the name kept.
func TestDirSetting(t *testing.T) {
	for _, c := range []struct{ in, vol, path string }{
		{"work", "", "work"},
		{"Work=stuff/work", "Work", "stuff/work"},
		{"/a=b/c", "", "/a=b/c"}, // '=' after a slash is part of the path
	} {
		if vol, p := SplitDir(c.in); vol != c.vol || p != c.path {
			t.Errorf("SplitDir(%q) = %q, %q", c.in, vol, p)
		}
	}
	got := mapPath("dir", "Work=stuff", func(p string) string { return "/base/" + p })
	if got != "Work=/base/stuff" {
		t.Errorf("mapPath kept %q", got)
	}
	if got := mapPath("dir", "Work=/abs", func(p string) string { return "x" }); got != "Work=/abs" {
		t.Errorf("mapPath changed an absolute folder: %q", got)
	}
}
