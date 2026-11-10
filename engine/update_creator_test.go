package engine

import (
	"context"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/auditfill"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestUpdateKeepsAWholeCreator: a row written before 0.1.18 has no
// created_by. An edit of it (an actor copy refreshed from its source is the
// common case) writes the creator `lore migrate run` would (auditfill:
// `unknown` here), never the editor, so the new version is whole and the
// next migrate's audit_fields step has nothing to fill on it. A row that
// has a creator keeps it.
func TestUpdateKeepsAWholeCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := setup(t)
	m := f.memory(t, "written by 0.1.17")
	back := f.repo.Open(store.OpenOptions{})
	raw, err := back.Get(ctx, schema.TableMemories, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := raw.Decode()
	if err != nil {
		t.Fatal(err)
	}
	old.CreatedBy, old.UpdatedBy = ids.ID[ids.Actor]{}, ids.ID[ids.Actor]{}
	old.HLC = hlc.Next(old.HLC, time.Now(), testActor)
	sr, err := store.FromRow(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := back.Write(ctx, []store.Change{{Row: sr, Expect: raw.HLC}}); err != nil {
		t.Fatal(err)
	}
	w, err := f.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"body": txt("edited by 0.1.18")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Row.CreatedBy != auditfill.UnknownActor() || w.Row.UpdatedBy != testActor {
		t.Errorf("edit of a row with no creator: created_by=%s updated_by=%s, want created_by %s (migrate's default, not the editor) and updated_by %s",
			w.Row.CreatedBy, w.Row.UpdatedBy, auditfill.UnknownActor(), testActor)
	}
	if filled, changed := auditfill.Defaults(w.Row); changed {
		t.Errorf("migrate would still fill the edited row: %+v", filled)
	}
	w, err = f.eng.Update(ctx, Ref{ID: m.ID}, map[string]value.Value{"body": txt("edited again")})
	if err != nil {
		t.Fatal(err)
	}
	if w.Row.CreatedBy != auditfill.UnknownActor() {
		t.Errorf("a second edit changed created_by to %s", w.Row.CreatedBy)
	}
}
