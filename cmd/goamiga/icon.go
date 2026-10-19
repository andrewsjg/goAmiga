//go:build !noebitengine

package main

import (
	"bytes"
	"image/png"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/andrewsjg/goamiga/internal/boing"
)

// setIcon gives the window the Boing Ball (Windows and Linux use it).
func setIcon() {
	ebiten.SetWindowIcon(boing.Icons())
}

// setDockIconOnce puts the ball in the macOS Dock, which ignores window
// icons. It is called from the first Update, once the game loop (and so
// the application) is running: touching AppKit before Ebitengine has set
// it up can leave the window blank.
func (g *app) setDockIconOnce() {
	if g.dockIconSet {
		return
	}
	g.dockIconSet = true
	var b bytes.Buffer
	if png.Encode(&b, boing.Ball(512)) == nil {
		setDockIcon(b.Bytes())
	}
}
