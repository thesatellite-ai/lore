package resmask

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeRows is a Getter over kind field rows keyed by id.
type fakeRows map[string]row.Row

func (f fakeRows) Get(_ context.Context, ref engine.Ref) (store.Source, row.Row, error) {
	r, ok := f[ref.ID.String()]
	if !ok {
		return "", row.Row{}, errs.New(errs.NotFound, "no row")
	}
	return store.SourceRepo, r, nil
}

func fld(t *testing.T, n byte) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny("fld_019a000000007000800000000000000" + string("0123456789abcdef"[n]))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestFieldsMatrix: kind field state x row shape. Sensitive, a field that
// extends a sensitive one, an unknown field, a deleted field row and an
// extends cycle are hidden; a plain field and a plain chain are shown; a
// row that is not a resource, has no fields or unreadable fields is left
// to the caller (ok false); the value is read whether it is JSON or text.
func TestFieldsMatrix(t *testing.T) {
	ctx := context.Background()
	sens, plain, child, plainChild, gone, cycA, cycB, unknown := fld(t, 1), fld(t, 2), fld(t, 3), fld(t, 4), fld(t, 5), fld(t, 6), fld(t, 7), fld(t, 8)
	ref := func(id ids.AnyID) value.Value {
		v, err := value.RefOf(id)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	env := fakeRows{
		sens.String():       {ID: sens, Fields: map[string]value.Value{"sensitive": value.BoolOf(true)}},
		plain.String():      {ID: plain, Fields: map[string]value.Value{"sensitive": value.BoolOf(false)}},
		child.String():      {ID: child, Fields: map[string]value.Value{"extends": ref(sens)}},
		plainChild.String(): {ID: plainChild, Fields: map[string]value.Value{"extends": ref(plain)}},
		gone.String():       {ID: gone, Deleted: true},
		cycA.String():       {ID: cycA, Fields: map[string]value.Value{"extends": ref(cycB)}},
		cycB.String():       {ID: cycB, Fields: map[string]value.Value{"extends": ref(cycA)}},
	}
	cases := []struct {
		name   string
		id     ids.AnyID
		hidden bool
	}{
		{"sensitive", sens, true},
		{"plain", plain, false},
		{"extends sensitive", child, true},
		{"extends plain", plainChild, false},
		{"deleted field row", gone, true},
		{"extends cycle", cycA, true},
		{"unknown field", unknown, true},
	}
	for _, c := range cases {
		doc := `{"` + c.id.String() + `":{"name":"f","v":"probe-value","v_name":"probe-label"}}`
		jv, err := value.JSONOf([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		tv, err := value.TextOf(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []value.Value{jv, tv} {
			r := row.Row{Table: schema.TableResources, Fields: map[string]value.Value{FieldsField: v}}
			got, names, ok := Fields(ctx, env, r, "MASK")
			if !ok {
				t.Fatalf("%s: ok false", c.name)
			}
			leaked := strings.Contains(got, "probe-value") || strings.Contains(got, "probe-label")
			if c.hidden == leaked || c.hidden != (len(names) == 1 && names[0] == "f") || c.hidden != strings.Contains(got, `"v":"MASK"`) {
				t.Errorf("%s (%T): %s names %v", c.name, v, got, names)
			}
		}
	}
	bad, err := value.TextOf("not json")
	if err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]row.Row{
		"memory row": {Table: schema.TableMemories, Fields: map[string]value.Value{FieldsField: bad}},
		"no fields":  {Table: schema.TableResources, Fields: map[string]value.Value{}},
		"unreadable": {Table: schema.TableResources, Fields: map[string]value.Value{FieldsField: bad}},
	} {
		if _, _, ok := Fields(ctx, env, r, "MASK"); ok {
			t.Errorf("%s: ok true", name)
		}
	}
}
