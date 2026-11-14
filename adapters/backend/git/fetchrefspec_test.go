package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/store"
)

// TestStoreCloneKeepsGitsFetchRefspec pins plain-files decision D-n: lore
// no longer writes remote.origin.fetch into a store clone, so a person's
// plain `git fetch` in the clone does what git does by default. After a
// clone, a write and a sync, the clone's fetch refspec is git's default
// alone.
func TestStoreCloneKeepsGitsFetchRefspec(t *testing.T) {
	t.Parallel()
	remote := newRemote(t, true)
	a := clone(t, remote)
	g := newGen(t)
	write(t, a, store.Change{Row: g.row(newID(t), "row", hlc.HLC{})})
	if _, err := a.SyncWith(context.Background(), SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(gitT(t, a.Dir(), "config", "--get-all", "remote.origin.fetch"))
	if got != "+refs/heads/*:refs/remotes/origin/*" {
		t.Fatalf("remote.origin.fetch in the store clone = %q; want git's default only", got)
	}
}
