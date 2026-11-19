package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestSyncCountsCommitsThatCarryNoRow (devco D-517): a commit that
// changes no row of the folder (an org file such as lore-org.yaml, as
// `lore store set-url` commits) is still sent: Commits counts it while
// Sent (rows) stays 0, so `lore store push` no longer says "sent 0"
// after pushing it. A sync with nothing to send counts 0 commits.
func TestSyncCountsCommitsThatCarryNoRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	if err := os.WriteFile(filepath.Join(a.Dir(), "lore-org.yaml"), []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, a.Dir(), "add", "lore-org.yaml")
	gitT(t, a.Dir(), "commit", "-q", "-m", "lore: the store moved")
	rep, err := a.Sync(ctx)
	if err != nil || rep.Offline {
		t.Fatalf("Sync = %+v, %v", rep, err)
	}
	if rep.Commits != 1 || rep.Sent != 0 {
		t.Fatalf("Sync sent %d commit(s), %d row(s); want 1 commit, 0 rows", rep.Commits, rep.Sent)
	}
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	if rep, err = a.Sync(ctx); err != nil || rep.Commits != 2 || rep.Sent != 2 {
		t.Fatalf("Sync after two writes = %+v, %v; want 2 commits, 2 rows", rep, err)
	}
	if rep, err = a.Sync(ctx); err != nil || rep.Commits != 0 {
		t.Fatalf("Sync with nothing to send = %+v, %v; want 0 commits", rep, err)
	}
}
