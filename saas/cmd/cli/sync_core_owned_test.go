package main

import (
	"strings"
	"testing"
)

// TestSyncMergeRowsCoreOwnedRefused: `lore sync merge-rows` of a
// core-owned table refuses (E_INVALID_INPUT, non-zero) with lsync's text
// naming the way that works, instead of a generic error or a success that
// changes only lore.db (a63c6f8); both rows are untouched.
func TestSyncMergeRowsCoreOwnedRefused(t *testing.T) {
	f := newCoreFixture(t)
	keep := f.addID("memory", "add", "--body", "one note to keep")
	drop := f.addID("memory", "add", "--body", "a duplicate to drop")
	r := f.run(e2eBin, "sync", "merge-rows", "memories", "--keep", keep, "--drop", drop)
	if r.code == 0 || !strings.Contains(r.stderr, "E_INVALID_INPUT") || !strings.Contains(r.stderr, "then delete "+drop+" with its lore command") {
		t.Fatalf("want the core-owned refusal naming the way out: %+v", r)
	}
	for _, id := range []string{keep, drop} {
		f.lore("memory", "show", id)
	}
}
