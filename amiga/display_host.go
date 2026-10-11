//go:build !noebitengine

package amiga

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"
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
//	Cmd+,  back to the configuration screen (when started from it)

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
	case inpututil.IsKeyJustPressed(ebiten.KeyR):
		if err := d.amiga.Reset(); err != nil {
			d.message("reset failed: %v", err)
		} else {
			d.message("reset")
		}
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

// ---- Status bar ----

// statusHeight is the status bar below the picture, in logical pixels (the
// picture is 512 or 568 high).
const statusHeight = 18

func (d *Display) statusBarHeight() int {
	if d.StatusBar {
		return statusHeight
	}
	return 0
}

var (
	ledOff    = color.RGBA{0x30, 0x30, 0x30, 0xFF}
	ledPower  = color.RGBA{0xE0, 0x20, 0x20, 0xFF}
	ledDim    = color.RGBA{0x70, 0x10, 0x10, 0xFF}
	ledDrive  = color.RGBA{0x20, 0xD0, 0x40, 0xFF}
	ledWrite  = color.RGBA{0xF0, 0x80, 0x10, 0xFF}
	statusBG  = color.RGBA{0x18, 0x18, 0x18, 0xFF}
	warpColor = color.RGBA{0xF0, 0xD0, 0x20, 0xFF}
)

// drawStatus draws the activity lights along the bottom: power (bright or
// dimmed with the audio filter, as on the A500), each connected drive's
// motor LED and head cylinder, the hard disk (green read, orange write),
// and the warp speed.
func (d *Display) drawStatus(screen *ebiten.Image, y int) {
	w := screen.Bounds().Dx()
	vector.FillRect(screen, 0, float32(y), float32(w), statusHeight, statusBG, false)
	x := 6
	led := func(label string, c color.Color) {
		ebitenutil.DebugPrintAt(screen, label, x, y+1)
		x += 6*len(label) + 3
		vector.FillRect(screen, float32(x), float32(y+6), 14, 6, c, false)
		x += 24
	}

	power := ledDim
	if d.amiga.PowerLEDBright() {
		power = ledPower
	}
	led("POWER", power)
	for n := 0; n < 4; n++ {
		dr := d.amiga.Disk.DF(n)
		if dr == nil {
			continue
		}
		c := color.Color(ledOff)
		if dr.MotorOn() {
			c = ledDrive
		}
		led(fmt.Sprintf("DF%d %02d", n, dr.Cylinder()), c)
	}
	if h := d.amiga.HardDisk; h != nil {
		c := color.Color(ledOff)
		if active, write := h.Activity(); active {
			c = ledDrive
			if write {
				c = ledWrite
			}
		}
		led("HD", c)
	}
	var notes []string
	if d.paused {
		notes = append(notes, "PAUSED (Cmd+P)")
	}
	if d.msg != "" && time.Now().Before(d.msgUntil) {
		notes = append(notes, d.msg)
	}
	if len(notes) > 0 {
		s := strings.Join(notes, "  ")
		ebitenutil.DebugPrintAt(screen, s, x+8, y+1)
	}
	if d.warp {
		s := fmt.Sprintf("WARP %.0fx", d.warpSpeed)
		if d.warpSpeed == 0 {
			s = "WARP"
		}
		// DebugPrint is white; mark warp with a coloured block before it.
		tx := w - 6*len(s) - 8
		vector.FillRect(screen, float32(tx-12), float32(y+6), 8, 6, warpColor, false)
		ebitenutil.DebugPrintAt(screen, s, tx, y+1)
	}
}
