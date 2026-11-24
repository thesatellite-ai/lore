package git

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLayerPromoteKeepsNewerMainEdits (#155, devco:D-947): promote merges
// the branch layer's copy of a row field by field against the layer base,
// as a landing does. Fields only the store branch changed after the layer
// copied the row keep the store branch's value (they were overwritten by
// the branch's whole row); a text both changed refuses the promote with
// E_LAND_CONFLICT and changes nothing, unless take chooses a side for
// those fields (every other field still merges).
func TestLayerPromoteKeepsNewerMainEdits(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	start := map[string]string{"body": "start", "kind": "core", "source_ref": "ref"}
	setup := func(t *testing.T, branch, main map[string]string) (*Backend, ids.AnyID) {
		t.Helper()
		b := clone(t, newRemote(t, true), WithMerger(merge.Driver{Registry: reg}))
		g := newGen(t)
		x := g.memRow(newID(t), hlc.HLC{}, start)
		write(t, b, store.Change{Row: x})
		syncOK(t, b)
		layerWrite(t, b, layerKey("feat/x"), store.Change{Row: g.memRow(x.ID, x.HLC, branch), Expect: x.HLC})
		write(t, b, store.Change{Row: g.memRow(x.ID, x.HLC, main), Expect: x.HLC})
		return b, x.ID
	}
	t.Run("fields only main changed stay", func(t *testing.T) {
		b, id := setup(t,
			map[string]string{"body": "branch body", "kind": "core", "source_ref": "ref"},
			map[string]string{"body": "start", "kind": "episodic", "source_ref": "main ref"})
		res, err := b.LayerPromote(context.Background(), layerKey("feat/x"), []ids.AnyID{id}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Merged) != 1 || res.Commit == "" {
			t.Fatalf("promote result %+v: want the row merged and committed", res)
		}
		got := fieldsOf(t, get(t, b, id))
		if got["body"] != "branch body" || got["kind"] != "episodic" || got["source_ref"] != "main ref" {
			t.Fatalf("promoted row %v: want the branch's body and main's newer kind and source_ref", got)
		}
		if rows := layerRowsOf(t, b, layerKey("feat/x")); len(rows) != 0 {
			t.Fatalf("the promoted row still differs on the layer: %v", rows)
		}
	})
	clash := func(t *testing.T) (*Backend, ids.AnyID) {
		return setup(t,
			map[string]string{"body": "branch body", "kind": "core", "source_ref": "branch ref"},
			map[string]string{"body": "main body", "kind": "episodic", "source_ref": "ref"})
	}
	t.Run("same text both changed refuses", func(t *testing.T) {
		b, id := clash(t)
		before := get(t, b, id)
		_, err := b.LayerPromote(context.Background(), layerKey("feat/x"), []ids.AnyID{id}, nil)
		var e *errs.Error
		if !errors.As(err, &e) || e.Code != errs.LandConflict {
			t.Fatalf("promote of a clash = %v, want E_LAND_CONFLICT", err)
		}
		if after := get(t, b, id); string(after.Doc) != string(before.Doc) {
			t.Fatalf("a refused promote changed the row:\n%s", after.Doc)
		}
		if rows := layerRowsOf(t, b, layerKey("feat/x")); len(rows) != 1 {
			t.Fatalf("a refused promote changed the layer: %v", rows)
		}
	})
	for _, c := range []struct {
		branch bool
		body   string
	}{{false, "main body"}, {true, "branch body"}} {
		t.Run("take settles only the clash", func(t *testing.T) {
			b, id := clash(t)
			res, err := b.LayerPromote(context.Background(), layerKey("feat/x"), []ids.AnyID{id}, &c.branch)
			if err != nil || len(res.Taken) != 1 {
				t.Fatalf("take %v: %+v %v", c.branch, res, err)
			}
			got := fieldsOf(t, get(t, b, id))
			if got["body"] != c.body || got["kind"] != "episodic" || got["source_ref"] != "branch ref" {
				t.Fatalf("take %v: %v, want body %q, main's kind, the branch's source_ref", c.branch, got, c.body)
			}
		})
	}
}
