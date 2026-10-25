package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewsjg/goamiga/internal/config"
)

// apiCall sends requests to the launcher API and decodes the answers.
func apiCall(t *testing.T, reqs ...apiRequest) []apiResponse {
	t.Helper()
	var in bytes.Buffer
	for _, r := range reqs {
		b, _ := json.Marshal(r)
		in.Write(append(b, '\n'))
	}
	var out bytes.Buffer
	if err := runLauncherAPI(&in, &out); err != nil {
		t.Fatal(err)
	}
	var resps []apiResponse
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r apiResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		resps = append(resps, r)
	}
	if len(resps) != len(reqs) {
		t.Fatalf("%d answers to %d requests", len(resps), len(reqs))
	}
	return resps
}

func TestLauncherAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir()) // no ./goamiga.conf

	r := apiCall(t, apiRequest{Op: "startup"})[0]
	if !r.OK || r.Settings == nil || r.Settings.CPU != "68000" || len(r.Saved) != 0 {
		t.Fatalf("startup: %+v", r)
	}

	s := config.Defaults()
	s.ROM = filepath.Join(home, "kick.rom")
	rom := make([]byte, 512*1024)
	copy(rom[12:], []byte{0, 40, 0, 68}) // Kickstart 40.68 (3.1)
	if err := os.WriteFile(s.ROM, rom, 0o644); err != nil {
		t.Fatal(err)
	}
	rs := apiCall(t,
		apiRequest{Op: "preset", Preset: "A1200", Settings: &s},
		apiRequest{Op: "save", Name: "Test", Settings: &s},
		apiRequest{Op: "load", Name: "Test"},
		apiRequest{Op: "choose", Target: "hdf", Path: "/tmp/a.hdf", Settings: &s},
		apiRequest{Op: "rom", Settings: &s},
		apiRequest{Op: "start", Name: "Test", Settings: &s},
		apiRequest{Op: "save", Name: "bad/name", Settings: &s},
		apiRequest{Op: "delete", Name: "Test"},
	)
	for i, r := range rs[:6] {
		if !r.OK {
			t.Fatalf("request %d: %s", i, r.Error)
		}
	}
	if rs[0].Settings.Chipset != "aga" || rs[0].Settings.ROM != s.ROM {
		t.Errorf("A1200 preset: %+v", rs[0].Settings)
	}
	if len(rs[1].Saved) != 1 || rs[1].Saved[0] != "Test" {
		t.Errorf("saved: %v", rs[1].Saved)
	}
	if rs[2].Settings.ROM != s.ROM || rs[2].Name == nil || *rs[2].Name != "Test" {
		t.Errorf("load: %+v", rs[2])
	}
	if h := rs[3].Settings.Hardfiles; len(h) != 1 || h[0] != "/tmp/a.hdf" {
		t.Errorf("choose hdf: %v", h)
	}
	if !strings.Contains(rs[4].ROM.Description, "3.1") || rs[4].ROM.Mismatch != "" {
		t.Errorf("rom: %+v", rs[4].ROM)
	}
	if got, err := config.Load(rs[5].Config); err != nil || got.ROM != s.ROM {
		t.Errorf("start wrote %s: %v %+v", rs[5].Config, err, got)
	}
	if config.LastUsed() != "Test" {
		t.Errorf("last used %q", config.LastUsed())
	}
	if rs[6].OK || rs[6].Error == "" {
		t.Errorf("a bad name saved: %+v", rs[6])
	}
	if !rs[7].OK || len(config.Saved()) != 0 {
		t.Errorf("delete: %+v", rs[7])
	}

	// Lists are [] rather than null, for the Swift side's decoding.
	var out bytes.Buffer
	runLauncherAPI(strings.NewReader(`{"op":"defaults"}`+"\n"), &out)
	if strings.Contains(out.String(), "null") {
		t.Errorf("defaults have nulls: %s", out.String())
	}

	// Starting needs a ROM.
	if r := apiCall(t, apiRequest{Op: "start"})[0]; r.OK {
		t.Error("started with no ROM")
	}
}
