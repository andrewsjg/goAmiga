//go:build !noebitengine

package amiga

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// Host commands use Cmd (macOS) or the Windows key. It is not an Amiga key,
// so a command never reaches the emulated keyboard (pollKeyboard ignores
// keys pressed with it).
//
//	Cmd+W  warp: run as fast as the host allows, without sound
//	Cmd+R  reset (as Ctrl+Amiga+Amiga): Kickstart restarts, disks stay in
//	Cmd+P  pause
//	Cmd+F  fullscreen
//	Cmd+S  screenshot: a PNG of the picture in ScreenshotDir
//	Cmd+Shift+1..9  save state to slot 1-9 (in StateDir)
//	Cmd+1..9        load state from slot 1-9
//	Cmd+Shift+R  power off and on (a cold start; the disks stay in)
//	Cmd+,  back to the configuration screen
//
// The status bar has Power, Reset and Config buttons for the same. Power
// and Config throw the running machine away, so they want a second click.

// hostKey reports whether the host command modifier is held.
func hostKey() bool { return ebiten.IsKeyPressed(ebiten.KeyMeta) }

func (d *Display) pollHostKeys() {
	if !hostKey() {
		return
	}
	for n := 1; n <= 9; n++ {
		if inpututil.IsKeyJustPressed(ebiten.KeyDigit0 + ebiten.Key(n)) {
			if ebiten.IsKeyPressed(ebiten.KeyShift) {
				d.saveSlot(n)
			} else {
				d.loadSlot(n)
			}
			return
		}
	}
	switch {
	case inpututil.IsKeyJustPressed(ebiten.KeyW):
		d.setWarp(!d.warp)
	case inpututil.IsKeyJustPressed(ebiten.KeyR) && ebiten.IsKeyPressed(ebiten.KeyShift):
		d.PowerRequested = true
	case inpututil.IsKeyJustPressed(ebiten.KeyR):
		d.reset()
	case inpututil.IsKeyJustPressed(ebiten.KeyP):
		d.paused = !d.paused
		if out := d.amiga.Paula.Output; out != nil {
			out.SetMuted(d.paused || d.warp)
		}
	case inpututil.IsKeyJustPressed(ebiten.KeyF):
		ebiten.SetFullscreen(!ebiten.IsFullscreen())
	case inpututil.IsKeyJustPressed(ebiten.KeyComma):
		d.ConfigRequested = true
	case inpututil.IsKeyJustPressed(ebiten.KeyS):
		if path, err := d.screenshot(); err != nil {
			d.message("screenshot failed: %v", err)
		} else {
			d.message("saved %s", filepath.Base(path))
		}
	}
}

// reset is the keyboard reset (Ctrl+Amiga+Amiga): Kickstart restarts.
func (d *Display) reset() {
	if err := d.amiga.Reset(); err != nil {
		d.message("reset failed: %v", err)
	} else {
		d.message("reset")
	}
}

// statusButton is one of the status bar's buttons.
type statusButton struct {
	id, label string
	x, w      int
	confirm   bool // wants a second click
	act       func()
}

// statusButtons lays out the buttons at the right of a status bar w wide.
func (d *Display) statusButtons(w int) []statusButton {
	bs := []statusButton{
		{id: "power", label: "Power", confirm: true, act: func() { d.PowerRequested = true }},
		{id: "reset", label: "Reset", act: d.reset},
		{id: "config", label: "Config", confirm: true, act: func() { d.ConfigRequested = true }},
	}
	x := w - 5
	for i := len(bs) - 1; i >= 0; i-- {
		label := bs[i].label
		if d.confirming(bs[i].id) {
			label = "Sure?"
		}
		bs[i].w = int(max(statusTextWidth(label), statusTextWidth(bs[i].label))) + 14
		x -= bs[i].w
		bs[i].x = x
		x -= 4
	}
	return bs
}

func (d *Display) confirming(id string) bool {
	return d.confirm == id && time.Now().Before(d.confirmUntil)
}

