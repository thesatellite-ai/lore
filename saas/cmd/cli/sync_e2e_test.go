package main

// End-to-end tests for git-backed lore sync (LORE_SYNC_SPEC.md): they build
// the real lore binary once and drive it together with real git across
// several clones of a bare origin, exactly as developers would. Each test
// names the spec edge case(s) it pins. Skipped with -short.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"dbent"

	"dbent/pkg/dbtemplate"
)

// e2eBin is the lore binary built by TestMain ("" when -short).
var e2eBin string

// templateDB is a migrated DB built once before any test runs and copied
// per test: ent's migrator mutates package-level metadata, so parallel
// migrations race (see dbent/pkg/dbtemplate).
var templateDB string

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runTests(m))
}

// runTests prepares the shared fixtures, runs the tests and cleans up.
// Split from TestMain because os.Exit skips deferred calls.
func runTests(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "lore-cli-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp) // temp fixtures; nothing to report on failure
	templateDB = filepath.Join(tmp, "template.db")
	if err := dbtemplate.Build(context.Background(), templateDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !testing.Short() {
		e2eBin = filepath.Join(tmp, BinaryName)
		build := exec.Command("go", "build", "-o", e2eBin, ".")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build lore for e2e: %v\n%s", err, out)
			return 1
		}
	}
	return m.Run()
}

// world is one isolated universe: its own HOME (so the developer's real
// identity and ~/.lore never leak in), a bare origin, and clones.
type world struct {
	t      *testing.T
	home   string
	origin string
	base   []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	if e2eBin == "" {
		t.Skip("e2e: skipped with -short")
	}
	t.Parallel()
	root := t.TempDir()
	w := &world{t: t, home: filepath.Join(root, "home"), origin: filepath.Join(root, "origin.git")}
	if err := os.MkdirAll(w.home, 0o755); err != nil {
		t.Fatal(err)
	}
	w.base = []string{
		"HOME=" + w.home,
		"PATH=" + filepath.Dir(e2eBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LORE_SYNC_QUIET=0",
		"NO_COLOR=1",
	}
	w.run(root, nil, "git", "init", "-q", "--bare", "-b", "main", w.origin)
	return w
}

// clone is one developer's checkout.
type clone struct {
	w   *world
	dir string
	env []string
}

// newClone clones origin (or initialises the first commit when empty).
func (w *world) newClone(name string) *clone {
	w.t.Helper()
	dir := filepath.Join(filepath.Dir(w.origin), name)
	w.run(filepath.Dir(w.origin), nil, "git", "clone", "-q", w.origin, dir)
	c := &clone{w: w, dir: dir}
	c.git("config", "user.email", name+"@example.com")
	c.git("config", "user.name", name)
	c.git("config", "pull.rebase", "false")
	if out, _ := c.tryGit("rev-parse", "--verify", "HEAD"); out == "" {
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# app\n"), 0o644); err != nil {
			w.t.Fatal(err)
		}
		c.git("checkout", "-q", "-b", "main")
		c.git("add", ".")
		c.git("commit", "-qm", "init")
		c.git("push", "-q", "-u", "origin", "main")
	}
	return c
}

type result struct {
	stdout, stderr string
	code           int
}

