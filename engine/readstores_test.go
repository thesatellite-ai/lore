package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestOnePerLocation pins the decision behind ReadStores: the first name of
// each Location is kept in order, every later name of it is dropped, and a
// name with no Location is always its own place (issue #1).
func TestOnePerLocation(t *testing.T) {
	t.Parallel()
	const (
		stag  = "git+https://x/notes?branch=staging"
		other = "git+https://x/notes?branch=main"
	)
	cases := []struct {
		name  string
		names []store.Source
		loc   map[store.Source]string
		want  []store.Source
	}{
		{"no stores", nil, nil, []store.Source{}},
		{"all own places", []store.Source{"repo", "a", "b"}, nil, []store.Source{"repo", "a", "b"}},
		{"two names one place", []store.Source{"repo", "stag", "stag2"}, map[store.Source]string{"stag": stag, "stag2": stag}, []store.Source{"repo", "stag"}},
		{"later name first in order wins", []store.Source{"stag2", "stag"}, map[store.Source]string{"stag": stag, "stag2": stag}, []store.Source{"stag2"}},
		{"three names one place", []store.Source{"a", "b", "c"}, map[store.Source]string{"a": stag, "b": stag, "c": stag}, []store.Source{"a"}},
		{"different places kept", []store.Source{"a", "b"}, map[store.Source]string{"a": stag, "b": other}, []store.Source{"a", "b"}},
		{"empty never dedupes", []store.Source{"a", "b"}, map[store.Source]string{"a": "", "b": ""}, []store.Source{"a", "b"}},
		{"interleaved", []store.Source{"repo", "s1", "m", "s2", "m2"}, map[store.Source]string{"s1": stag, "s2": stag, "m": other, "m2": other}, []store.Source{"repo", "s1", "m"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := onePerLocation(c.names, func(n store.Source) string { return c.loc[n] })
			if !slices.Equal(got, c.want) {
				t.Fatalf("onePerLocation(%v) = %v, want %v", c.names, got, c.want)
			}
		})
	}
}

// plainLister lists stores and has no ReadStores upgrade.
type plainLister struct{ names []store.Source }

func (p plainLister) Stores() []store.Source { return p.names }

// TestReadStoresOf: an Env with the upgrade answers ReadStores (one name per
// place); one without answers Stores() unchanged.
func TestReadStoresOf(t *testing.T) {
	t.Parallel()
	const loc = "file:///s?branch=staging"
	data := memstore.NewData()
	f := setup(t,
		WithStore(Attached{Name: "stag", Backend: data.Open(store.OpenOptions{}), Location: loc}),
		WithStore(Attached{Name: "stag2", Backend: data.Open(store.OpenOptions{}), Location: loc}))
	all := f.eng.Stores()
	if !slices.Contains(all, "stag2") {
		t.Fatalf("Stores() = %v, want stag2 attached", all)
	}
	read := ReadStoresOf(f.eng)
	if slices.Contains(read, "stag2") || !slices.Contains(read, "stag") || len(read) != len(all)-1 {
		t.Fatalf("ReadStoresOf(engine) = %v, want %v minus stag2", read, all)
	}
	p := plainLister{names: []store.Source{"a", "b"}}
	if got := ReadStoresOf(p); !slices.Equal(got, p.names) {
		t.Fatalf("ReadStoresOf(plain) = %v, want %v", got, p.names)
	}
}

// TestReadStoresRowOnce: a row written through either name of a place
// attached twice is listed once by a walk over ReadStores and found once by
// FindByKey (which walks the same list), through the first name.
func TestReadStoresRowOnce(t *testing.T) {
	t.Parallel()
	const loc = "file:///s?branch=staging"
	for _, to := range []store.Source{"stag", "stag2"} {
		t.Run(string(to), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			data := memstore.NewData()
			f := setup(t,
				WithStore(Attached{Name: "stag", Backend: data.Open(store.OpenOptions{}), Location: loc}),
				WithStore(Attached{Name: "stag2", Backend: data.Open(store.OpenOptions{}), Location: loc}))
			if _, err := f.eng.Create(ctx, to, schema.TableProjects, map[string]value.Value{"name": txt("globex")}); err != nil {
				t.Fatal(err)
			}
			var where []store.Source
			for _, src := range f.eng.ReadStores() {
				rows, err := f.eng.List(ctx, src, schema.TableProjects)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					if r.Fields["name"].String() == "globex" {
						where = append(where, src)
					}
				}
			}
			if !slices.Equal(where, []store.Source{"stag"}) {
				t.Fatalf("list over ReadStores: globex in %v, want once in stag", where)
			}
			found, err := FindByKey(ctx, f.eng, schema.TableProjects, map[string]string{"name": "globex"})
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 || found[0].Store != "stag" {
				t.Fatalf("FindByKey: %+v, want once in stag", found)
			}
		})
	}
}

// namedLister is a StoreLister with a fixed store list.
type namedLister []store.Source

func (n namedLister) Stores() []store.Source { return n }

// TestNamedSourcesTakesFolders (E-14): `--store acme` reads acme and every
// acme/<folder> attached (the org store's rows live in its folders), as
// `--store me` reads me/<org>; a folder name reads that folder alone, and
// a store whose name only starts the same (acme2) is another store.
func TestNamedSourcesTakesFolders(t *testing.T) {
	t.Parallel()
	l := namedLister{"repo", "me", "me/acme", "acme", "acme/org", "acme/site", "acme2", "acme2/org"}
	for _, c := range []struct {
		name store.Source
		want []store.Source
	}{
		{"acme", []store.Source{"acme", "acme/org", "acme/site"}},
		{"me", []store.Source{"me", "me/acme"}},
		{"acme/org", []store.Source{"acme/org"}},
		{"repo", []store.Source{"repo"}},
		{"gone", []store.Source{"gone"}},
	} {
		if got := NamedSources(l, c.name); !slices.Equal(got, c.want) {
			t.Errorf("NamedSources(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
