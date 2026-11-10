package metalist

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeKeys is a Lister holding one store's key registry (meta_keys,
// meta_values); every other table is empty.
type fakeKeys struct{ keys, values []row.Row }

func (f fakeKeys) Stores() []store.Source { return []store.Source{store.SourceRepo} }

func (f fakeKeys) List(_ context.Context, _ store.Source, t row.Table) ([]row.Row, error) {
	switch t {
	case schema.TableMetaKeys:
		return f.keys, nil
	case schema.TableMetaValues:
		return f.values, nil
	}
	return nil, nil
}

// regKey is one text key of the fake registry.
type regKey struct {
	id        ids.ID[ids.MetaKey]
	name      string
	sensitive bool
}

func newKey(t *testing.T, name string, sensitive bool) regKey {
	t.Helper()
	id, err := ids.New[ids.MetaKey]()
	if err != nil {
		t.Fatal(err)
	}
	return regKey{id: id, name: name, sensitive: sensitive}
}

func txt(t *testing.T, s string) value.Text {
	t.Helper()
	v, err := value.TextOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// registry is the fake Lister over keys.
func registry(t *testing.T, keys ...regKey) fakeKeys {
	t.Helper()
	var f fakeKeys
	for _, k := range keys {
		f.keys = append(f.keys, row.Row{Table: schema.TableMetaKeys, ID: k.id.Any(), Fields: map[string]value.Value{
			fName: txt(t, k.name), fType: txt(t, "text"), fSensitive: value.BoolOf(k.sensitive),
		}})
	}
	return f
}

// listed is a memory row holding vals (key → JSON value); archived sets
// archived_at.
func listed(t *testing.T, archived bool, vals map[regKey]string) row.Row {
	t.Helper()
	id, err := ids.New[ids.Memory]()
	if err != nil {
		t.Fatal(err)
	}
	r := row.Row{Table: schema.TableMemories, ID: id.Any(), Fields: map[string]value.Value{}, Meta: map[ids.ID[ids.MetaKey]]value.JSON{}}
	if archived {
		r.Fields[fArchivedAt] = txt(t, "2026-11-01T00:00:00Z")
	}
	for k, v := range vals {
		// The entry's own sensitive flag stays off: masking must come
		// from the registry (3.20), the stronger test.
		j, err := meta.Entry{Name: k.name, V: []byte(v)}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		r.Meta[k.id] = j
	}
	return r
}

// TestSelectArchived (later 481, METADATA 3.19, OV30-2): every list hides
// archived rows unless --archived, with and without a --meta filter, and
// the order of the kept rows is the input order.
func TestSelectArchived(t *testing.T) {
	ctx := context.Background()
	client := newKey(t, "client", false)
	env := registry(t, client)
	rows := []row.Row{
		listed(t, false, map[regKey]string{client: `"acme"`}),
		listed(t, true, map[regKey]string{client: `"acme"`}),
		listed(t, false, map[regKey]string{client: `"beta"`}),
		listed(t, true, nil),
	}
	cases := []struct {
		name string
		f    Flags
		want []int
	}{
		{"no flag", Flags{}, []int{0, 2}},
		{"archived", Flags{Archived: true}, []int{0, 1, 2, 3}},
		{"filter", Flags{Meta: []string{"client=acme"}}, []int{0}},
		{"filter archived", Flags{Meta: []string{"client=acme"}, Archived: true}, []int{0, 1}},
		{"has key archived", Flags{Meta: []string{"client?"}, Archived: true}, []int{0, 1, 2}},
		{"lacks key", Flags{Meta: []string{"client!?"}}, nil},
		{"lacks key archived", Flags{Meta: []string{"client!?"}, Archived: true}, []int{3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Select(ctx, env, c.f, rows)
			if err != nil {
				t.Fatal(err)
			}
			if !equalInts(res.Order, c.want) {
				t.Fatalf("order %v, want %v (notes %q)", res.Order, c.want, res.Notes)
			}
		})
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSelectColumnLines (later 26, METADATA 3.7, FA-META-5): each listed
// row gets one "meta:" line with every filtered key in filter order (a
// sort key too); a row without a key shows "-"; a key the registry marks
// sensitive is masked even when the entry is not; a filter by key id names
// the column by that id; rows filtered out get no line; no --meta and no
// --sort means no line at all.
func TestSelectColumnLines(t *testing.T) {
	ctx := context.Background()
	client, tier, vpn := newKey(t, "client", false), newKey(t, "tier", false), newKey(t, "vpn_host", true)
	env := registry(t, client, tier, vpn)
	full := listed(t, false, map[regKey]string{client: `"acme"`, tier: `"gold"`, vpn: `"probe-entry-1"`})
	bare := listed(t, false, map[regKey]string{client: `"acme"`})
	other := listed(t, false, map[regKey]string{client: `"beta"`, tier: `"gold"`})
	rows := []row.Row{full, bare, other}
	id := func(r row.Row) string { return r.ID.String() }
	cases := []struct {
		name string
		f    Flags
		want []string
	}{
		{"no filter", Flags{}, nil},
		{"one key", Flags{Meta: []string{"client=acme"}}, []string{
			"meta: " + id(full) + "  client=acme", "meta: " + id(bare) + "  client=acme"}},
		{"two keys", Flags{Meta: []string{"client=acme", "tier?"}}, []string{
			"meta: " + id(full) + "  client=acme  tier=gold"}},
		{"missing key shows dash", Flags{Meta: []string{"client=acme"}, Sort: "tier"}, []string{
			"meta: " + id(full) + "  client=acme  tier=gold", "meta: " + id(bare) + "  client=acme  tier=-"}},
		{"sort column", Flags{Meta: []string{"tier=gold"}, Sort: "meta.client:desc"}, []string{
			"meta: " + id(other) + "  tier=gold  client=beta", "meta: " + id(full) + "  tier=gold  client=acme"}},
		{"sensitive masked", Flags{Meta: []string{"vpn_host?"}}, []string{
			"meta: " + id(full) + "  vpn_host=•••"}},
		{"by key id", Flags{Meta: []string{tier.id.String() + "=gold"}}, []string{
			"meta: " + id(full) + "  " + tier.id.String() + "=gold", "meta: " + id(other) + "  " + tier.id.String() + "=gold"}},
		{"sort only, absent shows dash", Flags{Sort: "tier"}, []string{
			"meta: " + id(full) + "  tier=gold", "meta: " + id(other) + "  tier=gold", "meta: " + id(bare) + "  tier=-"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Select(ctx, env, c.f, rows)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, n := range res.Notes {
				if strings.HasPrefix(n, "meta: ") {
					got = append(got, n)
				}
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Fatalf("meta lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
			if strings.Contains(strings.Join(res.Notes, "\n"), "probe-entry-1") {
				t.Fatalf("a sensitive value leaked: %q", res.Notes)
			}
		})
	}
}

// TestColumnKey (later 26): the column a filter's key part names: scope
// prefix and type suffix dropped, a dotted path shows its top key, a key id
// (with or without a path) stays the id, a "$" json path is cut.
func TestColumnKey(t *testing.T) {
	for in, want := range map[string]string{
		"client":                               "client",
		"project:client":                       "client",
		"client:text":                          "client",
		"project:deploy.env:text":              "deploy",
		"deploy.env":                           "deploy",
		"settings$.a.b":                        "settings",
		"mky_01a1a59b891c70b789144cd3e711fe01": "mky_01a1a59b891c70b789144cd3e711fe01",
		"mky_01a1a59b891c70b789144cd3e711fe01.env": "mky_01a1a59b891c70b789144cd3e711fe01",
		"org:mky_01a1a59b891c70b789144cd3e711fe01": "mky_01a1a59b891c70b789144cd3e711fe01",
	} {
		if got := columnKey(in); got != want {
			t.Errorf("columnKey(%q) = %q, want %q", in, got, want)
		}
	}
}
