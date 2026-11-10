package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestLocateCopiesMatrix: which copy an unqualified ref (no store named)
// means when several stores hold the id. A live copy wins over
// tombstones (a move leaves one where the row was), one live copy is the
// answer, two live copies are E_AMBIGUOUS_ID naming only the live
// stores, and with no live copy the nearest tombstone answers (deleted).
func TestLocateCopiesMatrix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	type copyOf struct {
		in      store.Source
		deleted bool
	}
	cases := []struct {
		name      string
		copies    []copyOf
		want      store.Source // "" = ambiguous
		deleted   bool
		ambiguous []store.Source // stores the ambiguity names
	}{
		{"live only", []copyOf{{agency, false}}, agency, false, nil},
		{"tombstone only", []copyOf{{agency, true}}, agency, true, nil},
		{"live near, tombstone far", []copyOf{{store.SourceRepo, false}, {personal, true}}, store.SourceRepo, false, nil},
		{"tombstone near, live far", []copyOf{{store.SourceRepo, true}, {personal, false}}, personal, false, nil},
		{"tombstone, live, tombstone", []copyOf{{store.SourceRepo, true}, {agency, false}, {personal, true}}, agency, false, nil},
		{"two tombstones: the nearest", []copyOf{{agency, true}, {personal, true}}, agency, true, nil},
		{"two live", []copyOf{{store.SourceRepo, false}, {agency, false}}, "", false, []store.Source{store.SourceRepo, agency}},
		{"two live and a tombstone", []copyOf{{store.SourceRepo, false}, {agency, true}, {personal, false}}, "", false, []store.Source{store.SourceRepo, personal}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newF15World(t, false)
			tdef, ok := w.eng.Registry().Table(schema.TableTags)
			if !ok {
				t.Fatal("no tags table")
			}
			id := mustID(t, tdef.Prefix)
			for _, cp := range c.copies {
				w.place(t, schema.TableTags, id, "", cp.deleted, cp.in)
			}
			src, got, err := w.eng.Get(ctx, Ref{ID: id})
			if c.want == "" {
				if !errs.Has(err, errs.AmbiguousID) {
					t.Fatalf("got %s %v, want E_AMBIGUOUS_ID", src, err)
				}
				for _, n := range c.ambiguous {
					if !strings.Contains(err.Error(), string(n)) {
						t.Fatalf("%v does not name %s", err, n)
					}
				}
				for _, cp := range c.copies {
					if cp.deleted && strings.Contains(err.Error(), " "+string(cp.in)) {
						t.Fatalf("%v names the tombstone store %s", err, cp.in)
					}
				}
				return
			}
			if err != nil || src != c.want || got.Deleted != c.deleted {
				t.Fatalf("got %s deleted=%v %v, want %s deleted=%v", src, got.Deleted, err, c.want, c.deleted)
			}
		})
	}
}

// TestMoveChainByBareID (BATCH 2, t4b TestRecipe10): a row moved repo →
// agency → personal keeps answering by its bare id: each move leaves a
// tombstone behind, and Get, Update and the next Move find the one live
// copy instead of answering E_AMBIGUOUS_ID.
func TestMoveChainByBareID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newF15World(t, false)
	prj := w.project(t, store.SourceRepo, "chain")
	m := w.memIn(t, store.SourceRepo, prj, "moving note")
	ref := Ref{ID: m.ID}
	for _, to := range []store.Source{agency, personal} {
		if _, err := w.eng.Move(ctx, ref, to); err != nil {
			t.Fatalf("move to %s: %v", to, err)
		}
		src, got, err := w.eng.Get(ctx, ref)
		if err != nil || src != to || got.Deleted {
			t.Fatalf("after the move to %s: %s %+v %v", to, src, got.Deleted, err)
		}
	}
	upd, err := w.eng.Update(ctx, ref, map[string]value.Value{"body": txt("edited after two moves")})
	if err != nil || upd.Row.Fields["body"].String() != "edited after two moves" {
		t.Fatalf("update by bare id: %+v %v", upd.Row.Fields, err)
	}
	// the stores it left still hold tombstones, and a named store reads one
	if _, got, err := w.eng.Get(ctx, Ref{Store: agency, ID: m.ID}); err != nil || !got.Deleted {
		t.Fatalf("agency copy: %+v %v, want a tombstone", got.Deleted, err)
	}
	// a delete by bare id removes the live copy; then only tombstones remain
	if _, err := w.eng.Delete(ctx, ref); err != nil {
		t.Fatalf("delete by bare id: %v", err)
	}
	if src, got, err := w.eng.Get(ctx, ref); err != nil || !got.Deleted || src != store.SourceRepo {
		t.Fatalf("after delete: %s %v %v, want the nearest tombstone (repo)", src, got.Deleted, err)
	}
}

// TestLocatePurgedCopies: a purged id's resurrected file counts as a
// tombstone, so a live copy in another store still answers; with only the
// purged copy the ref reads deleted and a write says E_PURGED.
func TestLocatePurgedCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := newPurgedTag(t)
	p.resurrect(t)
	copyToRepo(t, p)
	src, got, err := p.f.eng.Get(ctx, Ref{ID: p.ref.ID})
	if err != nil || src != store.SourceRepo || got.Deleted {
		t.Fatalf("live repo copy beside a resurrected purged file: %s %v %v", src, got.Deleted, err)
	}
	q := newPurgedTag(t)
	q.resurrect(t)
	if _, got, err := q.f.eng.Get(ctx, Ref{ID: q.ref.ID}); err != nil || !got.Deleted {
		t.Fatalf("only the purged copy: %v %v, want deleted", got.Deleted, err)
	}
	if _, err := q.f.eng.Update(ctx, Ref{ID: q.ref.ID}, map[string]value.Value{"color": txt("red")}); !errs.Has(err, errs.Purged) {
		t.Fatalf("update by bare id of the purged copy: %v, want E_PURGED", err)
	}
}
