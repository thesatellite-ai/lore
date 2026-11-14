//go:build race

package sqlite

// raceDetector is true in a -race build. Wall-time bounds (TestSearchBigBodyFast)
// are skipped there: the detector slows the FTS5 and snippet code by an order of
// magnitude or more, so a time bound measures the instrumentation, not the code.
const raceDetector = true
