package main

import (
	"os"
	"path/filepath"
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

// TestSyncPurgeCoreOwnedArchived: an archived memory (core-owned) is
// counted by the dry run and purged by --confirm through the core: show
// then answers E_NOT_FOUND and the file is a tombstone (fixbatch J; lsync
// 8cd677d skips core rows, so before this purge said ✓ 1 and kept both).
func TestSyncPurgeCoreOwnedArchived(t *testing.T) {
	f := newCoreFixture(t)
	keep := f.addID("memory", "add", "--body", "a live note stays")
	id := f.addID("memory", "add", "--body", "an old note to purge")
	f.lore("memory", "archive", id)
	if out := f.lore("sync", "purge", "--archived-before", "0d", "--dry-run").stdout; !strings.Contains(out, "would purge 1 archived row(s)") {
		t.Fatalf("dry run: %s", out)
	}
	if out := f.lore("sync", "purge", "--archived-before", "0d", "--confirm").stdout; !strings.Contains(out, "purged 1 row(s)") {
		t.Fatalf("purge: %s", out)
	}
	if r := f.run(e2eBin, "memory", "show", id); r.code != 27 || !strings.Contains(r.stderr, "E_NOT_FOUND") {
		t.Fatalf("a purged row is gone: %+v", r)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, ".lore", "data", "memories", id+".json"))
	if err != nil || !strings.Contains(string(b), `"_deleted": true`) {
		t.Fatalf("the purged row's file is a tombstone: %v %s", err, b)
	}
	pb, err := os.ReadFile(filepath.Join(f.dir, ".lore", "data", "_purged.json"))
	if err != nil || !strings.Contains(string(pb), id) {
		t.Fatalf("_purged.json lists the purged id: %v %s", err, pb)
	}
	f.lore("memory", "show", keep)
	if out := f.lore("sync", "purge", "--archived-before", "0d", "--dry-run").stdout; !strings.Contains(out, "would purge 0 archived row(s)") {
		t.Fatalf("a second run has nothing to purge: %s", out)
	}
}
