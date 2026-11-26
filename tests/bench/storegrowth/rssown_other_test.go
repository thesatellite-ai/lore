//go:build bench && !linux

package storegrowth

// timedOwnRSS is timedRSS where /proc cannot tell one process's own peak
// (it then counts the git processes lore waited for too).
func (w *world) timedOwnRSS(dir, name string, args ...string) (result, int64) {
	return w.timedRSS(dir, name, args...)
}
