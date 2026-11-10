package engine

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
)

// TestValueNames: the refreshed v_name of a one-value and a list value
// takes each value id's current name from the key's allowed values; a
// value id the registry does not hold, or a value that is not an id
// string, leaves the entry untouched (ok=false), never half refreshed.
func TestValueNames(t *testing.T) {
	t.Parallel()
	newVal := func() ids.AnyID {
		id, err := ids.New[ids.MetaValue]()
		if err != nil {
			t.Fatal(err)
		}
		return id.Any()
	}
	asap, low, gone := newVal(), newVal(), newVal()
	d := keyDef{name: "priority_band", values: []valueDef{{id: asap, name: "asap"}, {id: low, name: "low", label: "Low"}}}
	list := func(items ...ids.AnyID) []byte {
		raw := [][]byte{}
		for _, it := range items {
			raw = append(raw, canon.String(it.String()))
		}
		b, err := canon.Compact(append(append([]byte("["), joinRaw(raw)...), ']'))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, c := range []struct {
		name string
		v    []byte
		want string
		ok   bool
	}{
		{"one value", canon.String(asap.String()), `"asap"`, true},
		{"labelled value keeps its name", canon.String(low.String()), `"low"`, true},
		{"list", list(low, asap), `["low","asap"]`, true},
		{"empty list", []byte(`[]`), `[]`, true},
		{"unknown id", canon.String(gone.String()), "", false},
		{"list with an unknown id", list(asap, gone), "", false},
		{"not a string", []byte(`3`), "", false},
	} {
		got, ok := valueNames(d, c.v)
		if ok != c.ok || (ok && string(got) != c.want) {
			t.Errorf("%s: valueNames = %s, %v; want %s, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
