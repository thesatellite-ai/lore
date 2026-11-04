package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestMoveInternalMetaIntoClient: a row holding an internal metadata value
// is refused into a client repo with E_META_INTERNAL (MQ11, nothing
// written); --drop-internal moves it without that value and warns; and
// --with-time totals is refused as not built (TQ3 hook).
func TestMoveInternalMetaIntoClient(t *testing.T) {
	ctx := context.Background()
	w := newProvWorld(t, fakeProv{})
	w.project(t, store.SourceRepo, "app")
	prj := w.project(t, agency, "acme-hub")
	put := func(r row.Row) {
		t.Helper()
		sr, err := store.FromRow(r)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.data[agency].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
	}
	kid, err := w.eng.ids.NewID(mustPrefix(t, w, schema.TableMetaKeys))
	if err != nil {
		t.Fatal(err)
	}
	h := hlc.Next(hlc.HLC{}, w.eng.wall(), testActor)
	put(row.Row{Table: schema.TableMetaKeys, ID: kid, HLC: h, Fields: map[string]value.Value{
		"name": txt("client_rate"), "scope": txt("store"), "type": txt("text"), "internal": value.BoolOf(true)}})
	mk, err := ids.Parse[ids.MetaKey](kid.String())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := value.JSONOf([]byte(`{"name":"client_rate","scope":"store","v":"90"}`))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := value.RefOf(prj)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.eng.ids.NewID(mustPrefix(t, w, schema.TableMemories))
	if err != nil {
		t.Fatal(err)
	}
	put(row.Row{Table: schema.TableMemories, ID: id, HLC: h, Meta: map[ids.ID[ids.MetaKey]]value.JSON{mk: entry},
		Fields: map[string]value.Value{"project_id": ref, "kind": txt(schema.MemoryKindCore), "body": txt("rates"), "source_kind": txt("manual")}})

	if _, err = w.eng.Move(ctx, Ref{ID: id}, store.SourceRepo); !errs.Has(err, errs.MetaInternal) {
		t.Fatalf("plain move: want E_META_INTERNAL, got %v", err)
	}
	if _, err = w.eng.MoveWith(ctx, Ref{ID: id}, store.SourceRepo, MoveOptions{WithTime: TimeTotals}); !errs.Has(err, errs.NotImplemented) {
		t.Fatalf("--with-time totals: want E_NOT_IMPLEMENTED, got %v", err)
	}
	got, err := w.eng.MoveWith(ctx, Ref{ID: id}, store.SourceRepo, MoveOptions{DropInternal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Row.Meta) != 0 || len(got.Warnings) != 1 || got.Warnings[0].Code != WarnMetaDropped || got.Warnings[0].Field != "client_rate" {
		t.Fatalf("drop-internal: meta %v warnings %v", got.Row.Meta, got.Warnings)
	}
}
