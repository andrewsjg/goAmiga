//go:build noebitengine || !darwin

package main

import "github.com/andrewsjg/goamiga/amiga"

// hostClipboard: clipboard sharing is for macOS for now.
func hostClipboard() amiga.HostClipboard { return nil }
