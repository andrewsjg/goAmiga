//go:build !noebitengine

package main

import (
	"bytes"
	"image/color"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/goregular"
)

// A small immediate-mode widget kit in the style of Workbench 2.0: a grey
// screen, raised and recessed bevels, a blue highlight. Each frame Update
// lays out the screen with the widget calls, which handle input as they
// go and record drawing operations for Draw to replay.

var (
	uiGrey   = color.RGBA{0xAA, 0xAA, 0xAA, 0xFF}
	uiWhite  = color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	uiBlack  = color.RGBA{0x00, 0x00, 0x00, 0xFF}
	uiBlue   = color.RGBA{0x66, 0x88, 0xBB, 0xFF}
	uiDim    = color.RGBA{0x77, 0x77, 0x77, 0xFF}
	uiRed    = color.RGBA{0xAA, 0x22, 0x22, 0xFF}
	uiDarker = color.RGBA{0x99, 0x99, 0x99, 0xFF}
)

const uiTextH = 16

var uiFaceSource *text.GoTextFaceSource

func uiFace(size float64) text.Face {
	if uiFaceSource == nil {
		s, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
		if err != nil {
			panic(err)
		}
		uiFaceSource = s
	}
	return &text.GoTextFace{Source: uiFaceSource, Size: size}
}

type ui struct {
	face, bold text.Face
	ops        []func(*ebiten.Image)

	mx, my     int
	clicked    bool // left button went down this frame
	rclicked   bool
	double     bool // and it was a double click
	lastClick  time.Time
	lastClickX int
	lastClickY int

	focus  string // the text field being edited
	typed  []rune
	back   bool // Backspace
	enter  bool
	escape bool
	tab    bool
}

func newUI() *ui {
	return &ui{face: uiFace(14), bold: uiFace(15)}
}

// begin reads the input for a frame and clears the drawing.
func (u *ui) begin() {
	u.ops = u.ops[:0]
	u.mx, u.my = ebiten.CursorPosition()
	u.clicked = inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	u.rclicked = inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight)
	u.double = false
	if u.clicked {
		now := time.Now()
		near := abs(u.mx-u.lastClickX) < 4 && abs(u.my-u.lastClickY) < 4
		u.double = near && now.Sub(u.lastClick) < 400*time.Millisecond
		u.lastClick, u.lastClickX, u.lastClickY = now, u.mx, u.my
	}
	u.typed = ebiten.AppendInputChars(u.typed[:0])
	u.back = repeating(ebiten.KeyBackspace)
	u.enter = inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyNumpadEnter)
	u.escape = inpututil.IsKeyJustPressed(ebiten.KeyEscape)
	u.tab = inpututil.IsKeyJustPressed(ebiten.KeyTab)
}

// repeating reports a key press, with auto-repeat while it is held.
func repeating(k ebiten.Key) bool {
	d := inpututil.KeyPressDuration(k)
	return d == 1 || d > 20 && d%3 == 0
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (u *ui) draw(dst *ebiten.Image) {
	for _, op := range u.ops {
		op(dst)
	}
}

func (u *ui) hit(x, y, w, h int) bool { return u.mx >= x && u.mx < x+w && u.my >= y && u.my < y+h }

// ---- Drawing ----

func (u *ui) fill(x, y, w, h int, c color.Color) {
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		vector.FillRect(dst, float32(x), float32(y), float32(w), float32(h), c, false)
	})
}

// bevel draws a box edge: raised (light top-left) or recessed.
func (u *ui) bevel(x, y, w, h int, raised bool) {
	hi, lo := uiWhite, uiBlack
	if !raised {
		hi, lo = lo, hi
	}
	u.fill(x, y, w, 1, hi)
	u.fill(x, y, 1, h, hi)
	u.fill(x, y+h-1, w, 1, lo)
	u.fill(x+w-1, y, 1, h, lo)
}

func (u *ui) text(x, y int, s string, c color.Color) { u.textFace(x, y, s, c, u.face) }

func (u *ui) textFace(x, y int, s string, c color.Color, f text.Face) {
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(x), float64(y))
		op.ColorScale.ScaleWithColor(c)
		text.Draw(dst, s, f, op)
	})
}

func (u *ui) textWidth(s string) int {
	w, _ := text.Measure(s, u.face, 0)
	return int(w)
}

