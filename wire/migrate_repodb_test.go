package wire

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
)

// repoDBApp is an App over a git repo whose v0.1.17 rows live only in its
// own .lore/lore.db (built by v017SharedDB: projects A and B), with meta
// as .lore/data/_meta.json ("" = none), and the v0.1.17 reader cmd/lore
// installs.
func repoDBApp(t *testing.T, meta string) (*App, string) {
	t.Helper()
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".lore", "data"), 0o750); err != nil {
		t.Fatal(err)
	}
	v017SharedDB(t, filepath.Join(repo, ".lore", "lore.db"))
	if meta != "" {
		if err := os.WriteFile(filepath.Join(repo, ".lore", "data", "_meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	actor, err := ids.Parse[ids.Actor](testActor)
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Options{RepoDir: repo, Home: t.TempDir(), Actor: actor, ModeB: V017ModeB{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() }) // test teardown
	return a, repo
}

// TestMigrateRepoLoreDB (owner report 2026-11-11): a v0.1.17 repo whose
// rows are only in its git-ignored .lore/lore.db. migrate status counts
// them in the repo's place and names them; the dry run lists each and
// writes nothing; the run writes the repo's project's rows (memory with
// its tag folded into tag_ids, tag, task, comment, project row) as _v: 2
// row files and never the other project's; the database is left as it
// was; a second run and status after it find nothing.
func TestMigrateRepoLoreDB(t *testing.T) {
	t.Parallel()
	a, repo := repoDBApp(t, `{"_v": 1, "project_id": "`+v17ProjA+`"}`+"\n")
	db := filepath.Join(repo, ".lore", "lore.db")
	dbBefore, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := run(t, a, opsmigrate.OpStatus, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var st opsmigrate.StatusOut
	if err = json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Places) == 0 || st.Places[0].Total != 5 || st.Places[0].V1 != 5 || !strings.Contains(strings.Join(st.Pending, "\n"), ".lore/lore.db: 5 rows not in .lore/data yet") {
		t.Fatalf("status of a lore.db-only repo: %+v", st)
	}
	raw, err = run(t, a, opsmigrate.OpRun, `{"dry_run":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if dry := migrateOut(t, raw); dry.Count != 5 || !strings.Contains(strings.Join(dry.Steps, "\n"), ".lore/lore.db: memories/"+v17MemA+" → .lore/data") {
		t.Fatalf("dry run: %+v", dry)
	}
	if _, serr := os.Stat(memPath(repo, v17MemA)); !os.IsNotExist(serr) {
		t.Fatalf("the dry run wrote a row: %v", serr)
	}
	raw, err = run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); got.Count != 5 {
		t.Fatalf("run: %+v, want 5 rows", got)
	}
	data := filepath.Join(repo, ".lore", "data")
	for _, c := range []struct{ table, id string }{{"memories", v17MemA}, {"tags", v17Tag}, {"tasks", v17TaskA}, {"comments", v17CmtA}, {"projects", v17ProjA}} {
		f := rowFile(t, filepath.Join(data, c.table, c.id+".json"))
		if string(f["_v"]) != "2" || len(f["_hlc"]) == 0 {
			t.Fatalf("%s/%s is not a _v 2 row: %v", c.table, c.id, f)
		}
	}
	if f := rowFile(t, memPath(repo, v17MemA)); !strings.Contains(string(f["tag_ids"]), v17Tag) {
		t.Fatalf("memory tag_ids = %s, want %s", f["tag_ids"], v17Tag)
	}
	for _, gone := range []string{memPath(repo, v17MemB), filepath.Join(data, "comments", v17CmtB+".json"), filepath.Join(data, "projects", v17ProjB+".json")} {
		if _, serr := os.Stat(gone); !os.IsNotExist(serr) {
			t.Fatalf("project B's row %s reached this repo: %v", gone, serr)
		}
	}
	if out, lerr := run(t, a, "memory.list", `{}`, false); lerr != nil || !strings.Contains(out, v17MemA) {
		t.Fatalf("memory list after the run: %s %v", out, lerr)
	}
	if dbAfter, rerr := os.ReadFile(db); rerr != nil || string(dbAfter) != string(dbBefore) {
		t.Fatalf("the run changed the v0.1.17 lore.db (%v)", rerr)
	}
	raw, err = run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if again := migrateOut(t, raw); again.Count != 0 {
		t.Fatalf("second run must be a no-op: %+v", again)
	}
	raw, err = run(t, a, opsmigrate.OpStatus, `{}`, false)
	if err != nil || strings.Contains(raw, "lore.db") {
		t.Fatalf("status after the run still names lore.db rows: %s %v", raw, err)
	}
	// once 0.1.18 holds the rows the old database is stale: a row gone
	// from .lore/data since (purged) never comes back from it
	if err = os.Remove(memPath(repo, v17MemA)); err != nil {
		t.Fatal(err)
	}
	raw, err = run(t, a, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(memPath(repo, v17MemA)); !os.IsNotExist(serr) || strings.Contains(raw, v17MemA) {
		t.Fatalf("a run brought a removed row back from the stale lore.db: %s (%v)", raw, serr)
	}
}

// TestMigrateRepoLoreDBProject: with no _meta.json the database's only
// project is the repo's (the import writes _meta.json naming it, as lore
// init does); a database with several is refused naming them and the
// next step, writing nothing.
func TestMigrateRepoLoreDBProject(t *testing.T) {
	t.Parallel()
	a, repo := repoDBApp(t, "")
	_, err := run(t, a, opsmigrate.OpRun, `{}`, false)
	if !errs.Has(err, errs.InvalidInput) || !strings.Contains(err.Error(), "holds 2 projects ("+v17ProjA+", "+v17ProjB+")") || !strings.Contains(err.Error(), "_meta.json") {
		t.Fatalf("two projects and no _meta.json: %v", err)
	}
	if _, serr := os.Stat(memPath(repo, v17MemA)); !os.IsNotExist(serr) {
		t.Fatalf("a refused run wrote a row: %v", serr)
	}

	one, oneRepo := repoDBApp(t, "")
	dbPath := filepath.Join(oneRepo, ".lore", "lore.db")
	dropProjectB(t, dbPath)
	raw, err := run(t, one, opsmigrate.OpRun, `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrateOut(t, raw); got.Count != 5 || !strings.Contains(strings.Join(got.Steps, "\n"), "project "+v17ProjA+" → .lore/data/_meta.json") {
		t.Fatalf("single-project run: %+v", got)
	}
	meta, err := os.ReadFile(filepath.Join(oneRepo, ".lore", "data", "_meta.json"))
	if err != nil || !strings.Contains(string(meta), v17ProjA) {
		t.Fatalf("_meta.json = %s (%v), want project %s", meta, err, v17ProjA)
	}
}

// dropProjectB removes project B and its rows from the fixture database.
func dropProjectB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open(sqliteDriver, fileuri.URL(path, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // test fixture
	for _, q := range []string{`DELETE FROM projects WHERE id = ?`, `DELETE FROM memories WHERE project_id = ?`} {
		if _, err := db.Exec(q, v17ProjB); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM comments WHERE id = ?`, v17CmtB); err != nil {
		t.Fatal(err)
	}
}
