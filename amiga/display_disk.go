//go:build !noebitengine

package amiga

import (
	"fmt"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/ncruces/zenity"
)

// Disk keys. F11 picks the drive: DF0:, Alt+F11 DF1:, Ctrl+F11 DF2:,
// Ctrl+Alt+F11 DF3: (DF0: if the drive the modifiers name isn't connected). On its own it opens a file dialog to choose an ADF to
// insert; with Shift it ejects; with Cmd (macOS) or the Windows key it
// swaps to the next disk in the drive's list.
//
// The dialog runs on its own goroutine (zenity: osascript on macOS, the
// Win32 dialog on Windows, zenity or kdialog on Linux) so the emulation
// keeps running, as a real Amiga does while you look for a disk. Update
// picks up the answer.

type diskChoice struct {
	unit int
	path string
	err  error
}

func (d *Display) pollDiskKeys() {
	d.pollChooser()
	if !inpututil.IsKeyJustPressed(ebiten.KeyF11) {
		return
	}
	unit := 0
	if ebiten.IsKeyPressed(ebiten.KeyAlt) {
		unit++
	}
	if ebiten.IsKeyPressed(ebiten.KeyControl) {
		unit += 2
	}
	dr := d.amiga.Disk.DF(unit)
	if dr == nil {
		// Modifier state is unreliable on some hosts (macOS reports Option
		// held after some shortcuts), and with only DF0: fitted there is
		// nothing else it could mean.
		fmt.Printf("DF%d: not connected (use --drives); using DF0: (modifiers seen:%s)\n", unit, heldModifiers())
		unit, dr = 0, d.amiga.Disk.DF(0)
	}
	switch {
	case ebiten.IsKeyPressed(ebiten.KeyShift):
		dr.Eject()
		d.DiskIndex[unit] = -1
		fmt.Printf("DF%d: ejected\n", unit)
	case ebiten.IsKeyPressed(ebiten.KeyMeta):
		disks := d.Disks[unit]
		if len(disks) > 0 {
			d.insert(unit, (d.DiskIndex[unit]+1)%len(disks))
		}
	default:
		d.openChooser(unit)
	}
}

// openChooser shows the file dialog for a drive, starting in the folder
// of its current (or last listed) disk.
func (d *Display) openChooser(unit int) {
	if d.chooser != nil {
		return // one dialog at a time
	}
	// The dialog needs the host pointer.
	if d.mouseGrabbed {
		d.applyGrab(false)
	}
	start := ""
	if disks := d.Disks[unit]; len(disks) > 0 {
		i := max(d.DiskIndex[unit], 0)
		start = filepath.Dir(disks[min(i, len(disks)-1)]) + string(filepath.Separator)
	}
	ch := make(chan diskChoice, 1)
	d.chooser = ch
	go func() {
		path, err := zenity.SelectFile(
			zenity.Title(fmt.Sprintf("Insert disk in DF%d:", unit)),
			zenity.Filename(start),
			zenity.FileFilters{
				{Name: "Amiga disk images", Patterns: []string{"*.adf", "*.ipf", "*.zip", "*.adz", "*.gz"}, CaseFold: true},
				{Name: "All files", Patterns: []string{"*"}},
			})
		ch <- diskChoice{unit, path, err}
	}()
}

// pollChooser inserts the disk chosen in the dialog, once it closes.
func (d *Display) pollChooser() {
	if d.chooser == nil {
		return
	}
	var c diskChoice
	select {
	case c = <-d.chooser:
	default:
		return
	}
	d.chooser = nil
	switch {
	case c.err == zenity.ErrCanceled:
		return
	case c.err != nil:
		fmt.Printf("DF%d: file dialog: %v\n", c.unit, c.err)
		return
	}
	i := -1
	for j, p := range d.Disks[c.unit] {
		if p == c.path {
			i = j
		}
	}
	if i < 0 {
		d.Disks[c.unit] = append(d.Disks[c.unit], c.path)
		i = len(d.Disks[c.unit]) - 1
	}
	d.insert(c.unit, i)
}

// insert puts disk i of the drive's list in the drive. SwapDisk waits
// until the Amiga has seen the drive empty, like a real disk change.
func (d *Display) insert(unit, i int) {
	path := d.Disks[unit][i]
	if err := d.amiga.SwapDiskUnit(unit, path); err != nil {
		fmt.Printf("DF%d: %v\n", unit, err)
		return
	}
	d.DiskIndex[unit] = i
	fmt.Printf("DF%d: %s\n", unit, path)
}

// heldModifiers lists the modifier keys Ebitengine reports as held.
func heldModifiers() string {
	var s string
	for _, m := range []struct {
		k    ebiten.Key
		name string
	}{
		{ebiten.KeyAltLeft, "AltLeft"}, {ebiten.KeyAltRight, "AltRight"},
		{ebiten.KeyControlLeft, "CtrlLeft"}, {ebiten.KeyControlRight, "CtrlRight"},
		{ebiten.KeyShiftLeft, "ShiftLeft"}, {ebiten.KeyShiftRight, "ShiftRight"},
		{ebiten.KeyMetaLeft, "MetaLeft"}, {ebiten.KeyMetaRight, "MetaRight"},
	} {
		if ebiten.IsKeyPressed(m.k) {
			s += " " + m.name
		}
	}
	if s == "" {
		s = " none"
	}
	return s
}
