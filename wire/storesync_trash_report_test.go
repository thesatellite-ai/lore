package wire

// storesync_trash_report_test.go — TE-F-1 (DELETION 3.7): a `lore sync` of
// a git store says when a soft delete lost to a teammate's edit, or two
// deletes met, as `lore merge-driver` says it for a repo row and a live
// merge note for the live ref. Before TE-F-1 the store merger had no
// report hook and the sync was silent.

import (
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
)

// TestStoreSyncReportsTrashOutcome: checkouts A and B share the git store
// kb. A deletes two memories and syncs; B, before pulling, edits the first
// and deletes the second, then syncs: B's sync merges both rows and its
// notices name the store, each row and its outcome (kept alive by the
// edit; deleted on both sides). A row nobody touched on B is not reported,
// and the store url in the line carries no query.
func TestStoreSyncReportsTrashOutcome(t *testing.T) {
	hosts := histGitWorld(t)
	url, _ := bareStore(t, hosts, "acme/kb")
	repoA, repoB := gitRepo(t), gitRepo(t)
	if _, err := run(t, openWith(t, repoA, Options{}), opsstores.OpInit, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, openWith(t, repoB, Options{}), opsstores.OpAdd, `{"name":"kb","url":"`+url+`"}`, true); err != nil {
		t.Fatalf("add: %v", err)
	}
	var notesA, notesB []string
	a := openWith(t, repoA, Options{Notice: func(m string) { notesA = append(notesA, m) }})
	b := openWith(t, repoB, Options{Notice: func(m string) { notesB = append(notesB, m) }})
	withProject(t, a)
	withProject(t, b)
	sync := func(app *App, who string) {
		t.Helper()
		if _, err := app.SyncStores(background(), "kb"); err != nil {
			t.Fatalf("sync %s: %v", who, err)
		}
	}
	add := func(body string) string {
		t.Helper()
		out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`","to":"kb"}`, true)
		if err != nil {
			t.Fatal(err)
		}
		return idOf(t, out)
	}
	edited, both, untouched := add("edited on B"), add("deleted on both"), add("deleted on A only")
	sync(a, "A")
	sync(b, "B")
	for _, id := range []string{edited, both, untouched} {
		if _, err := run(t, a, opsmemory.OpDelete, `{"id":"`+id+`"}`, true); err != nil {
			t.Fatalf("A delete %s: %v", id, err)
		}
	}
	sync(a, "A")
	if _, err := run(t, b, opsmemory.OpEdit, `{"id":"`+edited+`","body":"B kept this"}`, true); err != nil {
		t.Fatalf("B edit: %v", err)
	}
	if _, err := run(t, b, opsmemory.OpDelete, `{"id":"`+both+`"}`, true); err != nil {
		t.Fatalf("B delete: %v", err)
	}
	notesB = nil
	sync(b, "B")

	reports := func(notes []string) []string {
		var out []string
		for _, n := range notes {
			if strings.HasPrefix(n, storeMergeReportPrefix) {
				out = append(out, n)
			}
		}
		return out
	}
	got := reports(notesB)
	for _, w := range []struct {
		id      string
		outcome merge.TrashOutcome
	}{
		{edited, merge.TrashKeptEdit},
		{both, merge.TrashDeletedBoth},
	} {
		want := "/" + w.id + ": " + string(w.outcome)
		if !slices.ContainsFunc(got, func(n string) bool { return strings.HasSuffix(n, want) }) {
			t.Errorf("B's sync did not report %s %q; reports: %q (all notices %q)", w.id, w.outcome, got, notesB)
		}
	}
	for _, n := range got {
		if strings.Contains(n, untouched) {
			t.Errorf("a row only one side changed is not a merge decision: %q", n)
		}
		if !strings.Contains(n, "acme/kb") || strings.Contains(n, "?") {
			t.Errorf("the line names the store by its url without query: %q", n)
		}
	}
	if len(got) != 2 {
		t.Errorf("want exactly 2 reports, got %q", got)
	}
	notesA = nil
	sync(a, "A")
	if r := reports(notesA); len(r) != 0 {
		t.Errorf("A only fast-forwards to B's merge: nothing to report, got %q", r)
	}
}
