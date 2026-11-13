package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestSyncRefusesAResetRemote (B3-29): a store remote deleted and made
// again empty is never filled with the old store by a sync: the sync is
// refused (E_REFUSED, ErrRemoteReset) and keeps refusing although the
// fetch pruned the tracking ref; RestoreRemote lets the next sync put the
// copy back. A remote made again with another history is refused too.
func TestSyncRefusesAResetRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "old", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	remake := func() {
		t.Helper()
		if err := os.RemoveAll(remote); err != nil {
			t.Fatal(err)
		}
		gitT(t, filepath.Dir(remote), "init", "--quiet", "--bare", "--initial-branch="+testBranch, remote)
	}
	remake()
	write(t, a, store.Change{Row: g.row(newID(t), "after the reset", hlc.HLC{})})
	for i := 0; i < 2; i++ {
		_, err := a.Sync(ctx)
		if !errs.Has(err, errs.Refused) || !errors.Is(err, ErrRemoteReset) {
			t.Fatalf("sync %d into a reset remote: %v, want E_REFUSED / ErrRemoteReset", i+1, err)
		}
		if got := gitT(t, remote, "for-each-ref"); got != "" {
			t.Fatalf("sync %d pushed into the reset remote: %s", i+1, got)
		}
	}
	if err := RestoreRemote(a.Dir()); err != nil {
		t.Fatal(err)
	}
	if rep, err := a.Sync(ctx); err != nil || rep.Sent == 0 {
		t.Fatalf("sync after restore: %+v, %v", rep, err)
	}
	if remoteCount(t, remote) == 0 {
		t.Fatal("the restored copy is not on the remote")
	}

	// another history at the same address (another team's new store)
	remake()
	other := newRemote(t, true)
	gitT(t, filepath.Join(filepath.Dir(other), "seed"), "push", "--quiet", remote, testBranch)
	write(t, a, store.Change{Row: g.row(newID(t), "after the second reset", hlc.HLC{})})
	_, err := a.Sync(ctx)
	if !errors.Is(err, ErrRemoteReset) {
		t.Fatalf("sync into an unrelated history: %v, want ErrRemoteReset", err)
	}
	// B2-34: the remote is another lore store now (another store id):
	// only forget + sync is offered, never --restore or init
	var le *errs.Error
	if !errors.As(err, &le) || !strings.Contains(le.Message, "started fresh") || !strings.Contains(le.Hint, "lore store forget") ||
		strings.Contains(le.Hint, "--restore") || strings.Contains(le.Hint, "store init") {
		t.Fatalf("a store started fresh by someone: %v (hint %q)", err, le.Hint)
	}
}

// TestResetFoundAfterAnotherFetchPruned (B2-28): another fetch of the
// clone (a read's refresh, the org link index) pruned the tracking ref
// before the sync looked; the remote tip the clone last saw still tells
// the emptied remote from one that never had the branch.
func TestResetFoundAfterAnotherFetchPruned(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "old", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(remote), "init", "--quiet", "--bare", "--initial-branch="+testBranch, remote)
	gitT(t, a.Dir(), "fetch", "--quiet", "--prune", RemoteName)
	write(t, a, store.Change{Row: g.row(newID(t), "after the wipe", hlc.HLC{})})
	if _, err := a.Sync(ctx); !errors.Is(err, ErrRemoteReset) {
		t.Fatalf("sync after a pruning fetch: %v, want ErrRemoteReset", err)
	}
	if got := gitT(t, remote, "for-each-ref"); got != "" {
		t.Fatalf("the emptied remote was refilled: %s", got)
	}
}

// TestResetMarkClearsWhenTheStoreIsBack (B2-30): a clone marked reset
// during a wipe syncs as usual again once someone put the store back
// (the remote shares its history again), and says so once.
func TestResetMarkClearsWhenTheStoreIsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "old", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	backup := remote + ".backup"
	gitT(t, filepath.Dir(remote), "clone", "--quiet", "--bare", remote, backup)
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(remote), "init", "--quiet", "--bare", "--initial-branch="+testBranch, remote)
	if _, err := a.Sync(ctx); !errors.Is(err, ErrRemoteReset) {
		t.Fatalf("sync into the wiped remote: %v", err)
	}
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	gitT(t, filepath.Dir(remote), "clone", "--quiet", "--bare", backup, remote)
	write(t, a, store.Change{Row: g.row(newID(t), "after it came back", hlc.HLC{})})
	if rep, err := a.Sync(ctx); err != nil || rep.Sent == 0 {
		t.Fatalf("sync after the store came back: %+v, %v", rep, err)
	}
	if RemoteResetAt(a.Dir()) || !a.TakeCameBack() || a.TakeCameBack() {
		t.Fatal("the reset mark was not cleared once, with one came-back note")
	}
}
