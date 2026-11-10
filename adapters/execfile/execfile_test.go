package execfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// stressRounds is how many scripts the test writes and runs while other
// goroutines fork; with a plain os.WriteFile some of them fail with ETXTBSY
// on a multi-core machine.
const stressRounds = 200

// scriptMode is the mode of the written scripts: run by this user only.
const scriptMode = 0o700

// TestWriteThenExecUnderConcurrentForks pins the package's promise: a file
// written by WriteFile runs at once, even while other goroutines of the same
// process start programs, which is when a plain os.WriteFile gives "text
// file busy".
func TestWriteThenExecUnderConcurrentForks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ETXTBSY and sh scripts are unix-only")
	}
	forked, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("no true program to fork: %v", err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = exec.Command(forked).Run() // only the forks matter
			}
		}()
	}
	defer func() { stop.Store(true); wg.Wait() }()
	dir := t.TempDir()
	for i := range stressRounds {
		p := filepath.Join(dir, "script"+strconv.Itoa(i))
		if err := WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), scriptMode); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(p).CombinedOutput(); err != nil {
			t.Fatalf("round %d: running a freshly written script: %v %s", i, err, out)
		}
	}
}

// TestWriteFileSetsModeOnAnExistingFile: a rewrite gives the file the new
// mode, as a hook made executable again must be.
func TestWriteFileSetsModeOnAnExistingFile(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "f")
	if err := WriteFile(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("b"), scriptMode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != scriptMode {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), scriptMode)
	}
}
