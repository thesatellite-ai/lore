package wire

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/schema"
)

// v1Families are the core tables whose v0.1.17 `_v: 1` files migrate run
// must turn into `_v: 2` rows: the four work nouns (g3, later 190) and the
// twelve content-2 nouns (later 147).
var v1Families = []row.Table{
	schema.TableTasks, schema.TableMissions, schema.TableTasklists, schema.TablePlans,
	schema.TableArchitectureNotes, schema.TableBehaviours, schema.TableCookbookRecipes, schema.TableIncidents,
	schema.TableSuggestions, schema.TableTastePrefs, schema.TableWorkflows, schema.TableWorkspaces,
	schema.TableHandoffs, schema.TableSnapshots, schema.TableTechDocs, schema.TableExternalSources,
}

// v1Body is the JSON members of a v0.1.17 row of def: every required field
// with a sample value (the project ref named prj), plus "title" text where
// the table has it, so the rewrite has a value to keep.
func v1Body(t *testing.T, def schema.TableDef, prj string) (body, keep string) {
	t.Helper()
	var parts []string
	for _, f := range def.Fields {
		var raw string
		switch {
		case f.Name == "title" || f.Name == "name":
			raw, keep = strconv.Quote("v1 "+string(def.Name)), strconv.Quote("v1 "+string(def.Name))
		case !f.Required:
			continue
		case len(f.Values) > 0:
			raw = strconv.Quote(f.Values[0])
		case f.Kind == value.KindRef && f.RefTable == schema.TableProjects:
			raw = strconv.Quote(prj)
		case f.Kind == value.KindList:
			raw = `[]`
		case f.Kind == value.KindBool:
			raw = `false`
		case f.Kind == value.KindNumber:
			raw = `1`
		case f.Kind == value.KindDateTime:
			raw = `"2026-11-04T08:09:56Z"`
		case f.Kind == value.KindDate:
			raw = `"2026-11-04"`
		case f.Kind == value.KindText:
			raw = strconv.Quote("x")
			if keep == "" {
				keep = raw
			}
		default:
			t.Fatalf("%s.%s: no v1 sample for kind %s", def.Name, f.Name, f.Kind)
		}
		parts = append(parts, strconv.Quote(f.Name)+":"+raw)
	}
	if keep == "" {
		t.Fatalf("%s: nothing to keep", def.Name)
	}
	return strings.Join(parts, ","), keep
}

// TestMigrateRunRewritesEveryV1Family drives `lore migrate run` (the
// operation, not RewriteV1) over a v0.1.17 repo holding one `_v: 1` file of
// every work and content-2 table: the dry run lists each and writes
// nothing; the run rewrites each file to `_v: 2`, keeps its fields and
// makes it readable through the core; a second run lists nothing and
// leaves every file byte-identical.
func TestMigrateRunRewritesEveryV1Family(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	prj := projectID(t, a)
	reg := mustReg(t)
	type fixture struct {
		table row.Table
		id    string
		path  string
		keep  string
	}
	var fx []fixture
	for _, table := range v1Families {
		def, ok := reg.Table(table)
		if !ok || !slices.Contains(CoreTables(), table) {
			t.Fatalf("%s: not a core table", table)
		}
		id, err := ids.ParseAny(string(def.Prefix) + "_01a1a59b891c70b789144cd3e711f" + fmt.Sprintf("%03x", len(fx)))
		if err != nil {
			t.Fatal(err)
		}
		body, keep := v1Body(t, def, prj)
		v1Row(t, repo, table, id.String(), body)
		fx = append(fx, fixture{table, id.String(), filepath.Join(repo, ".lore", "data", string(table), id.String()+".json"), keep})
	}

	raw, err := run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	dry := migrateOut(t, raw)
	for _, f := range fx {
		if !slices.ContainsFunc(dry.Rows, func(l string) bool { return strings.Contains(l, f.id) }) {
			t.Errorf("dry run does not list %s/%s: %v", f.table, f.id, dry.Rows)
		}
		if string(rowFile(t, f.path)["_v"]) != "1" {
			t.Fatalf("%s: the dry run rewrote the file", f.table)
		}
	}

	if raw, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	if out := migrateOut(t, raw); out.Count < len(fx) {
		t.Fatalf("run count %d < %d fixtures: %s", out.Count, len(fx), raw)
	}
	after := map[string][]byte{}
	for _, f := range fx {
		b := mustRead(t, f.path)
		if !bytes.Contains(b, []byte(`"_v": 2`)) || !bytes.Contains(b, []byte(f.keep)) {
			t.Errorf("%s rewritten as %s", f.table, b)
		}
		if _, r, gerr := a.Get(background(), engineRef(f.table, f.id)); gerr != nil || r.Deleted {
			t.Errorf("%s not readable after migrate run: %v", f.table, gerr)
		}
		after[f.path] = b
	}

	if raw, err = run(t, a, opsmigrate.OpRun, `{}`, false); err != nil {
		t.Fatal(err)
	}
	again := migrateOut(t, raw)
	for _, f := range fx {
		if slices.ContainsFunc(again.Rows, func(l string) bool { return strings.Contains(l, f.id) }) {
			t.Errorf("second run lists %s again: %v", f.id, again.Rows)
		}
		if b, rerr := os.ReadFile(f.path); rerr != nil || !bytes.Equal(b, after[f.path]) {
			t.Errorf("second run changed %s", f.path)
		}
	}
}
