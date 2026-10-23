//go:build !noebitengine

package main

import (
	"strconv"
	"testing"

	"github.com/andrewsjg/goamiga/internal/config"
)

// No text on any tab of the configuration screen runs past the panel it
// is in: the tab panel (to x 810, y 474) or the saved configurations (to
// x 240).
// Every option that shows more is turned on.
func TestLauncherTextFits(t *testing.T) {
	s := config.Defaults()
	s.ROM = "../../ROMS/kick32a1200.rom"
	s.Serial, s.BSDSocket, s.A2065, s.JoyKeys, s.JoyAnalog = "telnet:2323", true, true, true, true
	s.Motherboard, s.RTG, s.Tape = "a4000", 16, "4:/a/rather/long/path/to/a/tape/folder"
	s.WHDLoad = "/Users/someone/Games/WHDLoad/A Game With A Long Name.lha"
	checkLauncherText(t, s, "every option")
	// The Hard disks tab with no drives, five (the explanation still
	// shows), and thirty (a page of them, scrolled).
	for _, drives := range []int{0, 5, 30} {
		s := s
		s.IDE = config.List{"/disks/ide-master.hdf", "/disks/ide-slave.hdf"}
		s.Hardfiles, s.Dirs = nil, nil
		for i := range drives {
			if i%2 == 0 {
				s.Hardfiles = append(s.Hardfiles, "/Users/someone/Amiga/Hard disks/a rather long name "+strconv.Itoa(i)+".hdf")
			} else {
				s.Dirs = append(s.Dirs, "/Users/someone/Amiga/Shared/folder "+strconv.Itoa(i))
			}
		}
		checkLauncherText(t, s, strconv.Itoa(drives)+" drives")
	}
}

func checkLauncherText(t *testing.T, s config.Settings, what string) {
	t.Helper()
	l := newLauncher(s, "A configuration")
	l.hdScroll = 3
	var texts []placedText
	l.ui.texts = &texts
	l.ui.place(launcherW, launcherH, launcherW, launcherH)
	for tab, name := range launcherTabs {
		l.tab = tab
		texts = texts[:0]
		l.update()
		for _, p := range texts {
			right := 810 // the tab panel
			if p.x < 250 {
				right = 240 // the saved configurations
			}
			if p.y >= 486 || p.y < 60 {
				continue // the title, the tabs, the status line
			}
			if p.x+p.w > right {
				t.Errorf("%s, %s tab: %q at x %d runs to %d, past %d", what, name, p.s, p.x, p.x+p.w, right)
			}
			if p.x >= 250 && p.y+uiTextH > 474 {
				t.Errorf("%s, %s tab: %q at y %d runs below the panel (474)", what, name, p.s, p.y)
			}
		}
	}
}
