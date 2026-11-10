//go:build userscen

package userscen

// migrate_init_test.go — `lore init` / `lore repo init` (ops/repoinit) and a
// migration in a client repo, the way a user starts:
//
//	TestMigrateInitFresh          a fresh clone: the text names project, name (from
//	                              origin), repo id and the next step; _meta.json declares
//	                              the 0.1.18 format; lore's local files are git-ignored;
//	                              `lore repo init --json` is the same operation
//	TestMigrateInitTwice          a second init (and `repo init`) is E_ALREADY_INITIALIZED
//	                              exit 23 naming the same project, and changes nothing
//	TestMigrateInitNotGit         outside a git work tree: E_NOT_A_STORE exit 19, no .lore
//	TestMigrateInitOddNames       folder names with spaces, unicode and great length, and
//	                              --name spellings (mixed case + spaces, unicode, empty):
//	                              the name lore stores is the one it prints and lists
//	TestMigrateInitAdoptAndMonorepo
//	                              a teammate's fresh clone adopts the shared project (same
//	                              id, nothing minted); a second project in a subfolder gets
//	                              its own id and the checkout's one repo id
//	TestMigrateClientRepo         a client repo linked to the org's store: v0.1.17 rows are
//	                              migrated, the team lock lives on the store, and nothing
//	                              of lore's coordination reaches the client's remote

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// migExitAlreadyInit is E_ALREADY_INITIALIZED's exit (class refused).
const migExitAlreadyInit = th.ExitRefused

// migInitRe reads the three lines of a fresh init.
var migInitRe = regexp.MustCompile(`lore initialized\n    project_id: (prj_[0-9a-f]{32})\n    name:       (.*)\n    repo_id:    (rep_[0-9a-f]{32})\n`)

// migInitOut is `lore repo init --json` (ops/repoinit InitOut).
type migInitOut struct {
	Path      string   `json:"path"`
	Adopted   bool     `json:"adopted"`
	ProjectID string   `json:"project_id"`
	Name      string   `json:"name"`
	RepoID    string   `json:"repo_id"`
	ToolLines string   `json:"tool_lines"`
	Warnings  []string `json:"warnings"`
}

// migInit runs `lore init` (plus args) in dir and returns project id, name
// and repo id it printed.
func migInit(t *testing.T, a *th.Actor, dir string, args ...string) (string, string, string) {
	t.Helper()
	r := a.Lore(dir, append([]string{"init"}, args...)...).OK(t)
	m := migInitRe.FindStringSubmatch(r.Stdout)
	if m == nil {
		t.Fatalf("init printed no project/name/repo lines:\n%s", r)
	}
	r.Says(t, "next: lore render")
	return m[1], m[2], m[3]
}

// migMeta is .lore/data/_meta.json.
type migMeta struct {
	V              int      `json:"_v"`
	Features       []string `json:"features"`
	MinLoreVersion string   `json:"min_lore_version"`
	ProjectID      string   `json:"project_id"`
	RepoID         string   `json:"repo_id"`
}

