//go:build !noebitengine

package amiga

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// Joystick input for game port 2 comes from any connected gamepad and,
// when JoyKeys is on (Ctrl+F12 toggles it), from the keyboard: the arrow
// keys move, Right Ctrl or Right Alt/Option fires, Right Shift is the
// second button. While JoyKeys is on those keys don't reach the Amiga
// keyboard.

const stickDeadZone = 0.5

// joyKeys are the host keys the keyboard joystick takes over.
var joyKeys = map[ebiten.Key]bool{
	ebiten.KeyArrowUp: true, ebiten.KeyArrowDown: true,
	ebiten.KeyArrowLeft: true, ebiten.KeyArrowRight: true,
	ebiten.KeyControlRight: true, ebiten.KeyAltRight: true,
	ebiten.KeyShiftRight: true,
}

// pollJoyToggle switches the keyboard joystick on Ctrl+F12 and reports
// whether it consumed the F12 press.
func (d *Display) pollJoyToggle() bool {
	if !inpututil.IsKeyJustPressed(ebiten.KeyF12) || !ebiten.IsKeyPressed(ebiten.KeyControl) {
		return false
	}
	d.JoyKeys = !d.JoyKeys
	if d.JoyKeys {
		// Let go of any taken-over key the Amiga thinks is held.
		for k := range joyKeys {
			if d.prevKeys[k] {
				if code, ok := AmigaKeyMap[k]; ok && d.amiga.Keyboard != nil {
					d.amiga.Keyboard.EnqueueKey(code, false)
				}
				d.prevKeys[k] = false
			}
		}
	}
	return true
}

func (d *Display) pollJoystick() {
	j := d.amiga.Joystick
	if j == nil {
		return
	}
	*j = Joystick{Analog: d.JoyAnalog}
	if d.JoyKeys {
		j.Up = ebiten.IsKeyPressed(ebiten.KeyArrowUp)
		j.Down = ebiten.IsKeyPressed(ebiten.KeyArrowDown)
		j.Left = ebiten.IsKeyPressed(ebiten.KeyArrowLeft)
		j.Right = ebiten.IsKeyPressed(ebiten.KeyArrowRight)
		j.Fire = ebiten.IsKeyPressed(ebiten.KeyControlRight) || ebiten.IsKeyPressed(ebiten.KeyAltRight)
		j.Fire2 = ebiten.IsKeyPressed(ebiten.KeyShiftRight)
	}
	d.gamepads = ebiten.AppendGamepadIDs(d.gamepads[:0])
	for _, id := range d.gamepads {
		var up, down, left, right, fire, fire2 bool
		var x, y float64
		if ebiten.IsStandardGamepadLayoutAvailable(id) {
			up = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonLeftTop)
			down = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonLeftBottom)
			left = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonLeftLeft)
			right = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonLeftRight)
			x = ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickHorizontal)
			y = ebiten.StandardGamepadAxisValue(id, ebiten.StandardGamepadAxisLeftStickVertical)
			fire = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonRightBottom)
			fire2 = ebiten.IsStandardGamepadButtonPressed(id, ebiten.StandardGamepadButtonRightRight)
		} else {
			if ebiten.GamepadAxisCount(id) >= 2 {
				x, y = ebiten.GamepadAxisValue(id, 0), ebiten.GamepadAxisValue(id, 1)
			}
			fire = ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton0)
			fire2 = ebiten.IsGamepadButtonPressed(id, ebiten.GamepadButton1)
		}
		if j.Analog && id == d.gamepads[0] {
			j.AnalogX, j.AnalogY = x, y
		}
		j.Up = j.Up || up || y < -stickDeadZone
		j.Down = j.Down || down || y > stickDeadZone
		j.Left = j.Left || left || x < -stickDeadZone
		j.Right = j.Right || right || x > stickDeadZone
		j.Fire = j.Fire || fire
		j.Fire2 = j.Fire2 || fire2
	}
}

// joystickTitle describes the joystick sources for the title bar.
func (d *Display) joystickTitle() string {
	s := "Ctrl+F12 joystick keys"
	if d.JoyKeys {
		s = "joystick: arrows+RCtrl (Ctrl+F12 off)"
	}
	if len(d.gamepads) > 0 {
		s += ", gamepad: " + ebiten.GamepadName(d.gamepads[0])
	}
	return s
}
