//go:build userscen

package userscen

// migrate_modeb_test.go — a v0.1.17 shared database (Mode B: the project's
// rows in a lore.db outside the repo, named by .lore/lore.yaml), built in
// v0.1.17's own table shapes by tests/docs/v017_shared_db.py:
//
//	TestMigrateModeBMissingDB   the pointer names a lore.db that is not there: dry run
//	                            and run are E_NOT_FOUND exit 27 naming both files, and
//	                            nothing changes
//	TestMigrateModeBConvert     every other command is refused naming the next step;
//	                            migrate status works and says what to run; a dry run
//	                            lists each row and step and changes nothing; the run
//	                            moves every row (unicode intact) into a folder store,
//	                            attaches it, keeps the pointer as .migrated; the rows
//	                            then list and show; a second run is a no-op
//	TestMigrateModeBDotDotPath  a db_path with a `..` part is E_BAD_PATH exit 20 naming
//	                            LORE_ALLOW_DB_PATH_OUTSIDE=1, which then lets it through

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// migModeBScript builds a v0.1.17 shared lore.db (relative to this package).
const migModeBScript = "../docs/v017_shared_db.py"

// migModeBPointer is the v0.1.17 pointer file.
const migModeBPointer = ".lore/lore.yaml"

// The rows the shared database holds.
const (
	migModeBRule   = "Terraform changes need a plan — ünï <b>&</b>"
	migModeBMemory = "The staging cluster sleeps at night"
)

// migModeBDB writes a v0.1.17 shared lore.db at path holding a rule and a
// memory of project name, and returns the project id.
func migModeBDB(t *testing.T, path, name string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 builds the v0.1.17 lore.db fixture: %v", err)
	}
	script, err := filepath.Abs(migModeBScript)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: py, Args: []string{py, "-I", script, path, name, "rule:" + migModeBRule, "memory:" + migModeBMemory}}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("v017_shared_db.py: %v", err)
	}
	pid := strings.TrimSpace(string(out))
	if !strings.HasPrefix(pid, "prj_") {
		t.Fatalf("v017_shared_db.py printed %q, want a project id", pid)
	}
	return pid
}

