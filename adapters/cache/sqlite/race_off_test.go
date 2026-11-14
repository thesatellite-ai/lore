//go:build !race

package sqlite

// raceDetector is false in a plain build, where wall-time bounds hold
// (race_on_test.go says why they are skipped under -race).
const raceDetector = false
