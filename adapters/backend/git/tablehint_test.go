package git

// A store clone honours the table hint (store.WithTables) on a full read,
// as rowfs does in repo mode: listing one small table (the actors every
// command looks up) must not read every row file at HEAD. Before devco
// D-790 a personal store whose folder held 5,000 feed rows paid ~4 s on
// every call, even `lore sql --query 'select 1'`.

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

func TestCloneReadHonoursTableHint(t *testing.T) {
	ctx := context.Background()
	b := clone(t, newRemote(t, true))
	g := newGen(t)
	for _, body := range []string{"one", "two", "three"} {
		write(t, b, store.Change{Row: g.row(newID(t), body, hlc.HLC{})})
	}

	other := row.Table("decisions") // a table with no rows here
	rows, cur, err := b.Read(store.WithTables(ctx, other), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("a read hinted to %s returned %d rows of other tables, want 0 (it read every row file)", other, len(rows))
	}
	if cur != "" {
		t.Fatalf("a hinted read returned cursor %q; a partial read must return the empty cursor", cur)
	}

	hinted, _, err := b.Read(store.WithTables(ctx, memories), "")
	if err != nil || len(hinted) != 3 {
		t.Fatalf("read hinted to %s: %d rows, %v; want 3", memories, len(hinted), err)
	}

	// the partial read is not kept as the full one
	full, fcur, err := b.Read(ctx, "")
	if err != nil || len(full) != 3 || fcur == "" {
		t.Fatalf("full read after hinted reads: %d rows at %q, %v; want 3 at HEAD", len(full), fcur, err)
	}
}
