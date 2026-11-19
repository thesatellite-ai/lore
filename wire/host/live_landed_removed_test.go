package host

// live_landed_removed_test.go — devco D-713: `lore store adopt --move
// --commit` removes the moved row files in a commit of the repo. Until
// that commit is pushed, the fetched origin/<default> still holds them,
// and the default-branch read-through read each one back: every moved id
// was "in repo and <org>: two live rows with one id" (E_AMBIGUOUS_ID), so
// show, edit and delete by bare id failed before the push. A file the
// merge base holds and HEAD lacks was removed here on purpose; only a file
// that landed after the last pull (the merge base lacks it) reads through.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLiveBackend_RowRemovedByALocalCommitIsNotReadThrough: three tasks
// pulled (the merge base); origin then gained task 3; this checkout
// committed the removal of task 1 and has not pushed. Get and a full Read
// leave task 1 out and still read task 3 through. Break-verification:
// without removedHere, Get answers origin's task 1 and the list holds it.
func TestLiveBackend_RowRemovedByALocalCommitIsNotReadThrough(t *testing.T) {
	f := newLiveFixture(t)
	dir := f.b.top
	rows := mbCommitRows(t, dir, 3, func(int) int { return 5 }, "pulled", "base")

	// origin gains task 3 after this checkout's last pull
	landed := ltRow(t, schema.TableTasks, mbTaskID(3), 7, "landed after the pull", false)
	landedFile := filepath.Join(dir, filepath.FromSlash(mainDataPath(live.RootFolder, schema.TableTasks+"/"+mbTaskID(3)+rowFileExt)))
	if err := os.WriteFile(landedFile, landed.Doc, 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "a teammate's task")
	gitT(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitT(t, dir, "reset", "-q", "--hard", "HEAD~1")

	// this checkout moves task 1 out in a commit it has not pushed
	gitT(t, dir, "rm", "-q", mainDataPath(live.RootFolder, schema.TableTasks+"/"+mbTaskID(1)+rowFileExt))
	gitT(t, dir, "commit", "-q", "-m", "lore: move the notes to acme/ledger")
	if err := f.inner.Write(f.ctx, []store.Change{{Row: rows[0]}, {Row: rows[2]}}); err != nil {
		t.Fatal(err)
	}

	if r, err := f.b.Get(f.ctx, schema.TableTasks, mustAnyID(t, mbTaskID(1))); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get of the row a local commit removed: %v (row %s), want not found: it reads origin's copy back", err, r.ID)
	}
	if _, err := f.b.Get(f.ctx, schema.TableTasks, mustAnyID(t, mbTaskID(3))); err != nil {
		t.Fatalf("Get of the row that landed after the pull: %v, want the read-through copy", err)
	}
	all, _, err := f.b.Read(f.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range all {
		if r.Table == schema.TableTasks {
			got[r.ID.String()] = true
		}
	}
	if got[mbTaskID(1)] {
		t.Errorf("the list holds the row a local commit removed (read back from origin): %v", got)
	}
	for _, i := range []int{0, 2, 3} {
		if !got[mbTaskID(i)] {
			t.Errorf("the list lacks %s: %v", mbTaskID(i), got)
		}
	}
}
