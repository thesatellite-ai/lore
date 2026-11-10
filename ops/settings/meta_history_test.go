package settings

import (
	"encoding/json"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/ids"
)

// OV62: meta history lines between two versions: set, changed, unset; a
// key hidden by any version or its key row today is masked on both sides,
// even where the old version did not say sensitive (OV49-H2).
func TestMetaDiff(t *testing.T) {
	t.Parallel()
	newKey := func() ids.ID[ids.MetaKey] {
		k, err := ids.New[ids.MetaKey]()
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	cost, jira, pass := newKey(), newKey(), newKey()
	prev := map[ids.ID[ids.MetaKey]]meta.Entry{
		cost: {Name: "cost_hours", V: []byte("3.5")},
		jira: {Name: "jira", V: []byte(`"HUB-1"`)},
		pass: {Name: "db_pass", V: []byte(`"hunter2"`)},
	}
	next := map[ids.ID[ids.MetaKey]]meta.Entry{
		cost: {Name: "cost_hours", V: []byte("4.5")},
		pass: {Name: "db_pass", V: []byte(`"hunter3"`)},
	}
	got := metaDiff(prev, next, map[ids.ID[ids.MetaKey]]bool{pass: true})
	if len(got) != 3 {
		t.Fatalf("lines %+v", got)
	}
	if got[0].Key != "cost_hours" || got[0].Change != metaHistChanged || got[0].From != "3.5" || got[0].To != "4.5" {
		t.Fatalf("cost %+v", got[0])
	}
	if got[1].Key != "db_pass" || got[1].From != "•••" || got[1].To != "•••" {
		t.Fatalf("sensitive value shown: %+v", got[1])
	}
	if got[2].Key != "jira" || got[2].Change != metaHistUnset || got[2].From != "HUB-1" {
		t.Fatalf("jira %+v", got[2])
	}
	first := metaDiff(map[ids.ID[ids.MetaKey]]meta.Entry{}, prev, nil)
	if len(first) != 3 || first[0].Change != metaHistSet {
		t.Fatalf("first version %+v", first)
	}
}

// OV62: --git resource diffs mask a field the kind no longer defines
// (its flags are unknown); the row's own list (unknownHidden false) does not.
func TestFieldChangesUnknownMasked(t *testing.T) {
	t.Parallel()
	d := kindDef{byID: map[string]located{}, byName: map[string]located{}}
	prev := resValues{vals: map[string]json.RawMessage{"fld_gone": json.RawMessage(`"old"`)}, names: map[string]string{"fld_gone": "token"}}
	next := resValues{vals: map[string]json.RawMessage{"fld_gone": json.RawMessage(`"new"`)}, names: map[string]string{"fld_gone": "token"}}
	if c := fieldChanges(d, prev, next, true); len(c) != 1 || c[0].From != maskedValue || c[0].To != maskedValue || c[0].Field != "token" {
		t.Fatalf("unknown field shown: %+v", c)
	}
	if c := fieldChanges(d, prev, next, false); len(c) != 1 || c[0].To == maskedValue {
		t.Fatalf("own list masked an unknown field: %+v", c)
	}
}
