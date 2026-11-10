package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// entry is one resources.fields entry as stored (STORE_SERVER_SPEC 4.4).
type entry struct {
	Name string          `json:"name"`
	V    json.RawMessage `json:"v"`
}

// rowFields reads the `fields` object of resource id from its row file.
func rowFields(t *testing.T, h *opstest.H, id string) map[string]entry {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(h.Repo, ".lore", "data", schema.TableResources, id+".json")))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields map[string]entry `json:"fields"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("row file %s: %v", b, err)
	}
	return doc.Fields
}

// byName turns stored entries into name → compact raw value text (row
// files are indented).
func byName(es map[string]entry) map[string]string {
	out := map[string]string{}
	for _, e := range es {
		var b bytes.Buffer
		if err := json.Compact(&b, e.V); err != nil {
			out[e.Name] = "<bad json " + string(e.V) + ">"
			continue
		}
		out[e.Name] = b.String()
	}
	return out
}

// sqlLines runs sql.run and returns its rows as "a|b" lines.
func sqlLines(t *testing.T, h *opstest.H, q string) []string {
	t.Helper()
	in, err := json.Marshal(opssql.RunIn{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	var out opssql.RunOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssql.OpRun, string(in)))), &out); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, r := range out.Rows {
		var parts []string
		for _, c := range r.Cells {
			parts = append(parts, c.Value)
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	return lines
}

// TestResourceFieldsSpecShape: resource add writes every value as
// {"fld_…": {"name", "v"}} (spec 4.4), the cache projects it, so spec 15
// step 5's `SELECT name, ip FROM res_server` answers; edit and a field
// rename keep the values (by fld_ id) and an edit re-writes the entry's
// name; --where filters on a field; remove --drop drops only that entry.
func TestResourceFieldsSpecShape(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"port","type":"number"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"tls","type":"bool"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"tags","type":"list"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"note","type":"text"}`},
	)
	prod := h.ID(h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_prod","set":["ip=203.0.113.10","port=443","tls=true","tags=a, b","note=héllo \"quoted\""]}`))
	h.MustYes(settings.OpResAdd, `{"kind":"server","name":"hub_stag","set":["ip=203.0.113.11"]}`)

	got := rowFields(t, h, prod)
	want := map[string]string{"ip": `"203.0.113.10"`, "port": `443`, "tls": `true`, "tags": `["a","b"]`, "note": `"héllo \"quoted\""`}
	if b := byName(got); len(b) != len(want) {
		t.Fatalf("row fields %v, want %v", b, want)
	}
	for k, v := range want {
		if byName(got)[k] != v {
			t.Errorf("row field %s = %s, want %s", k, byName(got)[k], v)
		}
	}
	for id := range got {
		if _, err := ids.Parse[ids.KindField](id); err != nil {
			t.Errorf("entry key %q is not a fld_ id", id)
		}
	}

	if lines := sqlLines(t, h, `SELECT name, ip FROM res_server ORDER BY name`); !slices.Equal(lines, []string{"hub_prod|203.0.113.10", "hub_stag|203.0.113.11"}) {
		t.Fatalf("spec 15 check = %v", lines)
	}
	if lines := sqlLines(t, h, `SELECT port, tls, note FROM res_server WHERE name = 'hub_prod'`); !slices.Equal(lines, []string{`443|1|héllo "quoted"`}) {
		t.Fatalf("typed columns = %v", lines)
	}

	// --where filters on a field value.
	var list settings.ResListOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpResList, `{"kind":"server","where":["ip=203.0.113.11"]}`))), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 1 || list.Data[0].Name != "hub_stag" {
		t.Fatalf("--where = %+v", list.Data)
	}

	// A rename keeps every value (same fld_ key) and the cache column follows.
	ipID := ""
	for id, e := range got {
		if e.Name == "ip" {
			ipID = id
		}
	}
	h.MustYes(settings.OpFieldRename, `{"kind":"server","field":"ip","new_name":"addr"}`)
	if v := resValues(t, h, "hub_prod"); v["addr"] != "203.0.113.10" {
		t.Fatalf("after rename values = %v", v)
	}
	if lines := sqlLines(t, h, `SELECT name, addr FROM res_server ORDER BY name`); !slices.Equal(lines, []string{"hub_prod|203.0.113.10", "hub_stag|203.0.113.11"}) {
		t.Fatalf("after rename sql = %v", lines)
	}
	// An edit re-writes the entry under the field's current name.
	h.MustYes(settings.OpResEdit, `{"id":"hub_prod","set":["port=8443"]}`)
	after := rowFields(t, h, prod)
	if e := after[ipID]; e.Name != "addr" || string(e.V) != `"203.0.113.10"` {
		t.Fatalf("renamed entry after edit = %+v", e)
	}

	// remove --drop removes only that entry; the rest keep the shape.
	h.MustYes(settings.OpFieldRemove, `{"kind":"server","field":"note","drop":true}`)
	if b := byName(rowFields(t, h, prod)); b["note"] != "" || b["port"] != "8443" || b["addr"] != `"203.0.113.10"` {
		t.Fatalf("after drop = %v", b)
	}
}

// TestResourceFieldsWrongShapeFailsLoudly: a resource row whose `fields`
// is not {fld: {name, v}} (a flat value, a missing v or name, not an
// object) is refused by every reader with E_RESOURCE_INVALID naming the
// row, never shown as unset.
func TestResourceFieldsWrongShapeFailsLoudly(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	mustAll(t, h,
		[2]string{settings.OpKindAdd, `{"kind":"server"}`},
		[2]string{settings.OpFieldAdd, `{"kind":"server","field":"ip","type":"ip"}`},
	)
	var kind settings.KindOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(settings.OpKindShow, `{"id":"server"}`))), &kind); err != nil {
		t.Fatal(err)
	}
	fld := ""
	for _, f := range kind.Data.Fields {
		if f.Name == "ip" {
			fld = f.ID
		}
	}
	if fld == "" {
		t.Fatalf("no ip field in %+v", kind)
	}
	shapes := map[string]string{
		"flat":       `{"` + fld + `":"203.0.113.10"}`,
		"missing_v":  `{"` + fld + `":{"name":"ip"}}`,
		"empty_name": `{"` + fld + `":{"name":"","v":"203.0.113.10"}}`,
		"array":      `["203.0.113.10"]`,
	}
	for name, raw := range shapes {
		t.Run(name, func(t *testing.T) {
			fields := map[string]value.Value{}
			var err error
			if fields["fields"], err = value.JSONOf([]byte(raw)); err != nil {
				t.Fatal(err)
			}
			kid, err := ids.ParseAny(kind.Data.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fields["kind"], err = value.RefOf(kid); err != nil {
				t.Fatal(err)
			}
			if fields["name"], err = value.TextOf("bad_" + name); err != nil {
				t.Fatal(err)
			}
			w, err := h.App.Create(context.Background(), store.SourceRepo, schema.TableResources, fields)
			if err != nil {
				t.Fatal(err)
			}
			msg := h.Fails(settings.OpResShow, `{"id":"bad_`+name+`"}`, errs.ResourceInvalid)
			if !strings.Contains(msg, w.Row.ID.String()) {
				t.Errorf("error does not name the row: %s", msg)
			}
			h.Fails(settings.OpResEdit, `{"id":"bad_`+name+`","set":["ip=10.0.0.1"]}`, errs.ResourceInvalid)
			if _, err := h.App.Delete(context.Background(), engine.Ref{Store: store.SourceRepo, Table: schema.TableResources, ID: w.Row.ID}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
