package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestSyncCountsCommitsThatArriveD648 (devco D-648): a pull said
// "received 4" (rows) when 8 commits arrived; CommitsIn counts what came
// in as git does (rev-list over the tips before and after), a commit that
// carries no row included, while Received still counts rows. The pushing
// side's own commits never count as arrived.
func TestSyncCountsCommitsThatArriveD648(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	for range 3 {
		write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	}
	if err := os.WriteFile(filepath.Join(a.Dir(), "lore-org.yaml"), []byte("stores: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitT(t, a.Dir(), "add", "lore-org.yaml")
	gitT(t, a.Dir(), "commit", "-q", "-m", "lore: org file")
	rep, err := a.Sync(ctx)
	if err != nil || rep.Commits != 4 || rep.CommitsIn != 0 {
		t.Fatalf("a: Sync = %+v, %v; want 4 commits out, 0 in", rep, err)
	}
	if rep, err = b.Sync(ctx); err != nil || rep.CommitsIn != 4 || rep.Received != 3 {
		t.Fatalf("b: Sync = %+v, %v; want 4 commits in, 3 rows received", rep, err)
	}
	if rep, err = b.Sync(ctx); err != nil || rep.CommitsIn != 0 {
		t.Fatalf("b: Sync with nothing new = %+v, %v; want 0 commits in", rep, err)
	}
}
