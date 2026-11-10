//go:build loretest

package locktrace

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// traceFileMode is the trace file's mode (test data only).
const traceFileMode = 0o600

var (
	traceOnce sync.Once
	traceMu   sync.Mutex
	traceFile *os.File
	// procToken tells two processes apart that got the same pid (a killed
	// lore whose pid the kernel handed out again).
	procToken = strconv.FormatInt(time.Now().UnixNano(), 10)
)

// record appends one event line to LORE_TEST_LOCK_TRACE (no file: nothing).
// A trace that cannot be written is dropped: the test reading it then sees
// a lock with no events and fails on that, never this process.
func record(ev Event, path string) {
	traceOnce.Do(func() {
		name := os.Getenv(EnvTrace)
		if name == "" {
			return
		}
		f, err := os.OpenFile(filepath.Clean(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, traceFileMode)
		if err == nil {
			traceFile = f
		}
	})
	if traceFile == nil {
		return
	}
	line := strconv.Itoa(os.Getpid()) + "\t" + procToken + "\t" + string(ev) + "\t" +
		strconv.FormatInt(time.Now().UnixNano(), 10) + "\t" + path + "\n"
	traceMu.Lock()
	defer traceMu.Unlock()
	_, _ = traceFile.WriteString(line) // best effort, see above
}
