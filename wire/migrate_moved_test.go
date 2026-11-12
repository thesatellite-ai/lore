package wire

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMigrateMovedLeftovers pins B1-5: a row a newer lore moved to
// another store (its copy there records the move) while this checkout
// kept the old copy is E_AMBIGUOUS_ID everywhere; the migrate step settles
// it as the move would have: the dry run names it and writes nothing, the
// real run leaves a tombstone here, the row resolves to the store that
// owns it, and a second run has nothing to do.
func TestMigrateMovedLeftovers(t *testing.T) {
	t.Parallel()
	ctx := background()
	repo := gitRepo(t)
	writeStoresYAML(t, repo, "stores:\n  - name: agency\n    url: "+folderStoreAt(t, folderStore, rowfs.VisibilityInternal)+"\n")
	a := openWith(t, repo, Options{RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	fields := map[string]value.Value{}
	var err error
	for k, v := range map[string]string{"project_id": projectIn(t, a, store.SourceRepo), "kind": schema.MemoryKindCore, "body": "moved away", "source_kind": "manual"} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	w, err := a.Create(ctx, store.SourceRepo, schema.TableMemories, fields)
	if err != nil {
		t.Fatal(err)
	}
	id := w.Row.ID
	// the moved copy: in agency, newer, with a moves entry (what a move in
	// another checkout wrote)
	st, err := a.state(ctx)
	if err != nil {
		t.Fatal(err)
	}
	moved := w.Row
	moved.HLC = hlc.Next(w.Row.HLC, time.Now(), w.Row.HLC.Actor)
	if moved.Origin, err = value.JSONOf([]byte(`{"moves":[{"at":"2026-01-02T00:00:00Z","by":"x","moved_from":"repo","moved_to":"agency"}]}`)); err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(moved)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.byName["agency"].Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.eng.Refresh(ctx, "agency"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = st.eng.Get(ctx, engine.Ref{ID: id}); !errs.Has(err, errs.AmbiguousID) {
		t.Fatalf("before: %v, want E_AMBIGUOUS_ID", err)
	}

	lines, n, err := a.migrateMovedLeftovers(ctx, true)
	if err != nil || n != 1 || len(lines) != 1 || !strings.Contains(lines[0], id.String()) || !strings.Contains(lines[0], "is in agency") {
		t.Fatalf("dry run: %v %d %v", lines, n, err)
	}
	if _, _, err = st.eng.Get(ctx, engine.Ref{ID: id}); !errs.Has(err, errs.AmbiguousID) {
		t.Fatalf("the dry run wrote: %v", err)
	}

	if _, n, err = a.migrateMovedLeftovers(ctx, false); err != nil || n != 1 {
		t.Fatalf("run: %d %v", n, err)
	}
	src, got, err := st.eng.Get(ctx, engine.Ref{ID: id})
	if err != nil || src != "agency" || got.Deleted {
		t.Fatalf("after: %s %+v %v", src, got, err)
	}
	if here, gerr := st.byName[store.SourceRepo].Get(ctx, schema.TableMemories, id); gerr != nil || !here.Deleted {
		t.Fatalf("the repo copy is not a tombstone: %+v %v", here, gerr)
	}
	if lines, n, err = a.migrateMovedLeftovers(ctx, false); err != nil || n != 0 {
		t.Fatalf("second run: %v %d %v", lines, n, err)
	}
}
