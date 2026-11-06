package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// resSource is the resources and kind_fields tables in one store.
type resSource struct{ rows map[row.Table][]row.Row }

func (resSource) Tables() []schema.TableDef {
	return []schema.TableDef{
		{Name: schema.TableResources, Synced: true, Fields: []schema.FieldDef{{Name: "name"}, {Name: resFieldsField}}},
		{Name: schema.TableKindFields, Synced: true},
	}
}
func (resSource) Stores() []store.Source { return []store.Source{"repo"} }
func (s resSource) List(_ context.Context, _ store.Source, t row.Table) ([]row.Row, error) {
	return s.rows[t], nil
}

func newID[T ids.Tag](t *testing.T) string {
	t.Helper()
	id, err := ids.New[T]()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// kfRow is a kind_fields row; extends "" = none.
func kfRow(t *testing.T, id string, sensitive bool, extends string, deleted bool) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(id)
	if err != nil {
		t.Fatal(err)
	}
	f := map[string]value.Value{kindFieldSens: value.BoolOf(sensitive)}
	if extends != "" {
		e, terr := value.TextOf(extends)
		if terr != nil {
			t.Fatal(terr)
		}
		f[kindFieldExtend] = e
	}
	return row.Row{Table: schema.TableKindFields, ID: aid, Fields: f, Deleted: deleted}
}

// resRow is a resource row named name whose "fields" JSON is raw.
func resRow(t *testing.T, name, raw string, asText bool) row.Row {
	t.Helper()
	aid, err := ids.ParseAny(newID[ids.Resource](t))
	if err != nil {
		t.Fatal(err)
	}
	n, err := value.TextOf(name)
	if err != nil {
		t.Fatal(err)
	}
	var fv value.Value
	if asText {
		fv, err = value.TextOf(raw)
	} else {
		fv, err = value.JSONOf([]byte(raw))
	}
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{Table: schema.TableResources, ID: aid, Fields: map[string]value.Value{"name": n, resFieldsField: fv}}
}

// TestTUIMasksResourceSensitiveFields (SECURITY): `lore tui` never prints
// a resource kind's sensitive field value: a field marked sensitive, one
// extending a sensitive field, one whose kind field row is missing or
// deleted, and one in an extends cycle are masked on the row screen and
// cannot be found with the /text filter; a plain field stays readable;
// a "fields" value that is not an object of entries is masked whole.
func TestTUIMasksResourceSensitiveFields(t *testing.T) {
	plain, sens, child, gone, cycA, cycB, missing := newID[ids.KindField](t), newID[ids.KindField](t), newID[ids.KindField](t),
		newID[ids.KindField](t), newID[ids.KindField](t), newID[ids.KindField](t), newID[ids.KindField](t)
	kfs := []row.Row{
		kfRow(t, plain, false, "", false),
		kfRow(t, sens, true, "", false),
		kfRow(t, child, false, sens, false),
		kfRow(t, gone, false, "", true),
		kfRow(t, cycA, false, cycB, false),
		kfRow(t, cycB, false, cycA, false),
	}
	cases := []struct {
		name   string
		raw    string
		asText bool
		secret string // must never show ("" = none)
		show   string // must show
	}{
		{"plain field readable", `{"` + plain + `":{"name":"region","v":"eu-west-9"}}`, false, "", "eu-west-9"},
		{"sensitive field", `{"` + sens + `":{"name":"pin","v":"PLAINPIN4242"},"` + plain + `":{"name":"region","v":"eu-west-9"}}`, false, "PLAINPIN4242", "eu-west-9"},
		{"extends a sensitive field", `{"` + child + `":{"name":"pin2","v":"CHILDPIN77"}}`, false, "CHILDPIN77", `"name":"pin2"`},
		{"kind field row missing", `{"` + missing + `":{"name":"x","v":"MISSINGVAL1"}}`, false, "MISSINGVAL1", masked},
		{"kind field row deleted", `{"` + gone + `":{"name":"x","v":"GONEVAL22"}}`, false, "GONEVAL22", masked},
		{"extends cycle", `{"` + cycA + `":{"name":"x","v":"CYCLEVAL33"}}`, false, "CYCLEVAL33", masked},
		{"fields stored as text", `{"` + sens + `":{"name":"pin","v":"TEXTPIN55"}}`, true, "TEXTPIN55", masked},
		{"not an object of entries", `["RAWLIST66"]`, false, "RAWLIST66", "fields: " + masked},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := resSource{rows: map[row.Table][]row.Row{
				schema.TableKindFields: kfs,
				schema.TableResources:  {resRow(t, "h1", c.raw, c.asText)},
			}}
			out, err := run(t, src, Options{Kind: "resource"}, "1", "b", "/"+c.secret, "1", "q")
			if err != nil {
				t.Fatal(err)
			}
			// The filter line echoes what was typed; every other line is the browser's.
			var shown []string
			for _, l := range strings.Split(out, "\n") {
				if !strings.Contains(l, "filter \"") {
					shown = append(shown, l)
				}
			}
			if c.secret != "" && strings.Contains(strings.Join(shown, "\n"), c.secret) {
				t.Fatalf("sensitive value %q printed:\n%s", c.secret, out)
			}
			if c.secret != "" && !strings.Contains(out, "0–0 of 0") {
				t.Fatalf("the filter matched on a masked value:\n%s", out)
			}
			if !strings.Contains(out, c.show) {
				t.Fatalf("want %q on screen:\n%s", c.show, out)
			}
		})
	}
}
