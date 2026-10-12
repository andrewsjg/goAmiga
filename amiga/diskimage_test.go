package amiga

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadDiskImage(t *testing.T) {
	dir := t.TempDir()
	adf := make([]byte, ADFSize)
	copy(adf, "DOS\x00")
	adf[ADFSize-1] = 0x5A

	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o644)
		return p
	}
	zipped := func(files map[string][]byte) []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for name, data := range files {
			w, _ := zw.Create(name)
			w.Write(data)
		}
		zw.Close()
		return b.Bytes()
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(adf)
	zw.Close()

	for _, c := range []struct {
		name     string
		path     string
		saveable bool
		errPart  string
	}{
		{"plain ADF", write("a.adf", adf), true, ""},
		{"zip", write("g.zip", zipped(map[string][]byte{"readme.txt": []byte("hi"), "Game.ADF": adf})), false, ""},
		{"gzip", write("g.adz", gz.Bytes()), false, ""},
		{"broken IPF in a zip", write("p.zip", zipped(map[string][]byte{"Disk1.ipf": []byte("CAPS....")})), false, "IPF"},
		{"broken raw IPF", write("p.ipf", []byte("CAPS\x00\x00\x00\x0c")), false, "IPF"},
		{"zip without a disk", write("n.zip", zipped(map[string][]byte{"x.txt": []byte("x")})), false, "no ADF or IPF"},
		{"wrong size", write("s.adf", make([]byte, 1000)), false, "not an ADF"},
	} {
		img, err := ReadDiskImage(c.path)
		if c.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), c.errPart) {
				t.Errorf("%s: error %v, want one mentioning %q", c.name, err, c.errPart)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		data, saveable := img.ADF, img.Saveable
		if !bytes.Equal(data, adf) || saveable != c.saveable {
			t.Errorf("%s: got %d bytes (equal %v), saveable %v; want the ADF, saveable %v",
				c.name, len(data), bytes.Equal(data, adf), saveable, c.saveable)
		}
	}
}

func TestBootDiskName(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"Skidmarks_ProgramDisk.ipf", "Skidmarks_AGACarDisk.ipf"},
		{"Game_Disk1of3.ipf", "Game_Disk2of3.ipf"},
		{"ChaosEngine,The_DiskOne.ipf", "ChaosEngine,The_DiskTwo.ipf"},
		{"Civ (Disk 1 of 4).adf", "Civ (Disk 3 of 4).adf"},
	} {
		if bootDiskName(c.a) <= bootDiskName(c.b) {
			t.Errorf("%q should be preferred over %q", c.a, c.b)
		}
	}
}

// A zip of several disks lists them all, boot disk first, and each can be
// read by its member path.
func TestDiskImagesInZip(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range []string{"Game_Disk2.adf", "Game_Disk1.adf", "readme.txt"} {
		w, _ := zw.Create(n)
		data := make([]byte, ADFSize)
		data[0] = n[9] // '2', '1'
		if n == "readme.txt" {
			data = []byte("hello")
		}
		w.Write(data)
	}
	zw.Close()
	p := filepath.Join(t.TempDir(), "game.zip")
	os.WriteFile(p, b.Bytes(), 0o644)

	paths, err := DiskImagesIn(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{p + "#Game_Disk1.adf", p + "#Game_Disk2.adf"}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("DiskImagesIn = %q, want %q", paths, want)
	}
	for i, path := range append([]string{p}, paths...) {
		img, err := ReadDiskImage(path)
		if err != nil {
			t.Fatal(err)
		}
		wantDisk := byte('1')
		if i == 2 {
			wantDisk = '2'
		}
		if img.ADF[0] != wantDisk || img.Saveable {
			t.Errorf("%s: disk %c (saveable %v), want disk %c", filepath.Base(path), img.ADF[0], img.Saveable, wantDisk)
		}
	}
	if _, err := ReadDiskImage(p + "#nope.adf"); err == nil {
		t.Error("a missing member read without error")
	}
	if single, _ := DiskImagesIn(filepath.Join(t.TempDir(), "x.adf")); len(single) != 1 {
		t.Errorf("a plain path expanded to %q", single)
	}
}

func TestDiskFile(t *testing.T) {
	dir := t.TempDir()
	zip := filepath.Join(dir, "game.zip")
	os.WriteFile(zip, nil, 0o644)
	for in, want := range map[string]string{
		zip + "#Disk2.adf": zip,
		zip:                zip,
		"plain.adf":        "plain.adf",
		"odd#name.adf":     "odd#name.adf", // not a zip member
	} {
		if got := DiskFile(in); got != want {
			t.Errorf("DiskFile(%q) = %q, want %q", in, got, want)
		}
	}
}
