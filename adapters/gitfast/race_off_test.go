//go:build !race

package gitfast

// raceDetector is false outside a -race build (race_on_test.go).
const raceDetector = false
