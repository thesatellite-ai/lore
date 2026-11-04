package lsync

import (
	"context"
	"os"
	"strings"
	"testing"

	"saas/pkg/aicoder/canonjson"
)

// These tests run the REAL core-owned set (owned.go), unlike the generic
// engine tests in engine_test.go which use legacyNoCoreOwned. They pin the
// one guarantee the 0.1.18 migration rests on: for a core-owned table no
// old writer (bootstrap, sync pass, adopt after `lore restore` with every
// --prefer, `sync export --all`, empty-file repair) ever writes a `_v: 1`
// document over the core's files, while the files still flow into lore.db
// so render, search and the TUI keep seeing them.

// realOwned runs a pass with the production core-owned set.
func realOwned(o *Options) { o.coreOwned = nil }

// coreMemoryDoc is a 0.1.18 core memory row file (`_v: 2`) for a row of e.
func (e *env) coreMemoryDoc(id, body string) []byte {
	e.t.Helper()
	doc := map[string]any{keyVersion: num(formatV2), keyTable: "memories", "id": id, "_hlc": "2026-11-02T10:00:00.000Z/0000/",
		"body": body, "kind": "retrieved", "source_kind": "manual", "project_id": e.project, "created_by": e.actor}
	b, err := canonjson.Encode(doc)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// coreTombstone is a 0.1.18 core tombstone (header only).
func coreTombstone(table, id string) []byte {
	return []byte(`{"_deleted":true,"_hlc":"2026-11-02T11:00:00.000Z/0000/","_table":"` + table + `","_v":2,"id":"` + id + `"}`)
}

// fileBytes returns the raw bytes of a row file, nil when absent.
func (e *env) fileBytes(table, id string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(e.file(table, id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

// notWrittenWarnings counts the "lore.db change not written" warnings.
func notWrittenWarnings(rep Report) int {
	n := 0
	for _, w := range rep.Warnings {
		if strings.Contains(w, "lore.db change not written") {
			n++
		}
	}
	return n
}

// f15b / f15 gap: bootstrap of a fresh `lore init` exports no core-owned
// table (memories, and in a checkout actors/projects too), clears their
// dirty marks, and prints no "lore.db change not written" warning.
func TestOwned_BootstrapSkipsCoreOwnedTables(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.addMemory("core owns me")
	rep := e.reconcile(realOwned)
	if rep.Pass != PassBootstrap {
		t.Fatalf("pass = %s", rep.Pass)
	}
	if n := notWrittenWarnings(rep); n != 0 {
		t.Fatalf("bootstrap must not warn about core-owned rows: %v", rep.Warnings)
	}
	for _, f := range []struct{ table, id string }{{"memories", id}, {"actors", e.actor}, {"projects", e.project}} {
		if b := e.fileBytes(f.table, f.id); b != nil {
			t.Fatalf("%s/%s written by bootstrap: %s", f.table, f.id, b)
		}
	}
	if rep.PendingExports != 0 {
		t.Fatalf("dirty marks of core-owned rows must be cleared: %d pending", rep.PendingExports)
	}
	if _, err := ReadProjectMeta(e.data); err != nil {
		t.Fatalf("_meta.json still pinned: %v", err)
	}
	if again := e.reconcile(realOwned); notWrittenWarnings(again) != 0 || again.Changed() {
		t.Fatalf("second pass must be quiet: %+v", again)
	}
}

// A lore.db-only change to a core-owned row is never exported by a sync
// pass; it is warned once per path per pass.
func TestOwned_SyncPassNeverExportsCoreOwnedRow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.reconcile(realOwned)
	id := e.addMemory("added through the old path")
	rep := e.reconcile(realOwned)
	if b := e.fileBytes("memories", id); b != nil {
		t.Fatalf("sync pass wrote a core-owned file: %s", b)
	}
	if n := notWrittenWarnings(rep); n != 1 {
		t.Fatalf("want one warning for the unexported row, got %d: %v", n, rep.Warnings)
	}
	if !e.memoryExists(id) {
		t.Fatal("the lore.db row must be kept")
	}
}

// `lore sync export --all` (MarkAllForExport) marks every row, and still
// writes nothing for a core-owned table.
func TestOwned_ExportAllNeverWritesCoreOwnedFiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.addMemory("m")
	e.reconcile(realOwned)
	if err := MarkAllForExport(context.Background(), e.db, e.data); err != nil {
		t.Fatal(err)
	}
	e.reconcile(realOwned)
	for _, f := range []struct{ table, id string }{{"memories", id}, {"actors", e.actor}, {"projects", e.project}} {
		if b := e.fileBytes(f.table, f.id); b != nil {
			t.Fatalf("export --all wrote %s/%s: %s", f.table, f.id, b)
		}
	}
}

// Adopt after `lore restore`, for every --prefer: the core's file is the
// source (imported into lore.db, its bytes untouched, never rewritten as
// `_v: 1`), and a row only in lore.db is kept there, neither exported nor
// trashed.
func TestOwned_RestoreEveryPreferKeepsCoreFiles(t *testing.T) {
	t.Parallel()
	for _, prefer := range []PreferMode{PreferNewest, PreferDB, PreferFiles} {
		t.Run("prefer="+string(prefer), func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			shared := e.addMemory("lore.db version")
			dbOnly := e.addMemory("only in lore.db")
			e.reconcile(realOwned)
			want := e.coreMemoryDoc(shared, "core file version")
			e.writeRaw("memories", shared, want)
			if err := ResetForRestore(context.Background(), e.db, prefer); err != nil {
				t.Fatal(err)
			}
			rep := e.reconcile(realOwned)
			if rep.Pass != PassAdopt {
				t.Fatalf("pass = %s", rep.Pass)
			}
			if got := e.fileBytes("memories", shared); string(got) != string(want) {
				t.Fatalf("the core file was rewritten:\n got %s\nwant %s", got, want)
			}
			if got := e.memoryBody(shared); got != "core file version" {
				t.Fatalf("the core file must win lore.db: %q", got)
			}
			if b := e.fileBytes("memories", dbOnly); b != nil {
				t.Fatalf("a lore.db-only core row was exported: %s", b)
			}
			if !e.memoryExists(dbOnly) {
				t.Fatal("a lore.db-only core row must not be trashed")
			}
		})
	}
}

// A zero-length core-owned file is reported and never repaired from
// lore.db (that repair would write a v1 document over the core's file).
func TestOwned_EmptyCoreFileNotRepaired(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.addMemory("m")
	e.reconcile(realOwned)
	e.writeRaw("memories", id, e.coreMemoryDoc(id, "from the core"))
	e.reconcile(realOwned)
	e.writeRaw("memories", id, []byte{})
	rep := e.reconcile(realOwned)
	if b := e.fileBytes("memories", id); len(b) != 0 {
		t.Fatalf("empty core file repaired: %s", b)
	}
	if !hasWarning(rep, "empty file") {
		t.Fatalf("the empty file must be reported: %v", rep.Warnings)
	}
	if got := e.memoryBody(id); got != "from the core" {
		t.Fatalf("lore.db keeps the last good import: %q", got)
	}
}

// A v2 tombstone imports as a delete (the row leaves lore.db via the
// trash), and the tombstone file is left as the core wrote it.
func TestOwned_V2TombstoneImportsAsDelete(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := e.addMemory("m")
	e.reconcile(realOwned)
	e.writeRaw("memories", id, e.coreMemoryDoc(id, "live"))
	e.reconcile(realOwned)
	if !e.memoryExists(id) {
		t.Fatal("the v2 row must import")
	}
	tomb := coreTombstone("memories", id)
	e.writeRaw("memories", id, tomb)
	e.reconcile(realOwned)
	if e.memoryExists(id) {
		t.Fatal("a v2 tombstone must delete the lore.db row")
	}
	if got := e.fileBytes("memories", id); string(got) != string(tomb) {
		t.Fatalf("the tombstone file was rewritten: %s", got)
	}
	if again := e.reconcile(realOwned); e.memoryExists(id) || notWrittenWarnings(again) != 0 {
		t.Fatalf("the row must stay deleted and quiet: %+v", again)
	}
}

// The git merge driver on a core-owned path returns the core's format
// (`_v: 2`), never a v1 upcast, for every core-owned table family.
func TestOwned_MergeDriverKeepsCoreFormat(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	id := "mem_01a10237c9e87e4c843d8045973a65a1"
	base := string(e.coreMemoryDoc(id, "base"))
	ours := strings.Replace(string(e.coreMemoryDoc(id, "base")), `"kind":"retrieved"`, `"kind":"observed"`, 1)
	theirs := string(e.coreMemoryDoc(id, "base"))
	out, doc, raw := runDriver(t, base, ours, theirs, ".lore/data/memories/x.json")
	if out.Conflicted || out.Fallback {
		t.Fatalf("clean core merge expected: %+v %s", out, raw)
	}
	if doc[keyVersion] != num(formatV2) {
		t.Fatalf("merge result must stay _v 2: %s", raw)
	}
}

func hasWarning(rep Report, sub string) bool {
	for _, w := range rep.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
