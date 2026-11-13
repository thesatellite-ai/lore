package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if _, err := a.Sync(ctx); !errors.Is(err, ErrRemoteReset) {
		t.Fatalf("sync into an unrelated history: %v, want ErrRemoteReset", err)
	}
}
