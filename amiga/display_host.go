//go:build !noebitengine

package amiga

import (
	"fmt"
	"image/color"
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

// hostKey reports whether the host command modifier is held.
func hostKey() bool { return ebiten.IsKeyPressed(ebiten.KeyMeta) }

func (d *Display) pollHostKeys() {
	if !hostKey() {
		return
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyW) {
		d.setWarp(!d.warp)
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
		out.SetMuted(on)
	}
	fmt.Printf("warp %s\n", map[bool]string{true: "on", false: "off"}[on])
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
		d.warpSpeed = float64(d.warpFrames) / el.Seconds() / (2 * PaulaClock / CyclesPerFrame)
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
