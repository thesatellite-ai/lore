package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// openLocal opens a local state file in a fresh folder (created by open).
func openLocal(t *testing.T, path string) *LocalState {
	t.Helper()
	l, err := OpenLocalState(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func keysOf(es []localstate.Entry) string {
	ks := make([]string, 0, len(es))
	for _, e := range es {
		ks = append(ks, e.Key)
	}
	return strings.Join(ks, ",")
}

// TestLocalStateContract (FA-OLD-LOCAL, later 53 + 15.2): put then get,
// put again replaces (one entry, newer doc), list by prefix in key order
// with % and _ and \ taken literally, the same key in two tables kept
// apart, delete (twice is fine), and every method refusing an unknown
// table or an empty key with E_INVALID_INPUT; PutJSON/GetJSON round-trip.
func TestLocalStateContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := openLocal(t, filepath.Join(t.TempDir(), "state", "local.db"))
	defer func() { _ = l.Close() }()
	u, s := localstate.Usage, localstate.SecretChecks
	put := func(tb localstate.Table, k, doc string) {
		t.Helper()
		if err := l.Put(ctx, tb, k, []byte(doc)); err != nil {
			t.Fatal(err)
		}
	}
	put(u, "a", `{"n":1}`)
	put(u, "a", `{"n":2}`) // replace
	if e, ok, err := l.Get(ctx, u, "a"); err != nil || !ok || string(e.Doc) != `{"n":2}` || e.UpdatedAt.IsZero() {
		t.Fatalf("get after replace: %+v %v %v", e, ok, err)
	}
	if _, ok, err := l.Get(ctx, u, "missing"); err != nil || ok {
		t.Fatalf("missing key: %v %v", ok, err)
	}
	for _, k := range []string{"p%x", "pax", "p_y", "pby", `p\z`, "q1"} {
		put(u, k, `{}`)
	}
	put(s, "a", `{"other":true}`) // same key, other table
	for prefix, want := range map[string]string{
		"":   `a,p%x,p\z,p_y,pax,pby,q1`,
		"p":  `p%x,p\z,p_y,pax,pby`,
		"p%": "p%x",
		"p_": "p_y",
		`p\`: `p\z`,
		"zz": "",
	} {
		got, err := l.List(ctx, u, prefix)
		if err != nil || keysOf(got) != want {
			t.Errorf("List(%q) = %q %v, want %q", prefix, keysOf(got), err, want)
		}
	}
	if e, _, _ := l.Get(ctx, s, "a"); string(e.Doc) != `{"other":true}` {
		t.Errorf("tables not isolated: %s", e.Doc)
	}
	if err := l.Delete(ctx, u, "a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, u, "a"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, ok, _ := l.Get(ctx, s, "a"); !ok {
		t.Error("a delete in one table removed the other table's key")
	}
	bad := localstate.Table("nope")
	refusals := map[string]error{
		"put unknown":  l.Put(ctx, bad, "k", []byte(`{}`)),
		"put empty":    l.Put(ctx, u, "", []byte(`{}`)),
		"del unknown":  l.Delete(ctx, bad, "k"),
		"del empty":    l.Delete(ctx, u, ""),
		"list unknown": func() error { _, err := l.List(ctx, bad, ""); return err }(),
		"get unknown":  func() error { _, _, err := l.Get(ctx, bad, "k"); return err }(),
		"get empty":    func() error { _, _, err := l.Get(ctx, u, ""); return err }(),
	}
	for name, err := range refusals {
		if !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: %v, want E_INVALID_INPUT", name, err)
		}
	}
	type span struct {
		Task string `json:"task"`
		Secs int    `json:"secs"`
	}
	if err := localstate.PutJSON(ctx, l, localstate.TimeOutbox, "sp1", span{"tsk_1", 90}); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := localstate.GetJSON[span](ctx, l, localstate.TimeOutbox, "sp1"); err != nil || !ok || v != (span{"tsk_1", 90}) {
		t.Fatalf("GetJSON = %+v %v %v", v, ok, err)
	}
}

// TestLocalStateLegacyMovedOnce: a file in the first layout (one SQLite
// table per localstate table) has its rows moved into local_state at open
// and the old tables dropped; a key already in local_state keeps its newer
// value; a second open moves nothing again.
func TestLocalStateLegacyMovedOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "local.db")
	dsn, err := fileDSN(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		localDDL,
		`INSERT INTO local_state (tbl, key, doc, updated_at) VALUES ('row_usage', 'kept', '{"new":true}', '2026-11-05T00:00:00Z')`,
		`CREATE TABLE row_usage (key TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO row_usage VALUES ('mem_1', '{"n":3}', '2026-01-01T00:00:00Z'), ('kept', '{"old":true}', '2026-01-01T00:00:00Z')`,
		`CREATE TABLE time_outbox (key TEXT PRIMARY KEY, doc TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO time_outbox VALUES ('sp_1', '{"s":1}', '2026-01-01T00:00:00Z')`,
	} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for round := range 2 {
		l := openLocal(t, path)
		got, err := l.List(ctx, localstate.Usage, "")
		if err != nil || keysOf(got) != "kept,mem_1" {
			t.Fatalf("round %d usage: %q %v", round, keysOf(got), err)
		}
		if e, _, _ := l.Get(ctx, localstate.Usage, "kept"); string(e.Doc) != `{"new":true}` {
			t.Errorf("round %d: the legacy copy overwrote a newer value: %s", round, e.Doc)
		}
		if e, ok, _ := l.Get(ctx, localstate.TimeOutbox, "sp_1"); !ok || string(e.Doc) != `{"s":1}` {
			t.Errorf("round %d: time_outbox not moved", round)
		}
		for _, old := range []string{"row_usage", "time_outbox"} {
			var one int
			if err := l.db.QueryRowContext(ctx, localHasTable, old).Scan(&one); err == nil {
				t.Errorf("round %d: legacy table %s not dropped", round, old)
			}
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
