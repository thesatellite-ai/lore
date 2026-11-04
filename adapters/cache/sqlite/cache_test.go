package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// TestDDLEveryBuiltin: every built-in def becomes a table with the internal
// columns then its fields in order, ref columns are indexed, FTS tables
// exist where declared.
func TestDDLEveryBuiltin(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	internal := []string{colID, colSource, colHLC, row.KeyCreatedAt, row.KeyCreatedBy, row.KeyUpdatedAt, row.KeyUpdatedBy}
	for _, def := range f.reg.Tables() {
		want := slices.Clone(internal)
		for _, fd := range def.Fields {
			want = append(want, fd.Name)
		}
		if got := columnsOf(t, c, def.Name.String()); !slices.Equal(got, want) {
			t.Errorf("%s columns = %v, want %v", def.Name, got, want)
		}
		for _, fd := range def.Fields {
			if fd.Kind != value.KindRef {
				continue
			}
			n := count(t, c, `SELECT COUNT(*) FROM pragma_index_list(?) AS l, pragma_index_info(l.name) AS i WHERE i.name = ? AND i.seqno = 0`,
				def.Name.String(), fd.Name)
			if n == 0 {
				t.Errorf("%s.%s: ref column has no index", def.Name, fd.Name)
			}
		}
		fts := count(t, c, `SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, ftsPrefix+def.Name.String())
		wantFTS := len(def.FTS) > 0 || def.Name == schema.TableResources // resources: cache-only FTS (resourceFTSDef)
		if (fts == 1) != wantFTS {
			t.Errorf("%s: fts table present=%v, FTS fields %v", def.Name, fts == 1, def.FTS)
		}
	}
	if got := c.Tables(); len(got) != len(f.reg.Tables()) {
		t.Errorf("Tables() = %v", got)
	}
}

// TestNoForeignKeys (owner decision O2): no table the cache creates has a
// foreign key, and a row pointing at nothing loads.
func TestNoForeignKeys(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	f.kindWorld(c) // kind tables and views are checked too
	dangling := f.memory(newMem(), newPrj(), "points at a project no store holds")
	f.apply(c, store.SourceRepo, "1", dangling)
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Fatalf("dangling memory rows = %d, want 1", n)
	}
	tables := strings1(t, c, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE ?`, "sqlite%")
	if len(tables) < len(f.reg.Tables()) {
		t.Fatalf("only %d tables", len(tables))
	}
	for _, tb := range tables {
		if n := count(t, c, `SELECT COUNT(*) FROM pragma_foreign_key_list(?)`, tb); n != 0 {
			t.Errorf("table %s has %d foreign keys", tb, n)
		}
	}
}

// TestFTSSearch: search finds bodies, follows edits, forgets tombstones.
func TestFTSSearch(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	a, b := newMem(), newMem()
	f.apply(c, store.SourceRepo, "1",
		f.memory(a, prj, "deploy with blue green rollout"),
		f.memory(b, prj, "the database runs on postgres"))
	hits := must(c.Search(f.ctx, schema.TableMemories, "postgres", 10))
	if len(hits) != 1 || hits[0] != b {
		t.Fatalf("search postgres = %v, want [%s]", hits, b)
	}
	f.apply(c, store.SourceRepo, "2", f.memory(b, prj, "the database runs on sqlite"))
	if hits := must(c.Search(f.ctx, schema.TableMemories, "postgres", 10)); len(hits) != 0 {
		t.Fatalf("stale term still found: %v", hits)
	}
	if hits := must(c.Search(f.ctx, schema.TableMemories, "f_body:rollout", 10)); len(hits) != 1 || hits[0] != a {
		t.Fatalf("column filter = %v", hits)
	}
	f.apply(c, store.SourceRepo, "3", f.tomb(schema.TableMemories, a))
	if hits := must(c.Search(f.ctx, schema.TableMemories, "rollout", 10)); len(hits) != 0 {
		t.Fatalf("tombstoned row still searchable: %v", hits)
	}
	if _, err := c.Search(f.ctx, schema.TableActors, "x", 10); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("search on a table without FTS: %v", err)
	}
}

