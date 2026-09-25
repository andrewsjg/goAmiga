//go:build !noebitengine

package amiga

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

const (
	displayLayoutW = 640
	displayLayoutH = 512
	maxMouseDelta  = 80
)

// Display implements ebiten.Game to show the Amiga's video output.
type Display struct {
	amiga  *Amiga
	screen *ebiten.Image

	prevKeys map[ebiten.Key]bool

	// mouseGrabbed: we feed relative motion to the Amiga. Explicit only —
	// never derived from ebiten.CursorMode() every frame (that + re-capture
	// after a click freezes the UI on macOS).
	mouseGrabbed bool

	mousePrevX     int
	mousePrevY     int
	mousePrevValid bool
	prevAnyButton  bool

	pendingGrab          bool
	ignoreButtonsUntilUp bool
	suppressEscape       bool

	// Rate-limit capture re-asserts. macOS often drops CursorModeCaptured on
	// click; re-asserting every frame freezes the process, but mid-drag with
	// button held never hit the old "on release" re-capture → relative motion
	// dies after ~seconds and the Amiga looks locked (CPU still idle-running).
	framesSinceRecapture int
	zeroMotionFrames     int
}

// NewDisplay creates a new Ebitengine display for the given Amiga.
func NewDisplay(a *Amiga) *Display {
	return &Display{
		amiga:    a,
		screen:   ebiten.NewImage(LowResWidth, PALVisibleLines),
		prevKeys: make(map[ebiten.Key]bool),
	}
}

// Update runs one PAL frame worth of CPU cycles.
func (d *Display) Update() error {
	d.pollMouseGrab()
	d.pollKeyboard()
	d.pollMouse()
	// Never tear down the window on a single-frame CPU fault. Step() already
	// converts access violations to bus errors; if anything still leaks out,
	// keep presenting frames so the user can see a Guru/requester.
	if err := d.amiga.RunFrame(); err != nil {
		return nil
	}
	return nil
}

func (d *Display) pollKeyboard() {
	kb := d.amiga.Keyboard
	if kb == nil {
		return
	}

	for eKey, amigaCode := range AmigaKeyMap {
		if eKey == ebiten.KeyEscape && (d.mouseGrabbed || d.pendingGrab || d.suppressEscape) {
			if d.suppressEscape && !ebiten.IsKeyPressed(ebiten.KeyEscape) {
				d.suppressEscape = false
			}
			d.prevKeys[eKey] = ebiten.IsKeyPressed(eKey)
			continue
		}

		pressed := ebiten.IsKeyPressed(eKey)
		wasPrev := d.prevKeys[eKey]
		if pressed && !wasPrev {
			kb.EnqueueKey(amigaCode, true)
		} else if !pressed && wasPrev {
			kb.EnqueueKey(amigaCode, false)
		}
		d.prevKeys[eKey] = pressed
	}
}

func (d *Display) anyHostMouseButton() bool {
	return ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) ||
		ebiten.IsMouseButtonPressed(ebiten.MouseButtonMiddle)
}

func (d *Display) applyGrab(grab bool) {
	d.mouseGrabbed = grab
	d.mousePrevValid = false
	d.pendingGrab = false
	d.prevAnyButton = false
	if grab {
		// Captured = relative motion without window-edge limits (needed for
		// multi-second window drags). Only call SetCursorMode on grab/ungrab
		// and once after a button release — never every frame (that freezes
		// macOS).
		ebiten.SetCursorMode(ebiten.CursorModeCaptured)
		ebiten.SetWindowTitle("goAmiga — mouse trapped (Esc / F12 release)")
	} else {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
		ebiten.SetWindowTitle("goAmiga — F12 or click to trap mouse")
	}
}

// pollMouseGrab handles trap/release only. No per-frame CursorMode polling.
func (d *Display) pollMouseGrab() {
	if inpututil.IsKeyJustPressed(ebiten.KeyF12) {
		if d.mouseGrabbed {
			d.applyGrab(false)
		} else {
			d.pendingGrab = true
			d.ignoreButtonsUntilUp = true
		}
		return
	}

	if d.mouseGrabbed && inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		d.applyGrab(false)
		d.suppressEscape = true
		return
	}

	// Click-to-trap when free — apply only after buttons are released.
	if !d.mouseGrabbed && !d.pendingGrab &&
		(inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) ||
			inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonMiddle)) {
		d.pendingGrab = true
		d.ignoreButtonsUntilUp = true
	}

	if d.pendingGrab && !d.mouseGrabbed && !d.anyHostMouseButton() {
		d.applyGrab(true)
		d.ignoreButtonsUntilUp = false
	}

	if d.ignoreButtonsUntilUp && !d.anyHostMouseButton() {
		d.ignoreButtonsUntilUp = false
	}

	// Re-assert capture on edges and (rate-limited) while held. macOS often
	// drops CursorModeCaptured on left-click. Old code only re-captured on
	// button *release*, so a multi-second window drag (button held) lost
	// relative motion after the OS dropped capture → frozen XOR outline /
	// "emulator locked" while the host process kept running. SetCursorMode
	// every frame freezes macOS; edges + ~2 Hz while held is safe.
	any := d.anyHostMouseButton()
	if d.mouseGrabbed {
		pressEdge := any && !d.prevAnyButton
		releaseEdge := !any && d.prevAnyButton
		d.framesSinceRecapture++
		need := pressEdge || releaseEdge
		// While dragging: if OS dropped capture, restore a few times/sec.
		if any && d.framesSinceRecapture >= 20 {
			if ebiten.CursorMode() != ebiten.CursorModeCaptured {
				need = true
			}
			d.framesSinceRecapture = 0
		}
		if need {
			ebiten.SetCursorMode(ebiten.CursorModeCaptured)
			d.mousePrevValid = false
			d.framesSinceRecapture = 0
		}
	}
	d.prevAnyButton = any
}

