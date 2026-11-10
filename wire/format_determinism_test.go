package wire

// Format fixtures and determinism (FORMAT_EVOLUTION rule 3 and 7.3):
//
//	TestFixturesCoverEveryTable          a v1 fixture for every synced table of the registry,
//	                                     read by the core, and its v2 form written by migrate
//	TestUntouchedRowsStayByteIdentical   reads, a write of another row and a cache rebuild leave
//	                                     every v1 file byte-identical (Example 2)
//	TestMigrateDeterministic             two copies migrated --offline under different clocks,
//	                                     time zones, actors, sessions and GOMAXPROCS give the same
//	                                     bytes; a second run changes nothing
//
// The fixtures are generated from the registry's table definitions, one row
// per table with EVERY field filled by a representative value of its kind
// (text with unicode and escapes, closed values, numbers, dates, refs to the
// fixture row of the referenced table, lists, JSON), so a new table or a new
// field kind is covered without anyone remembering to add a file, and a kind
// the generator cannot fill fails TestFixturesCoverEveryTable by name.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/canon"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// fixtureHex is the fixed hex head of every fixture id; the tail is the
// table's position in the registry (ids must be stable across runs).
const fixtureHex = "01a1a59b891c70b78914"

// fixtureTime is the audit time of every v1 fixture (the v0.1.17 layout:
// nanoseconds, UTC).
const fixtureTime = "2026-11-04T08:09:56.252002659Z"

// fixtureText is the free-text value: unicode, HTML characters, a quote and
// a control character, the spellings a re-encode must keep byte for byte.
const fixtureText = "fixture café — 日本 <b>&</b> \"q\"\ttab"

// fixtureTables are the registry's synced tables that live in the repo
// store (machine-local tables never reach a row file of the repo).
func fixtureTables(t *testing.T) []schema.TableDef {
	t.Helper()
	var out []schema.TableDef
	for _, def := range mustReg(t).Tables() {
		if def.Synced && !def.MachineLocal && def.Kind.IsZero() {
			out = append(out, def)
		}
	}
	if len(out) == 0 {
		t.Fatal("the registry has no synced tables")
	}
	return out
}

// fixtureID is the fixture row id of the table at index i.
func fixtureID(def schema.TableDef, i int) string {
	return fmt.Sprintf("%s_%s%012x", def.Prefix, fixtureHex, i+1)
}

// fixtureIDs maps each fixture table to its fixture row id.
func fixtureIDs(defs []schema.TableDef) map[row.Table]string {
	m := make(map[row.Table]string, len(defs))
	for i, def := range defs {
		m[def.Name] = fixtureID(def, i)
	}
	return m
}

// fixtureOverrides are the fields whose generic value is not a valid setting:
// a policy row's key must be a known key with a value of its shape (an
// unknown key fails every later write closed, logic/policy.Resolve), so the
// policies fixture is a real entry; the secret_rules fixture is a live rule,
// so its regex and keywords must not match the fixture text of every other
// row (that would turn each later write into a secret finding).
var fixtureOverrides = map[row.Table]map[string]string{
	schema.TablePolicies:    {"key": `"` + string(policy.KeyOnDetect) + `"`, "value": `"redact"`, "scope": `"repo"`},
	schema.TableSecretRules: {"regex": `"FIXTURENEVER-[0-9]{40}"`, "keywords": `["fixturenever-"]`},
}

