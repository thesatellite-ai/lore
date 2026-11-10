package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// TestWritePurgeCommitsTheList: a purge in a clone is ONE commit holding
// the tombstone and _purged.json ("lore: purge <id>"), the worktree is
// clean after it, a list-only purge (tombstone already there) is its own
// commit, and both clones' lists union through sync: each side purges a
// different id offline and after syncing both sides list both (the
// folder-file merge), so neither id can come back on either clone.
func TestWritePurgeCommitsTheList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := newRemote(t, true)
	a := clone(t, remote, WithMerger(newestMerger{}))
	b := clone(t, remote, WithMerger(newestMerger{}))
	g := newGen(t)
	x, y := g.row(newID(t), "x", hlc.HLC{}), g.row(newID(t), "y", hlc.HLC{})
	write(t, a, store.Change{Row: x}, store.Change{Row: y})
	syncOK(t, a)
	syncOK(t, b)

	tomb := func(r store.Row) store.Row {
		t.Helper()
		h := g.clock.Now(r.HLC)
		sr, err := store.FromRow(row.Row{Table: r.Table, ID: r.ID, HLC: h, Deleted: true, UpdatedAt: h.Wall, UpdatedBy: h.Actor})
		if err != nil {
			t.Fatal(err)
		}
		return sr
	}
	before := gitT(t, a.Dir(), "rev-list", "--count", "HEAD")
	if err := a.WritePurge(ctx, []store.Change{{Row: tomb(x), Expect: x.HLC}}, []ids.AnyID{x.ID}); err != nil {
		t.Fatal(err)
	}
	if after := gitT(t, a.Dir(), "rev-list", "--count", "HEAD"); after == before {
		t.Fatal("the purge made no commit")
	}
	if subj := gitT(t, a.Dir(), "log", "-1", "--format=%s"); subj != "lore: "+purgeOp+" "+x.ID.String() {
		t.Fatalf("subject %q", subj)
	}
	files := gitT(t, a.Dir(), "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, ".lore/data/"+rowfs.PurgedFileName) || !strings.Contains(files, rowfs.RowPath(memories, x.ID)) {
		t.Fatalf("the purge commit must hold the tombstone and the list:\n%s", files)
	}
	if status := gitT(t, a.Dir(), "status", "--porcelain"); status != "" {
		t.Fatalf("worktree not clean:\n%s", status)
	}
	// b purges y offline (list only: its tombstone is written first)
	write(t, b, store.Change{Row: tomb(y), Expect: y.HLC})
	if err := b.WritePurge(ctx, nil, []ids.AnyID{y.ID}); err != nil {
		t.Fatal(err)
	}
	if subj := gitT(t, b.Dir(), "log", "-1", "--format=%s"); subj != "lore: "+purgeOp+" "+y.ID.String() {
		t.Fatalf("list-only purge subject %q", subj)
	}
	syncOK(t, a)
	syncOK(t, b)
	syncOK(t, a)
	for name, c := range map[string]*Backend{"a": a, "b": b} {
		set, err := c.Purged(ctx)
		if err != nil || !set[x.ID.String()] || !set[y.ID.String()] {
			t.Fatalf("%s lists %v %v, want both purged ids (union)", name, set, err)
		}
	}
}
