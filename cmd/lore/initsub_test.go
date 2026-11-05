package main

// OWNERQ-1: a monorepo gets one lore project per sub-folder `lore init` ran
// in (initsub.go). Pure helpers first, then the release binary's command
// path in process (run), as a person.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/model/errs"
)

// writeMeta writes a minimal .lore/data/_meta.json under dir.
func writeMeta(t *testing.T, dir string, m map[string]string) {
	t.Helper()
	d := filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir)
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, rowfs.MetaFileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(r)), 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRootInitAt: every state of <wd>/.lore before init.
func TestRootInitAt(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, wd string)
		wantCode errs.Code
		created  bool
	}{
		{"no .lore: created", func(*testing.T, string) {}, "", true},
		{".lore folder exists: kept", func(t *testing.T, wd string) { mkdirs(t, wd, ".lore") }, "", false},
		{".lore is a file: refused", func(t *testing.T, wd string) {
			if err := os.WriteFile(filepath.Join(wd, ".lore"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, errs.InvalidInput, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wd := t.TempDir()
			c.setup(t, wd)
			undo, err := rootInitAt(wd)
			if c.wantCode != "" {
				if !errs.Has(err, c.wantCode) {
					t.Fatalf("want %s, got %v", c.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fi, serr := os.Stat(filepath.Join(wd, ".lore")); serr != nil || !fi.IsDir() {
				t.Fatalf(".lore must be a folder after rootInitAt: %v", serr)
			}
			undo()
			_, serr := os.Stat(filepath.Join(wd, ".lore"))
			if gone := os.IsNotExist(serr); gone != c.created {
				t.Fatalf("after undo, .lore removed = %v, want %v (only a folder rootInitAt made goes)", gone, c.created)
			}
		})
	}
	t.Run("undo keeps a folder init wrote into", func(t *testing.T) {
		wd := t.TempDir()
		undo, err := rootInitAt(wd)
		if err != nil {
			t.Fatal(err)
		}
		mkdirs(t, wd, ".lore/data")
		undo()
		if _, err := os.Stat(filepath.Join(wd, ".lore", "data")); err != nil {
			t.Fatalf("undo removed a folder that holds files: %v", err)
		}
	})
}

// TestAncestorRepoMeta: where the repo id of a new sub-project comes from.
func TestAncestorRepoMeta(t *testing.T) {
	const topID, midID = "rep_01a1acc07dde78bf85b8873dc9d1e16d", "rep_01a1acc07dde78bf85b8873dc9d1e16e"
	cases := []struct {
		name  string
		git   bool              // a .git folder at the root
		metas map[string]string // folder -> repo_id ("" = a _meta.json with no repo_id)
		wd    string
		want  string // "" = none
	}{
		{"repo top itself", true, map[string]string{"": topID}, "", ""},
		{"direct sub-folder", true, map[string]string{"": topID}, "web", topID},
		{"nested sub-folder", true, map[string]string{"": topID}, "services/billing/api", topID},
		{"nearer project wins", true, map[string]string{"": topID, "services": midID}, "services/billing", midID},
		{"project above has no repo id yet", true, map[string]string{"": ""}, "web", ""},
		{"no project above", true, nil, "web", ""},
		{"not in a git checkout", false, map[string]string{"": topID}, "web", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			outer := root
			root = filepath.Join(root, "repo")
			mkdirs(t, root, c.wd)
			if c.git {
				mkdirs(t, root, ".git")
			} else {
				writeMeta(t, outer, map[string]string{"repo_id": topID}) // above, but no checkout joins them
			}
			for f, id := range c.metas {
				m := map[string]string{}
				if id != "" {
					m["repo_id"] = id
				}
				writeMeta(t, filepath.Join(root, filepath.FromSlash(f)), m)
			}
			got, ok := ancestorRepoMeta(filepath.Join(root, filepath.FromSlash(c.wd)))
			if ok != (c.want != "") || got.RepoID != c.want {
				t.Fatalf("ancestorRepoMeta = %q, %v; want %q", got.RepoID, ok, c.want)
			}
		})
	}
}

// metaOf reads dir's _meta.json repo_id and project_id.
func metaOf(t *testing.T, dir string) (repoID, projectID string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, rowfs.LoreDir, rowfs.DataDir, rowfs.MetaFileName)))
	if err != nil {
		t.Fatalf("%s: %v", dir, err)
	}
	var m struct {
		RepoID    string `json:"repo_id"`
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m.RepoID, m.ProjectID
}

// memoryFiles are the memory row files under dir/.lore/data.
func memoryFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, ".lore", "data", "memories", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// mustRun runs lore in dir and fails on a non-zero exit.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	t.Chdir(dir)
	code, out, errOut := runCaptured(t, args...)
	if code != 0 {
		t.Fatalf("%s: lore %s: exit %d\nstdout: %s\nstderr: %s", dir, strings.Join(args, " "), code, out, errOut)
	}
	return out
}

