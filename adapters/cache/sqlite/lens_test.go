package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// threeCaches writes three cache files; the shared id is live in two.
func threeCaches(t *testing.T) (f *fixture, srcs []LensSource, shared, onlyGolang string) {
	t.Helper()
	f = newFixture(t)
	prj := newPrj()
	sharedID, golangID := newMem(), newMem()
	names := []string{"repo", "agency", "golang"}
	for i, n := range names {
		c := f.open(n + ".db")
		rows := []store.Row{f.memory(newMem(), prj, "row of "+n)}
		if i < 2 {
			rows = append(rows, f.memory(sharedID, prj, "copied into "+n))
		}
		if n == "golang" {
			rows = append(rows, f.memory(golangID, prj, "only golang"))
			f.kindWorld(c)
		}
		f.apply(c, store.Source(n), "1", rows...)
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, LensSource{Name: n, Path: filepath.Join(f.dir, n+".db")})
	}
	return f, srcs, sharedID.String(), golangID.String()
}

func mustAny(t *testing.T, s string) ids.AnyID {
	t.Helper()
	id, err := ids.ParseAny(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func openLens(t *testing.T, srcs []LensSource) *Lens {
	t.Helper()
	l, err := OpenLens(context.Background(), srcs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// TestLensViewsAndResolve: memories_all has a leading source column over
// all three files; a kind table present in one file still gets a view;
// ResolveID finds one source, refuses two, reports none.
func TestLensViewsAndResolve(t *testing.T) {
	t.Parallel()
	_, srcs, shared, only := threeCaches(t)
	l := openLens(t, srcs)
	res := must(l.Query(context.Background(), `SELECT source, COUNT(*) FROM memories_all GROUP BY source ORDER BY source`))
	if res.Columns[0] != "source" {
		t.Fatalf("first column = %q", res.Columns[0])
	}
	want := map[string]string{"agency": "2", "golang": "2", "repo": "2"}
	for _, r := range res.Rows {
		if want[r[0].String()] != r[1].String() {
			t.Fatalf("rows per source: %v", res.Rows)
		}
	}
	if len(res.Rows) != 3 {
		t.Fatalf("sources = %d", len(res.Rows))
	}
	res = must(l.Query(context.Background(), `SELECT source, name, ip FROM res_server_all`))
	if len(res.Rows) != 1 || res.Rows[0][0].String() != "golang" || res.Rows[0][2].String() != "203.0.113.10" {
		t.Fatalf("res_server_all = %v", res.Rows)
	}
	hit := must(l.ResolveID(context.Background(), mustAny(t, only)))
	if hit.Source != "golang" || hit.Table != schema.TableMemories {
		t.Fatalf("ResolveID = %+v", hit)
	}
	if _, err := l.ResolveID(context.Background(), mustAny(t, shared)); !errs.Has(err, errs.AmbiguousID) {
		t.Fatalf("shared id: %v", err)
	}
	if _, err := l.ResolveID(context.Background(), newMem()); !errs.Has(err, errs.NotFound) {
		t.Fatalf("missing id: %v", err)
	}
}

// TestLensTypedValues: Query narrows integer, real, text and NULL cells.
func TestLensTypedValues(t *testing.T) {
	t.Parallel()
	_, srcs, _, _ := threeCaches(t)
	l := openLens(t, srcs)
	res := must(l.Query(context.Background(), `SELECT 1, 2.5, 'x', NULL`))
	kinds := []value.Kind{value.KindNumber, value.KindNumber, value.KindText, value.KindNull}
	for i, k := range kinds {
		if res.Rows[0][i].Kind() != k {
			t.Fatalf("cell %d kind = %s, want %s", i, res.Rows[0][i].Kind(), k)
		}
	}
}

// TestLensRefusesWrites: every way to write is refused, and the files are
// unchanged.
func TestLensRefusesWrites(t *testing.T) {
	t.Parallel()
	f, srcs, _, _ := threeCaches(t)
	l := openLens(t, srcs)
	writes := []string{
		`INSERT INTO s_0.memories (id, _source, _hlc) VALUES ('x', 'y', 'z')`,
		`DELETE FROM s_0.memories`,
		`UPDATE s_1.memories SET body = 'x'`,
		`DROP VIEW memories_all`,
		`CREATE TABLE t (a)`,
		`ATTACH DATABASE '` + filepath.Join(f.dir, "new.db") + `' AS evil`,
		`PRAGMA query_only = OFF`,
		`SELECT 1; DELETE FROM s_0.memories`,
		`SELECT 1 /* ; */ ; DROP TABLE s_0.memories`,
		`WITH x AS (SELECT 1) DELETE FROM s_0.memories`,
		`WITH x AS (SELECT 1) INSERT INTO temp.foo SELECT * FROM x`,
		`  -- comment
		   VACUUM`,
	}
	for _, q := range writes {
		if _, err := l.Query(context.Background(), q); err == nil {
			t.Errorf("write accepted: %s", q)
		} else if !errs.Has(err, errs.SQLRefused) && !errs.Has(err, errs.InvalidInput) {
			t.Errorf("%s: unexpected error %v", q, err)
		}
	}
	if _, err := l.Query(context.Background(), `WITH x AS (SELECT 1) DELETE FROM s_0.memories`); !errs.Has(err, errs.SQLRefused) {
		t.Errorf("a WITH-hidden delete is not E_READ_ONLY: %v", err)
	}
	if fileExists(filepath.Join(f.dir, "new.db")) {
		t.Error("ATTACH created a file")
	}
	res := must(l.Query(context.Background(), `SELECT COUNT(*) FROM memories_all WHERE body LIKE '%;%' OR 1`))
	if res.Rows[0][0].String() != "6" {
		t.Fatalf("rows changed: %v", res.Rows)
	}
}

// TestAttachLimit pins the real attach limit of modernc.org/sqlite (the
// spec asks for 125; the driver is compiled with 10) and the refusal past it.
func TestAttachLimit(t *testing.T) {
	t.Parallel()
	db := must(sql.Open(driverName, "file::memory:"))
	defer func() { _ = db.Close() }()
	conn := must(db.Conn(context.Background()))
	defer func() { _ = conn.Close() }()
	if got := must(AttachLimit(conn)); got != MaxLensSources {
		t.Fatalf("attach limit = %d, MaxLensSources = %d", got, MaxLensSources)
	}
	var many []LensSource
	for i := range MaxLensSources + 1 {
		many = append(many, LensSource{Name: "s" + strconv.Itoa(i), Path: "x"})
	}
	if _, err := OpenLens(context.Background(), many); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("lens over %d sources: %v", len(many), err)
	}
}

// TestLensMaxSources: exactly MaxLensSources files attach.
func TestLensMaxSources(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var srcs []LensSource
	for i := range MaxLensSources {
		n := "s" + strconv.Itoa(i)
		// Each source starts as a copy of the empty template, not a fresh
		// create: this test is about attaching, and creating every table
		// MaxLensSources times was most of its run (fresh creates are
		// pinned by every other test and TestTemplateMatchesFreshCreate).
		if err := copyTemplate(f.ctx, filepath.Join(f.dir, n+".db"), f.reg, f.exp); err != nil {
			t.Fatal(err)
		}
		c := f.open(n + ".db")
		f.apply(c, store.Source(n), "1", f.memory(newMem(), newPrj(), n))
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, LensSource{Name: n, Path: filepath.Join(f.dir, n+".db")})
	}
	l := openLens(t, srcs)
	res := must(l.Query(context.Background(), `SELECT COUNT(*) FROM memories_all`))
	if res.Rows[0][0].String() != strconv.Itoa(MaxLensSources) {
		t.Fatalf("rows = %v", res.Rows)
	}
}

// TestLensSourceChecks: duplicates, empties and non-cache files are refused.
func TestLensSourceChecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := f.open("a.db")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(f.dir, "a.db")
	cases := map[string][]LensSource{
		"none":      nil,
		"duplicate": {{Name: "x", Path: a}, {Name: "x", Path: a}},
		"no name":   {{Name: "", Path: a}},
		"missing":   {{Name: "x", Path: filepath.Join(f.dir, "absent.db")}},
	}
	for name, srcs := range cases {
		if _, err := OpenLens(context.Background(), srcs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	plain := filepath.Join(f.dir, "plain.db")
	db := must(sql.Open(driverName, "file:"+plain))
	if _, err := db.Exec(`CREATE TABLE t (a)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := OpenLens(context.Background(), []LensSource{{Name: "p", Path: plain}}); !errs.Has(err, errs.NotAStore) {
		t.Errorf("plain sqlite file: %v", err)
	}
}