// TestTombstoneAndOlderCopy: a tombstone removes the typed row; replaying
// the older live copy does not bring it back; a newer live copy does.
func TestTombstoneAndOlderCopy(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	id := newMem()
	old := f.memory(id, prj, "first")
	f.apply(c, store.SourceRepo, "1", old)
	f.apply(c, store.SourceRepo, "2", f.tomb(schema.TableMemories, id))
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 0 {
		t.Fatalf("tombstoned row still in typed table")
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "_rows" WHERE id = ? AND deleted = 1`, id.String()); n != 1 {
		t.Fatalf("tombstone not remembered in _rows")
	}
	f.apply(c, store.SourceRepo, "3", old)
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 0 {
		t.Fatalf("older copy came back after the tombstone")
	}
	f.apply(c, store.SourceRepo, "4", f.memory(id, prj, "revived"))
	if n := count(t, c, `SELECT COUNT(*) FROM "memories" WHERE body = 'revived'`); n != 1 {
		t.Fatalf("newer live copy not applied")
	}
}

// TestApplyAtomic: a failure mid-batch (the expander fails on the second
// row) leaves neither rows nor the cursor behind.
func TestApplyAtomic(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	f.apply(c, store.SourceRepo, "c1", f.memory(newMem(), prj, "before"))
	f.exp.failOn = "boom"
	err := c.Apply(f.ctx, store.SourceRepo, []store.Row{
		f.memory(newMem(), prj, "fine"),
		f.memory(newMem(), prj, "boom"),
	}, "c2")
	if err == nil {
		t.Fatal("Apply succeeded despite the injected failure")
	}
	if cur := must(c.Cursor(f.ctx, store.SourceRepo)); cur != "c1" {
		t.Fatalf("cursor moved to %q on a failed Apply", cur)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Fatalf("typed rows = %d after failed Apply, want 1", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "_rows"`); n != 1 {
		t.Fatalf("raw rows = %d after failed Apply, want 1", n)
	}
	f.exp.failOn = ""
	f.apply(c, store.SourceRepo, "c3", f.memory(newMem(), prj, "after"))
	if cur := must(c.Cursor(f.ctx, store.SourceRepo)); cur != "c3" {
		t.Fatalf("cursor = %q", cur)
	}
}

// TestCursorRebuildUnknownTable: cursors are per source; rows of an unknown
// table are kept raw and survive a reopen; Rebuild empties one source only.
func TestCursorRebuildUnknownTable(t *testing.T) {
	f := newFixture(t)
	path := "c.db"
	c := f.open(path)
	if cur := must(c.Cursor(f.ctx, "other")); cur != "" {
		t.Fatalf("fresh cursor = %q", cur)
	}
	future := f.live("future_things", newMem(), map[string]value.Value{"x": value.Int(1)})
	f.apply(c, store.SourceRepo, "r1", future, f.memory(newMem(), newPrj(), "repo row"))
	f.apply(c, "other", "o1", f.memory(newMem(), newPrj(), "other row"))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = f.open(path)
	if n := count(t, c, `SELECT COUNT(*) FROM "_rows" WHERE tbl = 'future_things'`); n != 1 {
		t.Fatalf("unknown-table row lost across reopen")
	}
	if err := c.Rebuild(f.ctx, store.SourceRepo); err != nil {
		t.Fatal(err)
	}
	if cur := must(c.Cursor(f.ctx, store.SourceRepo)); cur != "" {
		t.Fatalf("cursor after rebuild = %q", cur)
	}
	if cur := must(c.Cursor(f.ctx, "other")); cur != "o1" {
		t.Fatalf("other source's cursor = %q", cur)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "memories"`); n != 1 {
		t.Fatalf("memories after rebuild of repo = %d, want the other source's 1", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "_rows" WHERE source = 'repo'`); n != 0 {
		t.Fatalf("raw rows of the rebuilt source remain")
	}
}

// TestSchemaChangeRefillsOnOpen: a def that gained a field is dropped and
// refilled from _rows on the next open, values included.
func TestSchemaChangeRefillsOnOpen(t *testing.T) {
	f := newFixture(t)
	prj := newPrj()
	c := f.open("c.db")
	r := f.memory(newMem(), prj, "keeps its body")
	f.apply(c, store.SourceRepo, "1", f.live(schema.TableMemories, r.ID, map[string]value.Value{
		"project_id": must(value.RefOf(prj)), "kind": text(t, "core"), "body": text(t, "keeps its body"),
		"source_kind": text(t, "test"), "mood": text(t, "calm"),
	}))
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	var defs []schema.TableDef
	for _, d := range schema.Builtin() {
		if d.Name == schema.TableMemories {
			d.Fields = append(d.Fields, schema.FieldDef{Name: "mood", Kind: value.KindText})
		}
		defs = append(defs, d)
	}
	f.reg = must(schema.NewRegistry(defs...))
	c = f.open("c.db")
	if !slices.Contains(columnsOf(t, c, "memories"), "mood") {
		t.Fatal("new field has no column after reopen")
	}
	if n := count(t, c, `SELECT COUNT(*) FROM "memories" WHERE mood = 'calm' AND body = 'keeps its body'`); n != 1 {
		t.Fatal("row not refilled from _rows with the new column")
	}
}

