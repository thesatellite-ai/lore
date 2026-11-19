package sqlite

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/store"
)

// TestTwoHandlesOneFileD667: a second handle on the same cache file (a
// second source at the same place, or another lore process) sees the kind
// tables the first one created or dropped since it opened. Before the fix
// its next write named a dropped res_<kind> table and failed E_INTERNAL
// "find referencing text", and a resource of a kind created through the
// other handle never reached its kind table (devco:D-667).
func TestTwoHandlesOneFileD667(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := f.open("c.db")
	f.kindWorld(a)
	b := f.open("c.db") // b's table list names res_server

	// dropped through a: b's writes must not name the table
	if err := a.Rebuild(f.ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReexpandMatching(f.ctx, []string{"${res:hub_prod"}); err != nil {
		t.Fatalf("ReexpandMatching after another handle dropped res_server: %v", err)
	}
	if _, err := b.ReexpandIDs(f.ctx, []string{"res_00000000000000000000000000000000"}); err != nil {
		t.Fatalf("ReexpandIDs after another handle dropped res_server: %v", err)
	}
	f.apply(b, "acme", "m1", f.memory(newMem(), newPrj(), "a note after the drop"))
	if err := b.Rebuild(f.ctx, "acme"); err != nil {
		t.Fatalf("Rebuild after another handle dropped res_server: %v", err)
	}

	// created through a: a resource b applies lands in the kind table
	k := f.kindWorld(a)
	res := must(ids.New[ids.Resource]())
	f.apply(b, "acme", "r1", f.resource(res, k.kind, "edge_one", map[ids.ID[ids.KindField]][2]string{
		k.regionF: {"region", `"us"`},
	}))
	if n := count(t, b, `SELECT COUNT(*) FROM "res_server" WHERE id = ?`, res.String()); n != 1 {
		t.Fatal("a resource applied through b is missing from res_server, which a created after b opened")
	}
	if _, err := b.ReexpandMatching(f.ctx, []string{"${res:edge_one"}); err != nil {
		t.Fatal(err)
	}
}
