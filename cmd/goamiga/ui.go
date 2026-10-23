//go:build !noebitengine

package main

import (
	"bytes"
	"image/color"
	"math"
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
//
// The layout is in design units (the launcher is 820×560 of them). The
// screen it draws on is the window at its real pixel size (Retina's too),
// and the drawing operations scale and centre the design onto it (sc,
// ox, oy), with fonts made at the scaled size, so text and edges are sharp
// rather than a small picture blown up. Measuring stays in design units.

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

	// The design's placement on the screen: screen = design*sc + offset.
	sc, ox, oy float64
	scaled     map[text.Face]text.Face // fonts at sc
	scaledAt   float64

	mx, my     int
	clicked    bool // left button went down this frame
	rclicked   bool
	double     bool // and it was a double click
	lastClick  time.Time
	lastClickX int
	lastClickY int

	// texts, when not nil, records where each piece of text went (tests
	// check the layout with it).
	texts *[]placedText

	focus  string // the text field being edited
	typed  []rune
	back   bool // Backspace
	enter  bool
	escape bool
	tab    bool
}

func newUI() *ui {
	return &ui{face: uiFace(14), bold: uiFace(15), sc: 1}
}

// place fits a design w×h onto a screen sw×sh pixels, centred.
func (u *ui) place(w, h, sw, sh int) {
	u.sc = min(float64(sw)/float64(w), float64(sh)/float64(h))
	if u.sc <= 0 {
		u.sc = 1
	}
	u.ox = (float64(sw) - float64(w)*u.sc) / 2
	u.oy = (float64(sh) - float64(h)*u.sc) / 2
}

// at is design point (x, y) on the screen.
func (u *ui) at(x, y int) (float32, float32) {
	return float32(float64(x)*u.sc + u.ox), float32(float64(y)*u.sc + u.oy)
}

// faceAt is f at the current scale.
func (u *ui) faceAt(f text.Face) text.Face {
	if u.scaledAt != u.sc {
		u.scaled, u.scaledAt = map[text.Face]text.Face{}, u.sc
	}
	if sf, ok := u.scaled[f]; ok {
		return sf
	}
	sf := f
	if g, ok := f.(*text.GoTextFace); ok {
		sf = &text.GoTextFace{Source: g.Source, Size: g.Size * u.sc}
	}
	u.scaled[f] = sf
	return sf
}

// begin reads the input for a frame and clears the drawing.
func (u *ui) begin() {
	u.ops = u.ops[:0]
	cx, cy := ebiten.CursorPosition()
	u.mx = int((float64(cx) - u.ox) / u.sc)
	u.my = int((float64(cy) - u.oy) / u.sc)
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
	// Edges on whole pixels, so 1-unit bevels stay crisp at any scale.
	x0, y0 := u.at(x, y)
	x1, y1 := u.at(x+w, y+h)
	x0, y0, x1, y1 = round32(x0), round32(y0), round32(x1), round32(y1)
	if x1 == x0 && w > 0 {
		x1++
	}
	if y1 == y0 && h > 0 {
		y1++
	}
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		vector.FillRect(dst, x0, y0, x1-x0, y1-y0, c, false)
	})
}

func round32(v float32) float32 { return float32(math.Round(float64(v))) }

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

// placedText is a piece of text and its box, in design units.
type placedText struct {
	x, y, w int
	s       string
}

func (u *ui) textFace(x, y int, s string, c color.Color, f text.Face) {
	if u.texts != nil {
		w, _ := text.Measure(s, f, 0)
		*u.texts = append(*u.texts, placedText{x, y, int(w), s})
	}
	sx, sy := u.at(x, y)
	sf := u.faceAt(f)
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(sx), float64(sy))
		op.ColorScale.ScaleWithColor(c)
		text.Draw(dst, s, sf, op)
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
	cx, cy := u.at(x+13, y+h/2)
	k := float32(u.sc)
	u.ops = append(u.ops, func(dst *ebiten.Image) {
		vector.StrokeCircle(dst, cx, cy, 5*k, 1.5*k, uiBlack, true)
		var p vector.Path
		p.MoveTo(cx+2*k, cy-8*k)
		p.LineTo(cx+7*k, cy-5*k)
		p.LineTo(cx+2*k, cy-2*k)
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
		ax, ay := u.at(x+5, y+9)
		bx, by := u.at(x+9, y+14)
		cx, cy := u.at(x+17, y+4)
		k := float32(u.sc)
		u.ops = append(u.ops, func(dst *ebiten.Image) {
			vector.StrokeLine(dst, ax, ay, bx, by, 2.5*k, uiBlack, true)
			vector.StrokeLine(dst, bx, by, cx, cy, 2.5*k, uiBlack, true)
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
