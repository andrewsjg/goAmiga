package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type diskList []string

func (l *diskList) String() string { return strings.Join(*l, ",") }
func (l *diskList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

func TestApplyConfig(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "goamiga.conf")
	os.WriteFile(conf, []byte(`# my A500
rom = ROMS/Kick32.rom
df0 = "disks/a.adf"   # first disk
df0 = /abs/b.adf
fastram = 8192
scale = 3
mute
`), 0o644)

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	rom := fs.String("rom", "", "")
	var df0 diskList
	fs.Var(&df0, "df0", "")
	fastram := fs.Int("fastram", 2048, "")
	scale := fs.Int("scale", 2, "")
	mute := fs.Bool("mute", false, "")
	fs.Parse([]string{"--scale", "1"}) // the command line wins

	if err := Apply(fs, conf); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ROMS/Kick32.rom"); *rom != want {
		t.Errorf("rom = %q, want %q (relative to the config file)", *rom, want)
	}
	if want := (diskList{filepath.Join(dir, "disks/a.adf"), "/abs/b.adf"}); !reflect.DeepEqual(df0, want) {
		t.Errorf("df0 = %v, want %v", df0, want)
	}
	if *fastram != 8192 || !*mute {
		t.Errorf("fastram %d, mute %v; want 8192, true", *fastram, *mute)
	}
	if *scale != 1 {
		t.Errorf("scale = %d, want the command line's 1", *scale)
	}
}

func TestApplyConfigErrors(t *testing.T) {
	for _, tc := range []string{"nosuch = 1", "fastram", "fastram = lots"} {
		conf := filepath.Join(t.TempDir(), "c.conf")
		os.WriteFile(conf, []byte(tc+"\n"), 0o644)
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.Int("fastram", 0, "")
		if err := Apply(fs, conf); err == nil {
			t.Errorf("%q: no error", tc)
		}
	}
}
