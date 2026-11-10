// Package fakebin writes the fake programs (ssh, gh, git, gopass, lore …)
// that wire's tests put on a PATH, in a way that a later exec of that file
// can never fail with ETXTBSY ("text file busy").
//
// Why: Linux refuses to exec a file that some process still holds open for
// writing. os.WriteFile opens the file O_CLOEXEC, but when another goroutine
// of the same test process forks (any exec.Cmd of a parallel test) between
// that open and the close, the forked child holds a copy of the write
// descriptor until it reaches its own exec. An exec of the fake program in
// that window fails. The flake needs only parallel tests that run programs,
// which wire has by the hundred.
//
// Fix at the cause, no retry: Write holds syscall.ForkLock for reading
// from the open to the close. The runtime takes that lock for writing
// around every fork, so no fork of this process can happen while the
// descriptor is open and no child can inherit it. Other processes never
// share this process's descriptors, so they cannot cause the race.
package fakebin

import (
	"os"
	"syscall"
	"testing"
)

// Write creates (or replaces) path with body and mode, the way
// os.WriteFile does, but with no fork of this process able to copy the
// write descriptor (see the package doc). The test fails on any error.
func Write(t testing.TB, path, body string, mode os.FileMode) {
	t.Helper()
	if err := write(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

func write(path, body string, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return err
	}
	// os.WriteFile applies mode only to a file it creates; a fake that is
	// rewritten keeps its first mode otherwise.
	return os.Chmod(path, mode)
}