// fixtureValue renders one field's value as raw JSON, or reports the kind it
// cannot fill.
func fixtureValue(f schema.FieldDef, kind value.Kind, idOf map[row.Table]string) (string, error) {
	switch kind {
	case value.KindText:
		if len(f.Values) > 0 {
			return string(canon.String(f.Values[0])), nil
		}
		return string(canon.String(fixtureText)), nil
	case value.KindNumber:
		return "3", nil
	case value.KindBool:
		return "true", nil
	case value.KindDate:
		return `"2026-11-04"`, nil
	case value.KindDateTime:
		return `"` + fixtureTime + `"`, nil
	case value.KindDuration:
		return `"1h30m0s"`, nil
	case value.KindIP:
		return `"203.0.113.10"`, nil
	case value.KindCIDR:
		return `"10.0.0.0/8"`, nil
	case value.KindDNS:
		return `"db.example.com"`, nil
	case value.KindURL:
		return `"https://example.com/a?b=c"`, nil
	case value.KindEmail:
		return `"ops@example.com"`, nil
	case value.KindJSON:
		return `{"k":"v","n":[1,2]}`, nil
	case value.KindNull:
		return "null", nil
	case value.KindSecretRef:
		return `"gopass://team/db/password"`, nil
	case value.KindRef:
		target := f.RefTable
		if target == "" {
			target = schema.TableMemories
		}
		id, ok := idOf[target]
		if !ok {
			return "", fmt.Errorf("ref to %s, which has no fixture", target)
		}
		return `"` + id + `"`, nil
	case value.KindList:
		if f.Elem == value.KindList || f.Elem == "" {
			return "", fmt.Errorf("list of %q", f.Elem)
		}
		item, err := fixtureValue(f, f.Elem, idOf)
		if err != nil {
			return "", err
		}
		return "[" + item + "]", nil
	}
	return "", fmt.Errorf("kind %q", kind)
}

// fixtureRow renders the v1 file of def's fixture row: the v0.1.17 envelope
// (`_v: 1`, no `_hlc`) and every field of the table.
func fixtureRow(def schema.TableDef, id string, idOf map[row.Table]string) (string, error) {
	members := []canon.Member{
		{Key: row.KeyVersion, Raw: []byte("1")},
		{Key: row.KeyTable, Raw: canon.String(string(def.Name))},
		{Key: row.KeyID, Raw: canon.String(id)},
		{Key: row.KeyCreatedAt, Raw: canon.String(fixtureTime)},
		{Key: row.KeyUpdatedAt, Raw: canon.String(fixtureTime)},
		{Key: row.KeyUpdatedBy, Raw: canon.String(testActor)},
	}
	for _, f := range def.Fields {
		raw, err := fixtureValue(f, f.Kind, idOf)
		if err != nil {
			return "", fmt.Errorf("%s.%s: %w", def.Name, f.Name, err)
		}
		if o, ok := fixtureOverrides[def.Name][f.Name]; ok {
			raw = o
		}
		members = append(members, canon.Member{Key: f.Name, Raw: []byte(raw)})
	}
	// The v0.1.17 writer's own layout: compact, keys in canonical order.
	doc, err := canon.Object(members)
	if err != nil {
		return "", fmt.Errorf("%s: %w", def.Name, err)
	}
	return string(doc), nil
}

// fixtureRepo makes a git work tree holding the v1 fixture of every synced
// table and returns it with the file bytes it wrote, keyed by relative path.
func fixtureRepo(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	repo := gitRepo(t)
	defs := fixtureTables(t)
	idOf := fixtureIDs(defs)
	files := map[string][]byte{}
	for _, def := range defs {
		doc, err := fixtureRow(def, idOf[def.Name], idOf)
		if err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join(".lore", "data", string(def.Name), idOf[def.Name]+".json")
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		files[rel] = []byte(doc)
	}
	return repo, files
}

