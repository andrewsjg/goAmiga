package lha

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every archive under ../../WHDLoad unpacks to the same files as the lha
// program unpacks it to.
func TestMatchesLha(t *testing.T) {
	archives, _ := filepath.Glob("../../WHDLoad/*.lha")
	more, _ := filepath.Glob("../../WHDLoad/installs/*.lha")
	archives = append(archives, more...)
	pic, _ := filepath.Glob("../../Picasso96/*.lha")
	archives = append(archives, pic...)
	if len(archives) == 0 {
		t.Skip("no archives")
	}
	lhaCmd, err := exec.LookPath("lha")
	if err != nil {
		t.Skip("lha not installed")
	}
	for _, a := range archives {
		t.Run(filepath.Base(a), func(t *testing.T) {
			want := t.TempDir()
			abs, _ := filepath.Abs(a)
			exec.Command(lhaCmd, "xqw="+want, abs).Run()
			got := t.TempDir()
			if err := ExtractAll(a, got); err != nil {
				t.Fatal(err)
			}
			n := 0
			filepath.Walk(want, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(want, p)
				w, _ := os.ReadFile(p)
				g, err := os.ReadFile(filepath.Join(got, rel))
				if err != nil {
					t.Errorf("%s: missing", rel)
				} else if !bytes.Equal(g, w) {
					t.Errorf("%s: %d bytes differ from lha's %d", rel, len(g), len(w))
				}
				n++
				return nil
			})
			fh, _ := os.Open(a)
			files, _ := Read(fh)
			fh.Close()
			methods := map[string]int{}
			for _, f := range files {
				methods[f.Method]++
			}
			t.Logf("%d files, methods %v", n, methods)
			if n == 0 && !strings.Contains(a, "empty") {
				t.Error("lha unpacked nothing")
			}
		})
	}
}

// level2 builds a level 2 header and its data: the file name and the
// directory ($FF separated) in extended headers.
func level2(method, dir, name string, data []byte) []byte {
	ext := func(typ byte, body []byte) []byte {
		return append([]byte{typ}, body...)
	}
	exts := [][]byte{ext(1, []byte(name))}
	if dir != "" {
		exts = append(exts, ext(2, []byte(dir)))
	}
	h := make([]byte, 26)
	copy(h[2:], method)
	put32 := func(b []byte, v uint32) { b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24) }
	put32(h[7:], uint32(len(data)))
	put32(h[11:], uint32(len(data)))
	put32(h[15:], 1700000000)
	h[19], h[20] = 0x20, 2
	c := crc16(data)
	h[21], h[22], h[23] = byte(c), byte(c>>8), 'U'
	// Each extended header is its type and data, then the next one's size.
	sizes := func(i int) int {
		if i >= len(exts) {
			return 0
		}
		return len(exts[i]) + 2
	}
	h[24], h[25] = byte(sizes(0)), byte(sizes(0)>>8)
	for i, e := range exts {
		n := sizes(i + 1)
		h = append(h, e...)
		h = append(h, byte(n), byte(n>>8))
	}
	h[0], h[1] = byte(len(h)), byte(len(h)>>8)
	return append(h, data...)
}

func TestLevel2Headers(t *testing.T) {
	var a []byte
	a = append(a, level2("-lhd-", "Game\xffdata", "", nil)...)
	a = append(a, level2("-lh0-", "Game\xffdata", "disk.1", []byte("hello"))...)
	a = append(a, level2("-lh0-", "", "Game.slave", []byte("slave"))...)
	a = append(a, 0)
	files, err := Read(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		d, err := f.Data()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, f.Name+"="+string(d))
	}
	want := []string{"Game/data=", "Game/data/disk.1=hello", "Game.slave=slave"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
	if !files[0].Dir || files[1].Modified.Unix() != 1700000000 {
		t.Errorf("dir %v, time %v", files[0].Dir, files[1].Modified)
	}
}
