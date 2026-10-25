//go:build noebitengine

package main

import (
	"errors"

	"github.com/andrewsjg/goamiga/internal/config"
)

var errNoDisplay = errors.New("built with -tags noebitengine: no display; use --headless")

// runMachine is unavailable in a build without Ebitengine: use --headless.
func runMachine(*machine, *config.Settings, string) error { return errNoDisplay }

// runLauncher is unavailable in a build without Ebitengine.
func runLauncher() error { return errNoDisplay }
