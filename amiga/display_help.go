//go:build !noebitengine

package amiga

import (
	"bytes"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/gofont/goregular"
)

// The help overlay (Cmd+/): every key and mouse action, over the picture.
// Like the status bar it is laid out in logical pixels and drawn at the
// window's resolution: rendered in Draw (renderHelp), put on the final
// screen in DrawFinalScreen (placeHelp).

// helpSections are the overlay's contents: a heading, then key and action
// pairs.
var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Mouse and joystick", [][2]string{
		{"Click the picture", "use the mouse (Esc or F12 releases it)"},
		{"Ctrl+F12", "arrow keys and Right Ctrl/Alt as the joystick"},
	}},
	{"Disks", [][2]string{
		{"Drop a disk image", "insert it in DF0: (on a drive's light: that drive)"},
		{"Click a drive's light", "choose a disk; right-click ejects"},
		{"F11", "choose a disk for DF0: (Alt DF1:, Ctrl DF2:, Ctrl+Alt DF3:)"},
		{"Cmd+F11 / Shift+F11", "the drive's next disk / eject"},
	}},
	{"Machine", [][2]string{
		{"Cmd+R / Cmd+Shift+R", "reset / power off and on"},
		{"Cmd+P / Cmd+W", "pause / warp (as fast as possible)"},
		{"Cmd+Shift+1-9 / Cmd+1-9", "save state / load state"},
		{"Cmd+S / Cmd+F", "screenshot / full screen"},
		{"Cmd+,", "the configuration screen"},
		{"Cmd+/", "this help"},
	}},
}

var (
	helpBG    = color.NRGBA{0x14, 0x15, 0x18, 0xEC}
	helpEdge  = color.RGBA{0x45, 0x5C, 0x86, 0xFF}
	helpTitle = color.RGBA{0xF2, 0xC0, 0x4A, 0xFF}
	helpKey   = color.RGBA{0xEC, 0xEC, 0xEE, 0xFF}
	helpText  = color.RGBA{0xB4, 0xB8, 0xC0, 0xFF}
)

var helpFontSource *text.GoTextFaceSource

func helpFace(size float64) text.Face {
	if helpFontSource == nil {
		s, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
		if err != nil {
			panic(err)
		}
		helpFontSource = s
	}
	return &text.GoTextFace{Source: helpFontSource, Size: size}
}

// The overlay's image, at helpScale window pixels per logical pixel, and
// its top left in logical pixels.
var (
	helpImg           *ebiten.Image
	helpScale         float64
	helpLeft, helpTop float64
)

// pollHelpKeys toggles the overlay on Cmd+/ and closes it on Esc. It
// reports whether it took the key.
func (d *Display) pollHelpKeys() bool {
	if hostKey() && inpututil.IsKeyJustPressed(ebiten.KeySlash) {
		d.helpShown = !d.helpShown
		return true
	}
	if d.helpShown && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		d.helpShown = false
		d.suppressEscape = true // not for the Amiga
		return true
	}
	return false
}

// renderHelp draws the overlay for a picture w×h logical pixels at scale.
func (d *Display) renderHelp(w, h int, scale float64) {
	const (
		size    = 11.0 // font size, logical pixels
		line    = 16.0
		pad     = 14.0
		keyCol  = 150.0 // the key column's width
		panelW  = 470.0
		heading = 20.0
	)
	rows := 0
	for _, s := range helpSections {
		rows += len(s.keys)
	}
	panelH := pad*2 + heading + float64(len(helpSections))*(line+4) + float64(rows)*line + line
	panelH = min(panelH, float64(h)-8)
	pw, ph := int(panelW*scale+0.5), int(panelH*scale+0.5)
	if helpImg == nil || helpImg.Bounds().Dx() != pw || helpImg.Bounds().Dy() != ph {
		helpImg = ebiten.NewImage(pw, ph)
	}
	helpScale = scale
	helpLeft, helpTop = (float64(w)-panelW)/2, (float64(h)-panelH)/2
	img := helpImg
	img.Clear()
	s := float32(scale)
	fillRoundedRect(img, 0, 0, float32(pw), float32(ph), 8*s, helpBG)
	strokeRoundedRect(img, s/2, s/2, float32(pw)-s, float32(ph)-s, 8*s, s, helpEdge)

	face := helpFace(size * scale)
	put := func(str string, x, y float64, c color.Color, f text.Face) {
		op := &text.DrawOptions{}
		op.GeoM.Translate(x*scale, y*scale)
		op.ColorScale.ScaleWithColor(c)
		text.Draw(img, str, f, op)
	}
	y := pad
	put("goAmiga keys", pad, y, helpTitle, helpFace((size+3)*scale))
	y += heading + 4
	for _, sec := range helpSections {
		put(sec.title, pad, y, helpTitle, face)
		y += line + 4
		for _, k := range sec.keys {
			put(k[0], pad+8, y, helpKey, face)
			put(k[1], pad+8+keyCol, y, helpText, face)
			y += line
		}
	}
	footer := "Cmd+/ or Esc closes this"
	if n := d.joystickNote(); n != "" {
		footer = n + " · " + footer
	}
	put(footer, pad, y, helpText, face)
}

// placeHelp puts the overlay on the final screen.
func placeHelp(screen ebiten.FinalScreen, geoM ebiten.GeoM) {
	if helpImg == nil || helpScale <= 0 {
		return
	}
	op := &ebiten.DrawImageOptions{}
	if k := geoM.Element(0, 0) / helpScale; k < 0.999 || k > 1.001 {
		op.GeoM.Scale(k, k)
		op.Filter = ebiten.FilterLinear
	}
	op.GeoM.Translate(geoM.Apply(helpLeft, helpTop))
	screen.DrawImage(helpImg, op)
}
