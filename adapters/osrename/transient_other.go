//go:build !windows

package osrename

// transient is false everywhere but Windows: a rename over an open file
// succeeds on unix-like systems, so every error there is a real one.
func transient(error) bool { return false }
