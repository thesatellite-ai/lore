package fakeexec

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// stressRounds is how many fake programs the test writes and runs while
// other goroutines fork; with a plain os.WriteFile some of them fail with
// ETXTBSY on a multi-core machine.
const stressRounds = 100

// TestWriteThenExecUnderConcurrentForks pins Write's promise (moved here
// from wire/internal/fakebin, which this package replaces): a fake
// written by Write runs at once, even while other goroutines of the same
// process start programs, which is when a plain os.WriteFile gives "text
// file busy".
func TestWriteThenExecUnderConcurrentForks(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("ETXTBSY and sh scripts are unix-only")
	}
	sh, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true program to fork")
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = exec.Command(sh).Run() // only the forks matter
			}
		}()
	}
	defer func() { stop.Store(true); wg.Wait() }()
	dir := t.TempDir()
	for i := range stressRounds {
		p := filepath.Join(dir, "fake"+strconv.Itoa(i))
		Write(t, p, "#!/bin/sh\nexit 0\n", 0o700)
		if out, err := exec.Command(p).CombinedOutput(); err != nil {
			t.Fatalf("round %d: running a freshly written fake: %v %s", i, err, out)
		}
	}
}
