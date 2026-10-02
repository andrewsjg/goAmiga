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