// migReadMeta decodes the _meta.json under dir.
func migReadMeta(t *testing.T, dir string) migMeta {
	t.Helper()
	var m migMeta
	if err := json.Unmarshal(migRead(t, filepath.Join(dir, migMetaPath)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// migGitRepo makes an empty git repo at <World>/work/<name> (no remote).
func migGitRepo(t *testing.T, w *th.World, a *th.Actor, name string) string {
	t.Helper()
	dir := filepath.Join(w.Root, "work", name)
	a.Lore(dir, "version").OK(t) // creates the folder
	w.Git(dir, "init", "-q", "-b", "main")
	return dir
}

func TestMigrateInitFresh(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	c := alice.Clone(w.Remote("github.com/octo-personal/Shop-API"), "checkout")
	pid, name, rid := migInit(t, alice, c.Dir, "--non-interactive")
	if name != "shop-api" {
		t.Fatalf("name %q, want origin's repo name lower-cased (shop-api)", name)
	}
	m := migReadMeta(t, c.Dir)
	if m.V != 2 || m.ProjectID != pid || m.RepoID != rid || m.MinLoreVersion != "0.1.18" || len(m.Features) == 0 {
		t.Fatalf("_meta.json %+v, want _v 2, the printed ids and the 0.1.18 floor", m)
	}
	ign, _ := c.ReadFile(".gitignore")
	if !strings.Contains(ign, "lore-cache.db") {
		t.Fatalf(".gitignore does not ignore lore's cache:\n%s", ign)
	}
	if strings.Contains(c.Status(), "lore-cache.db") {
		t.Fatalf("lore's cache shows in git status:\n%s", c.Status())
	}
	c.Lore("project", "show", pid).OK(t).Says(t, name)

	// `lore repo init` is the same operation (fresh folder, --json).
	d2 := migGitRepo(t, w, alice, "second")
	got := th.DecodeJSON[migInitOut](t, alice.Lore(d2, "repo", "init", "--name", "Second", "--json"))
	if got.Adopted || got.Name != "second" || !strings.HasPrefix(got.ProjectID, "prj_") || !strings.HasPrefix(got.RepoID, "rep_") || got.Path != d2 {
		t.Fatalf("repo init --json: %+v", got)
	}
	if m2 := migReadMeta(t, d2); m2.ProjectID != got.ProjectID {
		t.Fatalf("repo init wrote project %s, printed %s", m2.ProjectID, got.ProjectID)
	}
}

func TestMigrateInitTwice(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	dir := migGitRepo(t, w, alice, "app")
	pid, _, _ := migInit(t, alice, dir)
	before := migSnapshotDir(t, dir)
	for _, args := range [][]string{{"init"}, {"init", "--name", "other"}, {"repo", "init"}} {
		alice.Lore(dir, args...).Fails(t, migExitAlreadyInit, "[E_ALREADY_INITIALIZED] a lore project already exists here ("+pid+")",
			"hint: nothing to do: the project is ready; lore doctor checks it")
	}
	migSameSnapshot(t, "after a second init", migSnapshotDir(t, dir), before)
	if m := migReadMeta(t, dir); m.ProjectID != pid {
		t.Fatalf("a refused init changed the project to %s", m.ProjectID)
	}
}

// migSnapshotDir reads every file under dir/.lore/data plus the tool files
// init writes, keyed by relative path.
func migSnapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := migSnapshot(t, &th.Clone{Dir: dir})
	for _, f := range []string{".gitignore", "AGENTS.md"} {
		if b, err := readIfExists(filepath.Join(dir, f)); err == nil {
			snap["../../"+f] = b
		}
	}
	return snap
}

func TestMigrateInitNotGit(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	dir := filepath.Join(w.Root, "plain folder")
	for _, args := range [][]string{{"init"}, {"init", "--name", "x"}, {"repo", "init"}} {
		alice.Lore(dir, args...).Fails(t, th.ExitGuard, "[E_NOT_A_STORE] git: the folder is not inside a git work tree", "hint: "+cliNotAStoreHint)
	}
	if migExists(filepath.Join(dir, ".lore")) || migExists(filepath.Join(dir, "AGENTS.md")) {
		t.Fatal("a refused init left files behind")
	}
}

func TestMigrateInitOddNames(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	long := strings.Repeat("deep-", 40) + "end"
	for _, c := range []struct {
		folder string
		flag   []string
		want   string
	}{
		{"my app", nil, "my app"},
		{"Café Ünï 日本", nil, "café ünï 日本"},
		{long, nil, long},
		{"plain", []string{"--name", "  Mixed Case Name  "}, "mixed case name"},
		{"plain2", []string{"--name", "Ünïcode-日本"}, "ünïcode-日本"},
		{"With Space", []string{"--name="}, "with space"},
	} {
		t.Run(c.want, func(t *testing.T) {
			dir := migGitRepo(t, w, alice, c.folder)
			pid, name, _ := migInit(t, alice, dir, c.flag...)
			if name != c.want {
				t.Fatalf("name %q, want %q", name, c.want)
			}
			alice.Lore(dir, "project", "show", pid).OK(t).Says(t, c.want)
			// The project works: a row goes in and comes back.
			alice.Lore(dir, "memory", "add", "a note in "+c.want, "--no-live").OK(t)
			alice.Lore(dir, "memory", "list").OK(t).Says(t, "a note in ") // list cuts a long body
			if m := migReadMeta(t, dir); m.ProjectID != pid {
				t.Fatalf("_meta.json names %s, init printed %s", m.ProjectID, pid)
			}
		})
	}
}

func TestMigrateInitAdoptAndMonorepo(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	alice := w.Human("alice", "person@example.com")
	app := w.Remote(migSlug)
	c := alice.Clone(app, "app")
	pid, _, rid := migInit(t, alice, c.Dir)
	c.CommitAll("lore init")
	c.AdminPush("origin", "main")

	bob := w.Human("bob", "bob@example.com")
	bc := bob.Clone(app, "app")
	r := bc.Lore("init").OK(t)
	r.Says(t, "✓ adopted the shared lore project at "+bc.Dir, "project_id: "+pid).Never(t, "lore initialized")
	if bc.Status() != "" && strings.Contains(bc.Status(), migMetaPath) {
		t.Fatalf("adopting changed _meta.json:\n%s", bc.Status())
	}
	bc.Lore("init").Fails(t, migExitAlreadyInit, "E_ALREADY_INITIALIZED", pid)

	// A second project in a subfolder: its own id, the checkout's repo id.
	sub := filepath.Join(c.Dir, "services", "billing")
	spid, _, srid := migInit(t, alice, sub, "--name", "billing")
	if spid == pid || srid != rid {
		t.Fatalf("subfolder project %s repo %s; want a new project id and the checkout's repo id %s", spid, srid, rid)
	}
	if m := migReadMeta(t, sub); m.ProjectID != spid || m.RepoID != rid {
		t.Fatalf("subfolder _meta.json %+v", m)
	}
	if m := migReadMeta(t, c.Dir); m.ProjectID != pid {
		t.Fatalf("the root project changed to %s", m.ProjectID)
	}
}

func TestMigrateClientRepo(t *testing.T) {
	o := newOrgScen(t)
	client := o.w.Remote("github.com/clientco/site")
	c := o.mira.Clone(client, "site")
	c.MustLore("init", "--non-interactive", "--name=site")
	c.CommitAll("lore init")
	c.AdminPush("origin", "main")
	c.MustLore("org", "link", orgName, "--project", "site", "--yes")

	k := migKinds()[0]
	k.add = append(k.add, "--to", "repo") // a client repo's default target is the org store
	id := migAdd(t, c, k)
	show := c.Lore(k.noun, "show", id, "--json").OK(t).Stdout
	planted := []migPlanted{{k: k, id: id, show: show, v1: migDowngrade(t, migRowPath(c, k.table, id))}}
	c.CommitAll("v0.1.17 row")
	c.AdminPush("origin", "main")
	clientRefs := client.Refs("refs/lore")

	out := th.DecodeJSON[migRunOut](t, c.Lore("migrate", "run", "--json"))
	// The count also holds the org's personal-store and roster steps (see
	// the migrate report); the planted branch row is the one asserted here.
	if !slices.Contains(out.Rows, k.table+"/"+id) {
		t.Fatalf("client repo run: %+v, want row %s/%s", out, k.table, id)
	}
	if len(out.Journal) == 0 || !strings.Contains(out.Journal[0], "team lock: taken on") || !strings.Contains(out.Journal[0], "of the linked store") {
		t.Fatalf("client repo run: the team lock must live on the linked store: %q", out.Journal)
	}
	migAssertRewritten(t, c, planted)
	migShowsSame(t, "client repo after the run", c, planted)
	if now := client.Refs("refs/lore"); strings.Join(now, ",") != strings.Join(clientRefs, ",") {
		t.Fatalf("the migration wrote lore refs to the client's remote: %v → %v", clientRefs, now)
	}
}

// readIfExists reads path; the error is the read's (missing included).
func readIfExists(path string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	return string(b), err
}
