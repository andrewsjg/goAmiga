// Command mkiconset writes the Boing Ball as a macOS iconset: the folder
// of PNGs iconutil turns into an .icns (scripts/macos-app.sh uses it).
//
//	go run ./internal/boing/mkiconset goamiga.iconset
package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/andrewsjg/goamiga/internal/boing"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mkiconset DIR.iconset")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	// Each size at 1x and 2x, as iconutil names them.
	for _, n := range []int{16, 32, 128, 256, 512} {
		for _, scale := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", n, n)
			if scale == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", n, n)
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				fail(err)
			}
			if err := png.Encode(f, boing.Ball(n*scale)); err != nil {
				fail(err)
			}
			f.Close()
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "mkiconset:", err)
	os.Exit(1)
}