// dataFiles reads every file under repo/.lore/data, keyed by relative path.
func dataFiles(t *testing.T, repo string) map[string][]byte {
	t.Helper()
	var paths []string
	root := filepath.Join(repo, ".lore", "data")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(repo, path)
		if err != nil {
			t.Fatal(err)
		}
		if out[rel], err = os.ReadFile(filepath.Clean(path)); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// copyTree copies src's files into a fresh git work tree.
func copyTree(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dst := gitRepo(t)
	for rel, b := range files {
		path := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// TestFixturesCoverEveryTable: every synced table of the registry has a v1
// fixture (generated, so a new table is covered or fails here by name); the
// core reads each one as an upcast `_v: 1` row with its fields, and migrate
// rewrites each core table's fixture to a `_v: 2` file (the v2 fixture) that
// keeps every field.
func TestFixturesCoverEveryTable(t *testing.T) {
	t.Parallel()
	defs := fixtureTables(t)
	idOf := fixtureIDs(defs)
	for _, def := range defs {
		doc, err := fixtureRow(def, idOf[def.Name], idOf)
		if err != nil {
			t.Errorf("no v1 fixture: %v", err)
			continue
		}
		r, err := row.Decode([]byte(doc))
		if err != nil {
			t.Errorf("%s: the v1 fixture does not decode: %v", def.Name, err)
			continue
		}
		if r.SourceVersion != 1 || r.HLC.IsZero() || len(r.Fields) != len(def.Fields) {
			t.Errorf("%s: v1 fixture read as v%d hlc=%q with %d of %d fields", def.Name, r.SourceVersion, r.HLC, len(r.Fields), len(def.Fields))
		}
	}
	repo, before := fixtureRepo(t)
	a := newTestApp(t, repo)
	core := CoreTables()
	for _, tb := range core {
		got, err := a.RewriteV1(background(), tb, false)
		if err != nil {
			t.Fatalf("%s: rewrite: %v", tb, err)
		}
		if len(got) != 1 || got[0].String() != idOf[tb] {
			t.Fatalf("%s: rewrote %v, want the one fixture %s", tb, got, idOf[tb])
		}
	}
	after := dataFiles(t, repo)
	for rel, old := range before {
		tb := row.Table(filepath.Base(filepath.Dir(rel)))
		now := after[rel]
		if !slices.Contains(core, tb) {
			if !bytes.Equal(now, old) {
				t.Errorf("%s is not a core table, yet migrate changed its file", tb)
			}
			continue
		}
		v2, err := row.Decode(now)
		if err != nil || v2.SourceVersion != row.FormatVersion {
			t.Errorf("%s: v2 fixture %v (v%d)", tb, err, v2.SourceVersion)
			continue
		}
		v1, err := row.Decode(old)
		if err != nil {
			t.Fatal(err)
		}
		if v2.HLC.String() != v1.HLC.String() || !v2.UpdatedAt.Equal(v1.UpdatedAt) || !v2.CreatedAt.Equal(v1.CreatedAt) {
			t.Errorf("%s: migrate changed the stamp or audit times: %v %v→%v %v", tb, v1.HLC, v1.UpdatedAt, v2.HLC, v2.UpdatedAt)
		}
		for k, v := range v1.Fields {
			if tb == schema.TablePlans && k == schema.FieldPlanStatusStr {
				continue // TSQ9: status_str folds into status
			}
			if w, ok := v2.Fields[k]; !ok || !value.Equal(v, w) {
				t.Errorf("%s.%s lost by the rewrite", tb, k)
			}
		}
	}
}

// TestUntouchedRowsStayByteIdentical (rule 3, Example 2): with every v1
// fixture in place, reading each row, listing each table, writing an
// unrelated row and rebuilding the caches leave every fixture file
// byte-identical; the only new file is the written row.
func TestUntouchedRowsStayByteIdentical(t *testing.T) {
	t.Parallel()
	repo, before := fixtureRepo(t)
	a := newTestApp(t, repo)
	eng, err := a.Engine(background())
	if err != nil {
		t.Fatal(err)
	}
	for rel := range before {
		tb := row.Table(filepath.Base(filepath.Dir(rel)))
		id := strings.TrimSuffix(filepath.Base(rel), ".json")
		if _, r, err := a.Get(background(), engineRef(tb, id)); err != nil || r.Deleted {
			t.Errorf("%s/%s not readable: %v", tb, id, err)
		}
	}
	// The unrelated write: a memory with its required fields, the refs
	// pointing at fixture rows.
	defs := fixtureTables(t)
	mem, ok := mustReg(t).Table(schema.TableMemories)
	if !ok {
		t.Fatal("no memories table")
	}
	fields := map[string]value.Value{}
	for _, f := range mem.Fields {
		if !f.Required && f.Name != "project_id" {
			continue
		}
		raw, err := fixtureValue(f, f.Kind, fixtureIDs(defs))
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "body" {
			raw = `"an unrelated new memory"`
		}
		v, err := value.Parse(f.Kind, []byte(raw))
		if err != nil {
			t.Fatalf("memories.%s: %v", f.Name, err)
		}
		fields[f.Name] = v
	}
	if _, err := eng.Create(background(), "repo", schema.TableMemories, fields); err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second pass over warm caches must not differ
		if _, err := eng.Refresh(background(), "repo"); err != nil {
			t.Fatal(err)
		}
	}
	after := dataFiles(t, repo)
	added := 0
	for rel, now := range after {
		old, ok := before[rel]
		if !ok {
			added++
			if !strings.Contains(rel, string(schema.TableMemories)) {
				t.Errorf("unexpected new file %s", rel)
			}
			continue
		}
		if !bytes.Equal(now, old) {
			t.Errorf("%s rewritten although nobody edited it:\n%s", rel, now)
		}
	}
	if added != 1 {
		t.Errorf("%d new files, want the one written memory", added)
	}
}

// migrateMachine is one machine's conditions for TestMigrateDeterministic.
type migrateMachine struct {
	actor    string
	wall     time.Time
	tz       string
	session  string
	procs    int
	hostname string
}

// TestMigrateDeterministic (7.3): one fixture repo copied into two clones,
// each migrated with `lore migrate run --offline` on a machine with a
// different clock (three days apart), time zone, actor, LORE_SESSION and
// GOMAXPROCS, gives byte-identical `.lore/data` trees; a second run on each
// changes nothing. Not covered here: the shuffled file-walk hook of the
// tagged binary (the backends sort their reads; the walk order of
// rewriteStore is the backend's).
func TestMigrateDeterministic(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	_, files := fixtureRepo(t)
	machines := []migrateMachine{
		{testActor, time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC), "UTC", "sess-a", 1, "mira-laptop"},
		{otherActor, time.Date(2026, 11, 8, 23, 59, 59, 999, time.UTC), "Asia/Kolkata", "sess-b", 8, "tomas-desk"},
	}
	var trees []map[string][]byte
	for _, m := range machines {
		loc, err := time.LoadLocation(m.tz)
		if err != nil {
			t.Skipf("no tzdata for %s: %v", m.tz, err)
		}
		t.Setenv("LORE_SESSION", m.session)
		t.Setenv("HOSTNAME", m.hostname)
		t.Setenv("TZ", m.tz)
		prevLocal, prevProcs := time.Local, runtime.GOMAXPROCS(m.procs)
		time.Local = loc
		repo := copyTree(t, files)
		actor, err := ids.Parse[ids.Actor](m.actor)
		if err != nil {
			t.Fatal(err)
		}
		wall := m.wall
		a, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, Wall: func() time.Time { wall = wall.Add(time.Second); return wall }})
		if err != nil {
			t.Fatal(err)
		}
		out, err := run(t, a, "migrate.run", `{"offline":true}`, true)
		if err != nil {
			t.Fatalf("%s: migrate run: %v", m.actor, err)
		}
		if !strings.Contains(out, `"count":`) {
			t.Fatalf("migrate run said %s", out)
		}
		trees = append(trees, dataFiles(t, repo))
		again, err := run(t, a, "migrate.run", `{"offline":true}`, true)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(again, `"rows":["`) {
			t.Fatalf("a second run rewrote rows: %s", again)
		}
		if second := dataFiles(t, repo); !sameFiles(second, trees[len(trees)-1]) {
			t.Fatal("a second run changed files")
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		time.Local = prevLocal
		runtime.GOMAXPROCS(prevProcs)
	}
	if !sameFiles(trees[0], trees[1]) {
		for rel, b := range trees[0] {
			if !bytes.Equal(b, trees[1][rel]) {
				t.Errorf("%s differs between the machines:\n%s\n%s", rel, b, trees[1][rel])
			}
		}
		t.Fatal("two offline migrations produced different trees")
	}
	upgraded := 0
	for rel, b := range trees[0] {
		if !bytes.Equal(b, files[rel]) {
			upgraded++
		}
	}
	if upgraded < len(CoreTables())-1 {
		t.Fatalf("only %d files upgraded", upgraded)
	}
}

// sameFiles reports whether two file maps hold the same paths and bytes.
func sameFiles(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}