// fit shortens s with "…" at the front (paths: the end matters) to fit w.
func (u *ui) fit(s string, w int) string {
	if u.textWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && u.textWidth("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// ---- Widgets ----

// button is a raised box with a centred label; it reports a click.
func (u *ui) button(x, y, w, h int, label string) bool {
	return u.buttonOn(x, y, w, h, label, true)
}

func (u *ui) buttonOn(x, y, w, h int, label string, enabled bool) bool {
	over := enabled && u.hit(x, y, w, h)
	pressed := over && ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
	if pressed {
		u.fill(x, y, w, h, uiBlue)
	}
	u.bevel(x, y, w, h, !pressed)
	col := uiBlack
	if !enabled {
		col = uiDim
	}
	tw := u.textWidth(label)
	u.text(x+(w-tw)/2, y+(h-uiTextH)/2, label, col)
	return over && u.clicked
}

// cycle is a cycle gadget: a click steps through opts (a right click
// steps back). It returns the chosen index.
func (u *ui) cycle(x, y, w int, opts []string, cur int) int {
	const h = 22
	if cur < 0 || cur >= len(opts) {
		cur = 0
	}
	over := u.hit(x, y, w, h)
	if over && u.clicked {
		cur = (cur + 1) % len(opts)
	} else if over && u.rclicked {
		cur = (cur + len(opts) - 1) % len(opts)
	}
	u.bevel(x, y, w, h, true)
	// The cycle glyph: a ring with an arrowhead.
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		cx, cy := float32(x+13), float32(y+h/2)
		vector.StrokeCircle(dst, cx, cy, 5, 1.5, uiBlack, true)
		var p vector.Path
		p.MoveTo(cx+2, cy-8)
		p.LineTo(cx+7, cy-5)
		p.LineTo(cx+2, cy-2)
		p.Close()
		vs, is := p.AppendVerticesAndIndicesForFilling(nil, nil)
		for i := range vs {
			vs[i].ColorR, vs[i].ColorG, vs[i].ColorB, vs[i].ColorA = 0, 0, 0, 1
		}
		dst.DrawTriangles(vs, is, uiPixel(), &ebiten.DrawTrianglesOptions{AntiAlias: true})
	})
	u.fill(x+24, y+4, 1, h-8, uiBlack)
	u.fill(x+25, y+4, 1, h-8, uiWhite)
	u.text(x+32, y+(h-uiTextH)/2, u.fit(opts[cur], w-38), uiBlack)
	return cur
}

// checkbox toggles *v on a click on the box or its label.
func (u *ui) checkbox(x, y int, label string, v *bool) bool {
	const s = 18
	w := s + 8 + u.textWidth(label)
	changed := false
	if u.hit(x, y, w, s) && u.clicked {
		*v = !*v
		changed = true
	}
	u.bevel(x, y, s+4, s, true)
	if *v {
		u.ops = append(u.ops, func(dst *ebiten.Image) {
			vector.StrokeLine(dst, float32(x+5), float32(y+9), float32(x+9), float32(y+14), 2.5, uiBlack, true)
			vector.StrokeLine(dst, float32(x+9), float32(y+14), float32(x+17), float32(y+4), 2.5, uiBlack, true)
		})
	}
	u.text(x+s+12, y+1, label, uiBlack)
	return changed
}

// textField edits *v; id names it for keyboard focus. It reports when
// editing ended with Enter.
func (u *ui) textField(id string, x, y, w int, v *string) bool {
	const h = 22
	if u.clicked {
		if u.hit(x, y, w, h) {
			u.focus = id
		} else if u.focus == id {
			u.focus = ""
		}
	}
	done := false
	if u.focus == id {
		for _, r := range u.typed {
			if r >= ' ' && r != 0x7F {
				*v += string(r)
			}
		}
		if u.back && *v != "" {
			r := []rune(*v)
			*v = string(r[:len(r)-1])
		}
		if u.enter || u.escape || u.tab {
			u.focus = ""
			done = u.enter
			u.enter = false // consumed
		}
	}
	u.fill(x, y, w, h, uiDarker)
	u.bevel(x, y, w, h, false)
	u.bevel(x+1, y+1, w-2, h-2, true)
	shown := u.fit(*v, w-12)
	u.text(x+5, y+(h-uiTextH)/2, shown, uiBlack)
	if u.focus == id && time.Now().UnixMilli()/500%2 == 0 {
		cx := x + 6 + u.textWidth(shown)
		u.fill(cx, y+4, 2, h-8, uiBlue)
	}
	return done
}

// list shows items with *sel highlighted; a click selects. It reports a
// double click on an item.
func (u *ui) list(x, y, w, h int, items []string, sel *int) bool {
	const row = 20
	u.fill(x, y, w, h, uiGrey)
	u.bevel(x, y, w, h, false)
	activated := false
	for i, it := range items {
		ry := y + 3 + i*row
		if ry+row > y+h-2 {
			break
		}
		if u.hit(x+2, ry, w-4, row) && u.clicked {
			*sel = i
			activated = u.double
		}
		if i == *sel {
			u.fill(x+2, ry, w-4, row, uiBlue)
			u.text(x+8, ry+2, u.fit(it, w-16), uiWhite)
		} else {
			u.text(x+8, ry+2, u.fit(it, w-16), uiBlack)
		}
	}
	return activated
}

// label draws a right-aligned field label ending at x.
func (u *ui) label(x, y int, s string) {
	u.text(x-u.textWidth(s), y+3, s, uiBlack)
}

// heading draws a section title with a groove under it.
func (u *ui) heading(x, y, w int, s string) {
	u.textFace(x, y, s, uiBlack, u.bold)
	u.fill(x, y+uiTextH+4, w, 1, uiDim)
	u.fill(x, y+uiTextH+5, w, 1, uiWhite)
}

// wrap splits s into lines no wider than w.
func (u *ui) wrap(s string, w int) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			try := strings.TrimSpace(line + " " + word)
			if line != "" && u.textWidth(try) > w {
				lines = append(lines, line)
				line = word
			} else {
				line = try
			}
		}
		lines = append(lines, line)
	}
	return lines
}

var uiWhitePixel *ebiten.Image

// uiPixel is a white source image for DrawTriangles.
func uiPixel() *ebiten.Image {
	if uiWhitePixel == nil {
		uiWhitePixel = ebiten.NewImage(1, 1)
		uiWhitePixel.Fill(color.White)
	}
	return uiWhitePixel
}
