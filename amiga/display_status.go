//go:build !noebitengine

package amiga

import (
	"bytes"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/gomono"
)

// ---- Status bar ----
//
// The bar below the picture: activity lights at the left (power, each
// drive with its head's track, the hard disks, the network), a note
// (paused, or a message) after them, and at the right the warp speed and
// the Power, Reset and Config buttons.
//
// It is laid out in logical pixels, like the picture, so clicks are
// tested there (statusClick); but it is drawn at the window's own
// resolution (DrawFinalScreen), so its text stays sharp however far the
// window is scaled, Retina included.

// statusHeight is the bar's height in logical pixels.
const statusHeight = 20

// statusFontSize is the bar's font size in logical pixels.
const statusFontSize = 10

func (d *Display) statusBarHeight() int {
	if d.StatusBar {
		return statusHeight
	}
	return 0
}

var (
	statusBG     = color.RGBA{0x1C, 0x1D, 0x20, 0xFF}
	statusTop    = color.RGBA{0x3A, 0x3C, 0x42, 0xFF}
	statusText   = color.RGBA{0xC8, 0xCA, 0xD0, 0xFF}
	statusDim    = color.RGBA{0x80, 0x84, 0x8C, 0xFF}
	statusNote   = color.RGBA{0xF2, 0xC0, 0x4A, 0xFF}
	ledOff       = color.RGBA{0x34, 0x36, 0x3B, 0xFF}
	ledOffEdge   = color.RGBA{0x4A, 0x4D, 0x54, 0xFF}
	ledPower     = color.RGBA{0xFF, 0x3B, 0x30, 0xFF}
	ledPowerDim  = color.RGBA{0x8A, 0x22, 0x1C, 0xFF}
	ledDrive     = color.RGBA{0x3D, 0xDC, 0x5A, 0xFF}
	ledWrite     = color.RGBA{0xFF, 0x96, 0x1E, 0xFF}
	warpColor    = color.RGBA{0xF2, 0xC0, 0x4A, 0xFF}
	buttonBG     = color.RGBA{0x34, 0x36, 0x3B, 0xFF}
	buttonHover  = color.RGBA{0x45, 0x5C, 0x86, 0xFF}
	buttonDanger = color.RGBA{0xA8, 0x34, 0x28, 0xFF}
	buttonEdge   = color.RGBA{0x55, 0x58, 0x60, 0xFF}
	buttonText   = color.RGBA{0xEC, 0xEC, 0xEE, 0xFF}
)

var statusFontSource *text.GoTextFaceSource

// statusFace is the bar's font (Go Mono, so numbers don't jitter) at
// size pixels.
func statusFace(size float64) text.Face {
	if statusFontSource == nil {
		s, err := text.NewGoTextFaceSource(bytes.NewReader(gomono.TTF))
		if err != nil {
			panic(err)
		}
		statusFontSource = s
	}
	return &text.GoTextFace{Source: statusFontSource, Size: size}
}

// statusTextWidth is s's width in logical pixels.
func statusTextWidth(s string) float64 {
	return text.Advance(s, statusFace(statusFontSize))
}

// statusLight is an activity light: its label and colour (nil: off), the
// floppy drive it stands for (-1 for the others), and where it is: x and
// w in logical pixels, label and LED together.
type statusLight struct {
	label string
	on    color.Color
	unit  int
	x, w  float64
}

// The lights' geometry, in logical pixels.
const (
	lightsLeft = 7.0
	ledGap     = 5.0  // label to LED
	ledW       = 13.0 // the LED
	lightGap   = 14.0 // LED to the next label
)

// statusLights are the bar's lights, left to right.
func (d *Display) statusLights() []statusLight {
	a := d.amiga
	power := color.Color(ledPowerDim) // the audio filter off dims it, as on the A500
	if a.PowerLEDBright() {
		power = ledPower
	}
	ls := []statusLight{{label: "POWER", on: power, unit: -1}}
	for n := 0; n < 4; n++ {
		dr := a.Disk.DF(n)
		if dr == nil {
			continue
		}
		var c color.Color
		if dr.MotorOn() {
			c = ledDrive
		}
		ls = append(ls, statusLight{label: fmt.Sprintf("DF%d %02d", n, dr.Cylinder()), on: c, unit: n})
	}
	activity := func(label string, active, write bool) {
		var c color.Color
		if active {
			c = ledDrive
			if write {
				c = ledWrite
			}
		}
		ls = append(ls, statusLight{label: label, on: c, unit: -1})
	}
	if h := a.HardDisk; h != nil {
		active, write := h.Activity()
		activity("HD", active, write)
	}
	if g := a.Gayle; g != nil && (g.drives[0] != nil || g.drives[1] != nil) {
		active, write := g.Activity()
		activity("IDE", active, write)
	}
	if sc := a.SCSI; sc != nil {
		active, write := sc.Activity()
		activity("SCSI", active, write)
	}
	if e := a.Ethernet; e != nil {
		activity("NET", e.Activity(), false)
	}
	x := lightsLeft
	for i := range ls {
		ls[i].x = x
		ls[i].w = statusTextWidth(ls[i].label) + ledGap + ledW
		x += ls[i].w + lightGap
	}
	return ls
}

