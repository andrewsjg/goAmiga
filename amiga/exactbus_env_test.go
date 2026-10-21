package amiga

import "os"

// GOAMIGA_EXACTBUS=0 runs the tests with the chip bus forecast instead of
// exact timing.
func init() {
	if os.Getenv("GOAMIGA_EXACTBUS") == "0" {
		defaultExactBus = false
	}
}