func (w *world) run(dir string, extraEnv []string, name string, args ...string) result {
	w.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(append([]string{}, os.Environ()...), w.base...), extraEnv...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			w.t.Fatalf("%s %v: %v", name, args, err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

// lore runs the binary and fails the test on a non-zero exit.
func (c *clone) lore(args ...string) result {
	c.w.t.Helper()
	r := c.w.run(c.dir, c.env, e2eBin, args...)
	if r.code != 0 {
		c.w.t.Fatalf("lore %v exited %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// loreAny runs the binary and returns whatever happened.
func (c *clone) loreAny(args ...string) result {
	c.w.t.Helper()
	return c.w.run(c.dir, c.env, e2eBin, args...)
}

func (c *clone) git(args ...string) string {
	c.w.t.Helper()
	r := c.w.run(c.dir, nil, "git", args...)
	if r.code != 0 {
		c.w.t.Fatalf("git %v exited %d: %s %s", args, r.code, r.stdout, r.stderr)
	}
	return strings.TrimSpace(r.stdout)
}

func (c *clone) tryGit(args ...string) (string, int) {
	c.w.t.Helper()
	r := c.w.run(c.dir, nil, "git", args...)
	return strings.TrimSpace(r.stdout), r.code
}

func (c *clone) commitAll(msg string) {
	c.w.t.Helper()
	c.git("add", "-A")
	c.git("commit", "-qm", msg)
}

var idPattern = regexp.MustCompile(`[a-z]{3}_[0-9a-f]{32}`)

// addMemory creates a memory and returns its id.
func (c *clone) addMemory(body string) string {
	c.w.t.Helper()
	r := c.lore("memory", "add", "--body", body)
	id := idPattern.FindString(r.stdout)
	if id == "" {
		c.w.t.Fatalf("no id in %q", r.stdout)
	}
	return id
}

func (c *clone) addRule(body string) string {
	c.w.t.Helper()
	r := c.lore("rule", "add", "--body", body)
	id := idPattern.FindString(r.stdout)
	if id == "" {
		c.w.t.Fatalf("no id in %q", r.stdout)
	}
	return id
}

func (c *clone) memories() string {
	c.w.t.Helper()
	return c.lore("memory", "list").stdout
}

func (c *clone) dataFile(table, id string) string {
	return filepath.Join(c.dir, ".lore", "data", table, id+".json")
}

func (c *clone) readDoc(table, id string) map[string]any {
	c.w.t.Helper()
	b, err := os.ReadFile(c.dataFile(table, id))
	if err != nil {
		c.w.t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		c.w.t.Fatalf("decode %s: %v\n%s", id, err, b)
	}
	return doc
}

func (c *clone) writeDocField(table, id, key string, value any) {
	c.w.t.Helper()
	doc := c.readDoc(table, id)
	doc[key] = value
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		c.w.t.Fatal(err)
	}
	if err := os.WriteFile(c.dataFile(table, id), append(b, '\n'), 0o644); err != nil {
		c.w.t.Fatal(err)
	}
}

// --------------------------------------------------------------- scenarios

// §8.3, §8.4: init bootstraps, the first commit carries .lore/data, a
// fresh clone builds lore.db from it, and git wiring is installed.
func TestE2E_InitBootstrapFreshCloneAdopt(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	r := alice.lore("init", "--non-interactive", "--name=app")
	if !strings.Contains(r.stderr, "exported") {
		t.Fatalf("init did not bootstrap: %s", r.stderr)
	}
	mem := alice.addMemory("use JWT for auth")
	for _, p := range []string{".lore/data/_meta.json", ".gitattributes"} {
		if _, err := os.Stat(filepath.Join(alice.dir, p)); err != nil {
			t.Fatalf("%s missing: %v", p, err)
		}
	}
	if v := alice.git("config", "--get", "merge.lore.driver"); !strings.Contains(v, "merge-driver") {
		t.Fatalf("merge driver not configured: %q", v)
	}
	if status := alice.git("status", "--porcelain", "--", ".lore/lore.db"); status != "" {
		t.Fatalf("lore.db must stay gitignored: %q", status)
	}
	alice.commitAll("lore")
	alice.git("push", "-q")

	bob := w.newClone("bob")
	if _, err := os.Stat(filepath.Join(bob.dir, ".lore", "lore.db")); err == nil {
		t.Fatal("clone must not ship a lore.db")
	}
	if out := bob.memories(); !strings.Contains(out, "use JWT for auth") {
		t.Fatalf("fresh clone did not import: %s", out)
	}
	if bob.readDoc("memories", mem)["body"] != "use JWT for auth" {
		t.Fatal("file content changed by the fresh clone")
	}
	if out, _ := bob.tryGit("status", "--porcelain", "--", ".lore/data"); out != "" {
		t.Fatalf("adopting a fresh clone must not dirty shared files: %s", out)
	}
}

// §8.4, §8.5: knowledge follows branches; different rows merge on main.
func TestE2E_BranchesFollowAndMerge(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	alice.commitAll("lore")
	alice.git("push", "-q")
	bob := w.newClone("bob")
	bob.memories()

	bob.git("checkout", "-q", "-b", "feature/b")
	bob.addMemory("bob knowledge")
	bob.commitAll("bob")
	alice.addMemory("alice knowledge")
	alice.commitAll("alice")
	alice.git("push", "-q")

	bob.git("checkout", "-q", "main")
	if strings.Contains(bob.memories(), "bob knowledge") {
		t.Fatal("feature knowledge visible on main before merge")
	}
	bob.git("pull", "-q")
	bob.git("merge", "-q", "--no-edit", "feature/b")
	out := bob.memories()
	if !strings.Contains(out, "bob knowledge") || !strings.Contains(out, "alice knowledge") {
		t.Fatalf("merge lost knowledge: %s", out)
	}
}

// §9, E10–E12: same row, different fields → driver merges cleanly; same
// field → markers inside the JSON value, lore refuses them, pre-commit
// blocks the commit, a hand resolution imports.
func TestE2E_SameRowMergeDriverAndConflict(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	rule := c.addRule("use JWT")
	c.commitAll("base")

	c.git("checkout", "-q", "-b", "feat")
	c.writeDocField("rules", rule, "severity", "should")
	c.writeDocField("rules", rule, "updated_at", "2030-01-01T00:00:00.000000000Z")
	c.lore("sync")
	c.commitAll("feat severity")
	c.git("checkout", "-q", "main")
	c.lore("sync")
	c.writeDocField("rules", rule, "applies_to_description", "only api")
	c.writeDocField("rules", rule, "updated_at", "2030-02-01T00:00:00.000000000Z")
	c.lore("sync")
	c.commitAll("main desc")
	if out, code := c.tryGit("merge", "--no-edit", "feat"); code != 0 {
		t.Fatalf("different fields must merge cleanly: %s", out)
	}
	doc := c.readDoc("rules", rule)
	if doc["severity"] != "should" || doc["applies_to_description"] != "only api" {
		t.Fatalf("driver lost a side: %v", doc)
	}
	if !strings.Contains(c.lore("rule", "list").stdout, "should") {
		t.Fatal("merged row not imported")
	}

	c.git("checkout", "-q", "-b", "feat2")
	c.writeDocField("rules", rule, "body", "feature body")
	c.lore("sync")
	c.commitAll("f2")
	c.git("checkout", "-q", "main")
	c.lore("sync")
	c.writeDocField("rules", rule, "body", "main body")
	c.lore("sync")
	c.commitAll("m2")
	if _, code := c.tryGit("merge", "--no-edit", "feat2"); code == 0 {
		t.Fatal("same-field edit must conflict")
	}
	body, _ := c.readDoc("rules", rule)["body"].(string)
	if !strings.Contains(body, "<<<<<<< ours") || !strings.Contains(body, "feature body") {
		t.Fatalf("markers missing from value: %q", body)
	}
	if r := c.loreAny("sync"); r.code == 0 || !strings.Contains(r.stderr, "conflict-markers") {
		t.Fatalf("lore must refuse marker text: %d %s", r.code, r.stderr)
	}
	c.git("add", "-A")
	if _, code := c.tryGit("commit", "-qm", "bad"); code == 0 {
		t.Fatal("pre-commit must block committing conflict markers")
	}
	c.writeDocField("rules", rule, "body", "resolved body")
	c.git("add", "-A")
	c.git("commit", "-qm", "resolved")
	if !strings.Contains(c.lore("rule", "list").stdout, "resolved body") {
		t.Fatal("hand resolution not imported")
	}
}

// E10/E11: without the driver (GitHub's merge button) a same-row edit is a
// visible text conflict; resolving by taking a side imports cleanly, and
// the next lore command re-installs the driver.
func TestE2E_NoDriverIsVisibleTextConflict(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	mem := c.addMemory("base")
	c.commitAll("base")
	c.git("checkout", "-q", "-b", "feat")
	c.writeDocField("memories", mem, "body", "theirs")
	c.lore("sync")
	c.commitAll("t")
	c.git("checkout", "-q", "main")
	c.lore("sync")
	c.writeDocField("memories", mem, "body", "ours")
	c.lore("sync")
	c.commitAll("o")
	c.git("config", "--remove-section", "merge.lore") // GitHub knows no custom drivers
	if _, code := c.tryGit("merge", "--no-edit", "feat"); code == 0 {
		t.Fatal("expected a text conflict without the driver")
	}
	c.git("checkout", "--theirs", "--", ".lore/data")
	c.git("add", "-A")
	c.git("commit", "-qm", "take theirs")
	if !strings.Contains(c.memories(), "theirs") {
		t.Fatal("taken side not imported")
	}
	if v := c.git("config", "--get", "merge.lore.driver"); v == "" {
		t.Fatal("driver not re-installed")
	}
}

// E21, trash: a revert removes the row, the trash keeps it, restore brings
// it back as an ordinary edit.
func TestE2E_RevertTrashRestore(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.commitAll("base")
	mem := c.addMemory("temporary")
	c.commitAll("add")
	c.git("revert", "--no-edit", "HEAD")
	r := c.lore("sync")
	if !strings.Contains(r.stderr, "trashed rows 1") || strings.Contains(c.memories(), "temporary") {
		t.Fatalf("revert not applied: %s", r.stderr)
	}
	trash := c.lore("sync", "trash", "--json").stdout
	if !strings.Contains(trash, mem) {
		t.Fatalf("trash missing row: %s", trash)
	}
	c.lore("sync", "trash", "restore", "1")
	if !strings.Contains(c.memories(), "temporary") {
		t.Fatal("restore failed")
	}
	if _, err := os.Stat(c.dataFile("memories", mem)); err != nil {
		t.Fatal("restored row not exported")
	}
}

// E46: an existing hook manager directory is chained into, never repointed.
func TestE2E_HooksChainIntoExistingHooksPath(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	hooks := filepath.Join(c.dir, ".husky")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "#!/bin/sh\necho user-hook-ran\nexit 0\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(existing), 0o755); err != nil {
		t.Fatal(err)
	}
	c.git("config", "core.hooksPath", ".husky")
	c.lore("init", "--non-interactive", "--name=app")
	if v := c.git("config", "--get", "core.hooksPath"); v != ".husky" {
		t.Fatalf("core.hooksPath repointed to %q", v)
	}
	b, err := os.ReadFile(filepath.Join(hooks, "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "#!/bin/sh\n# >>> lore-sync >>>") || !strings.Contains(s, "echo user-hook-ran\nexit 0\n") {
		t.Fatalf("hook not chained correctly:\n%s", s)
	}
	c.git("add", "-A")
	r := c.w.run(c.dir, nil, "git", "commit", "-m", "x")
	if r.code != 0 || !strings.Contains(r.stdout+r.stderr, "user-hook-ran") {
		t.Fatalf("user hook must still run: %d %s %s", r.code, r.stdout, r.stderr)
	}
}

// LORE_SYNC=0 keeps the old cache-only behaviour.
func TestE2E_SyncDisabled(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.env = []string{"LORE_SYNC=0"}
	c.lore("init", "--non-interactive", "--name=app")
	c.addMemory("x")
	if _, err := os.Stat(filepath.Join(c.dir, ".lore", "data")); !os.IsNotExist(err) {
		t.Fatal("LORE_SYNC=0 must not create .lore/data")
	}
	if _, err := os.Stat(filepath.Join(c.dir, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatal("LORE_SYNC=0 must not touch git")
	}
}

// §12.3/§12.4: two developers with pre-existing, never-shared DBs roll out
// sync: one bootstraps on main, the other adopts and keeps private rows.
func TestE2E_ExistingDBsRollout(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.env = []string{"LORE_SYNC=0"}
	alice.lore("init", "--non-interactive", "--name=app")
	alice.addMemory("alice old knowledge")
	alice.git("add", "-A")
	alice.git("commit", "-qm", "gitignore")
	alice.git("push", "-q")

	bob := w.newClone("bob")
	bob.env = []string{"LORE_SYNC=0"}
	bob.lore("init", "--non-interactive", "--name=app")
	bobOld := bob.addMemory("bob old knowledge")

	alice.env = nil // upgrade: sync on
	r := alice.lore("memory", "list")
	if !strings.Contains(r.stderr, "exported") {
		t.Fatalf("alice did not bootstrap: %s", r.stderr)
	}
	alice.commitAll("bootstrap lore data")
	alice.git("push", "-q")

	bob.env = nil
	bob.git("pull", "-q")
	r = bob.lore("memory", "list")
	if !strings.Contains(r.stderr, "adopted") || !strings.Contains(r.stdout, "alice old knowledge") || !strings.Contains(r.stdout, "bob old knowledge") {
		t.Fatalf("bob adopt failed:\nstdout %s\nstderr %s", r.stdout, r.stderr)
	}
	aliceProject := alice.readDoc("memories", idPattern.FindString(alice.lore("memory", "list").stdout))["project_id"]
	if bob.readDoc("memories", bobOld)["project_id"] != aliceProject {
		t.Fatal("bob's rows not re-pointed to the shared project")
	}
	if !strings.Contains(r.stderr, "pre-sync.sqlite") {
		t.Fatalf("no backup before adopt: %s", r.stderr)
	}
}

// E43: restore --prefer db brings the DB's version back into the files.
func TestE2E_RestorePreferDB(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	mem := c.addMemory("before backup")
	backup := filepath.Join(c.dir, "snap.sqlite")
	c.lore("backup", "--out", backup)
	c.writeDocField("memories", mem, "body", "after backup")
	c.lore("sync")
	c.lore("restore", backup, "--confirm", "--prefer", "db")
	c.lore("sync")
	if c.readDoc("memories", mem)["body"] != "before backup" {
		t.Fatal("restore --prefer db did not win")
	}
}

// E1: `lore init` in a clone that already has .lore/data adopts it.
func TestE2E_InitOnCloneAdopts(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	alice.commitAll("lore")
	alice.git("push", "-q")
	bob := w.newClone("bob")
	r := bob.lore("init", "--non-interactive", "--name=other")
	if !strings.Contains(r.stdout, "adopted") {
		t.Fatalf("init must adopt: %s", r.stdout)
	}
	if out := bob.lore("project", "list").stdout; strings.Contains(out, "other") || !strings.Contains(out, "app") {
		t.Fatalf("second project minted: %s", out)
	}
}

// E33: a credential never reaches a git-tracked file.
func TestE2E_SecretNotExported(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	r := c.lore("memory", "add", "--allow-secrets", "--body", "aws key AKIAIOSFODNN7EXAMPLE")
	id := idPattern.FindString(r.stdout)
	if _, err := os.Stat(c.dataFile("memories", id)); !os.IsNotExist(err) {
		t.Fatal("secret exported to a git-tracked file")
	}
	if !strings.Contains(r.stderr, "secret-detected") {
		t.Fatalf("secret refusal not reported: %s", r.stderr)
	}
	st := c.lore("sync", "status", "--json").stdout
	if !strings.Contains(st, "secret-detected") {
		t.Fatalf("status must list it: %s", st)
	}
}

// Hooks: post-merge imports a pull before any lore command runs.
func TestE2E_PostMergeHookImports(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	alice.commitAll("lore")
	alice.git("push", "-q")
	bob := w.newClone("bob")
	bob.memories()
	alice.addMemory("pulled by hook")
	alice.commitAll("m")
	alice.git("push", "-q")
	bob.git("pull", "-q")
	bob.env = []string{"LORE_SYNC=0"} // read the cache without a sync pass
	if !strings.Contains(bob.memories(), "pulled by hook") {
		t.Fatal("post-merge hook did not import")
	}
}

// doctor and status --json expose the sync state.
func TestE2E_DoctorAndStatus(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.addMemory("x")
	r := c.loreAny("doctor", "--json")
	var rep struct {
		Status string `json:"status"`
		Sync   *struct {
			Baselined     bool `json:"baselined"`
			MergeDriverOK bool `json:"merge_driver_configured"`
			Uncommitted   int  `json:"uncommitted_files"`
		} `json:"sync"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil || rep.Sync == nil {
		t.Fatalf("doctor json: %v %s", err, r.stdout)
	}
	if !rep.Sync.Baselined || !rep.Sync.MergeDriverOK || rep.Sync.Uncommitted == 0 {
		t.Fatalf("doctor sync = %+v", rep.Sync)
	}
	// Uncommitted work is informational: a fresh project is healthy.
	if rep.Status != "healthy" || r.code != 0 {
		t.Fatalf("fresh project must be healthy: %s %d %v", rep.Status, r.code, rep.Warnings)
	}
	// A broken file degrades.
	mem := idPattern.FindString(c.lore("memory", "list").stdout)
	if err := os.WriteFile(c.dataFile("memories", mem), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2 := c.loreAny("doctor", "--json")
	if r2.code != 1 || !strings.Contains(r2.stdout, "lore file memories/") {
		t.Fatalf("broken file must degrade doctor: %d %s", r2.code, r2.stdout)
	}
	// The [sync] file-error list is exactly what the warnings report.
	var broken struct {
		Sync struct {
			FileErrors []struct {
				Path string `json:"path"`
			} `json:"file_errors"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(r2.stdout), &broken); err != nil || len(broken.Sync.FileErrors) != 1 ||
		broken.Sync.FileErrors[0].Path != "memories/"+mem+".json" {
		t.Fatalf("file_errors = %+v (%v)", broken.Sync.FileErrors, err)
	}
	st := c.lore("sync", "status", "--json").stdout
	if !strings.Contains(st, `"kind": "sync.status"`) {
		t.Fatalf("status envelope: %s", st)
	}
}

// Read-only mode refreshes the cache but writes no files.
func TestE2E_ReadOnly(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	mem := c.addMemory("x")
	c.writeDocField("memories", mem, "body", "edited in file")
	c.env = []string{"LORE_READ_ONLY=1"}
	if !strings.Contains(c.memories(), "edited in file") {
		t.Fatal("read-only must still import")
	}
	if r := c.loreAny("memory", "add", "--body", "nope"); r.code == 0 {
		t.Fatal("read-only must refuse writes")
	}
}

// Q1/P7: peek lists another branch's rows without checking it out.
func TestE2E_PeekOtherBranch(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.commitAll("base")
	c.git("checkout", "-q", "-b", "feature/x")
	mem := c.addMemory("feature-only fact")
	c.commitAll("feature")
	c.git("checkout", "-q", "main")
	if strings.Contains(c.memories(), "feature-only fact") {
		t.Fatal("feature row visible on main")
	}
	out := c.lore("sync", "peek", "feature/x", "memories").stdout
	if !strings.Contains(out, mem) || !strings.Contains(out, "feature-only fact") {
		t.Fatalf("peek: %s", out)
	}
	js := c.lore("sync", "peek", "feature/x", "--json").stdout
	if !strings.Contains(js, `"kind": "sync.peek"`) || !strings.Contains(js, "projects") {
		t.Fatalf("peek json: %s", js)
	}
	if r := c.loreAny("sync", "peek", "no-such-ref"); r.code == 0 {
		t.Fatal("bad ref must fail")
	}
	if strings.Contains(c.memories(), "feature-only fact") {
		t.Fatal("peek must not import")
	}
}

// E39/P7: promote commits a row onto another branch without checkout.
func TestE2E_PromoteToMain(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.commitAll("base")
	c.git("checkout", "-q", "-b", "feature/y")
	rule := c.addRule("urgent: never log tokens")
	c.commitAll("feature")
	if r := c.lore("sync", "promote", rule, "--to", "main", "--dry-run"); !strings.Contains(r.stdout, "would commit") {
		t.Fatalf("dry run: %s", r.stdout)
	}
	if r := c.loreAny("sync", "promote", rule, "--to", "feature/y"); r.code == 0 {
		t.Fatal("promoting onto the current branch must fail")
	}
	c.lore("sync", "promote", rule, "--to", "main")
	if st := c.git("status", "--porcelain"); st != "" {
		t.Fatalf("promote touched the work tree: %s", st)
	}
	c.git("checkout", "-q", "main")
	if !strings.Contains(c.lore("rule", "list").stdout, "never log tokens") {
		t.Fatal("promoted rule missing on main")
	}
	if r := c.loreAny("sync", "promote", "rul_00000000000070008000000000000000", "--to", "feature/y"); r.code == 0 {
		t.Fatal("unknown id must fail")
	}
}

// E48: duplicates found and folded.
func TestE2E_DupesAndMergeRows(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	a := c.addMemory("Same Fact")
	b := c.addMemory("same   fact")
	out := c.lore("sync", "dupes").stdout
	if !strings.Contains(out, a) || !strings.Contains(out, b) {
		t.Fatalf("dupes: %s", out)
	}
	c.lore("sync", "merge-rows", "memories", "--keep", a, "--drop", b)
	if strings.Contains(c.memories(), b) {
		t.Fatal("dropped row still listed")
	}
	if out := c.lore("sync", "dupes").stdout; !strings.Contains(out, "no duplicates") {
		t.Fatalf("still dupes: %s", out)
	}
}

// E16: a second worktree builds its own cache from the shared files.
func TestE2E_Worktree(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.addMemory("in main worktree")
	c.commitAll("base")
	wt := filepath.Join(filepath.Dir(c.dir), "wt")
	c.git("worktree", "add", "-q", "-b", "wt-branch", wt)
	other := &clone{w: w, dir: wt}
	if !strings.Contains(other.memories(), "in main worktree") {
		t.Fatal("worktree did not build its cache")
	}
	other.addMemory("only in worktree")
	if strings.Contains(c.memories(), "only in worktree") {
		t.Fatal("worktrees must not share an uncommitted cache")
	}
}

// E22: `git stash -u` hides uncommitted rows; pop brings them back.
func TestE2E_StashRoundTrip(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.commitAll("base")
	c.addMemory("work in progress")
	c.git("stash", "-u", "-q")
	if strings.Contains(c.memories(), "work in progress") {
		t.Fatal("stashed row still visible")
	}
	c.git("stash", "pop", "-q")
	if !strings.Contains(c.memories(), "work in progress") {
		t.Fatal("popped row not back")
	}
}

// E44: a deleted lore.db is rebuilt from the files on the next command.
func TestE2E_DeletedDBRebuilt(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	c.addMemory("survives")
	for _, f := range []string{"lore.db", "lore.db-wal", "lore.db-shm"} {
		_ = os.Remove(filepath.Join(c.dir, ".lore", f)) // sidecars may not exist
	}
	r := c.lore("memory", "list")
	if !strings.Contains(r.stdout, "survives") || !strings.Contains(r.stderr, "built .lore/lore.db") {
		t.Fatalf("not rebuilt: %s %s", r.stdout, r.stderr)
	}
}

// E33: a hand-added secret is blocked at commit time.
func TestE2E_PreCommitBlocksHandAddedSecret(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	mem := c.addMemory("clean")
	c.commitAll("base")
	c.writeDocField("memories", mem, "body", "key AKIAIOSFODNN7EXAMPLE")
	c.git("add", "-A")
	if _, code := c.tryGit("commit", "-qm", "leak"); code == 0 {
		t.Fatal("secret committed")
	}
}

// E35: LORE.md is re-rendered after teammates' changes are imported.
func TestE2E_LoreMDRerenderedAfterPull(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	alice.lore("render", "--no-pointer")
	alice.commitAll("lore")
	alice.git("push", "-q")
	bob := w.newClone("bob")
	bob.memories()
	// Alice commits the rule's DATA but not a re-rendered LORE.md: only
	// bob's automatic re-render can put the rule into his LORE.md.
	alice.addRule("must rule from alice")
	alice.commitAll("rule")
	alice.git("push", "-q")
	bob.git("pull", "-q")
	bob.memories()
	b, err := os.ReadFile(filepath.Join(bob.dir, ".lore", "LORE.md"))
	if err != nil || !strings.Contains(string(b), "must rule from alice") {
		t.Fatalf("LORE.md not re-rendered after import: %v\n%s", err, b)
	}
	// Determinism: alice rendering the same data produces the same bytes.
	alice.lore("render", "--no-pointer")
	a, err := os.ReadFile(filepath.Join(alice.dir, ".lore", "LORE.md"))
	if err != nil || string(a) != string(b) {
		t.Fatal("the same data must render byte-identical LORE.md on every clone")
	}
}

// Two lore processes writing at once (two agents, two terminals) must both
// succeed: connections wait for the write lock (busy_timeout per connection
// + immediate transactions) instead of failing with SQLITE_BUSY, and every
// row reaches .lore/data.
func TestE2E_ConcurrentWriters(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	const perWriter = 15
	errs := make(chan string, 2*perWriter)
	done := make(chan struct{})
	for _, prefix := range []string{"A", "B"} {
		go func(p string) {
			for i := range perWriter {
				if r := c.loreAny("memory", "add", "--body", fmt.Sprintf("%s-%d", p, i)); r.code != 0 {
					errs <- r.stderr
				}
			}
			done <- struct{}{}
		}(prefix)
	}
	<-done
	<-done
	close(errs)
	for e := range errs {
		t.Errorf("concurrent write failed: %s", e)
	}
	c.lore("sync")
	entries, err := os.ReadDir(filepath.Join(c.dir, ".lore", "data", "memories"))
	if err != nil || len(entries) != 2*perWriter {
		t.Fatalf("want %d row files, got %d (%v)", 2*perWriter, len(entries), err)
	}
}

// Mode B (`project shared-init`) builds the same search index as `lore init`;
// before, every search in a shared project failed with
// "no such table: memory_fts". Mode B never syncs through .lore/data.
func TestE2E_SharedInitSearchAndNoSync(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	shared := filepath.Join(filepath.Dir(c.dir), "shared.db")
	c.lore("project", "shared-init", "--db="+shared, "--name=alpha")
	c.lore("memory", "add", "--body", "alpha round-trip memory")
	if out := c.lore("memory", "search", "round-trip").stdout; !strings.Contains(out, "alpha round-trip memory") {
		t.Fatalf("search in a shared project: %s", out)
	}
	if _, err := os.Stat(filepath.Join(c.dir, ".lore", "data")); !os.IsNotExist(err) {
		t.Fatal("Mode B must not create .lore/data")
	}
}

// Review findings 1+2: the pre-commit guard must inspect UNSTAGED lore
// files (it stages them) and must find them when the lore project lives in
// a subdirectory of the repo. Before the fix both cases skipped the file as
// "deleted" and auto-staged it unchecked.
func TestE2E_PreCommitGuardsUnstagedFileInSubdirProject(t *testing.T) {
	w := newWorld(t)
	repo := w.newClone("mono")
	svc := filepath.Join(repo.dir, "svc")
	if err := os.MkdirAll(svc, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &clone{w: w, dir: svc}
	c.lore("init", "--non-interactive", "--name=svc")
	mem := c.addMemory("clean")
	repo.commitAll("base")
	// The lore file gets a secret but stays UNSTAGED; only README is staged.
	c.writeDocField("memories", mem, "body", "key AKIAIOSFODNN7EXAMPLE")
	if err := os.WriteFile(filepath.Join(repo.dir, "README.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo.git("add", "README.md")
	if _, code := repo.tryGit("commit", "-qm", "readme only"); code == 0 {
		t.Fatal("pre-commit staged an unchecked lore file containing a secret")
	}
}

// Review finding: a bad --prefer must be rejected before the live DB is
// touched (it used to replace the DB, then fail, skipping the sync reset).
func TestE2E_RestoreRejectsBadPreferBeforeTouchingDB(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	backup := filepath.Join(c.dir, "snap.sqlite")
	c.lore("backup", "--out", backup)
	c.addMemory("after backup")
	before, err := os.ReadFile(filepath.Join(c.dir, ".lore", "lore.db"))
	if err != nil {
		t.Fatal(err)
	}
	if r := c.loreAny("restore", backup, "--confirm", "--prefer", "file"); r.code == 0 {
		t.Fatal("typo in --prefer must fail")
	}
	after, err := os.ReadFile(filepath.Join(c.dir, ".lore", "lore.db"))
	if err != nil || string(after) != string(before) {
		t.Fatal("the live DB was replaced despite the rejected flag")
	}
}

// Review finding: two lore projects in one repo must each keep their own
// hook block (they used to overwrite each other's `cd <project>` line).
func TestE2E_TwoProjectsShareHooks(t *testing.T) {
	w := newWorld(t)
	repo := w.newClone("mono")
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(repo.dir, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		(&clone{w: w, dir: dir}).lore("init", "--non-interactive", "--name="+name)
	}
	(&clone{w: w, dir: filepath.Join(repo.dir, "a")}).lore("memory", "list")
	b, err := os.ReadFile(filepath.Join(repo.dir, ".git", "hooks", "pre-commit"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `cd "a"`) || !strings.Contains(s, `cd "b"`) {
		t.Fatalf("both projects need their own pre-commit block:\n%s", s)
	}
}

// Audit trail: lore writes are logged and chained; a row edited behind
// lore's back is caught by `audit verify` (which runs no sync pass), and is
// recorded as an external write once any lore command runs; editing a past
// audit entry breaks the chain.
func TestE2E_AuditVerifyAndLog(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	c.lore("init", "--non-interactive", "--name=app")
	mem := c.addMemory("audited")
	c.lore("audit", "verify")
	log := c.lore("audit", "log", "--json").stdout
	if !strings.Contains(log, `"memories.write"`) || !strings.Contains(log, mem) {
		t.Fatalf("write not logged: %s", log)
	}
	db := filepath.Join(c.dir, ".lore", "lore.db")
	tamper := func(sqlText string) {
		t.Helper()
		r := w.run(c.dir, nil, "python3", "-c",
			"import sqlite3,sys;c=sqlite3.connect(sys.argv[1]);c.execute(sys.argv[2]);c.commit()", db, sqlText)
		if r.code != 0 {
			t.Fatalf("tamper: %s", r.stderr)
		}
	}
	tamper("UPDATE memories SET body = 'HACKED' WHERE id = '" + mem + "'")
	if r := c.loreAny("audit", "verify"); r.code == 0 || !strings.Contains(r.stdout+r.stderr, "hash mismatch") {
		t.Fatalf("tamper not detected: %d %s %s", r.code, r.stdout, r.stderr)
	}
	c.lore("memory", "list") // any command adopts the change, as an external write
	if log := c.lore("audit", "log", "--json").stdout; !strings.Contains(log, `"memories.external-write"`) {
		t.Fatalf("external write not recorded: %s", log)
	}
	if out := c.lore("audit", "verify").stdout; !strings.Contains(out, "outside lore and later recorded") {
		t.Fatalf("verify must surface recorded external writes: %s", out)
	}
	tamper("UPDATE audit_logs SET action = 'forged' WHERE rowid = 1")
	if r := c.loreAny("audit", "verify", "--json"); r.code == 0 || !strings.Contains(r.stdout, "chain_broken_at") {
		t.Fatalf("forged entry not detected: %d %s", r.code, r.stdout)
	}
}

// init refuses only a real project: a .lore/ holding leftovers (an empty
// state/ dir) must not block it; an existing lore.db must.
func TestE2E_InitRefusesOnlyRealProject(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	if err := os.MkdirAll(filepath.Join(c.dir, ".lore", "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.lore("init", "--non-interactive", "--name=app")
	if r := c.loreAny("init", "--non-interactive", "--name=again"); r.code == 0 || !strings.Contains(r.stderr, "E_ALREADY_INITIALIZED") {
		t.Fatalf("second init must refuse: %d %s", r.code, r.stderr)
	}
}

// init must not append ignore lines git already covers (a broader `.lore/`).
func TestE2E_InitRespectsBroaderIgnore(t *testing.T) {
	w := newWorld(t)
	c := w.newClone("solo")
	if err := os.WriteFile(filepath.Join(c.dir, ".gitignore"), []byte(".lore/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.lore("init", "--non-interactive", "--name=app")
	b, err := os.ReadFile(filepath.Join(c.dir, ".gitignore"))
	if err != nil || string(b) != ".lore/\n" {
		t.Fatalf("redundant ignore lines appended: %q %v", b, err)
	}
}

// Found migrating a real repo: its .gitignore had `_*`, so `git add` silently
// dropped .lore/data/_meta.json and a teammate's fresh clone did not
// recognise the folder (E_NOT_PROJECT_ROOT). Lore re-includes its data.
func TestE2E_RepoIgnoreRuleCannotHideData(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	if err := os.WriteFile(filepath.Join(alice.dir, ".gitignore"), []byte("_*\n*.json\nsnapshots/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alice.commitAll("repo ignore rules")
	r := alice.lore("init", "--non-interactive", "--name=app")
	if !strings.Contains(r.stderr, "re-included .lore/data") {
		t.Fatalf("re-include not reported: %s", r.stderr)
	}
	alice.addMemory("survives a broad ignore rule")
	alice.commitAll("lore")
	if out := alice.git("ls-files", ".lore/data/_meta.json"); out == "" {
		t.Fatal("_meta.json was not committed")
	}
	alice.git("push", "-q")

	bob := w.newClone("bob")
	if out := bob.memories(); !strings.Contains(out, "survives a broad ignore rule") {
		t.Fatalf("fresh clone did not import: %s", out)
	}
	if r := bob.lore("sync", "install-git"); !strings.Contains(r.stdout, "gitignore:   changed=false") {
		t.Fatalf("bob must not need another .gitignore change: %s", r.stdout)
	}
}

// When the whole .lore/ folder is ignored a negation cannot help: lore leaves
// .gitignore alone, says so once (it is often deliberate), and install-git
// fails naming the rule.
func TestE2E_IgnoredLoreFolderIsReported(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	if err := os.WriteFile(filepath.Join(alice.dir, ".gitignore"), []byte(".lore/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	alice.commitAll("ignore everything under .lore")
	r := alice.lore("init", "--non-interactive", "--name=app")
	if !strings.Contains(r.stderr, "git ignores lore data") || !strings.Contains(r.stderr, ".gitignore:1: .lore/") {
		t.Fatalf("ignored data not reported with its rule: %s", r.stderr)
	}
	if got, err := os.ReadFile(filepath.Join(alice.dir, ".gitignore")); err != nil || string(got) != ".lore/\n" {
		t.Fatalf("a re-include that cannot help must not be written: %q %v", got, err)
	}
	if r := alice.lore("memory", "list"); strings.Contains(r.stderr, "git ignores lore data") {
		t.Fatalf("warning must not repeat while nothing changed: %s", r.stderr)
	}
	r = alice.loreAny("sync", "install-git")
	if r.code == 0 || !strings.Contains(r.stderr+r.stdout, "E_SYNC_DATA_IGNORED") {
		t.Fatalf("install-git must fail: %d %s %s", r.code, r.stdout, r.stderr)
	}
	// Editing the rule re-runs the check; fixing it clears the warning.
	if err := os.WriteFile(filepath.Join(alice.dir, ".gitignore"), []byte(".lore/\n# still private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := alice.lore("memory", "list"); !strings.Contains(r.stderr, "git ignores lore data") {
		t.Fatalf("an edited .gitignore must be re-checked: %s", r.stderr)
	}
	if err := os.WriteFile(filepath.Join(alice.dir, ".gitignore"), []byte(".lore/lore.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := alice.lore("memory", "list"); strings.Contains(r.stderr, "git ignores lore data") {
		t.Fatalf("still warning after the fix: %s", r.stderr)
	}
	if r := alice.lore("sync", "install-git"); r.code != 0 {
		t.Fatalf("install-git after the fix: %s", r.stderr)
	}
}

// Found migrating a real repo: `memory show` dereferenced the optional
// valid_until, so it crashed on every memory without one (all 550 there),
// and the run commands did the same with an optional started_at.
func TestE2E_ShowCommandsWithOptionalTimesUnset(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	mem := alice.addMemory("no valid-until on this one")
	if r := alice.lore("memory", "show", mem); !strings.Contains(r.stdout, "no valid-until on this one") || strings.Contains(r.stdout, "valid-until:") {
		t.Fatalf("memory show: %s", r.stdout)
	}
	run := idPattern.FindString(alice.lore("run", "start", "--goal", "check").stdout)
	if run == "" {
		t.Fatal("run start printed no id")
	}
	db := dbent.InitDB(filepath.Join(alice.dir, ".lore", "lore.db"))
	if _, err := db.Exec(`UPDATE runs SET started_at = NULL WHERE id = ?`, run); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if r := alice.lore("run", "show", run); strings.Contains(r.stdout, "started:") {
		t.Fatalf("run show printed a start time it does not have: %s", r.stdout)
	}
	if r := alice.lore("run", "end", run, "--outcome", "success"); !strings.Contains(r.stdout, "start time unknown") {
		t.Fatalf("run end: %s", r.stdout)
	}
}

// ciMergeWorld: alice owns main, bob works on feature; both start from one
// shared memory. Returns the clones and the memory id.
func ciMergeWorld(t *testing.T) (*world, *clone, *clone, string) {
	t.Helper()
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	mem := alice.addMemory("v1")
	alice.commitAll("lore")
	alice.git("push", "-q")
	bob := w.newClone("bob")
	bob.memories()
	bob.git("checkout", "-q", "-b", "feature")
	return w, alice, bob, mem
}

func (c *clone) ciMerge(base string) ciMergeResult {
	c.w.t.Helper()
	r := c.lore("sync", "ci-merge", "--base", base, "--json")
	var env struct {
		Data ciMergeResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		c.w.t.Fatalf("ci-merge output: %v\n%s", err, r.stdout)
	}
	return env.Data
}

// Different fields of one row on two branches: the host sees a conflict,
// ci-merge settles it with lore's driver and commits a merge.
func TestE2E_CIMergeSettlesLoreConflict(t *testing.T) {
	_, alice, bob, mem := ciMergeWorld(t)
	bob.lore("memory", "edit", mem, "--kind", "procedural")
	bob.commitAll("bob: kind")
	bob.git("push", "-q", "-u", "origin", "feature")
	alice.lore("memory", "edit", mem, "--body", "v2 from main")
	alice.commitAll("alice: body")
	alice.git("push", "-q")

	bob.git("fetch", "-q")
	head := bob.git("rev-parse", "HEAD")
	res := bob.ciMerge("origin/main")
	if res.Outcome != ciMergeMerged || res.Commit == "" || len(res.Merged) == 0 {
		t.Fatalf("result = %+v", res)
	}
	doc := bob.readDoc("memories", mem)
	if doc["body"] != "v2 from main" || doc["kind"] != "procedural" {
		t.Fatalf("both edits must survive: body=%v kind=%v", doc["body"], doc["kind"])
	}
	if parents := strings.Fields(bob.git("log", "-1", "--format=%P")); len(parents) != 2 || parents[0] != head {
		t.Fatalf("not a merge on top of the branch: %v", parents)
	}
	if out := bob.git("status", "--porcelain"); out != "" {
		t.Fatalf("left changes behind: %s", out)
	}
	if again := bob.ciMerge("origin/main"); again.Outcome != ciMergeUpToDate {
		t.Fatalf("second run = %+v", again)
	}
}

// The same text on both sides, or a conflict outside lore data: nothing is
// changed and the blocking paths are named.
func TestE2E_CIMergeLeavesHumanConflictsAlone(t *testing.T) {
	t.Run("same text", func(t *testing.T) {
		_, alice, bob, mem := ciMergeWorld(t)
		bob.lore("memory", "edit", mem, "--body", "bob's text")
		bob.commitAll("bob")
		alice.lore("memory", "edit", mem, "--body", "alice's text")
		alice.commitAll("alice")
		alice.git("push", "-q")
		bob.git("fetch", "-q")
		head := bob.git("rev-parse", "HEAD")
		res := bob.ciMerge("origin/main")
		if res.Outcome != ciMergeNeedsHuman || len(res.Blocking) != 1 || !strings.Contains(res.Blocking[0], mem) {
			t.Fatalf("result = %+v", res)
		}
		if bob.git("rev-parse", "HEAD") != head || bob.git("status", "--porcelain") != "" {
			t.Fatal("a needs-human run changed the branch")
		}
	})
	t.Run("code conflict", func(t *testing.T) {
		_, alice, bob, _ := ciMergeWorld(t)
		if err := os.WriteFile(filepath.Join(bob.dir, "README.md"), []byte("# bob\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		bob.commitAll("bob readme")
		if err := os.WriteFile(filepath.Join(alice.dir, "README.md"), []byte("# alice\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		alice.commitAll("alice readme")
		alice.git("push", "-q")
		bob.git("fetch", "-q")
		res := bob.ciMerge("origin/main")
		if res.Outcome != ciMergeNeedsHuman || strings.Join(res.Blocking, ",") != "README.md" {
			t.Fatalf("result = %+v", res)
		}
		if _, err := os.Stat(filepath.Join(bob.dir, ".git", "MERGE_HEAD")); err == nil {
			t.Fatal("left a merge in progress")
		}
	})
}

func TestE2E_CIMergeCleanAndGuards(t *testing.T) {
	_, alice, bob, _ := ciMergeWorld(t)
	bob.addMemory("only on feature")
	bob.commitAll("bob")
	alice.addMemory("only on main")
	alice.commitAll("alice")
	alice.git("push", "-q")
	bob.git("fetch", "-q")
	if res := bob.ciMerge("origin/main"); res.Outcome != ciMergeClean {
		t.Fatalf("new rows on both sides must be a clean merge: %+v", res)
	}
	if err := os.WriteFile(filepath.Join(bob.dir, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := bob.loreAny("sync", "ci-merge", "--base", "origin/main"); r.code == 0 {
		t.Fatal("ci-merge must refuse a dirty checkout")
	}
	if r := bob.loreAny("sync", "ci-merge"); r.code == 0 {
		t.Fatal("--base is required")
	}
}

// Runs the merge step of the generated workflow — the real shell script —
// against a local origin, with a stand-in `gh` answering for pull request #7.
func TestE2E_InstalledWorkflowScriptMergesPullRequest(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed (GitHub runners have it)")
	}
	w, alice, bob, mem := ciMergeWorld(t)
	bob.lore("memory", "edit", mem, "--kind", "procedural")
	bob.commitAll("bob")
	bob.git("push", "-q", "-u", "origin", "feature")
	alice.lore("memory", "edit", mem, "--body", "v2 from main")
	alice.commitAll("alice")
	alice.git("push", "-q")

	gen := alice.lore("sync", "install-action", "--dry-run", "--lore-version", "v0.1.10")
	var doc workflowDoc
	if err := yaml.Unmarshal([]byte(gen.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	script := ""
	for _, s := range doc.Jobs["merge"].Steps {
		if strings.Contains(s.Run, "lore sync ci-merge") {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("merge step not found")
	}

	// The CI checkout: a fresh clone (on main), like actions/checkout.
	ci := w.newClone("ci")
	stub := t.TempDir()
	gh := "#!/bin/sh\ncase \"$*\" in\n  *headRefName*) echo feature ;;\n  *baseRefName*) echo main ;;\n  *'pr list'*) echo 7 ;;\n  *) echo \"unexpected gh $*\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(stub, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(t.TempDir(), "summary.md")
	env := []string{
		"PATH=" + stub + string(os.PathListSeparator) + filepath.Dir(e2eBin) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"EVENT=pull_request", "PR=7", "GITHUB_STEP_SUMMARY=" + summary,
	}
	r := w.run(ci.dir, env, "bash", "-e", "-o", "pipefail", "-c", script)
	if r.code != 0 {
		t.Fatalf("workflow script failed: %d\n%s\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "#7 (feature <- main): "+string(ciMergeMerged)) {
		t.Fatalf("unexpected run: %s", r.stdout)
	}
	if got, _ := os.ReadFile(summary); !strings.Contains(string(got), "#7: merged") {
		t.Fatalf("summary = %q", got)
	}
	// The pushed branch now merges cleanly into main.
	bob.git("fetch", "-q")
	bob.git("reset", "-q", "--hard", "origin/feature")
	if res := bob.ciMerge("origin/main"); res.Outcome != ciMergeUpToDate {
		t.Fatalf("pushed branch not up to date with main: %+v", res)
	}
	if doc := bob.readDoc("memories", mem); doc["body"] != "v2 from main" || doc["kind"] != "procedural" {
		t.Fatalf("pushed merge lost an edit: %v", doc)
	}
}

// The playbooks pipe `mission add --json` and `mission show --json` into jq
// (.data.id, .data.tasks, .data.target_date); both flags were documented but
// did not exist, and an invalid --target was silently dropped.
func TestE2E_MissionJSONForScripts(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	var added struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	r := alice.lore("mission", "add", "Ship v0.2", "--target=2026-06-15", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &added); err != nil || !strings.HasPrefix(added.Data.ID, "msn_") || added.Data.Title != "Ship v0.2" {
		t.Fatalf("mission add --json: %v %s", err, r.stdout)
	}
	var tl struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	r = alice.lore("tasklist", "add", "--title=Sprint", "--body=current sprint", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &tl); err != nil || !strings.HasPrefix(tl.Data.ID, "tlt_") {
		t.Fatalf("tasklist add --json: %v %s", err, r.stdout)
	}
	alice.lore("task", "add", "first", "--tasklist="+tl.Data.ID, "--mission="+added.Data.ID)
	alice.lore("task", "add", "second", "--tasklist="+tl.Data.ID, "--mission="+added.Data.ID)
	var shown struct {
		Data struct {
			Title      string `json:"title"`
			Status     string `json:"status"`
			TargetDate string `json:"target_date"`
			Tasks      []struct {
				Status string `json:"status"`
			} `json:"tasks"`
		} `json:"data"`
	}
	r = alice.lore("mission", "show", added.Data.ID, "--json")
	if err := json.Unmarshal([]byte(r.stdout), &shown); err != nil {
		t.Fatalf("mission show --json: %v %s", err, r.stdout)
	}
	if shown.Data.Title != "Ship v0.2" || shown.Data.Status == "" || !strings.HasPrefix(shown.Data.TargetDate, "2026-06-15") || len(shown.Data.Tasks) != 2 || shown.Data.Tasks[0].Status == "" {
		t.Fatalf("mission show --json payload: %+v", shown.Data)
	}
	if r := alice.loreAny("mission", "add", "bad date", "--target=15/06/2026"); r.code == 0 {
		t.Fatal("an invalid --target must be refused, not dropped")
	}
}

// buildReleaseLore builds the CLI stamped as release ver (the e2e binary is a
// development build, which never refreshes a workflow pin).
func buildReleaseLore(t *testing.T, ver string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), BinaryName)
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+ver, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release lore: %v\n%s", err, out)
	}
	return bin
}

// An ordinary command of a newer lore release moves an auto-pinned workflow
// to that release and says so; a read-only command writes nothing.
func TestE2E_WorkflowPinFollowsLoreUpgrade(t *testing.T) {
	w := newWorld(t)
	alice := w.newClone("alice")
	alice.lore("init", "--non-interactive", "--name=app")
	alice.lore("sync", "install-action", "--branch", "main")
	file := filepath.Join(alice.dir, filepath.FromSlash(syncActionFileRel("")))
	alice.commitAll("lore + workflow")
	release := buildReleaseLore(t, "0.1.99")

	// Read-only first: nothing may be written, not even git wiring.
	if err := os.Remove(filepath.Join(alice.dir, ".gitattributes")); err != nil {
		t.Fatal(err)
	}
	r := w.run(alice.dir, nil, release, "memory", "list", "--read-only")
	if r.code != 0 {
		t.Fatalf("read-only list: %s", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(alice.dir, ".gitattributes")); err == nil {
		t.Fatal("a read-only command rewrote .gitattributes")
	}
	if strings.Contains(readFileE2E(t, file), "v0.1.99") {
		t.Fatal("a read-only command refreshed the workflow")
	}

	r = w.run(alice.dir, nil, release, "memory", "list")
	if r.code != 0 || !strings.Contains(r.stderr, "to lore v0.1.99") || !strings.Contains(r.stderr, "commit it") {
		t.Fatalf("upgrade not reported: %d %s", r.code, r.stderr)
	}
	s, err := parseSyncAction(readFileE2E(t, file))
	if err != nil || s.version != "v0.1.99" || s.pin != syncActionPinAuto {
		t.Fatalf("workflow after upgrade: %+v %v", s, err)
	}
	// The development build (older by definition here) never downgrades.
	alice.lore("memory", "list")
	if s, _ := parseSyncAction(readFileE2E(t, file)); s.version != "v0.1.99" {
		t.Fatalf("downgraded to %s", s.version)
	}
}

func readFileE2E(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
