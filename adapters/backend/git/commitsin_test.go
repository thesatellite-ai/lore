package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

// TestSyncCountsMergedPullOnceD684 (devco D-684): a teammate pushed 2
// commits, this clone holds 3 of its own; its sync merges (one merge
// commit) and pushes. Received is the teammate's 2 commits, never the merge
// commit this clone made (it was counted as received and as sent: "received
// 3"); Sent is every commit that reached the remote, the merge included.
func TestSyncCountsMergedPullOnceD684(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	for range 2 {
		write(t, b, store.Change{Row: g.row(newID(t), "teammate row", hlc.HLC{})})
	}
	if _, err := b.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		write(t, a, store.Change{Row: g.row(newID(t), "own row", hlc.HLC{})})
	}
	tipBefore := gitT(t, remote, "rev-parse", "main")
	rep, err := a.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	went := gitT(t, remote, "rev-list", "--count", tipBefore+"..main")
	if rep.CommitsIn != 2 || strconv.Itoa(rep.Commits) != went {
		t.Fatalf("a: Sync = commits in %d, out %d; want 2 in (the teammate's), out %s (git rev-list on the remote)", rep.CommitsIn, rep.Commits, went)
	}
}