// hoveredDrive is the floppy drive whose light the pointer is over
// (logical coordinates, the bar's top at y), or -1.
func (d *Display) hoveredDrive(mx, my, y int) int {
	if d.mouseGrabbed || my < y || my >= y+statusHeight {
		return -1
	}
	for _, l := range d.statusLights() {
		if l.unit >= 0 && float64(mx) >= l.x-3 && float64(mx) < l.x+l.w+3 {
			return l.unit
		}
	}
	return -1
}

// statusNoteText is the note after the lights: with the pointer on a
// drive's light, its disk and what clicking does; otherwise paused, and
// any message.
func (d *Display) statusNoteText(y int) string {
	mx, my := ebiten.CursorPosition()
	if unit := d.hoveredDrive(mx, my, y); unit >= 0 {
		disk := "empty"
		if i := d.DiskIndex[unit]; i >= 0 && i < len(d.Disks[unit]) && d.amiga.Disk.DF(unit).Inserted() {
			disk = filepath.Base(d.Disks[unit][i])
			if _, inner, ok := strings.Cut(disk, "#"); ok {
				disk = inner // a disk inside a zip
			}
		}
		return disk + " · click: change, right-click: eject"
	}
	var notes []string
	if d.paused {
		notes = append(notes, "Paused (Cmd+P)")
	}
	if d.msg != "" && time.Now().Before(d.msgUntil) {
		notes = append(notes, d.msg)
	}
	return strings.Join(notes, "   ")
}

// The bar's own image, drawn at the window's resolution (statusScale
// window pixels per logical pixel).
var (
	statusImg   *ebiten.Image
	statusScale float64
)

// finalScale is how many window pixels a logical pixel takes: the
// window's size (from Layout) over the logical screen's, at the monitor's
// pixel density, as Ebitengine fits the screen to the window.
func (d *Display) finalScale(lw, lh int) float64 {
	if lw <= 0 || lh <= 0 || d.outsideW <= 0 || d.outsideH <= 0 {
		return 1
	}
	dsf := 1.0
	if m := ebiten.Monitor(); m != nil {
		dsf = m.DeviceScaleFactor()
	}
	return min(float64(d.outsideW)/float64(lw), float64(d.outsideH)/float64(lh)) * dsf
}

// placeStatus puts the bar drawn by renderStatus onto the final screen,
// where geoM puts logical row y.
func placeStatus(screen ebiten.FinalScreen, geoM ebiten.GeoM, y int) {
	if statusImg == nil || statusScale <= 0 {
		return
	}
	op := &ebiten.DrawImageOptions{}
	// The image is at statusScale already; correct for any difference.
	if k := geoM.Element(0, 0) / statusScale; k < 0.999 || k > 1.001 {
		op.GeoM.Scale(k, k)
		op.Filter = ebiten.FilterLinear
	}
	op.GeoM.Translate(geoM.Apply(0, float64(y)))
	screen.DrawImage(statusImg, op)
}

