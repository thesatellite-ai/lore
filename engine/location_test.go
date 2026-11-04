package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestLocateDedupesOneLocation: two attachment names over one store folder
// (the same Location: an org store attached at its linked folder as
// "northwind" and the same folder as "northwind/globex") are one place, so
// a row held there is found once, through the first name in order, and is
// never E_AMBIGUOUS_ID with itself. Different or empty Locations keep the
// ambiguity check (two real copies must still be reported), a named store
// still answers by its own name, and Move never treats another name of the
// place it moves to as the place to move from (which would tombstone the
// row it just wrote).
func TestLocateDedupesOneLocation(t *testing.T) {
	const (
		loc     = "file:///s?folder=globex"
		otherLc = "file:///s?folder=acme"
		first   = store.Source("northwind")
		second  = store.Source("northwind/globex")
	)
	cases := []struct {
		name      string
		locA      string
		locB      string
		ambiguous bool
	}{
		{"same location", loc, loc, false},
		{"different locations", loc, otherLc, true},
		{"both empty (own places)", "", "", true},
		{"one empty", loc, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			data := memstore.NewData()
			b := memstore.NewData() // a second copy of the data when locations differ
			backB := data
			if c.locA != c.locB || c.locA == "" {
				backB = b
			}
			f := setup(t, WithStore(Attached{Name: first, Backend: data.Open(store.OpenOptions{}), Location: c.locA}),
				WithStore(Attached{Name: second, Backend: backB.Open(store.OpenOptions{}), Location: c.locB}))
			w, err := f.eng.Create(ctx, first, schema.TableProjects, map[string]value.Value{"name": txt("globex")})
			if err != nil {
				t.Fatal(err)
			}
			id := w.Row.ID
			if backB != data {
				// the second name is really another place holding a copy
				sr, gerr := data.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, id)
				if gerr != nil {
					t.Fatal(gerr)
				}
				if werr := backB.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); werr != nil {
					t.Fatal(werr)
				}
			}
			src, _, err := f.eng.Get(ctx, Ref{ID: id})
			if c.ambiguous {
				if !errs.Has(err, errs.AmbiguousID) {
					t.Fatalf("err = %v (src %s), want E_AMBIGUOUS_ID", err, src)
				}
			} else if err != nil || src != first {
				t.Fatalf("got %s %v, want the first name %s", src, err, first)
			}
			for _, name := range []store.Source{first, second} {
				if got, _, err := f.eng.Get(ctx, Ref{Store: name, ID: id}); err != nil || got != name {
					t.Fatalf("named %s: %s %v", name, got, err)
				}
			}
			if !c.ambiguous {
				// moving to the second name of the same place: no other place holds it
				if _, err := f.eng.Move(ctx, Ref{ID: id}, second); !errs.Has(err, errs.NotFound) {
					t.Fatalf("move into another name of the same place: %v, want E_NOT_FOUND", err)
				}
				if _, err := data.Open(store.OpenOptions{}).Get(ctx, schema.TableProjects, id); err != nil {
					t.Fatalf("the row must still be there after the refused move: %v", err)
				}
			}
		})
	}
}