func clampDelta(v int) int {
	if v > maxMouseDelta {
		return maxMouseDelta
	}
	if v < -maxMouseDelta {
		return -maxMouseDelta
	}
	return v
}

func (d *Display) pollMouse() {
	m := d.amiga.Mouse
	if m == nil {
		return
	}

	// Buttons: always deliver when not in a grab handshake. Relative motion
	// still requires capture, but system requesters ("Software error - task
	// held", CANCEL) must stay clickable if macOS drops CursorModeCaptured
	// or the user never re-traps. Swallowing buttons while uncaptured made
	// those dialogs look like a full emulator lockup.
	if d.ignoreButtonsUntilUp || d.pendingGrab {
		m.LeftButton = false
		m.RightButton = false
	} else {
		m.LeftButton = ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
		m.RightButton = ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight)
	}

	if !d.mouseGrabbed {
		d.mousePrevValid = false
		return
	}

	x, y := ebiten.CursorPosition()
	if d.mousePrevValid {
		dx := clampDelta(x - d.mousePrevX)
		dy := clampDelta(y - d.mousePrevY)
		if dx != 0 || dy != 0 {
			d.zeroMotionFrames = 0
			aw, ah := LowResWidth, PALVisibleLines
			if d.amiga.Video != nil {
				if d.amiga.Video.Width > 0 {
					aw = d.amiga.Video.Width
				}
				if d.amiga.Video.Height > 0 {
					ah = d.amiga.Video.Height
				}
			}
			adx := dx * aw / displayLayoutW
			ady := dy * ah / displayLayoutH
			if adx == 0 && dx != 0 {
				if dx > 0 {
					adx = 1
				} else {
					adx = -1
				}
			}
			if ady == 0 && dy != 0 {
				if dy > 0 {
					ady = 1
				} else {
					ady = -1
				}
			}
			if adx != 0 || ady != 0 {
				m.Move(adx, ady)
			}
		} else if d.anyHostMouseButton() {
			// No host delta while dragging: often means capture was lost and
			// the cursor is pinned at the window edge. Nudge re-capture.
			d.zeroMotionFrames++
			if d.zeroMotionFrames == 12 {
				ebiten.SetCursorMode(ebiten.CursorModeCaptured)
				d.mousePrevValid = false
				d.zeroMotionFrames = 0
				d.framesSinceRecapture = 0
			}
		}
	}
	d.mousePrevX = x
	d.mousePrevY = y
	d.mousePrevValid = true
}

// Draw copies the Video framebuffer to the Ebitengine screen.
func (d *Display) Draw(screen *ebiten.Image) {
	v := d.amiga.Video
	if v == nil || v.Width <= 0 || v.Height <= 0 {
		return
	}

	need := v.Width * v.Height * 4
	if len(v.Pixels) < need {
		return
	}

	w, h := d.screen.Bounds().Dx(), d.screen.Bounds().Dy()
	if w != v.Width || h != v.Height {
		d.screen = ebiten.NewImage(v.Width, v.Height)
	}

	d.screen.WritePixels(v.Pixels[:need])

	op := &ebiten.DrawImageOptions{}
	sw := float64(screen.Bounds().Dx()) / float64(v.Width)
	sh := float64(screen.Bounds().Dy()) / float64(v.Height)
	op.GeoM.Scale(sw, sh)
	screen.DrawImage(d.screen, op)
}

// Layout returns the logical screen size (2x vertical scaling).
func (d *Display) Layout(outsideWidth, outsideHeight int) (int, int) {
	return displayLayoutW, displayLayoutH
}

// Run starts the Ebitengine game loop at 50fps (PAL).
func (d *Display) Run() error {
	d.amiga.Video.DisplayActive = true
	ebiten.SetWindowSize(displayLayoutW, displayLayoutH)
	ebiten.SetTPS(50)
	ebiten.SetScreenClearedEveryFrame(false)
	d.applyGrab(false)
	return ebiten.RunGame(d)
}

// ScreenImage returns an RGBA image snapshot of the current video output.
func (v *Video) ScreenImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	copy(img.Pix, v.Pixels)
	return img
}
