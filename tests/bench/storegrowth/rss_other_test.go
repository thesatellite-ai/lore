//go:build bench && !unix

package storegrowth

// timedRSS is timed without a peak RSS where the OS does not report one.
func (w *world) timedRSS(dir, name string, args ...string) (result, int64) {
	return w.timed(dir, name, args...), 0
}