// migModeBRepo makes a git repo at <World>/work/<name> with a v0.1.17
// pointer naming dbPath (written as given, so ${HOME} stays a variable).
func migModeBRepo(t *testing.T, w *th.World, a *th.Actor, name, dbPath, pid string) string {
	t.Helper()
	dir := filepath.Join(w.Root, "work", name)
	a.Lore(dir, "version").OK(t) // creates the folder
	w.Git(dir, "init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, ".lore"), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "db_path: " + dbPath + "\nproject_id: " + pid + "\n"
	if err := os.WriteFile(filepath.Join(dir, migModeBPointer), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// migExists reports whether path exists.
func migExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestMigrateModeBMissingDB(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	dir := migModeBRepo(t, w, alice, "infra", "${HOME}/.lore/client2.db", "prj_01a1b49c653f7c4a9bb4b63a24de3fca")
	for _, args := range [][]string{{"migrate", "run", "--dry-run"}, {"migrate", "run"}} {
		alice.Lore(dir, args...).Fails(t, th.ExitNotFound, "E_NOT_FOUND", "migrate: the shared lore.db", "client2.db", "named by", filepath.FromSlash(migModeBPointer), "is not there")
	}
	if !migExists(filepath.Join(dir, migModeBPointer)) || migExists(filepath.Join(dir, ".lore", "stores.yaml")) {
		t.Fatal("a refused Mode B migration moved the pointer or attached a store")
	}
}

func TestMigrateModeBConvert(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	pid := migModeBDB(t, alice.LorePath("client2.db"), "infra")
	dir := migModeBRepo(t, w, alice, "infra", "${HOME}/.lore/client2.db", pid)
	const retired = "this repo uses the retired shared-database mode (.lore/lore.yaml); run `lore migrate run`"

	// Every other command names the one next step.
	for _, args := range [][]string{{"rule", "list"}, {"memory", "add", "new note"}, {"migrate", "enable", "vars"}} {
		alice.Lore(dir, args...).Fails(t, th.ExitRefused, "E_UNSUPPORTED", retired, "lore migrate run --dry-run")
	}
	// status works there and says what to run.
	st := th.DecodeJSON[migStatusOut](t, alice.Lore(dir, "migrate", "status", "--json"))
	if !slices.Contains(st.Notes, retired) {
		t.Fatalf("migrate status in a Mode B repo: notes %q, want %q", st.Notes, retired)
	}

	dry := th.DecodeJSON[migRunOut](t, alice.Lore(dir, "migrate", "run", "--dry-run", "--json"))
	if !dry.DryRun || dry.Count != 2 {
		t.Fatalf("dry run: %+v, want 2 rows", dry)
	}
	steps := strings.Join(dry.Steps, "\n")
	for _, want := range []string{"mode-b: rules/rul_", "mode-b: memories/mem_", "→ store shared", "mode-b: .lore/stores.yaml: add store shared file://", "mode-b: .lore/lore.yaml → .lore/lore.yaml.migrated (kept)"} {
		if !strings.Contains(steps, want) {
			t.Errorf("dry run steps do not say %q:\n%s", want, steps)
		}
	}
	if !migExists(filepath.Join(dir, migModeBPointer)) || migExists(filepath.Join(dir, ".lore", "stores.yaml")) || migExists(alice.LorePath("stores")) {
		t.Fatal("the dry run changed files")
	}

	out := th.DecodeJSON[migRunOut](t, alice.Lore(dir, "migrate", "run", "--json"))
	if out.Count != 2 || !slices.Equal(out.Steps, dry.Steps) {
		t.Fatalf("run: %+v, want the dry run's 2 rows and steps %q", out, dry.Steps)
	}
	if migExists(filepath.Join(dir, migModeBPointer)) || !migExists(filepath.Join(dir, migModeBPointer+".migrated")) {
		t.Fatal("the pointer was not kept as lore.yaml.migrated")
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".lore", "stores.yaml")); err != nil || !strings.Contains(string(b), "name: shared") {
		t.Fatalf("stores.yaml: %v\n%s", err, b)
	}
	alice.Lore(dir, "rule", "list").OK(t).Says(t, migModeBRule, "[shared]")
	alice.Lore(dir, "memory", "list").OK(t).Says(t, migModeBMemory, "[shared]")
	again := th.DecodeJSON[migRunOut](t, alice.Lore(dir, "migrate", "run", "--json"))
	if again.Count != 0 || len(again.Steps) != 0 {
		t.Fatalf("second run: %+v, want nothing", again)
	}
	if st = th.DecodeJSON[migStatusOut](t, alice.Lore(dir, "migrate", "status", "--json")); slices.Contains(st.Notes, retired) {
		t.Fatalf("status after the run still says Mode B: %q", st.Notes)
	}
}

func TestMigrateModeBDotDotPath(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	pid := migModeBDB(t, filepath.Join(w.Root, "work", "shared-dbs", "client.db"), "blog")
	const rel = "../shared-dbs/client.db"
	dir := migModeBRepo(t, w, alice, "blog", rel, pid)
	for _, args := range [][]string{{"migrate", "run", "--dry-run"}, {"migrate", "run"}} {
		alice.Lore(dir, args...).Fails(t, migExitInvalid, "[E_BAD_PATH] migrate: db_path \""+rel+"\" has a `..` part", "hint: set LORE_ALLOW_DB_PATH_OUTSIDE=1 to allow it")
	}
	if migExists(filepath.Join(dir, ".lore", "stores.yaml")) || !migExists(filepath.Join(dir, migModeBPointer)) {
		t.Fatal("a refused run changed files")
	}
	alice.SetEnv("LORE_ALLOW_DB_PATH_OUTSIDE", "1")
	alice.Lore(dir, "migrate", "run").OK(t).Says(t, "Upgraded 2 rows")
	alice.Lore(dir, "rule", "list").OK(t).Says(t, migModeBRule)
}
