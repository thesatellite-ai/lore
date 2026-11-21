//go:build !linux && !darwin

package app

// The read server runs on linux and darwin only (readserver_unix.go); on
// every other platform each command runs one-shot.

// callReadServer never serves here.
func callReadServer([]string) (int, bool) { return 0, false }

// serveReads exits at once here: no platform support.
func serveReads() int { return 0 }
