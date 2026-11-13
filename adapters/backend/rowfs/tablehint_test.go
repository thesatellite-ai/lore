package rowfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/store"
)

// A full Read under a table hint (store.WithTables) reads only those
// tables' folders: listing the identity tables of a 5,000-row repo used to
// decode every row file of it on every command (`lore whoami` 5 s). The
// hinted read never touches another table's files, answers the empty
// cursor (it is no picture of the folder) and leaves the problems a full
// read found as they were.
func TestReadTableHintReadsOnlyThoseTables(t *testing.T) {
	t.Parallel()
	r, g, ctx := openRoot(t), newGen(t), context.Background()
	f := folder(t, r, FolderRoot)
	mem := g.row(newID(t), "a memory", hlc.HLC{})
	act := actorRow(t, g)
	apply(t, f, store.Change{Row: mem}, store.Change{Row: act})

	// a broken file in the memories folder: a full read reports it
	bad := filepath.Join(f.dataDir, string(memories), newID(t).String()+".json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	all, cur, err := f.Read(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || cur == "" {
		t.Fatalf("full read: %d rows, cursor %q; want 2 rows and a cursor", len(all), cur)
	}
	if len(f.ReadProblems()) != 1 {
		t.Fatalf("full read problems = %v, want the broken memory file", f.ReadProblems())
	}

	rows, next, err := f.Read(store.WithTables(ctx, actors), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Table != actors || rows[0].ID != act.ID {
		t.Fatalf("hinted read returned %v, want only the actor row", rows)
	}
	if next != "" {
		t.Fatalf("hinted read cursor = %q, want empty (it saw part of the folder)", next)
	}
	if len(f.ReadProblems()) != 1 {
		t.Fatalf("hinted read changed the problems a full read found: %v", f.ReadProblems())
	}

	// a table with no folder has no rows; a cursor read ignores the hint
	if rows, _, err = f.Read(store.WithTables(ctx, "rules"), ""); err != nil || len(rows) != 0 {
		t.Fatalf("hinted read of an absent table = %v, %v; want no rows", rows, err)
	}
	if rows, _, err = f.Read(store.WithTables(ctx, actors), cur); err != nil || len(rows) != 0 {
		t.Fatalf("cursor read under a hint = %v, %v; want the cursor's (no change)", rows, err)
	}
}

// actors is a second table for the hint test (one identity matching reads).
const actors row.Table = "actors"

// actorRow is one row of the actors table.
func actorRow(t *testing.T, g *gen) store.Row {
	t.Helper()
	id, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.TextOf("A Person")
	if err != nil {
		t.Fatal(err)
	}
	stamp := g.clock.Now(hlc.HLC{})
	r, err := store.FromRow(row.Row{Table: actors, ID: id.Any(), HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: map[string]value.Value{"display_name": v}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
