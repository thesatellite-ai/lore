package content

import (
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
)

// TestTagIDsBothForms: a list field reads the same items whether it was
// built in memory (value.List) or decoded from a row file as JSON; absent
// and null are empty.
func TestTagIDsBothForms(t *testing.T) {
	a, b := "tag_7aab3ed507407f139f49d934fee0e558", "tag_d148b702c9a3758ca589aef5bc129f30"
	items := make([]value.Value, 0, 2)
	for _, s := range []string{a, b} {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, v)
	}
	list, err := value.ListOf(value.KindText, items...)
	if err != nil {
		t.Fatal(err)
	}
	js, err := value.JSONOf([]byte(`["` + a + `","` + b + `"]`))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]value.Value{"list": list, "json": js} {
		got := TagIDs(row.Row{Fields: map[string]value.Value{schema.FieldTagIDs: v}})
		if len(got) != 2 || got[0] != a || got[1] != b {
			t.Errorf("%s form: %v", name, got)
		}
	}
	for name, f := range map[string]map[string]value.Value{"absent": {}, "null": {schema.FieldTagIDs: value.Null{}}} {
		if got := TagIDs(row.Row{Fields: f}); len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
}
