package main

// OWNERQ-1: a monorepo gets one lore project per sub-folder `lore init` ran
// in (initsub.go). Pure helpers first, then the release binary's command
// path through main in a child process (cliworld_test.go), as a person.

import (
	"encoding/json"
	"errors"
	"os"
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
	// A failed init undoes everything it made (decided 2026-11-08): the
	// cache db, state/audit-v2.jsonl and row files go with the folder.
	t.Run("undo removes what a failed init wrote", func(t *testing.T) {
		wd := t.TempDir()
		undo, err := rootInitAt(wd)
		if err != nil {
			t.Fatal(err)
		}
		mkdirs(t, wd, ".lore/data", ".lore/state")
		if err := os.WriteFile(filepath.Join(wd, ".lore", "lore-cache.db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		undo()
		if _, err := os.Stat(filepath.Join(wd, ".lore")); !os.IsNotExist(err) {
			t.Fatalf("undo left the .lore folder a failed init wrote into: %v", err)
		}
	})
	// A .lore that was there before is never touched by the undo.
	t.Run("undo keeps a .lore that existed", func(t *testing.T) {
		wd := t.TempDir()
		mkdirs(t, wd, ".lore/data")
		undo, err := rootInitAt(wd)
		if err != nil {
			t.Fatal(err)
		}
		undo()
		if _, err := os.Stat(filepath.Join(wd, ".lore", "data")); err != nil {
			t.Fatalf("undo removed a .lore that existed before: %v", err)
		}
	})
}

// TestChooseRepoMeta: the one repo id the projects of a checkout share
// (G-MONO-1), over every shape of the checkout's _meta.json files.
func TestChooseRepoMeta(t *testing.T) {
	const idA, idB = "rep_01a1acc07dde78bf85b8873dc9d1e16d", "rep_01a1acc07dde78bf85b8873dc9d1e16e"
	cases := []struct {
		name    string
		metas   map[string]rowfs.RepoMeta
		want    string // "" = none
		refused bool
	}{
		{"none", nil, "", false},
		{"empty map", map[string]rowfs.RepoMeta{}, "", false},
		{"one at the top", map[string]rowfs.RepoMeta{".": {RepoID: idA}}, idA, false},
		{"one in a sub-folder", map[string]rowfs.RepoMeta{"web": {RepoID: idA}}, idA, false},
		{"same id twice", map[string]rowfs.RepoMeta{".": {RepoID: idA}, "web": {RepoID: idA}}, idA, false},
		{"alias counts as same", map[string]rowfs.RepoMeta{"api": {RepoID: idA, Aliases: []string{idB}}, "web": {RepoID: idB}}, idA, false},
		{"alias counts as same, alias holder sorts last", map[string]rowfs.RepoMeta{"a": {RepoID: idB}, "z": {RepoID: idA, Aliases: []string{idB}}}, idA, false},
		{"two different ids", map[string]rowfs.RepoMeta{".": {RepoID: idA}, "web": {RepoID: idB}}, "", true},
		{"two different ids in sub-folders", map[string]rowfs.RepoMeta{"api": {RepoID: idA}, "web": {RepoID: idB}, "docs": {RepoID: idA}}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok, err := chooseRepoMeta(c.metas)
			if c.refused {
				var e *errs.Error
				if !errs.Has(err, errs.AmbiguousID) || !errors.As(err, &e) {
					t.Fatalf("want E_AMBIGUOUS_ID, got %v", err)
				}
				if !strings.Contains(e.Hint, "lore repo merge-ids") || !strings.Contains(err.Error(), idA) || !strings.Contains(err.Error(), idB) {
					t.Fatalf("the refusal names both ids and the merge-ids hint: %v (hint %q)", err, e.Hint)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ok != (c.want != "") || got.RepoID != c.want {
				t.Fatalf("chooseRepoMeta = %q, %v; want %q", got.RepoID, ok, c.want)
			}
		})
	}
}

// newGitRepo is a fresh git checkout (no lore project) under a test HOME.
func newGitRepo(t *testing.T) (*cliWorld, string) {
	t.Helper()
	w := newCLIWorld(t).person()
	root := w.initRepo(t) // its own repo is not used
	repo := filepath.Join(root, "mono")
	mkdirs(t, root, "mono")
	w.git(t, repo, "init", "-q", "-b", "main")
	return w, repo
}

// TestInitSharesRepoIDAcrossCheckout: whatever order `lore init` runs in a
// checkout's folders, every project carries one repo id; two different ids
// already in the checkout refuse init with nothing written (G-MONO-1).
func TestInitSharesRepoIDAcrossCheckout(t *testing.T) {
	t.Parallel()
	sameID := func(t *testing.T, repo string, folders ...string) {
		t.Helper()
		want, _ := metaOf(t, filepath.Join(repo, filepath.FromSlash(folders[0])))
		if want == "" {
			t.Fatalf("%s: no repo id", folders[0])
		}
		for _, f := range folders[1:] {
			if got, _ := metaOf(t, filepath.Join(repo, filepath.FromSlash(f))); got != want {
				t.Fatalf("%s: repo_id %s, want the checkout's one id %s", f, got, want)
			}
		}
	}
	t.Run("sub first, then top", func(t *testing.T) {
		t.Parallel()
		w, repo := newGitRepo(t)
		mkdirs(t, repo, "web")
		w.mustRun(t, filepath.Join(repo, "web"), "init", "--name", "web")
		w.mustRun(t, repo, "init", "--name", "top")
		sameID(t, repo, "web", ".")
	})
	t.Run("top first, then sub", func(t *testing.T) {
		t.Parallel()
		w, repo := newGitRepo(t)
		mkdirs(t, repo, "web")
		w.mustRun(t, repo, "init", "--name", "top")
		w.mustRun(t, filepath.Join(repo, "web"), "init", "--name", "web")
		sameID(t, repo, ".", "web")
	})
	t.Run("two subs, no top", func(t *testing.T) {
		t.Parallel()
		w, repo := newGitRepo(t)
		mkdirs(t, repo, "web", "services/api")
		w.mustRun(t, filepath.Join(repo, "web"), "init", "--name", "web")
		w.mustRun(t, filepath.Join(repo, "services", "api"), "init", "--name", "api")
		sameID(t, repo, "web", "services/api")
		if _, err := os.Stat(filepath.Join(repo, ".lore", "data")); !os.IsNotExist(err) {
			t.Fatalf("init in sub-folders made a project at the top (%v)", err)
		}
	})
	t.Run("conflicting ids refuse, nothing written", func(t *testing.T) {
		t.Parallel()
		w, repo := newGitRepo(t)
		const idA, idB = "rep_01a1acc07dde78bf85b8873dc9d1e16d", "rep_01a1acc07dde78bf85b8873dc9d1e16e"
		writeMeta(t, filepath.Join(repo, "web"), map[string]string{"repo_id": idA})
		writeMeta(t, filepath.Join(repo, "api"), map[string]string{"repo_id": idB})
		for _, wd := range []string{".", "docs"} {
			dir := filepath.Join(repo, filepath.FromSlash(wd))
			mkdirs(t, repo, wd)
			code, out, errOut := w.lore(t, dir, "init", "--name", "x")
			all := out + errOut
			if code == 0 || !strings.Contains(all, "E_AMBIGUOUS_ID") || !strings.Contains(all, idA) || !strings.Contains(all, idB) || !strings.Contains(all, "merge-ids") {
				t.Fatalf("init in %s with two repo ids: exit %d\n%s", wd, code, all)
			}
			for _, f := range []string{".lore", "AGENTS.md", ".gitignore"} {
				if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
					t.Fatalf("refused init in %s left %s (%v)", wd, f, err)
				}
			}
		}
	})
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
func (w *cliWorld) mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	code, out, errOut := w.lore(t, dir, args...)
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
	t.Parallel()
	w := newCLIWorld(t).person()
	root := w.initRepo(t)
	projects := []string{".", "web", "services/billing"}
	mkdirs(t, root, "web/src/deep", "services/billing", "docs")
	for _, p := range projects[1:] {
		out := w.mustRun(t, filepath.Join(root, filepath.FromSlash(p)), "init", "--name", filepath.Base(p))
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
		w.mustRun(t, dir, "memory", "add", "--body", "note of "+p)
	}
	// where a command runs -> the project whose note it lists
	where := []struct{ dir, project string }{
		{".", "."}, {"docs", "."}, {"web", "web"}, {"web/src/deep", "web"}, {"services", "."}, {"services/billing", "services/billing"},
	}
	for _, at := range where {
		out := w.mustRun(t, filepath.Join(root, filepath.FromSlash(at.dir)), "memory", "list")
		for _, p := range projects {
			if has := strings.Contains(out, "note of "+p); has != (p == at.project) {
				t.Fatalf("memory list in %s: lists %q = %v (each project reads only its own folder)\n%s", at.dir, p, has, out)
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
		if code, out, errOut := w.lore(t, filepath.Join(root, "web"), "init", "--name", "web"); code == 0 || !strings.Contains(out+errOut, "E_ALREADY_INITIALIZED") {
			t.Fatalf("second init in web: exit %d\n%s\n%s", code, out, errOut)
		}
	})

	t.Run("renamed project folder", func(t *testing.T) {
		if err := os.Rename(filepath.Join(root, "web"), filepath.Join(root, "site")); err != nil {
			t.Fatal(err)
		}
		site := filepath.Join(root, "site")
		out := w.mustRun(t, filepath.Join(site, "src"), "memory", "list")
		if !strings.Contains(out, "note of web") || strings.Contains(out, "note of .") {
			t.Fatalf("a renamed folder keeps its project:\n%s", out)
		}
		w.mustRun(t, site, "memory", "add", "--body", "after the rename")
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
		out := w.mustRun(t, billing, "init")
		if !strings.Contains(out, "adopted") || !strings.Contains(out, pid) {
			t.Fatalf("init in a fresh checkout of a sub-project adopts it (%s):\n%s", pid, out)
		}
		if got := w.mustRun(t, billing, "memory", "list"); !strings.Contains(got, "note of services/billing") {
			t.Fatalf("the adopted project lists its rows:\n%s", got)
		}
	})
}

// TestInitSubfolderNoProjectAbove: with no project anywhere in the repo,
// init in a sub-folder roots the project there (v0.1.17: the project root is
// the working folder), and the repo top stays free.
func TestInitSubfolderNoProjectAbove(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person()
	root := w.initRepo(t) // initialises the top; start over in a new repo below
	repo := filepath.Join(root, "other")
	mkdirs(t, repo, "app/sub")
	w.git(t, repo, "init", "-q", "-b", "main") // the envScopeRepo shape
	w.mustRun(t, filepath.Join(repo, "app"), "init", "--name", "app")
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
