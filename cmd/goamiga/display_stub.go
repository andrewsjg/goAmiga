//go:build noebitengine

package main

import (
	"errors"

	"github.com/cavaliercoder/goAmiga/amiga"
)

// runDisplay is unavailable in a build without Ebitengine: use --headless.
func runDisplay(*amiga.Amiga, displayOptions) error {
	return errors.New("built with -tags noebitengine: no display; use --headless")
}
