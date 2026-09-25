package amiga

import (
	"os"
	"testing"
)

// requireDiag skips print-heavy diagnostic tests unless GOAMIGA_DIAG is set.
// These tests trace boot/rendering internals to stdout rather than assert
// behaviour, so they are opt-in to keep `go test` output readable:
//
//	GOAMIGA_DIAG=1 go test -run TestHotPCs ./amiga/
func requireDiag(t *testing.T) {
	t.Helper()
	if os.Getenv("GOAMIGA_DIAG") == "" {
		t.Skip("diagnostic trace; set GOAMIGA_DIAG=1 to run")
	}
}
