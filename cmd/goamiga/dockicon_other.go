//go:build !noebitengine && !darwin

package main

// setDockIcon: only macOS has a Dock; elsewhere the window icon serves.
func setDockIcon([]byte) {}