// statusClick handles a click on the status bar's buttons and reports
// whether it was on one.
//
// A click on a floppy drive's light opens the file chooser for it; a
// right click ejects its disk.
func (d *Display) statusClick(right bool) bool {
	if !d.StatusBar {
		return false
	}
	w, h := d.amiga.Video.DisplaySize()
	mx, my := ebiten.CursorPosition()
	if my < h || my >= h+statusHeight {
		return false
	}
	if unit := d.hoveredDrive(mx, my, h); unit >= 0 {
		if right {
			d.eject(unit)
		} else {
			d.openChooser(unit)
		}
		return true
	}
	if right {
		return true
	}
	for _, b := range d.statusButtons(w) {
		if mx < b.x || mx >= b.x+b.w {
			continue
		}
		switch {
		case !b.confirm:
			b.act()
		case d.confirming(b.id):
			d.confirm = ""
			b.act()
		default:
			d.confirm, d.confirmUntil = b.id, time.Now().Add(3*time.Second)
			what := map[string]string{"power": "switch the Amiga off and on", "config": "switch the Amiga off and change its settings"}[b.id]
			d.message("click again to %s", what)
		}
		return true
	}
	d.confirm = ""
	return true // the rest of the bar: not the Amiga's either
}

// message shows a note in the status bar for a few seconds (and prints it).
func (d *Display) message(format string, args ...any) {
	d.msg = fmt.Sprintf(format, args...)
	d.msgUntil = time.Now().Add(3 * time.Second)
	fmt.Println(d.msg)
}

// screenshot saves the picture as a PNG named by the time. Non-interlaced
// rows are doubled, as on screen, so the image has the right proportions.
func (d *Display) screenshot() (string, error) {
	src := d.amiga.Video.ScreenImage()
	w, h := d.amiga.Video.DisplaySize()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sb := src.Bounds()
	for y := 0; y < h; y++ {
		sy := y * sb.Dy() / h
		copy(img.Pix[y*img.Stride:y*img.Stride+4*w], src.Pix[sy*src.Stride:sy*src.Stride+4*min(w, sb.Dx())])
	}
	dir := d.ScreenshotDir
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, time.Now().Format("goamiga-20060102-150405.png"))
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return "", err
	}
	return path, f.Close()
}

// ---- Save states ----

func (d *Display) slotPath(n int) string {
	dir := d.StateDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, fmt.Sprintf("goamiga-%d.state", n))
}

func (d *Display) saveSlot(n int) {
	path := d.slotPath(n)
	f, err := os.Create(path)
	if err == nil {
		err = d.amiga.SaveState(f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		d.message("save state %d failed: %v", n, err)
		return
	}
	d.message("saved state %d", n)
}

func (d *Display) loadSlot(n int) {
	f, err := os.Open(d.slotPath(n))
	if err != nil {
		d.message("no state %d", n)
		return
	}
	defer f.Close()
	if err := d.amiga.LoadState(f); err != nil {
		d.message("load state %d failed: %v", n, err)
		return
	}
	d.syncDisks()
	if out := d.amiga.Paula.Output; out != nil {
		out.SetMuted(d.warp || d.paused)
	}
	d.message("loaded state %d", n)
}

// syncDisks points the title's disk names at whatever the drives hold
// after a state load.
func (d *Display) syncDisks() {
	for n := range d.DiskIndex {
		d.DiskIndex[n] = -1
		dr := d.amiga.Disk.DF(n)
		if dr == nil || !dr.Inserted() || dr.path == "" {
			continue
		}
		i := slices.Index(d.Disks[n], dr.path)
		if i < 0 {
			d.Disks[n] = append(d.Disks[n], dr.path)
			i = len(d.Disks[n]) - 1
		}
		d.DiskIndex[n] = i
	}
}

// ---- Warp ----

// warpBudget is the host time each tick spends emulating in warp mode,
// leaving the rest of the 20 ms tick for drawing and input.
const warpBudget = 14 * time.Millisecond

func (d *Display) setWarp(on bool) {
	d.warp = on
	d.warpFrames, d.warpSince, d.warpSpeed = 0, time.Now(), 0
	if out := d.amiga.Paula.Output; out != nil {
		out.SetMuted(on || d.paused)
	}
	d.message("warp %s", map[bool]string{true: "on", false: "off"}[on])
}

// runWarp emulates for warpBudget. Only the last frame is drawn: the rest
// run with the display off, which is several times faster.
func (d *Display) runWarp() {
	v := d.amiga.Video
	deadline := time.Now().Add(warpBudget)
	frames := 0
	v.DisplayActive = false
	for time.Now().Before(deadline) {
		if d.amiga.RunFrame() != nil {
			break
		}
		frames++
	}
	v.DisplayActive = true
	d.amiga.RunFrame()
	frames++

	d.warpFrames += frames
	if el := time.Since(d.warpSince); el >= time.Second {
		d.warpSpeed = float64(d.warpFrames) / el.Seconds() / d.amiga.FrameRate()
		d.warpFrames, d.warpSince = 0, time.Now()
	}
}
