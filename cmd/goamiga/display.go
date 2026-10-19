//go:build !noebitengine

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/andrewsjg/goamiga/amiga"
	"github.com/andrewsjg/goamiga/internal/config"
)

// app is the window's game: the configuration screen or a running
// machine. Ebitengine runs one game per process, so switching between
// them happens inside it. Cmd+, goes back from the machine to the
// configuration screen.
type app struct {
	launcher *launcher // showing the configuration screen, or nil

	m    *machine
	disp *amiga.Display
	s    config.Settings // the running machine's settings
	name string          // and its saved name, if any

	// shotDir, from GOAMIGA_UISHOT, saves each tab of the configuration
	// screen as a PNG there and quits (for checking the layout).
	shotDir   string
	shotFrame int
}

// runLauncher opens the window on the configuration screen.
func runLauncher() error {
	g := &app{shotDir: os.Getenv("GOAMIGA_UISHOT")}
	g.showLauncher(startupLauncher())
	setIcon()
	return ebiten.RunGame(g)
}

// runMachine opens the window on a machine built from the command line.
func runMachine(m *machine, s *config.Settings) error {
	g := &app{}
	g.startMachine(m, s, "")
	setIcon()
	return ebiten.RunGame(g)
}

func (g *app) showLauncher(l *launcher) {
	g.launcher = l
	ebiten.SetWindowTitle("goAmiga")
	ebiten.SetWindowSize(launcherW, launcherH)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetTPS(60)
	ebiten.SetScreenClearedEveryFrame(true)
}

func (g *app) startMachine(m *machine, s *config.Settings, name string) {
	d := amiga.NewDisplay(m.a)
	d.Mute = s.Mute
	d.Scale = s.Scale
	d.StatusBar = s.StatusBar
	d.StartWarp = s.Warp
	d.JoyKeys = s.JoyKeys
	d.JoyAnalog = s.JoyAnalog
	d.ScreenshotDir = s.ScreenshotDir
	d.StateDir = s.StateDir
	for n, list := range m.drives {
		d.Disks[n] = list
		if len(list) > 0 {
			d.DiskIndex[n] = 0
		}
	}
	fmt.Printf("Starting display (%.2f fps %s)...\n", m.a.FrameRate(), m.a.VideoStandard())
	g.m, g.disp, g.s, g.name, g.launcher = m, d, *s, name, nil
	d.Start()
}

// stopMachine ends the machine and frees what it holds.
func (g *app) stopMachine() {
	if g.disp != nil {
		g.disp.Stop()
	}
	if g.m != nil {
		g.m.close()
	}
	g.m, g.disp = nil, nil
}

func (g *app) Update() error {
	if g.disp != nil {
		if err := g.disp.Update(); err != nil {
			return err
		}
		if g.disp.PowerRequested {
			g.powerCycle()
			return nil
		}
		if g.disp.ConfigRequested {
			s, name := g.s, g.name
			g.stopMachine()
			l := newLauncher(s, name)
			l.say(false, "The machine has been switched off. Change the settings and Start it again.")
			g.showLauncher(l)
		}
		return nil
	}
	if g.shotDir != "" && g.launcher.tab >= len(launcherTabs) {
		return ebiten.Termination
	}
	start, quit := g.launcher.update()
	if quit {
		return ebiten.Termination
	}
	if start != nil {
		s := *start
		m, err := buildMachine(&s, "", nil, os.Stdout)
		if err != nil {
			g.launcher.say(true, "Can't start: %v", err)
			return nil
		}
		g.startMachine(m, &s, g.launcher.name)
	}
	return nil
}

// powerCycle switches the machine off and on: a new machine from the same
// settings, with the disks that are in the drives now.
func (g *app) powerCycle() {
	s, name := g.s, g.name
	for n, list := range g.disp.Disks {
		if i := g.disp.DiskIndex[n]; i >= 0 && i < len(list) {
			// The disk in the drive goes in first; the rest stay listed.
			s.Drives[n] = append(config.List{list[i]}, slices.Delete(slices.Clone(list), i, i+1)...)
		} else {
			s.Drives[n] = nil
		}
	}
	// The drives as they are now (DiskIndex is -1 for an empty one).
	if s.NumDrives < len(g.disp.Disks) {
		for n := len(g.disp.Disks) - 1; n > 0; n-- {
			if g.m.a.Disk.DF(n) != nil {
				s.NumDrives = max(s.NumDrives, n+1)
				break
			}
		}
	}
	g.stopMachine()
	m, err := buildMachine(&s, "", nil, os.Stdout)
	if err != nil {
		l := newLauncher(s, name)
		l.say(true, "Can't switch it on again: %v", err)
		g.showLauncher(l)
		return
	}
	fmt.Println("Power cycled")
	g.startMachine(m, &s, name)
}

func (g *app) Draw(screen *ebiten.Image) {
	if g.disp != nil {
		g.disp.Draw(screen)
		return
	}
	g.launcher.draw(screen)
	if g.shotDir != "" {
		g.shotFrame++
		if g.shotFrame%10 == 0 { // a few frames per tab, for fonts to settle
			tab := g.shotFrame/10 - 1
			img := image.NewRGBA(screen.Bounds())
			screen.ReadPixels(img.Pix)
			if f, err := os.Create(filepath.Join(g.shotDir, fmt.Sprintf("tab%d.png", tab))); err == nil {
				png.Encode(f, img)
				f.Close()
			}
			g.launcher.tab = tab + 1
		}
	}
}

// DrawFinalScreen lets the emulator's display draw super-hires frames at
// the window's resolution (Display.DrawFinalScreen).
func (g *app) DrawFinalScreen(screen ebiten.FinalScreen, offscreen *ebiten.Image, geoM ebiten.GeoM) {
	if g.disp != nil {
		g.disp.DrawFinalScreen(screen, offscreen, geoM)
		return
	}
	ebiten.DefaultDrawFinalScreen(screen, offscreen, geoM)
}

func (g *app) Layout(w, h int) (int, int) {
	if g.disp != nil {
		return g.disp.Layout(w, h)
	}
	return launcherW, launcherH
}