// TestMonorepoProjectPerSubfolder: `lore init` in sub-folders of an
// initialised repo makes one project per folder (nested folders too), with
// the repo's one repo id; every command resolves the nearest project at or
// above the working folder, so each project writes and lists only its own
// rows; a folder with no project of its own uses the one above it; a
// renamed project folder keeps its project; init again refuses; a fresh
// checkout of the folder adopts its project.
func TestMonorepoProjectPerSubfolder(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t)
	projects := []string{".", "web", "services/billing"}
	mkdirs(t, root, "web/src/deep", "services/billing", "docs")
	for _, p := range projects[1:] {
		out := mustRun(t, filepath.Join(root, filepath.FromSlash(p)), "init", "--name", filepath.Base(p))
		if !strings.Contains(out, "lore initialized") {
			t.Fatalf("%s: init must make a new project, not adopt the parent's:\n%s", p, out)
		}
	}
	topRepo, _ := metaOf(t, root)
	pids := map[string]bool{}
	for _, p := range projects {
		dir := filepath.Join(root, filepath.FromSlash(p))
		rid, pid := metaOf(t, dir)
		if rid != topRepo {
			t.Fatalf("%s: repo_id %s, want the repo's one id %s (PROVENANCE 11.6)", p, rid, topRepo)
		}
		if pid == "" || pids[pid] {
			t.Fatalf("%s: project_id %q is missing or shared", p, pid)
		}
		pids[pid] = true
		mustRun(t, dir, "memory", "add", "--body", "note of "+p)
	}
	// where a command runs -> the project whose note it lists
	where := []struct{ dir, project string }{
		{".", "."}, {"docs", "."}, {"web", "web"}, {"web/src/deep", "web"}, {"services", "."}, {"services/billing", "services/billing"},
	}
	for _, w := range where {
		out := mustRun(t, filepath.Join(root, filepath.FromSlash(w.dir)), "memory", "list")
		for _, p := range projects {
			if has := strings.Contains(out, "note of "+p); has != (p == w.project) {
				t.Fatalf("memory list in %s: lists %q = %v (each project reads only its own folder)\n%s", w.dir, p, has, out)
			}
		}
	}
	for _, p := range projects {
		if n := len(memoryFiles(t, filepath.Join(root, filepath.FromSlash(p)))); n != 1 {
			t.Fatalf("%s: %d memory row files, want its own one", p, n)
		}
	}
	for _, nested := range []string{"docs", "web/src", "web/src/deep", "services"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(nested), ".lore")); !os.IsNotExist(err) {
			t.Fatalf("%s: a command made a .lore in a folder with no project (%v)", nested, err)
		}
	}

	t.Run("init again refuses", func(t *testing.T) {
		t.Chdir(filepath.Join(root, "web"))
		if code, out, errOut := runCaptured(t, "init", "--name", "web"); code == 0 || !strings.Contains(out+errOut, "E_ALREADY_INITIALIZED") {
			t.Fatalf("second init in web: exit %d\n%s\n%s", code, out, errOut)
		}
	})

	t.Run("renamed project folder", func(t *testing.T) {
		if err := os.Rename(filepath.Join(root, "web"), filepath.Join(root, "site")); err != nil {
			t.Fatal(err)
		}
		site := filepath.Join(root, "site")
		out := mustRun(t, filepath.Join(site, "src"), "memory", "list")
		if !strings.Contains(out, "note of web") || strings.Contains(out, "note of .") {
			t.Fatalf("a renamed folder keeps its project:\n%s", out)
		}
		mustRun(t, site, "memory", "add", "--body", "after the rename")
		if n := len(memoryFiles(t, site)); n != 2 {
			t.Fatalf("the write after a rename goes to the folder's own project: %d files", n)
		}
	})

	t.Run("fresh checkout adopts the sub-project", func(t *testing.T) {
		billing := filepath.Join(root, "services", "billing")
		_, pid := metaOf(t, billing)
		for _, f := range localStateFiles { // what a clone does not carry (all gitignored)
			_ = os.Remove(filepath.Join(billing, ".lore", f))
		}
		out := mustRun(t, billing, "init")
		if !strings.Contains(out, "adopted") || !strings.Contains(out, pid) {
			t.Fatalf("init in a fresh checkout of a sub-project adopts it (%s):\n%s", pid, out)
		}
		if got := mustRun(t, billing, "memory", "list"); !strings.Contains(got, "note of services/billing") {
			t.Fatalf("the adopted project lists its rows:\n%s", got)
		}
	})
}

// TestInitSubfolderNoProjectAbove: with no project anywhere in the repo,
// init in a sub-folder roots the project there (v0.1.17: the project root is
// the working folder), and the repo top stays free.
func TestInitSubfolderNoProjectAbove(t *testing.T) {
	asPerson(t)
	root := envScopeRepo(t) // initialises the top; start over in a new repo below
	repo := filepath.Join(root, "other")
	mkdirs(t, repo, "app/sub")
	t.Chdir(repo)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	cmd := &exec.Cmd{Path: git, Args: []string{git, "-C", repo, "init", "-q", "-b", "main"}} // the envScopeRepo shape
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	mustRun(t, filepath.Join(repo, "app"), "init", "--name", "app")
	if _, err := os.Stat(filepath.Join(repo, ".lore", "data")); !os.IsNotExist(err) {
		t.Fatalf("init in app must not make a project at the repo top (%v)", err)
	}
	if rid, pid := metaOf(t, filepath.Join(repo, "app")); rid == "" || pid == "" {
		t.Fatalf("app: repo_id %q project_id %q", rid, pid)
	}
	// the project's own files sit in its folder, as v0.1.17 wrote them
	for _, f := range []string{".gitignore", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repo, "app", f)); err != nil {
			t.Fatalf("app/%s: %v", f, err)
		}
	}
	for _, f := range []string{".gitignore", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(repo, f)); !os.IsNotExist(err) {
			t.Fatalf("init in app wrote %s at the repo top (%v)", f, err)
		}
	}
}
