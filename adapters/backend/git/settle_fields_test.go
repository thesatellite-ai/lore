package git

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// memRow is a memory row with the given text fields, stamped after prev.
func (g *gen) memRow(id ids.AnyID, prev hlc.HLC, fields map[string]string) store.Row {
	g.t.Helper()
	fs := map[string]value.Value{}
	for k, s := range fields {
		v, err := value.TextOf(s)
		if err != nil {
			g.t.Fatal(err)
		}
		fs[k] = v
	}
	stamp := g.clock.Now(prev)
	r, err := store.FromRow(row.Row{Table: memories, ID: id, HLC: stamp, UpdatedAt: stamp.Wall, UpdatedBy: stamp.Actor, Fields: fs})
	if err != nil {
		g.t.Fatal(err)
	}
	return r
}

// fieldsOf decodes a stored row's text fields.
func fieldsOf(t *testing.T, r store.Row) map[string]string {
	t.Helper()
	d, err := r.Decode()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range d.Fields {
		out[k] = v.String()
	}
	return out
}

// TestSettleKeepsOtherFields (bug hunt B2-22): settling a clash, by
// resolve (take either side) or by editing the row after the refusal,
// changes only the clashing field; a field only one side changed keeps
// that change on both machines.
func TestSettleKeepsOtherFields(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		settle     func(t *testing.T, b *Backend, ga *gen, id ids.AnyID, prev hlc.HLC)
		body, kind string
	}{
		{"take kept", func(t *testing.T, b *Backend, _ *gen, id ids.AnyID, _ hlc.HLC) {
			if err := b.SettleRow(context.Background(), id, false); err != nil {
				t.Fatal(err)
			}
		}, "b body", "episodic"},
		{"take other", func(t *testing.T, b *Backend, _ *gen, id ids.AnyID, _ hlc.HLC) {
			if err := b.SettleRow(context.Background(), id, true); err != nil {
				t.Fatal(err)
			}
		}, "a body", "episodic"},
		{"edit after the refusal", func(t *testing.T, b *Backend, gb *gen, id ids.AnyID, prev hlc.HLC) {
			write(t, b, store.Change{Row: gb.memRow(id, prev, map[string]string{"body": "agreed", "kind": "core", "source_ref": "b ref"}), Expect: prev})
		}, "agreed", "episodic"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			remote := newRemote(t, true)
			m := WithMerger(merge.Driver{Registry: reg})
			a, b := clone(t, remote, m), clone(t, remote, m)
			ga, gb := newGen(t), newGen(t)
			x := ga.memRow(newID(t), hlc.HLC{}, map[string]string{"body": "start", "kind": "core", "source_ref": "ref"})
			write(t, a, store.Change{Row: x})
			syncOK(t, a)
			syncOK(t, b)
			// a: body and kind; b: body and source_ref
			write(t, a, store.Change{Row: ga.memRow(x.ID, x.HLC, map[string]string{"body": "a body", "kind": "episodic", "source_ref": "ref"}), Expect: x.HLC})
			syncOK(t, a)
			b1 := gb.memRow(x.ID, x.HLC, map[string]string{"body": "b body", "kind": "core", "source_ref": "b ref"})
			write(t, b, store.Change{Row: b1, Expect: x.HLC})
			if rep, err := b.Sync(ctx); err != nil || len(rep.Refused) != 1 {
				t.Fatalf("sync = %+v %v, want the body clash refused", rep, err)
			}
			c.settle(t, b, gb, x.ID, b1.HLC)
			syncOK(t, b)
			syncOK(t, a)
			for _, cl := range []*Backend{a, b} {
				got := fieldsOf(t, get(t, cl, x.ID))
				if got["body"] != c.body || got["kind"] != c.kind || got["source_ref"] != "b ref" {
					t.Fatalf("settled row %v: want body %q, a's kind %q, b's source_ref", got, c.body, c.kind)
				}
			}
		})
	}
}