// renderStatus draws the bar into its image at scale window pixels per
// logical pixel. y is the bar's top and w the width, in logical pixels.
func (d *Display) renderStatus(w, y int, scale float64) {
	if scale <= 0 {
		return
	}
	statusScale = scale
	pw, ph := int(float64(w)*scale+0.5), int(statusHeight*scale+0.5)
	if pw <= 0 || ph <= 0 {
		return
	}
	if statusImg == nil || statusImg.Bounds().Dx() != pw || statusImg.Bounds().Dy() != ph {
		statusImg = ebiten.NewImage(pw, ph)
	}
	img := statusImg
	img.Fill(statusBG)
	s := float32(scale)
	vector.FillRect(img, 0, 0, float32(pw), max(1, s/2), statusTop, false)

	face := statusFace(statusFontSize * scale)
	// drawText puts s with its left edge at logical x, centred in the bar.
	drawText := func(str string, x float64, c color.Color) {
		op := &text.DrawOptions{}
		op.GeoM.Translate(x*scale, float64(ph)/2)
		op.PrimaryAlign, op.SecondaryAlign = text.AlignStart, text.AlignCenter
		op.ColorScale.ScaleWithColor(c)
		text.Draw(img, str, face, op)
	}
	mid := float32(statusHeight) / 2 * s

	// The lights; a drive's is highlighted under the pointer.
	x := lightsLeft
	mx, my := ebiten.CursorPosition()
	hovered := d.hoveredDrive(mx, my, y)
	for _, l := range d.statusLights() {
		if l.unit >= 0 && l.unit == hovered {
			fillRoundedRect(img, float32(l.x-3)*s, 2*s, float32(l.w+6)*s, float32(statusHeight-4)*s, 3*s, buttonHover)
		}
		drawText(l.label, l.x, statusText)
		lx, lw, lh := float32(l.x+l.w-ledW)*s, ledW*s, 6*s
		ly := mid - lh/2
		if l.on != nil {
			// A soft glow round a lit LED.
			r, g, b, _ := l.on.RGBA()
			glow := color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0x50}
			fillRoundedRect(img, lx-2*s, ly-2*s, lw+4*s, lh+4*s, 4*s, glow)
			fillRoundedRect(img, lx, ly, lw, lh, 2*s, l.on)
		} else {
			fillRoundedRect(img, lx, ly, lw, lh, 2*s, ledOff)
			strokeRoundedRect(img, lx+s/2, ly+s/2, lw-s, lh-s, 2*s, s/2, ledOffEdge)
		}
		x = l.x + l.w + lightGap
	}

	// The buttons and the warp speed, at the right.
	buttons := d.statusButtons(w)
	right := float64(buttons[0].x)
	if d.warp {
		label := "WARP"
		if d.warpSpeed > 0 {
			label = fmt.Sprintf("WARP %.0f×", d.warpSpeed)
		}
		pillW := statusTextWidth(label) + 12
		px := right - 10 - pillW
		fillRoundedRect(img, float32(px)*s, 3*s, float32(pillW)*s, float32(statusHeight-6)*s, 3*s, warpColor)
		drawText(label, px+6, statusBG)
		right = px
	}
	for _, b := range buttons {
		bg := buttonBG
		if !d.mouseGrabbed && my >= y && my < y+statusHeight && mx >= b.x && mx < b.x+b.w {
			bg = buttonHover
		}
		label := b.label
		if d.confirming(b.id) {
			label, bg = "Sure?", buttonDanger
		}
		bx, bw := float32(b.x)*s, float32(b.w)*s
		fillRoundedRect(img, bx, 3*s, bw, float32(statusHeight-6)*s, 3*s, bg)
		strokeRoundedRect(img, bx+s/2, 3*s+s/2, bw-s, float32(statusHeight-6)*s-s, 3*s, s/2, buttonEdge)
		drawText(label, float64(b.x)+(float64(b.w)-statusTextWidth(label))/2, buttonText)
	}

	// The note, between the lights and the right-hand side, shortened to
	// fit.
	if note := []rune(d.statusNoteText(y)); len(note) > 0 {
		room := right - 12 - (x + 4)
		if statusTextWidth(string(note)) > room {
			for len(note) > 0 && statusTextWidth(string(note)+"…") > room {
				note = note[:len(note)-1]
			}
			note = append(note, '…')
		}
		drawText(string(note), x+4, statusNote)
	} else if hint := "Cmd+/ help"; right-12-statusTextWidth(hint) > x+4 {
		drawText(hint, right-12-statusTextWidth(hint), statusDim)
	}

}

// roundedRect is the path of a rectangle with corners of radius r.
func roundedRect(x, y, w, h, r float32) *vector.Path {
	r = min(r, w/2, h/2)
	var p vector.Path
	p.MoveTo(x+r, y)
	p.LineTo(x+w-r, y)
	p.ArcTo(x+w, y, x+w, y+r, r)
	p.LineTo(x+w, y+h-r)
	p.ArcTo(x+w, y+h, x+w-r, y+h, r)
	p.LineTo(x+r, y+h)
	p.ArcTo(x, y+h, x, y+h-r, r)
	p.LineTo(x, y+r)
	p.ArcTo(x, y, x+r, y, r)
	p.Close()
	return &p
}

func fillRoundedRect(dst *ebiten.Image, x, y, w, h, r float32, c color.Color) {
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(c)
	vector.FillPath(dst, roundedRect(x, y, w, h, r), nil, op)
}

func strokeRoundedRect(dst *ebiten.Image, x, y, w, h, r, width float32, c color.Color) {
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(c)
	vector.StrokePath(dst, roundedRect(x, y, w, h, r), &vector.StrokeOptions{Width: width}, op)
}
