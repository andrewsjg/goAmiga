//go:build !noebitengine

package amiga

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

const (
	maxMouseDelta = 80
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

	// Disks[n] are the ADF files Cmd/Win+F11 cycles DFn: through (the
	// command line's, plus any chosen with F11), and DiskIndex[n] is the one
	// in the drive (-1 = none). See display_disk.go.
	Disks     [4][]string
	DiskIndex [4]int
	chooser   chan diskChoice // result of an open file dialog, or nil

	lastTitle string

	// JoyKeys makes the arrow keys and Right Ctrl/Alt/Shift a joystick in
	// port 2 (Ctrl+F12 toggles). Gamepads always work.
	JoyKeys  bool
	gamepads []ebiten.GamepadID

	// Mute turns off host audio (Paula still runs).
	Mute bool

	// Scale multiplies the window size (the emulated screen stays the
	// same); Run lowers it if the window would not fit the monitor.
	Scale       int
	audioPlayer *audio.Player
}

// NewDisplay creates a new Ebitengine display for the given Amiga.
func NewDisplay(a *Amiga) *Display {
	return &Display{
		amiga:     a,
		DiskIndex: [4]int{-1, -1, -1, -1},
		screen:    ebiten.NewImage(LowResWidth, PALVisibleLines),
		prevKeys:  make(map[ebiten.Key]bool),
	}
}

// Update runs one PAL frame worth of CPU cycles.
func (d *Display) Update() error {
	d.pollMouseGrab()
	d.pollDiskKeys()
	d.updateTitle()
	d.pollKeyboard()
	d.pollMouse()
	d.pollJoystick()
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

		if d.JoyKeys && joyKeys[eKey] {
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
	} else {
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
	}
	d.updateTitle()
}

// pollDiskKeys handles the disk keys: F11 swaps DF0: to the next disk in
// its list, Alt+F11 DF1:, Ctrl+F11 DF2:, Ctrl+Alt+F11 DF3:; holding Shift
// ejects instead.
// updateTitle shows each connected drive's disk and the mouse state in the
// title bar.
func (d *Display) updateTitle() {
	mouse := "F12 or click to trap mouse"
	if d.mouseGrabbed {
		mouse = "mouse trapped (Esc / F12 release)"
	}
	var drives []string
	for n := 0; n < 4; n++ {
		dr := d.amiga.Disk.DF(n)
		if dr == nil {
			continue
		}
		name := "empty"
		i := d.DiskIndex[n]
		switch {
		case dr.Swapping() && i >= 0:
			name = "inserting " + filepath.Base(d.Disks[n][i])
		case dr.Inserted() && i >= 0:
			name = filepath.Base(d.Disks[n][i])
		case dr.Inserted():
			name = "disk"
		}
		drives = append(drives, fmt.Sprintf("DF%d: %s", n, name))
	}
	title := fmt.Sprintf("goAmiga — %s — %s — %s", strings.Join(drives, " | "), mouse, d.joystickTitle())
	if title != d.lastTitle {
		ebiten.SetWindowTitle(title)
		d.lastTitle = title
	}
}

// pollMouseGrab handles trap/release only. No per-frame CursorMode polling.
func (d *Display) pollMouseGrab() {
	if d.pollJoyToggle() {
		return
	}
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
			lw, lh := d.amiga.Video.DisplaySize()
			adx := dx * aw / lw
			ady := dy * ah / lh
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

// Layout returns the logical screen size: the framebuffer width and two
// rows per non-interlaced line (Video.DisplaySize).
func (d *Display) Layout(outsideWidth, outsideHeight int) (int, int) {
	return d.amiga.Video.DisplaySize()
}

// Run starts the Ebitengine game loop at 50fps (PAL).
func (d *Display) Run() error {
	d.amiga.Video.DisplayActive = true
	if !d.Mute {
		if err := d.startAudio(); err != nil {
			fmt.Printf("audio disabled: %v\n", err)
		}
	}
	w, h := d.amiga.Video.DisplaySize()
	s := d.windowScale(w, h)
	ebiten.SetWindowSize(w*s, h*s)
	// Resizing scales the picture; the emulated screen size is fixed by
	// Layout.
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetTPS(50)
	ebiten.SetScreenClearedEveryFrame(false)
	d.applyGrab(false)
	return ebiten.RunGame(d)
}

// windowScale is the largest scale up to d.Scale at which a w×h screen
// fits the monitor, leaving room for the title bar and menus.
func (d *Display) windowScale(w, h int) int {
	s := max(d.Scale, 1)
	m := ebiten.Monitor()
	if m == nil {
		return s
	}
	mw, mh := m.Size()
	if mw == 0 || mh == 0 {
		return s
	}
	for s > 1 && (w*s > mw*95/100 || h*s > mh*90/100) {
		s--
	}
	if s < d.Scale {
		fmt.Printf("window scale %d does not fit the %dx%d monitor; using %d\n", d.Scale, mw, mh, s)
	}
	return s
}

// Host audio. Paula writes 48 kHz stereo into an AudioBuffer as the
// emulator runs; Ebitengine's player pulls from it on its own goroutine.
// The buffer holds at most audioLatency: the emulated frame (313 lines,
// 49.92 Hz) is slightly longer than the 50 Hz tick, so Paula produces a
// little more than the host plays and the oldest samples are dropped
// rather than letting latency grow. Underruns play silence.
const (
	audioLatency       = 100 * time.Millisecond
	audioPlayerBuffer  = 50 * time.Millisecond
	audioLatencyFrames = int(audioLatency * AudioSampleRate / time.Second)
)

func (d *Display) startAudio() error {
	ctx := audio.NewContext(AudioSampleRate)
	d.amiga.Paula.Output = NewAudioBuffer(audioLatencyFrames)
	p, err := ctx.NewPlayerF32(d.amiga.Paula.Output)
	if err != nil {
		d.amiga.Paula.Output = nil
		return err
	}
	p.SetBufferSize(audioPlayerBuffer)
	p.Play()
	d.audioPlayer = p
	return nil
}
