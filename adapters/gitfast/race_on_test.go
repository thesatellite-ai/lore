//go:build race

package gitfast

// raceDetector is true in a -race build. Allocation bounds
// (TestInflateReusesReaders) are skipped there: the detector makes
// sync.Pool drop a share of what is put back, so a pooled reader is made
// again at random and the bound measures the instrumentation, not the code.
const raceDetector = true
