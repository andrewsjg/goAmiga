//go:build !noebitengine

package main

import (
	"bytes"
	"image/png"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/andrewsjg/goamiga/internal/boing"
)

// setIcon gives the window the Boing Ball (Windows and Linux use it) and,
// on macOS, which ignores window icons, puts it in the Dock.
func setIcon() {
	ebiten.SetWindowIcon(boing.Icons())
	var b bytes.Buffer
	if png.Encode(&b, boing.Ball(512)) == nil {
		setDockIcon(b.Bytes())
	}
}
