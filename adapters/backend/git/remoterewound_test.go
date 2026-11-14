package git

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestSyncRefusesARewoundRemote (H-9): someone rewinds the store branch
// (a force-push of an older commit, to drop a wrong row); a clone that
// holds the dropped commit never pushes it back on its next sync: the
// sync is refused naming the rewrite and both ways out, and keeps
// refusing (shared history does not clear the mark); RestoreRemote lets
// the next sync put the commits back.
func TestSyncRefusesARewoundRemote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "kept", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	keep := strings.TrimSpace(gitT(t, remote, "rev-parse", testBranch))
	write(t, a, store.Change{Row: g.row(newID(t), "dropped by the admin", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	dropped := strings.TrimSpace(gitT(t, remote, "rev-parse", testBranch))
	gitT(t, remote, "update-ref", "refs/heads/"+testBranch, keep) // the admin's force-push
	for i := 0; i < 2; i++ {
		_, err := a.Sync(ctx)
		var le *errs.Error
		if !errors.Is(err, ErrRemoteReset) || !errors.As(err, &le) || !strings.Contains(le.Message, "rewritten by someone") ||
			!strings.Contains(le.Hint, "lore store push --restore --store") || !strings.Contains(le.Hint, "lore store forget --url") {
			t.Fatalf("sync %d into a rewound remote: %v", i+1, err)
		}
		if got := strings.TrimSpace(gitT(t, remote, "rev-parse", testBranch)); got != keep {
			t.Fatalf("sync %d pushed the dropped commit back: remote at %s", i+1, got)
		}
	}
	if err := RestoreRemote(a.Dir()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Sync(ctx); err != nil {
		t.Fatalf("sync after restore: %v", err)
	}
	if got := strings.TrimSpace(gitT(t, remote, "rev-parse", testBranch)); got != dropped {
		t.Fatalf("restore: remote at %s, want %s", got, dropped)
	}
}

// TestSyncAfterAForwardMoveIsNotARewind (H-9): a teammate's ordinary push
// (the remote moved forward) is merged and pushed over as before.
func TestSyncAfterAForwardMoveIsNotARewind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "a1", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	write(t, b, store.Change{Row: g.row(newID(t), "b1", hlc.HLC{})})
	if _, err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	write(t, a, store.Change{Row: g.row(newID(t), "a2", hlc.HLC{})})
	if _, err := a.Sync(ctx); err != nil {
		t.Fatalf("a sync after a teammate's push: %v", err)
	}
}
