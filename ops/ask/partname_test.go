package ask

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
)

// partFixture is issue #157's store (devco D-949): three code repos named
// widget_*, a product word in their names, and the notes that answer how
// the widget import works and how a widget feed's public URL is built.
func partFixture() data {
	repo := func(id, name, purpose string) resourceRow {
		return resourceRow{ID: id, Place: "kb/org", Kind: "code_repo", Name: name, Fields: []fieldValue{{"project", "widget"}, {"purpose", purpose}}}
	}
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	return data{
		resources: []resourceRow{
			repo("res_api", "widget_api", "The widget backend: API, import workers, public URL and admin"),
			repo("res_web", "widget_web", "The widget admin frontend"),
			repo("res_old", "widget_legacy", "Old widget system, reference only"),
		},
		rows: []textRow{
			{Table: "decisions", ID: "dec_1", Place: "kb/app", Title: "Widget import runs as a two-step workflow", Body: "Widget import: fetch the bulk file in step one, parse and ingest it in step two.", At: at},
			{Table: "memories", ID: "mem_1", Place: "kb/app", Body: "Architecture: the widget import fetches a bulk file, then a parse_and_ingest step writes rows in batches of 500.", At: at},
			{Table: "memories", ID: "mem_2", Place: "kb/app", Body: "The public URL of a widget feed is minted by the server; clients never build it.", At: at},
			{Table: "memories", ID: "mem_3", Place: "kb/app", Body: "The widget_web deploy runs on every merge to main.", At: at},
		},
	}
}

// TestPartOfNameRowsAnswer (devco D-949, public issue #157): a question
// that names resources by one part of their name (widget of widget_api)
// is answered by the rows that say that part, first, with the resources
// after them; it was the resource cards alone, since only a row naming
// widget_api in full counted as about it.
func TestPartOfNameRowsAnswer(t *testing.T) {
	t.Parallel()
	for q, want := range map[string]string{
		"how does the widget import work":              "dec_1",
		"how is the public URL of a widget feed built": "mem_2",
	} {
		o := build(parseQuestion(q, now), partFixture(), 0, 0)
		if len(o.Ranked) == 0 || o.Ranked[0].Kind != KindRow || len(o.Rows) == 0 {
			t.Errorf("%q: the answer does not lead with a row: %+v\n%s", q, o.Ranked, o.Text())
			continue
		}
		if !slices.ContainsFunc(o.Rows, func(r Row) bool { return r.ID == want }) {
			t.Errorf("%q: %s is not in the answer:\n%s", q, want, o.Text())
		}
	}
	o := build(parseQuestion("how does the widget import work", now), partFixture(), 0, 0)
	if ids := rowIDs(o.Rows); !slices.Contains(ids, "mem_1") || slices.Contains(ids, "mem_3") {
		t.Errorf("rows %v: want the import notes, not the deploy note that only names a repo", ids)
	}
}

// TestSplitNameIsWhole (devco D-949): a name the question's words split
// (widget_api: widget api) is matched whole, so a question naming it
// matches it alone and not every widget_* resource by the part widget;
// a row that writes widget_web is about widget_web.
func TestSplitNameIsWhole(t *testing.T) {
	t.Parallel()
	o := build(parseQuestion("when does widget_web deploy", now), partFixture(), 0, 0)
	if len(o.Resources) != 1 || o.Resources[0].Name != "widget_web" || o.Resources[0].Match != MatchName {
		t.Errorf("resources %+v: want widget_web alone, by name", o.Resources)
	}
	if ids := rowIDs(o.Rows); !slices.Equal(ids, []string{"mem_3"}) {
		t.Errorf("rows %v: want mem_3, the row that writes widget_web\n%s", ids, o.Text())
	}
}

// TestFieldsOfClosedValues (devco D-949, public issue #157): a closed
// value stored as its val_ id reads as its current name by the field's
// value ids, else as the v_name stored beside it; never as the val_ id.
func TestFieldsOfClosedValues(t *testing.T) {
	t.Parallel()
	raw := `{"fld_p":{"name":"project","v":"val_p1","v_name":"gadget"},` +
		`"fld_s":{"name":"status","v":"val_s1","v_name":"active"},` +
		`"fld_t":{"name":"tags","v":["val_t1","val_t2"],"v_name":["a","b"]},` +
		`"fld_u":{"name":"url","v":"https://git.example.test/widget_api"}}`
	ids := map[string]map[string]string{
		"fld_p": {"val_p1": "widget"}, // renamed since the row was written
		"fld_t": {"val_t1": "alpha", "val_t2": "beta"},
	}
	got := map[string]string{}
	for _, f := range fieldsOf(raw, ids) {
		got[f.Name] = f.Value
	}
	want := map[string]string{"project": "widget", "status": "active", "tags": `["alpha","beta"]`, "url": "https://git.example.test/widget_api"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	for k, v := range got {
		if strings.Contains(v, "val_") {
			t.Errorf("%s prints a value id: %q", k, v)
		}
	}
}

// TestLoadTypedNamesClosedValues (devco D-949): the typed statement reads
// each kind field's value ids beside the resources, in any line order,
// so a resource's closed values come back as names.
func TestLoadTypedNamesClosedValues(t *testing.T) {
	t.Parallel()
	txt := func(s string) value.Value { v, _ := value.TextOf(s); return v }
	line := func(c ...string) []value.Value {
		out := make([]value.Value, len(c))
		for i, s := range c {
			out[i] = txt(s)
		}
		return out
	}
	run := func(_ context.Context, s string) (opssql.Result, error) {
		if !strings.Contains(s, "kind_fields_all") {
			t.Errorf("the typed statement reads no kind_fields_all:\n%s", s)
		}
		return opssql.Result{Rows: [][]value.Value{
			line("res", "res_api", "kb/org", "code_repo", "widget_api", `{"fld_s":{"name":"status","v":"val_s1","v_name":"live"}}`, "", "", ""),
			line("fld", "fld_s", "", "", "", `[{"id":"val_s1","name":"active"},{"id":"val_s2","name":"archived"}]`, "", "", ""),
		}}, nil
	}
	rs, _, err := loadTyped(context.Background(), run)
	if err != nil || len(rs) != 1 || len(rs[0].Fields) != 1 || rs[0].Fields[0].Value != "active" {
		t.Fatalf("loadTyped: %v %+v; want status active", err, rs)
	}
}

// TestNamedResourceRowsStillAnswer (devco D-949, owner addendum to public
// issue #157): a question naming a resource by its whole name, where no
// row says that name, is answered by the rows holding its other words,
// first, and the resource after them; never by the resource alone.
func TestNamedResourceRowsStillAnswer(t *testing.T) {
	t.Parallel()
	d := partFixture()
	d.resources = append(d.resources, resourceRow{ID: "res_gz", Place: "kb/org", Kind: "project", Name: "gizmo", Fields: []fieldValue{{"status", "active"}}})
	d.rows = append(d.rows, textRow{Table: "memories", ID: "mem_4", Place: "kb/app", Body: "The nightly export writes a bulk file per tenant.", At: d.rows[0].At})
	o := build(parseQuestion("how does the gizmo nightly export work", now), d, 0, 0)
	if len(o.Ranked) == 0 || o.Ranked[0].Kind != KindRow || o.Ranked[0].ID != "mem_4" {
		t.Errorf("ranked %+v: want mem_4 first, the resource after it\n%s", o.Ranked, o.Text())
	}
	if len(o.Resources) != 1 || o.Resources[0].Name != "gizmo" {
		t.Errorf("resources %+v: want gizmo as context", o.Resources)
	}
}
