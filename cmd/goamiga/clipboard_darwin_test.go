//go:build !noebitengine

package main

import (
	"os"
	"testing"
)

// The Mac's pasteboard: text set comes back, and the change count moves.
// It changes the real clipboard, so it only runs with GOAMIGA_PASTEBOARD.
func TestMacClipboard(t *testing.T) {
	if os.Getenv("GOAMIGA_PASTEBOARD") == "" {
		t.Skip("set GOAMIGA_PASTEBOARD to use the real clipboard")
	}
	c := hostClipboard()
	before, _ := c.Text()
	defer c.SetText(before)
	n := c.ChangeCount()
	c.SetText("goAmiga café ✓")
	if got, ok := c.Text(); !ok || got != "goAmiga café ✓" {
		t.Errorf("read back %q, %v", got, ok)
	}
	if c.ChangeCount() == n {
		t.Error("the change count didn't move")
	}
}