// TestReexpand: Apply fills row_refs/row_expanded; a changed reference
// refreshes only the rows that use it.
func TestReexpand(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	prj := newPrj()
	host := must(ids.New[ids.Var]()).String()
	port := must(ids.New[ids.Var]()).String()
	f.exp.set(host, "db1")
	f.exp.set(port, "5432")
	a, b, d, plain := newMem(), newMem(), newMem(), newMem()
	f.apply(c, store.SourceRepo, "1",
		f.memory(a, prj, "connect to ${ref:"+host+"}"),
		f.memory(b, prj, "host ${ref:"+host+"} port ${ref:"+port+"}"),
		f.memory(d, prj, "port ${ref:"+port+"}"),
		f.memory(plain, prj, "no references here"))
	if n := count(t, c, `SELECT COUNT(*) FROM row_refs WHERE ref_id = ?`, host); n != 2 {
		t.Fatalf("row_refs for host = %d, want 2", n)
	}
	if n := count(t, c, `SELECT COUNT(*) FROM row_expanded WHERE entity_id = ?`, plain.String()); n != 0 {
		t.Fatal("a field without references got a row_expanded row")
	}
	if got := strings1(t, c, `SELECT text FROM row_expanded WHERE entity_id = ?`, b.String()); len(got) != 1 || got[0] != "host db1 port 5432" {
		t.Fatalf("expanded = %v", got)
	}
	f.exp.resetCalls()
	f.exp.set(host, "db2")
	n := must(c.Reexpand(f.ctx, host))
	if n != 2 {
		t.Fatalf("Reexpand refreshed %d fields, want 2", n)
	}
	calls := f.exp.resetCalls()
	if len(calls) != 2 {
		t.Fatalf("expander saw %d texts, want only the 2 referencing rows: %v", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, host) {
			t.Fatalf("Reexpand touched a row that does not reference host: %q", call)
		}
	}
	if got := strings1(t, c, `SELECT text FROM row_expanded WHERE entity_id = ?`, a.String()); got[0] != "connect to db2" {
		t.Fatalf("a expanded = %v", got)
	}
	if got := strings1(t, c, `SELECT text FROM row_expanded WHERE entity_id = ?`, d.String()); got[0] != "port 5432" {
		t.Fatalf("d changed: %v", got)
	}
}

// TestClose: Close is idempotent and every method fails after it.
func TestClose(t *testing.T) {
	f := newFixture(t)
	c := f.open("c.db")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := c.Apply(f.ctx, store.SourceRepo, nil, ""); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Apply after Close: %v", err)
	}
	if _, err := c.Cursor(f.ctx, store.SourceRepo); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Cursor after Close: %v", err)
	}
	if err := c.Rebuild(f.ctx, store.SourceRepo); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Rebuild after Close: %v", err)
	}
}

// TestOpenRefusals: Open needs a registry and an expander; a path with
// URI-special characters is a plain path, not DSN options.
func TestOpenRefusals(t *testing.T) {
	f := newFixture(t)
	if _, err := Open(f.ctx, filepath.Join(f.dir, "x.db"), f.reg, nil); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("nil expander: %v", err)
	}
	if _, err := Open(f.ctx, filepath.Join(f.dir, "x.db"), nil, f.exp); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("nil registry: %v", err)
	}
	odd := filepath.Join(f.dir, "a b?mode=ro#x", "c.db")
	if err := mkdirAll(filepath.Dir(odd)); err != nil {
		t.Fatal(err)
	}
	c, err := Open(context.Background(), odd, f.reg, f.exp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	f.apply(c, store.SourceRepo, "1", f.memory(newMem(), newPrj(), "written"))
	if !fileExists(odd) {
		t.Fatal("cache file not at the odd path")
	}
}
