package git

import (
	"context"
	"errors"
	"testing"

	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLayerStackedLandKeepsMainEdits (#155 stacked, devco:D-951): when a
// parent layer lands, the layer stacked on it moves onto the landing with
// each row merged against the copy it was taken from, so main's edits made
// after that copy (before or after the parent landed) stay main's when the
// stacked layer lands or promotes: fields only main changed keep main's
// value, a text both changed stops with E_LAND_CONFLICT, and --take main
// keeps main's text of it. The old move wrote the stacked layer's raw
// copies onto the landing, so its own land undid main's edits silently,
// also for the row --take main named.
func TestLayerStackedLandKeepsMainEdits(t *testing.T) {
	t.Parallel()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := map[string]string{"body": "start", "kind": "core", "source_ref": "ref"}
	kx, ky, kz := layerKey("feat/x"), layerKey("feat/y"), layerKey("feat/z")
	// setup: a row on main; feat/x with a note of its own; feat/y stacked
	// on feat/x edits the row (child); main then edits it (mainBefore,
	// nil = no edit) before feat/x lands.
	setup := func(t *testing.T, child, mainBefore map[string]string) *stackWorld {
		t.Helper()
		b := clone(t, newRemote(t, true), WithMerger(merge.Driver{Registry: reg}))
		g := newGen(t)
		x := g.memRow(newID(t), hlc.HLC{}, start)
		write(t, b, store.Change{Row: x})
		syncOK(t, b)
		layerWrite(t, b, kx, store.Change{Row: g.memRow(newID(t), hlc.HLC{}, map[string]string{"body": "parent note", "kind": "core", "source_ref": "p"})})
		cy := g.memRow(x.ID, x.HLC, child)
		if _, err := b.LayerWrite(ctx, LayerWriteRequest{Key: ky, Op: "memory.edit",
			Facts:   branchlayer.Facts{Repo: "example.com/acme/app", Parent: "feat/x"},
			Changes: []store.Change{{Row: cy, Expect: x.HLC}}}); err != nil {
			t.Fatal(err)
		}
		w := &stackWorld{b: b, g: g, id: x.ID, cur: x.HLC, child: cy.HLC}
		if mainBefore != nil {
			w.mainEdit(t, mainBefore)
		}
		return w
	}
	landX := func(t *testing.T, w *stackWorld) {
		t.Helper()
		res, err := w.b.LayerLand(ctx, LandRequest{Key: kx, Subject: "lore: land feat/x"})
		if err != nil || len(res.Rebased) != 1 {
			t.Fatalf("land feat/x: %+v %v", res, err)
		}
	}
	isConflict := func(err error) bool {
		var e *errs.Error
		return errors.As(err, &e) && e.Code == errs.LandConflict
	}
	main := func(t *testing.T, w *stackWorld) map[string]string { return fieldsOf(t, get(t, w.b, w.id)) }

	t.Run("fields only main changed before the parent landed stay", func(t *testing.T) {
		w := setup(t, map[string]string{"body": "child body", "kind": "core", "source_ref": "ref"},
			map[string]string{"body": "start", "kind": "episodic", "source_ref": "main ref"})
		landX(t, w)
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y"}); err != nil {
			t.Fatal(err)
		}
		if got := main(t, w); got["body"] != "child body" || got["kind"] != "episodic" || got["source_ref"] != "main ref" {
			t.Fatalf("after the child land: %v, want the child's body and main's kind and source_ref", got)
		}
	})
	t.Run("a main edit after the parent landed stays", func(t *testing.T) {
		w := setup(t, map[string]string{"body": "child body", "kind": "core", "source_ref": "ref"}, nil)
		landX(t, w)
		w.mainEdit(t, map[string]string{"body": "start", "kind": "episodic", "source_ref": "ref"})
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y"}); err != nil {
			t.Fatal(err)
		}
		if got := main(t, w); got["body"] != "child body" || got["kind"] != "episodic" {
			t.Fatalf("after the child land: %v, want the child's body and main's kind", got)
		}
	})
	clash := func(t *testing.T) *stackWorld {
		w := setup(t, map[string]string{"body": "child body", "kind": "core", "source_ref": "child ref"},
			map[string]string{"body": "main body", "kind": "episodic", "source_ref": "ref"})
		landX(t, w)
		return w
	}
	t.Run("same text both changed stops the child land, take main keeps main's", func(t *testing.T) {
		w := clash(t)
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y"}); !isConflict(err) {
			t.Fatalf("child land of a clash = %v, want E_LAND_CONFLICT", err)
		}
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y", DryRun: true, Predict: true}); !isConflict(err) {
			t.Fatalf("child land --dry-run of a clash = %v, want E_LAND_CONFLICT", err)
		}
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y", Take: map[ids.AnyID]bool{w.id: false}}); err != nil {
			t.Fatal(err)
		}
		if got := main(t, w); got["body"] != "main body" || got["kind"] != "episodic" || got["source_ref"] != "child ref" {
			t.Fatalf("after --take main: %v, want main's body and kind, the child's source_ref", got)
		}
	})
	t.Run("promote after the parent landed refuses the clash, take main keeps main's", func(t *testing.T) {
		w := clash(t)
		if _, err := w.b.LayerPromote(ctx, ky, []ids.AnyID{w.id}, nil); !isConflict(err) {
			t.Fatalf("promote of a clash = %v, want E_LAND_CONFLICT", err)
		}
		side := false
		if _, err := w.b.LayerPromote(ctx, ky, []ids.AnyID{w.id}, &side); err != nil {
			t.Fatal(err)
		}
		if got := main(t, w); got["body"] != "main body" || got["kind"] != "episodic" || got["source_ref"] != "child ref" {
			t.Fatalf("after promote --take main: %v, want main's body and kind, the child's source_ref", got)
		}
		if l, _ := w.b.LayerOpen(ctx, ky); len(l.Facts.SplitRows) != 0 {
			t.Fatalf("the promoted row keeps an old base on the layer: %v", l.Facts.SplitRows)
		}
	})
	t.Run("two levels: a grandchild keeps main's edits through both landings", func(t *testing.T) {
		w := setup(t, map[string]string{"body": "start", "kind": "core", "source_ref": "child ref"}, nil)
		if _, err := w.b.LayerWrite(ctx, LayerWriteRequest{Key: kz, Op: "memory.edit",
			Facts:   branchlayer.Facts{Repo: "example.com/acme/app", Parent: "feat/y"},
			Changes: []store.Change{{Row: w.g.memRow(w.id, w.child, map[string]string{"body": "grandchild body", "kind": "core", "source_ref": "child ref"}), Expect: w.child}}}); err != nil {
			t.Fatal(err)
		}
		w.mainEdit(t, map[string]string{"body": "start", "kind": "episodic", "source_ref": "ref"})
		landX(t, w)
		w.mainEdit(t, map[string]string{"body": "start", "kind": "episodic", "source_ref": "ref", "title": "main title"})
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: ky, Subject: "lore: land feat/y"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.b.LayerLand(ctx, LandRequest{Key: kz, Subject: "lore: land feat/z"}); err != nil {
			t.Fatal(err)
		}
		if got := main(t, w); got["body"] != "grandchild body" || got["kind"] != "episodic" || got["source_ref"] != "child ref" || got["title"] != "main title" {
			t.Fatalf("after both stacked lands: %v, want the grandchild's body, the child's source_ref, main's kind and title", got)
		}
	})
}

// stackWorld is one stacked-layer setup: the backend, its row generator,
// the row both sides edit, main's newest clock of it and the stacked
// layer's.
type stackWorld struct {
	b   *Backend
	g   *gen
	id  ids.AnyID
	cur hlc.HLC
	// child is the clock of the stacked layer's copy of the row.
	child hlc.HLC
}

// mainEdit writes fields as main's next copy of w's row.
func (w *stackWorld) mainEdit(t *testing.T, fields map[string]string) {
	t.Helper()
	r := w.g.memRow(w.id, w.cur, fields)
	write(t, w.b, store.Change{Row: r, Expect: w.cur})
	w.cur = r.HLC
}
