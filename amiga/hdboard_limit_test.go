package amiga

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The board takes HardfileLimit (28) hardfiles; one more is refused
// rather than overwriting the board's other structures.
func TestHardfileLimit(t *testing.T) {
	a := createTestAmiga(t)
	dir := t.TempDir()
	for i := 0; i <= HardfileLimit; i++ {
		p := filepath.Join(dir, "d"+string(rune('a'+i%26))+string(rune('a'+i/26))+".hdf")
		f, _ := os.Create(p)
		f.Truncate(1 << 20) // a blank 1 MB hardfile (sparse)
		f.Close()
		err := a.AttachHardfile(p, true)
		switch {
		case i < HardfileLimit && err != nil:
			t.Fatalf("hardfile %d: %v", i+1, err)
		case i == HardfileLimit && (err == nil || !strings.Contains(err.Error(), "at most 28")):
			t.Fatalf("hardfile %d: %v, want it refused", i+1, err)
		}
	}
}
