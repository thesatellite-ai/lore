package wire

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// lsyncOwnedFile is the v0.1.17 module's single switch of core-owned
// tables (saas/pkg/aicoder/lsync/owned.go). The two modules cannot import
// each other in a test of this package (saas imports the core, not the
// other way round), so the test reads the source.
const lsyncOwnedFile = "../saas/pkg/aicoder/lsync/owned.go"

// mapKeys returns the string keys of the map literal assigned to the
// package-level var name in a parsed file.
func mapKeys(t *testing.T, f *ast.File, name string) []string {
	t.Helper()
	var out []string
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != name || len(vs.Values) != 1 {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatalf("%s is not a map literal", name)
		}
		found = true
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				t.Fatalf("%s holds a non key-value element", name)
			}
			bl, ok := kv.Key.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				t.Fatalf("%s holds a key that is not a string literal", name)
			}
			s, err := strconv.Unquote(bl.Value)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return false
	})
	if !found {
		t.Fatalf("no var %s in %s", name, lsyncOwnedFile)
	}
	slices.Sort(out)
	return out
}

// TestCoreTablesMatchLsyncCoreOwned: wire.CoreTables (what migrate run
// rewrites and the core writes) and lsync's coreOwned (what the v0.1.17
// writers leave alone) must name the same tables, or one side writes
// `_v: 1` over the other's `_v: 2` rows. repoOwned (the roster and
// project rows of the checkout) is a separate set and must not overlap.
func TestCoreTablesMatchLsyncCoreOwned(t *testing.T) {
	t.Parallel()
	f, err := parser.ParseFile(token.NewFileSet(), lsyncOwnedFile, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	owned := mapKeys(t, f, "coreOwned")
	repo := mapKeys(t, f, "repoOwned")
	var core []string
	for _, tb := range CoreTables() {
		core = append(core, string(tb))
	}
	slices.Sort(core)
	if !slices.Equal(core, slices.Compact(slices.Clone(core))) {
		t.Fatalf("CoreTables names a table twice: %v", core)
	}
	for _, c := range core {
		if !slices.Contains(owned, c) {
			t.Errorf("%s is in wire.CoreTables but not in lsync coreOwned", c)
		}
	}
	for _, o := range owned {
		if !slices.Contains(core, o) {
			t.Errorf("%s is in lsync coreOwned but not in wire.CoreTables", o)
		}
	}
	for _, r := range repo {
		if slices.Contains(core, r) {
			t.Errorf("%s is both repoOwned and a core table", r)
		}
	}
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range core {
		if def, ok := reg.Table(row.Table(c)); !ok || !def.Synced {
			t.Errorf("core table %s is not a synced table of the registry", c)
		}
	}
}

// tagLinkIDs are fixed ids for the mergeTagLinks matrix.
const (
	tagA   = "tag_01a1a59b891c70b789144cd3e711fb01"
	tagB   = "tag_01a1a59b891c70b789144cd3e711fb02"
	memX   = "mem_01a1a59b891c70b789144cd3e711fb03"
	patY   = "pat_01a1a59b891c70b789144cd3e711fb04"
	prjZ   = "prj_01a1a59b891c70b789144cd3e711fb05"
	notTag = "mem_01a1a59b891c70b789144cd3e711fb06"
)

// TestMergeTagLinks: links group per tagged row of any table carrying
// tag_ids (memories, patterns, projects, ...), merged with the ids the row
// already lists, sorted and de-duplicated; a link whose entity id carries
// another table's prefix, or whose tag id is not a tag_ id, is skipped and
// returned; a table without tag_ids or unknown to the core is ignored (its
// v0.1.17 readers keep entity_tags).
func TestMergeTagLinks(t *testing.T) {
	t.Parallel()
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if def, ok := reg.Table(schema.TablePatterns); !ok {
		t.Fatal("no patterns table")
	} else if _, has := def.Field(schema.FieldTagIDs); !has {
		t.Fatal("patterns carry no tag_ids: the matrix below assumes they do")
	}
	links := []opsmemory.TagLink{
		{EntityTable: "memories", EntityID: memX, TagID: tagB},
		{EntityTable: "memories", EntityID: memX, TagID: tagA},
		{EntityTable: "memories", EntityID: memX, TagID: tagA}, // duplicate
		{EntityTable: "patterns", EntityID: patY, TagID: tagA}, // another tagged table
		{EntityTable: "projects", EntityID: prjZ, TagID: tagB}, // projects carry tag_ids too
		{EntityTable: "memories", EntityID: patY, TagID: tagA}, // wrong prefix: skipped
		{EntityTable: "memories", EntityID: "garbage", TagID: tagA},
		{EntityTable: "memories", EntityID: memX, TagID: notTag}, // not a tag id: skipped
		{EntityTable: "memories", EntityID: memX, TagID: ""},
		{EntityTable: "secret_rules", EntityID: memX, TagID: tagA}, // no tag_ids: ignored
		{EntityTable: "not_a_table", EntityID: memX, TagID: tagA},  // unknown: ignored
		{EntityTable: "", EntityID: "", TagID: ""},
	}
	existing := func(tb row.Table, id ids.AnyID) []string {
		if tb == schema.TableMemories && id.String() == memX {
			return []string{tagB, tagB}
		}
		return nil
	}
	merged, skipped := mergeTagLinks(reg, links, existing)
	got := map[string]string{}
	for k, v := range merged {
		got[string(k.table)+"/"+k.id.String()] = strings.Join(v, ",")
	}
	want := map[string]string{
		"memories/" + memX: tagA + "," + tagB,
		"patterns/" + patY: tagA,
		"projects/" + prjZ: tagB,
	}
	if len(got) != len(want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(skipped) != 4 {
		t.Fatalf("skipped = %+v, want the 4 malformed links", skipped)
	}
	if m, s := mergeTagLinks(reg, nil, existing); len(m) != 0 || len(s) != 0 {
		t.Fatalf("no links: %v %v", m, s)
	}
}

// v1Row writes a `_v: 1` row file into repo's data folder.
func v1Row(t *testing.T, repo string, table row.Table, id, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".lore", "data", string(table))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := `{"_table":"` + string(table) + `","_v":1,"created_at":"2026-11-04T08:09:56.252002659Z","id":"` + id + `",` + body +
		`,"updated_at":"2026-11-04T08:09:56.252002894Z"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRewriteV1EveryCoreTableFamily: v0.1.17 `_v: 1` rows of the work
// nouns (tasks, missions, task_lists, plans) and the content-2 nouns are
// read by the core and rewritten once by migrate run (RewriteV1): `_v: 2`,
// the fields kept, plans' status_str turned into the closed status; a
// second run finds nothing.
func TestRewriteV1EveryCoreTableFamily(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	prj := projectID(t, a)
	cases := []struct {
		table row.Table
		id    string
		body  string
		keep  string // a field value the rewrite must keep
	}{
		{schema.TableTasks, "tsk_01a1a59b891c70b789144cd3e711fc01", `"project_id":"` + prj + `","title":"ship it","status":"todo"`, `"ship it"`},
		{schema.TableMissions, "msn_01a1a59b891c70b789144cd3e711fc02", `"project_id":"` + prj + `","title":"m1","status":"open"`, `"m1"`},
		{schema.TableTasklists, "tkl_01a1a59b891c70b789144cd3e711fc03", `"project_id":"` + prj + `","name":"l1","body":"b"`, `"l1"`},
		{schema.TablePlans, "pln_01a1a59b891c70b789144cd3e711fc04", `"project_id":"` + prj + `","title":"p1","body":"b","status_str":"active"`, `"in_progress"`},
		{schema.TableIncidents, "inc_01a1a59b891c70b789144cd3e711fc05", `"project_id":"` + prj + `","title":"down","body":"b"`, `"down"`},
		{schema.TableTechDocs, "tdc_01a1a59b891c70b789144cd3e711fc06", `"project_id":"` + prj + `","title":"api","body":"b"`, `"api"`},
	}
	for _, c := range cases {
		def, ok := mustReg(t).Table(c.table)
		if !ok {
			t.Fatalf("no table %s", c.table)
		}
		// use the table's real prefix for the fixture id
		id := string(def.Prefix) + c.id[3:]
		v1Row(t, repo, c.table, id, c.body)
		if !slices.Contains(CoreTables(), c.table) {
			t.Fatalf("%s is not a core table", c.table)
		}
		got, err := a.RewriteV1(background(), c.table, true)
		if err != nil || len(got) != 1 || got[0].String() != id {
			t.Fatalf("%s dry run: %v %v", c.table, got, err)
		}
		if f := rowFile(t, filepath.Join(repo, ".lore", "data", string(c.table), id+".json")); string(f["_v"]) != "1" {
			t.Fatalf("%s: a dry run rewrote the file", c.table)
		}
		if got, err = a.RewriteV1(background(), c.table, false); err != nil || len(got) != 1 {
			t.Fatalf("%s run: %v %v", c.table, got, err)
		}
		raw := string(mustRead(t, filepath.Join(repo, ".lore", "data", string(c.table), id+".json")))
		if !strings.Contains(raw, `"_v": 2`) || !strings.Contains(raw, c.keep) {
			t.Fatalf("%s rewritten as %s", c.table, raw)
		}
		if c.table == schema.TablePlans && strings.Contains(raw, "status_str") {
			t.Fatalf("plans kept status_str: %s", raw)
		}
		if got, err = a.RewriteV1(background(), c.table, false); err != nil || len(got) != 0 {
			t.Fatalf("%s second run: %v %v", c.table, got, err)
		}
		if _, r, err := a.Get(background(), engineRef(c.table, id)); err != nil || r.Deleted {
			t.Fatalf("%s not readable after the rewrite: %v", c.table, err)
		}
	}
}

// mustReg is the built-in registry.
func mustReg(t *testing.T) *schema.Registry {
	t.Helper()
	reg, err := builtinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// engineRef names a repo-store row.
func engineRef(table row.Table, id string) engine.Ref {
	aid, err := ids.ParseAny(id)
	if err != nil {
		panic(err)
	}
	return engine.Ref{Store: store.SourceRepo, Table: table, ID: aid}
}
