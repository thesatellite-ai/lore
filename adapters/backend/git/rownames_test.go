package git

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestRowNamesNameEveryReadRow (bug hunt H-276, devco:D-1014): a store
// clone names every row file its full read returns from the tree alone,
// also the admin's rule a member removed by hand (gone from the tree, the
// read serves its accepted version from the hold), so lore doctor's
// copied_id check reads only the rows two stores share instead of every
// row of every folder.
func TestRowNamesNameEveryReadRow(t *testing.T) {
	t.Parallel()
	o := newOrgTeam(t)
	kept, removed := prefixedID(t, ids.PrefixRule), prefixedID(t, ids.PrefixRule)
	o.h.commit(o.boss.email, map[string]string{
		orgPath("rules", kept):    ruleAt(kept, o.boss.actor, "2026-01-05T00:00:00.000Z", "must", "no secrets in logs"),
		orgPath("rules", removed): ruleAt(removed, o.boss.actor, "2026-01-05T00:00:00.100Z", "must", "review every migration"),
	})
	o.h.commit(o.max.email, map[string]string{orgPath("rules", removed): ""})
	b := orgFolderClone(t, o.h.remote)
	if heldReason(t, b, removed) == "" {
		t.Fatal("the member's removal is not held: the fixture lost its point")
	}
	ctx := context.Background()
	rows, _, err := b.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	names, ok, err := b.RowNames(ctx)
	if err != nil || !ok {
		t.Fatalf("RowNames: ok %v, %v; want the names", ok, err)
	}
	named := map[store.RowName]bool{}
	for _, n := range names {
		named[n] = true
	}
	read := map[ids.AnyID]bool{}
	for _, r := range rows {
		read[r.ID] = true
		if !named[store.RowName{Table: r.Table, ID: r.ID}] {
			t.Errorf("the read returns %s %s, RowNames does not name it", r.Table, r.ID)
		}
	}
	if !read[kept] || !read[removed] {
		t.Fatalf("the read returns kept %v, removed %v; want both (the fixture lost its point)", read[kept], read[removed])
	}
}
