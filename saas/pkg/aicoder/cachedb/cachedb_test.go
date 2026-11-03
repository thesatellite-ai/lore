package cachedb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dbent"
)

var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

func ctx() context.Context { return context.Background() }

func src(source, label string, files, live map[string]string) SourceFiles {
	s := SourceFiles{Source: source, Label: label, Files: map[string][]byte{}, LiveFiles: map[string][]byte{}}
	for k, v := range files {
		s.Files[k] = []byte(v)
	}
	for k, v := range live {
		s.LiveFiles[k] = []byte(v)
	}
	return s
}

// RowsOf matrix: branch only / live only / both (live wins) / tombstone /
// meta files / broken JSON / nested path.
func TestRowsOf(t *testing.T) {
	s := src(StoreSource("northwind", "acme"), "[northwind/acme]", map[string]string{
		"_meta.json":            `{"project_id":"prj_1"}`,
		"memories/mem_a.json":   `{"id":"mem_a","body":"branch only"}`,
		"tasks/tsk_b.json":      `{"id":"tsk_b","title":"old title","status":"todo"}`,
		"tasks/tsk_gone.json":   `{"id":"tsk_gone","title":"deleted in live"}`,
		"rules/rul_bad.json":    `{not json`,
		"rules/x/rul_deep.json": `{"id":"rul_deep"}`,
	}, map[string]string{
		"tasks/tsk_b.json":    `{"id":"tsk_b","title":"live title","status":"done","_live":{"hlc":"x"}}`,
		"tasks/tsk_gone.json": `{"id":"tsk_gone","_live":{"deleted":true}}`,
		"tasks/tsk_new.json":  `{"id":"tsk_new","title":"live only"}`,
	})
	rows := RowsOf(s)
	got := map[string]Row{}
	for _, r := range rows {
		got[r.Table+"/"+r.ID] = r
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %+v", rows)
	}
	if r := got["tasks/tsk_b"]; !r.Live || r.Title != "live title" || r.Status != "done" {
		t.Fatalf("live wins: %+v", r)
	}
	if _, ok := got["tasks/tsk_gone"]; ok {
		t.Fatal("a tombstone hides the branch copy")
	}
	if r := got["memories/mem_a"]; r.Live || r.Body != "branch only" || r.Source != "store:northwind/acme" || r.Label != "[northwind/acme]" {
		t.Fatalf("branch row: %+v", r)
	}
}

func TestRebuildKeyAndQuery(t *testing.T) {
	path := Path(t.TempDir(), "prj_x")
	d, err := Open(ctx(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	k1 := Key{Entry: "northwind", Commits: map[string]string{"s@main": "a1"}}
	if ok, _ := d.Current(ctx(), k1); ok {
		t.Fatal("a new cache is not current")
	}
	err = d.Rebuild(ctx(), k1, []SourceFiles{
		src(StoreSource("northwind", "acme"), "[northwind/acme]", map[string]string{"memories/m1.json": `{"body":"Client is SLOW to pay"}`}, nil),
		src(PersonalSource("northwind"), "[me]", map[string]string{"memories/m2.json": `{"body":"my vpn note"}`}, nil),
	}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Current(ctx(), k1); !ok {
		t.Fatal("current after rebuild")
	}
	if ok, _ := d.Current(ctx(), Key{Entry: "northwind-stag", Commits: k1.Commits}); ok {
		t.Fatal("another entry is never current: switching org rebuilds")
	}
	rows, err := d.Rows(ctx(), Query{Text: "slow to pay"})
	if err != nil || len(rows) != 1 || rows[0].ID != "m1" {
		t.Fatalf("search: %v %+v", err, rows)
	}
	rows, _ = d.Rows(ctx(), Query{Table: "memories", Sources: []string{PersonalSource("northwind")}})
	if len(rows) != 1 || rows[0].Label != "[me]" {
		t.Fatalf("source filter: %+v", rows)
	}
	// Rebuild for another entry drops the old rows.
	if err := d.Rebuild(ctx(), Key{Entry: "betalabs"}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if rows, _ := d.Rows(ctx(), Query{}); len(rows) != 0 {
		t.Fatalf("org change drops old rows: %+v", rows)
	}
}

// The schema version guards the file: a newer file is refused, a foreign
// SQLite file is refused, an older one is rebuilt.
func TestSchemaVersionGuard(t *testing.T) {
	dir := t.TempDir()
	newer := filepath.Join(dir, "prj_newer.db")
	db := dbent.InitDB(newer)
	if _, err := db.Exec("PRAGMA application_id = 1280266840; PRAGMA user_version = 999;"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(ctx(), newer); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("a newer cache must be refused: %v", err)
	}
	foreign := filepath.Join(dir, "prj_foreign.db")
	db = dbent.InitDB(foreign)
	if _, err := db.Exec("CREATE TABLE x (a int); PRAGMA user_version = 3;"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(ctx(), foreign); !errors.Is(err, ErrNotACache) {
		t.Fatalf("a foreign file must be refused: %v", err)
	}
	link := filepath.Join(dir, "prj_link.db")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx(), link); err == nil {
		t.Fatal("a symlinked cache must be refused")
	}
}

// Side effect 40: prune removes caches idle > maxIdle and their render.
func TestPrune(t *testing.T) {
	cache, render := t.TempDir(), t.TempDir()
	for _, pid := range []string{"prj_old", "prj_new"} {
		d, err := Open(ctx(), Path(cache, pid))
		if err != nil {
			t.Fatal(err)
		}
		when := t0
		if pid == "prj_new" {
			when = t0.Add(80 * 24 * time.Hour)
		}
		if err := d.Rebuild(ctx(), Key{Entry: "e"}, nil, when); err != nil {
			t.Fatal(err)
		}
		d.Close()
		if err := os.WriteFile(filepath.Join(render, pid+".md"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cache, "links-northwind.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Prune(ctx(), cache, render, t0.Add(91*24*time.Hour), 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "prj_old" || res.Kept != 1 {
		t.Fatalf("prune: %+v", res)
	}
	for p, want := range map[string]bool{
		Path(cache, "prj_old"): false, filepath.Join(render, "prj_old.md"): false,
		Path(cache, "prj_new"): true, filepath.Join(render, "prj_new.md"): true,
		filepath.Join(cache, "links-northwind.json"): true,
	} {
		_, err := os.Stat(p)
		if (err == nil) != want {
			t.Fatalf("%s exists=%v want %v", p, err == nil, want)
		}
	}
}
