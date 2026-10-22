package whdload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The icons of WHDLoad installs carry the slave and its options.
func TestToolTypes(t *testing.T) {
	icons, _ := filepath.Glob("../../WHDLoad/installs/x/*/*/*icon*")
	if len(icons) == 0 {
		t.Skip("no unpacked installs")
	}
	found := 0
	for _, f := range icons {
		b, _ := os.ReadFile(f)
		tts := ToolTypes(b)
		for _, tt := range tts {
			if len(tt) > 6 && strings.EqualFold(tt[:6], "SLAVE=") {
				found++
				t.Logf("%s: %q", filepath.Base(f), tts[:min(len(tts), 5)])
			}
		}
	}
	if found == 0 {
		t.Error("no SLAVE= tooltype in any icon")
	}
}
