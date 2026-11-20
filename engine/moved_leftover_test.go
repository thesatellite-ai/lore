package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// putCopy writes one memory copy of id into the store in, at wall, moved
// there from movedFrom when that is set (the `moves` entry a move adds).
func putCopy(t *testing.T, w *f15World, id ids.AnyID, in store.Source, wall time.Time, movedFrom string) {
	t.Helper()
	h := hlc.Next(hlc.HLC{}, wall, testActor)
	r := row.Row{Table: schema.TableMemories, ID: id, HLC: h, Fields: map[string]value.Value{"body": txt("the cache key includes the region")}}
	if movedFrom != "" {
		o, err := withMove(value.JSON{}, h, testActor.String(), movedFrom, string(in))
		if err != nil {
			t.Fatal(err)
		}
		r.Origin = o
	}
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.data[in].Open(store.OpenOptions{}).Write(context.Background(), []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
}

// TestMoveLeftoverYields (devco:D-749): a row a move took from the repo to
// me, whose tombstone has not reached this checkout (a clone still on the
// lore 0.1.17 format), is read by its bare id from me and listed once, at
// me; the repo's leftover copy is not listed. A leftover edited after the
// move stays visible and the id stays E_AMBIGUOUS_ID; in a 0.1.17-format
// repo its hint names commands that run there (no row move, no lore fix).
func TestMoveLeftoverYields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t0 := time.Now().Add(-time.Hour)
	cases := []struct {
		name      string
		repoWall  time.Time // the leftover's write
		movedFrom func(*f15World) string
		want      store.Source // "" = E_AMBIGUOUS_ID
		repoLists bool
	}{
		{"moved from the repo: the moved copy answers", t0, func(w *f15World) string { return w.eng.placeOf(store.SourceRepo) }, personal, false},
		{"moved from the repo by name", t0, func(*f15World) string { return string(store.SourceRepo) }, personal, false},
		{"leftover edited after the move", t0.Add(2 * time.Hour), func(w *f15World) string { return w.eng.placeOf(store.SourceRepo) }, "", true},
		{"moved from another store: no leftover here", t0, func(*f15World) string { return string(agency) }, "", true},
		{"no move record", t0, func(*f15World) string { return "" }, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newF15World(t, false)
			def, _ := w.eng.Registry().Table(schema.TableMemories)
			id := mustID(t, def.Prefix)
			putCopy(t, w, id, store.SourceRepo, c.repoWall, "")
			putCopy(t, w, id, personal, t0.Add(time.Minute), c.movedFrom(w))

			src, _, err := w.eng.Get(ctx, Ref{ID: id})
			if c.want == "" {
				if !errs.Has(err, errs.AmbiguousID) {
					t.Fatalf("Get = %s %v, want E_AMBIGUOUS_ID", src, err)
				}
			} else if err != nil || src != c.want {
				t.Fatalf("Get = %s %v, want %s", src, err, c.want)
			}
			rows, err := w.eng.List(ctx, store.SourceRepo, schema.TableMemories)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(rows) == 1; got != c.repoLists {
				t.Fatalf("repo lists the copy = %v, want %v", got, c.repoLists)
			}
			if mine, _ := w.eng.List(ctx, personal, schema.TableMemories); len(mine) != 1 {
				t.Fatalf("me lists %d rows, want 1", len(mine))
			}
		})
	}

	t.Run("0.1.17-format repo: the hint names what runs there", func(t *testing.T) {
		t.Parallel()
		w := newF15World(t, false)
		w.eng.repoFormatOld = true
		def, _ := w.eng.Registry().Table(schema.TableMemories)
		id := mustID(t, def.Prefix)
		putCopy(t, w, id, store.SourceRepo, t0.Add(2*time.Hour), "")
		putCopy(t, w, id, personal, t0, w.eng.placeOf(store.SourceRepo))
		_, _, err := w.eng.Get(ctx, Ref{ID: id})
		if !errs.Has(err, errs.AmbiguousID) {
			t.Fatalf("Get = %v, want E_AMBIGUOUS_ID", err)
		}
		var e *errs.Error
		if !errors.As(err, &e) {
			t.Fatalf("not an errs.Error: %v", err)
		}
		hint := e.Hint
		for _, refused := range []string{"lore row move", "lore fix"} {
			if strings.Contains(hint, refused) {
				t.Errorf("hint names %q, refused in a 0.1.17-format repo: %s", refused, hint)
			}
		}
		if !strings.Contains(hint, "`lore migrate run`") || !strings.Contains(hint, "--store "+string(personal)) {
			t.Errorf("hint = %s, want lore migrate run and a read of each copy", hint)
		}
	})
}
