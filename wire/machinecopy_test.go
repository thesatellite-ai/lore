package wire

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// tableCopyRuns is a machine-local table of these tests, one field per
// kind the copy converts.
const tableCopyRuns row.Table = "copy_runs"

// Ids of the v0.1.17 rows the tests copy (v0.1.17 ids are prefix + 32 hex).
const (
	copyRowFull  = "ses_0190e6a0c7d27b4c8a1e2f3a4b5c6d7e"
	copyRowNulls = "ses_0190e6a0c7d27b4c8a1e2f3a4b5c6d7f"
)

// newCopyEngine is an engine over a memstore repo and the real machine
// store under home, with tableCopyRuns registered.
func newCopyEngine(t *testing.T, home string) *engine.Engine {
	t.Helper()
	ctx := context.Background()
	var shared []schema.TableDef
	for _, d := range schema.Builtin() {
		if !d.MachineLocal {
			shared = append(shared, d)
		}
	}
	reg, err := schema.NewRegistry(append(shared, schema.Table(string(tableCopyRuns), ids.PrefixSession, schema.MachineLocal(),
		schema.Field("kind", value.KindText, schema.Required()),
		schema.Field("steps", value.KindNumber),
		schema.Field("ok", value.KindBool),
		schema.Field("started_at", value.KindDateTime),
		schema.Field("notes", value.KindJSON)))...)
	if err != nil {
		t.Fatal(err)
	}
	c := newCaches()
	attachOpt, closer, err := openMachine(ctx, home, reg, c, refs.Expander{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close(); _ = c.Close() })
	if err = c.open(ctx, store.SourceRepo, filepath.Join(t.TempDir(), "repo.db"), reg, refs.Expander{}); err != nil {
		t.Fatal(err)
	}
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(engine.WithStore(engine.Attached{Name: store.SourceRepo, Backend: memstore.NewData().Open(store.OpenOptions{})}),
		attachOpt, engine.WithCache(c), engine.WithRegistry(reg), engine.WithScanner(sc), engine.WithActor(actor), engine.WithAudit(nopAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

// writeV017DB writes a v0.1.17-shaped lore.db at path: the ent table with
// its envelope columns, one column the TableDef does not know, ent's
// time text and 0/1 bools, and a row of NULLs.
func writeV017DB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	db, err := sql.Open(sqliteDriver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, s := range stmts {
		if _, err = db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

const v017CopyRunsDDL = `CREATE TABLE copy_runs (id TEXT PRIMARY KEY, created_at DATETIME, updated_at DATETIME, kind TEXT NOT NULL, steps INTEGER, ok BOOL, started_at DATETIME, notes TEXT, old_only TEXT)`

// TestCopyMachineLocalOnce: the copy keeps the v0.1.17 id, converts every
// kind (text, number, 0/1 bool, ent time text, JSON text), leaves NULL
// columns unset and ignores columns the def lacks; a dry run reports
// without writing, a rerun copies nothing, and a row trashed since is not
// brought back (P3-LOCAL-3).
func TestCopyMachineLocalOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lore.db")
	writeV017DB(t, dbPath, v017CopyRunsDDL,
		`INSERT INTO copy_runs VALUES ('`+copyRowFull+`', '2026-01-02 03:04:05.123456789+00:00', NULL, 'agent', 3, 1, '2026-01-02 03:04:05.5+05:30', '{"a":1}', 'x')`,
		`INSERT INTO copy_runs (id, kind) VALUES ('`+copyRowNulls+`', 'manual')`)
	eng := newCopyEngine(t, filepath.Join(dir, "home"))

	steps, n, err := copyMachineLocal(ctx, eng, dbPath, true)
	if err != nil || n != 2 || len(steps) != 1 || !strings.Contains(steps[0], "would copy 2 copy_runs rows") {
		t.Fatalf("dry run = %q, %d, %v", steps, n, err)
	}
	if rows, lerr := eng.List(ctx, store.SourceRepo, tableCopyRuns); lerr != nil || len(rows) != 0 {
		t.Fatalf("dry run wrote %d rows, %v", len(rows), lerr)
	}
	if steps, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 2 || !strings.Contains(steps[0], "copied 2 copy_runs rows") {
		t.Fatalf("copy = %q, %d, %v", steps, n, err)
	}
	full, err := ids.ParseAny(copyRowFull)
	if err != nil {
		t.Fatal(err)
	}
	src, r, err := eng.Get(ctx, engine.Ref{ID: full})
	if err != nil || src != store.SourceMachine {
		t.Fatalf("Get = %s, %v; want the machine store", src, err)
	}
	want := map[string]string{"kind": "agent", "steps": "3", "ok": "true", "started_at": "2026-01-01T21:34:05.500000000Z", "notes": `{"a":1}`}
	for k, w := range want {
		if v, ok := r.Fields[k]; !ok || v.String() != w {
			t.Errorf("field %s = %v, want %s", k, v, w)
		}
	}
	if _, ok := r.Fields["old_only"]; ok {
		t.Error("a column the def lacks was copied")
	}
	nulls, err := ids.ParseAny(copyRowNulls)
	if err != nil {
		t.Fatal(err)
	}
	if _, r, err = eng.Get(ctx, engine.Ref{ID: nulls}); err != nil || len(r.Fields) != 1 {
		t.Fatalf("NULL row = %v, %v; want only kind set", r.Fields, err)
	}

	if steps, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 0 || len(steps) != 0 {
		t.Fatalf("rerun = %q, %d, %v; want nothing", steps, n, err)
	}
	if _, err = eng.Delete(ctx, engine.Ref{ID: full}); err != nil {
		t.Fatal(err)
	}
	if _, n, err = copyMachineLocal(ctx, eng, dbPath, false); err != nil || n != 0 {
		t.Fatalf("after a delete: %d, %v; the trashed row must stay trashed", n, err)
	}
}

// TestCopyMachineLocalNothingToDo: no lore.db, or a lore.db without the
// table, copies nothing; a row that does not fit is skipped with a line
// naming it, in a dry run too, never dropped silently (P3-LOCAL-4).
func TestCopyMachineLocalNothingToDo(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	eng := newCopyEngine(t, filepath.Join(dir, "home"))
	if steps, n, err := copyMachineLocal(ctx, eng, filepath.Join(dir, "missing.db"), false); err != nil || n != 0 || steps != nil {
		t.Fatalf("no lore.db = %q, %d, %v", steps, n, err)
	}
	other := filepath.Join(dir, "other.db")
	writeV017DB(t, other, `CREATE TABLE memories (id TEXT PRIMARY KEY)`)
	if steps, n, err := copyMachineLocal(ctx, eng, other, false); err != nil || n != 0 || steps != nil {
		t.Fatalf("no table = %q, %d, %v", steps, n, err)
	}
	// A row 0.1.18 refuses (a time v0.1.17 never wrote, a value outside a
	// OneOf, a missing required field) is skipped with a line naming it;
	// the good row beside it is copied.
	bad := filepath.Join(dir, "bad.db")
	writeV017DB(t, bad, v017CopyRunsDDL,
		`INSERT INTO copy_runs (id, kind, started_at) VALUES ('`+copyRowFull+`', 'agent', 'yesterday')`,
		`INSERT INTO copy_runs (id, kind) VALUES ('`+copyRowNulls+`', 'manual')`,
		`INSERT INTO copy_runs (id, kind) VALUES ('not-an-id', 'manual')`)
	for _, dry := range []bool{true, false} {
		steps, n, err := copyMachineLocal(ctx, eng, bad, dry)
		if err != nil || n != 1 || len(steps) != 3 {
			t.Fatalf("dry=%v: %q, %d, %v; want 1 copied and 2 skipped lines", dry, steps, n, err)
		}
		if !strings.Contains(steps[1], "skipped copy_runs row not-an-id (id:") || !strings.Contains(steps[2], "skipped copy_runs row "+copyRowFull+" (column started_at") {
			t.Fatalf("dry=%v: skip lines = %q", dry, steps[1:])
		}
	}
	none := filepath.Join(dir, "none.db")
	writeV017DB(t, none, `CREATE TABLE copy_runs (id TEXT PRIMARY KEY, kind TEXT)`, `INSERT INTO copy_runs (id) VALUES ('`+copyRowFull+`')`)
	if steps, n, err := copyMachineLocal(ctx, eng, none, true); err != nil || n != 0 || len(steps) != 1 || !strings.Contains(steps[0], "skipped") {
		t.Fatalf("missing required kind: %q, %d, %v; want one skip line", steps, n, err)
	}
}
