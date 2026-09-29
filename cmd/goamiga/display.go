//go:build !noebitengine

package main

import "github.com/cavaliercoder/goAmiga/amiga"

// runDisplay opens the window and runs the emulator until it closes.
func runDisplay(a *amiga.Amiga, o displayOptions) error {
	display := amiga.NewDisplay(a)
	display.Mute = o.mute
	display.Scale = o.scale
	display.StatusBar = o.statusBar
	display.StartWarp = o.warp
	display.JoyKeys = o.joyKeys
	for n, list := range o.drives {
		display.Disks[n] = list
		if len(list) > 0 {
			display.DiskIndex[n] = 0
		}
	}
	return display.Run()
}
