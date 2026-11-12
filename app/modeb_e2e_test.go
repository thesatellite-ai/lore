package app

// Q-MODEB on the release binary (replaces the deleted v0.1.17 e2e
// TestE2E_SharedInitSearchAndNoSync): shared-init and shared-list are
// refused naming the store replacement; a repo still holding a v0.1.17
// Mode B pointer refuses other commands naming `lore migrate run`, whose
// dry run lists what moves and changes nothing, whose run writes the
// project's rows as `_v: 2` files into <db dir>/stores/<project>, adds the
// store to .lore/stores.yaml and keeps the pointer as lore.yaml.migrated;
// the rows are then readable through the store and a second run is a
// no-op.

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/fileuri"
	"github.com/thesatellite-ai/lore/wire"
)

// Ids of the shared lore.db fixture (project B shares the file).
const (
	mbProjA = "prj_01a1af104e66762ab4fe145c43aba9d8"
	mbProjB = "prj_01a1af104e66762ab4fe145c43aba9d9"
	mbMemA  = "mem_01a1af106a357424ba3b084bd5010e36"
	mbMemB  = "mem_01a1af106a357424ba3b084bd5010e37"
	mbTime  = "2026-11-06 04:14:10.997235823 +0000 UTC m=+0.096559973"
)

// modeBReleaseRepo is a git repo (the world's root) whose .lore holds only a Mode B
// pointer to a shared lore.db with v0.1.17's column shapes.
func modeBReleaseRepo(t *testing.T, w *cliWorld) (repo, shared string) {
	t.Helper()
	w.unscoped()
	repo = w.root
	w.git(t, repo, "init", "-q", "-b", "main")
	shared = t.TempDir()
	db, err := sql.Open("sqlite", fileuri.URL(filepath.Join(shared, "lore.db"), "")) // file:///C:/... on Windows
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }() // fixture: a close error shows in the run that follows
	for _, s := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, name TEXT NOT NULL, last_active_at datetime NOT NULL)`,
		`CREATE TABLE memories (id TEXT PRIMARY KEY, created_at datetime NOT NULL, updated_at datetime NOT NULL, trust_score REAL NOT NULL, source_kind TEXT NOT NULL, kind TEXT NOT NULL, body TEXT NOT NULL, tx_at datetime NOT NULL, embedding BLOB, project_id TEXT NOT NULL)`,
		`INSERT INTO projects VALUES ('` + mbProjA + `', '` + mbTime + `', '` + mbTime + `', 'infra', '` + mbTime + `')`,
		`INSERT INTO projects VALUES ('` + mbProjB + `', '` + mbTime + `', '` + mbTime + `', 'other', '` + mbTime + `')`,
		`INSERT INTO memories VALUES ('` + mbMemA + `', '` + mbTime + `', '` + mbTime + `', 0.5, 'manual', 'retrieved', 'the staging cluster sleeps', '` + mbTime + `', x'01', '` + mbProjA + `')`,
		`INSERT INTO memories VALUES ('` + mbMemB + `', '` + mbTime + `', '` + mbTime + `', 0.5, 'manual', 'retrieved', 'other project note', '` + mbTime + `', NULL, '` + mbProjB + `')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	ptr := "db_path: " + filepath.Join(shared, "lore.db") + "\nproject_id: " + mbProjA + "\n"
	if err := os.WriteFile(filepath.Join(repo, ".lore", "lore.yaml"), []byte(ptr), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, shared
}

// TestModeBRetiredOnRelease: see the file comment.
func TestModeBRetiredOnRelease(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	repo, shared := modeBReleaseRepo(t, w)
	pointer := filepath.Join(repo, ".lore", "lore.yaml")
	pointerBytes, err := os.ReadFile(filepath.Clean(pointer))
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"shared-init", "shared-list"} {
		code, _, errOut := w.lore(t, repo, "project", cmd)
		if code != 23 || !strings.Contains(errOut, "E_UNSUPPORTED") || !strings.Contains(errOut, "lore store init") || !strings.Contains(errOut, "lore fix") {
			t.Fatalf("project %s: exit %d\n%s", cmd, code, errOut)
		}
	}
	code, _, errOut := w.lore(t, repo, "memory", "list")
	if code != 23 || !strings.Contains(errOut, wire.RetiredModeBMessage(pointer)) {
		t.Fatalf("memory list in a Mode B repo: exit %d\n%s", code, errOut)
	}

	type runOut struct {
		Count int      `json:"count"`
		Steps []string `json:"steps"`
	}
	decode := func(s string) runOut {
		t.Helper()
		var o runOut
		if uerr := json.Unmarshal([]byte(s), &o); uerr != nil {
			t.Fatalf("migrate output %q: %v", s, uerr)
		}
		return o
	}
	storeDir := filepath.Join(shared, "stores", "infra")
	code, out, errOut := w.lore(t, repo, "migrate", "run", "--dry-run", "--json")
	dry := decode(out)
	if code != 0 || dry.Count != 1 || !strings.Contains(out, mbMemA) || strings.Contains(out, mbMemB) {
		t.Fatalf("dry run: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, serr := os.Stat(storeDir); !os.IsNotExist(serr) {
		t.Fatalf("the dry run made the store folder: %v", serr)
	}
	if _, serr := os.Stat(pointer); serr != nil {
		t.Fatalf("the dry run moved the pointer: %v", serr)
	}

	code, out, errOut = w.lore(t, repo, "migrate", "run", "--json")
	if got := decode(out); code != 0 || got.Count != dry.Count || len(got.Steps) != len(dry.Steps) {
		t.Fatalf("run: exit %d\n%s\n%s", code, out, errOut)
	}
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(storeDir, "_store", "memories", mbMemA+".json")))
	if err != nil || !strings.Contains(string(raw), `"_v": 2`) {
		t.Fatalf("memory row file: %v\n%s", err, raw)
	}
	if _, serr := os.Stat(filepath.Join(storeDir, "memories", mbMemB+".json")); !os.IsNotExist(serr) {
		t.Fatalf("project B's memory reached the store: %v", serr)
	}
	if cfg, err := os.ReadFile(filepath.Clean(filepath.Join(repo, ".lore", "stores.yaml"))); err != nil || !strings.Contains(string(cfg), "name: "+wire.ModeBStoreName) {
		t.Fatalf("stores.yaml: %v\n%s", err, cfg)
	}
	if kept, err := os.ReadFile(filepath.Clean(pointer + wire.ModeBMigratedSuffix)); err != nil || string(kept) != string(pointerBytes) {
		t.Fatalf("lore.yaml.migrated: %v %q", err, kept)
	}

	code, out, errOut = w.lore(t, repo, "memory", "list", "--store", wire.ModeBStoreName)
	if code != 0 || !strings.Contains(out, mbMemA) {
		t.Fatalf("memory list --store shared: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, errOut = w.lore(t, repo, "migrate", "run", "--json")
	if again := decode(out); code != 0 || again.Count != 0 || len(again.Steps) != 0 {
		t.Fatalf("second run: exit %d\n%s\n%s", code, out, errOut)
	}
}
