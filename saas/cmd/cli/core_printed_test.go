package main

import (
	"strings"
	"testing"
)

// TestCoreErrorPrintedOnce: a failing generated command outside the compat
// nouns (store init into a folder that cannot be created: E_INTERNAL, exit
// 1, the usage-error code) prints its error once, never a second bare
// "internal error" line from main.
func TestCoreErrorPrintedOnce(t *testing.T) {
	f := newCoreFixture(t)
	r := f.run(e2eBin, "store", "init", "extra", "file:///proc/lore-cannot-create/x", "--yes")
	if r.code == 0 {
		t.Fatalf("want a failure: %+v", r)
	}
	if n := strings.Count(r.stderr, "ERROR:"); n != 1 {
		t.Fatalf("the error must be printed once, got %d:\n%s", n, r.stderr)
	}
}
