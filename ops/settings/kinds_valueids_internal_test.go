package settings

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// valueField is a kind_fields row with allowed values names and entries es.
func valueField(t *testing.T, names []string, es []ValueEntry) row.Row {
	t.Helper()
	items := make([]value.Value, len(names))
	for i, n := range names {
		v, err := value.TextOf(n)
		if err != nil {
			t.Fatal(err)
		}
		items[i] = v
	}
	l, err := value.ListOf(value.KindText, items...)
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{fValues: l}
	if es != nil {
		v, verr := valueEntriesValue(es)
		if verr != nil {
			t.Fatal(verr)
		}
		f[fValueIDs] = v
	}
	return row.Row{Fields: f}
}

// TestSyncValueIDs (later 38/33, OQ-AV): a rename keeps the value's id and
// label, a new name gets a fresh val_ id, a removed name loses its entry,
// labels apply last (trimmed) and must name an allowed value; a malformed
// label pair is refused.
func TestSyncValueIDs(t *testing.T) {
	t.Parallel()
	prev := []ValueEntry{{ID: "val_a", Name: "prod", Label: "Production"}, {ID: "val_b", Name: "dev"}, {ID: "val_c", Name: "old"}}
	got, err := syncValueIDs("host.env", prev, []string{"production", "dev", "stag"}, []valueRename{{old: "prod", new: "production"}}, []string{"stag= Staging "})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != (ValueEntry{ID: "val_a", Name: "production", Label: "Production"}) || got[1] != prev[1] ||
		got[2].Name != "stag" || !strings.HasPrefix(got[2].ID, "val_") || got[2].ID == "val_c" || got[2].Label != "Staging" {
		t.Fatalf("sync: %+v", got)
	}
	for _, bad := range []string{"nolabel", "=x", "ghost=X"} {
		if _, err := syncValueIDs("host.env", prev, []string{"prod"}, nil, []string{bad}); !errs.Has(err, errs.InvalidInput) {
			t.Errorf("label %q: %v, want E_INVALID_INPUT", bad, err)
		}
	}
}

// TestNormaliseValue: an allowed name stays, a label (any case) or a
// val_ id maps to the name, anything else comes back unchanged.
func TestNormaliseValue(t *testing.T) {
	t.Parallel()
	f := valueField(t, []string{"prod", "dev"}, []ValueEntry{{ID: "val_a", Name: "prod", Label: "Production"}, {ID: "val_b", Name: "dev"}})
	for in, want := range map[string]string{"prod": "prod", "Production": "prod", "PRODUCTION": "prod", "val_b": "dev", "staging": "staging", "": ""} {
		if got := normaliseValue(f, in); got != want {
			t.Errorf("normaliseValue(%q) = %q, want %q", in, got, want)
		}
	}
	if got := normaliseValue(valueField(t, []string{"prod"}, nil), "val_a"); got != "val_a" {
		t.Errorf("no entries: %q", got)
	}
}

// TestValueIDsRoundTrip: valueIDsOf and namesOf map a string and a list
// both ways; a name or id without an entry, a field without entries, or
// an empty list is "not mapped" (false), stored and read as given.
func TestValueIDsRoundTrip(t *testing.T) {
	t.Parallel()
	f := valueField(t, []string{"prod", "dev"}, []ValueEntry{{ID: "val_a", Name: "prod"}, {ID: "val_b", Name: "dev"}})
	cases := []struct{ names, ids string }{
		{`"prod"`, `"val_a"`},
		{`["dev","prod"]`, `["val_b","val_a"]`},
	}
	for _, c := range cases {
		got, ok := valueIDsOf(f, json.RawMessage(c.names))
		if !ok || string(got) != c.ids {
			t.Errorf("valueIDsOf(%s) = %s %v, want %s", c.names, got, ok, c.ids)
		}
		back, ok := namesOf(f, got)
		if !ok || string(back) != c.names {
			t.Errorf("namesOf(%s) = %s %v, want %s", got, back, ok, c.names)
		}
	}
	for _, in := range []string{`"ghost"`, `["prod","ghost"]`, `[]`, `5`} {
		if _, ok := valueIDsOf(f, json.RawMessage(in)); ok {
			t.Errorf("valueIDsOf(%s) mapped", in)
		}
	}
	for _, in := range []string{`"val_zz"`, `["val_a","val_zz"]`, `5`} {
		if _, ok := namesOf(f, json.RawMessage(in)); ok {
			t.Errorf("namesOf(%s) mapped", in)
		}
	}
	if _, ok := valueIDsOf(valueField(t, []string{"prod"}, nil), json.RawMessage(`"prod"`)); ok {
		t.Error("a field without entries mapped a name")
	}
}

// TestFoldValueIDs: an extension adds its values with their own ids; a
// name the base already has keeps the base's entry; no extension entries
// leave the row as it is.
func TestFoldValueIDs(t *testing.T) {
	t.Parallel()
	base := valueField(t, []string{"prod"}, []ValueEntry{{ID: "val_a", Name: "prod"}})
	ext := valueField(t, []string{"prod", "edge"}, []ValueEntry{{ID: "val_x", Name: "prod"}, {ID: "val_e", Name: "edge", Label: "Edge"}})
	got, err := foldValueIDs(base, ext)
	if err != nil {
		t.Fatal(err)
	}
	want := []ValueEntry{{ID: "val_a", Name: "prod"}, {ID: "val_e", Name: "edge", Label: "Edge"}}
	if es := valueEntries(got); !slices.Equal(es, want) {
		t.Fatalf("fold: %+v", es)
	}
	same, err := foldValueIDs(base, valueField(t, []string{"z"}, nil))
	if err != nil || !slices.Equal(valueEntries(same), []ValueEntry{{ID: "val_a", Name: "prod"}}) {
		t.Fatalf("no extension entries: %+v %v", valueEntries(same), err)
	}
}
