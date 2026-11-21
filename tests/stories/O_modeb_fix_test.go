//go:build stories && !windows

package stories

// O_modeb_fix_test.go — devco D-855 on an owner-shaped repo (story O): a
// developer's private repo (`.lore/` listed in .git/info/exclude, never in
// .gitignore) whose .lore/lore.yaml still pointed at the retired shared
// database ~/.lore/<name>.db. lore fix moved the rows into the new folder
// store's _store/ (store-wide) with no project row, so every plain add and
// render failed E_NOT_FOUND "this repository has no lore project". The
// cell: lore fix links the repo to the project folder of the new store
// (rows and the project row in that folder, the folder keeping the v0.1.17
// project id), plain add, render and doctor work with no lore init, git
// status stays clean, and a repo an older fix left in the broken shape is
// healed by the next lore fix.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// oModeBScript builds a v0.1.17 shared lore.db (relative to this package).
const oModeBScript = "../docs/v017_shared_db.py"

// oModeBDB writes a v0.1.17 shared lore.db at path holding a rule and a
// memory of project name, and returns the project id.
func oModeBDB(t *testing.T, path, name string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 builds the v0.1.17 lore.db fixture: %v", err)
	}
	script, err := filepath.Abs(oModeBScript)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: py, Args: []string{py, "-I", script, path, name, "rule:Plan before every apply", "memory:Staging sleeps at night"}}
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

// oModeBRepo clones remote and makes it the developer's private Mode B
// repo: .lore/ excluded in .git/info/exclude, .lore/lore.yaml naming the
// shared database ~/.lore/<db>.db and project pid.
func oModeBRepo(t *testing.T, dev *journeys.Machine, r *teamharness.Remote, name, db, pid string) *teamharness.Clone {
	t.Helper()
	c := dev.Clone(r, name)
	exclude := filepath.Join(c.Dir, ".git", "info", "exclude")
	b, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeFile(t, exclude, string(b)+".lore/\n")
	writeFile(t, filepath.Join(c.Dir, ".lore", "lore.yaml"), "db_path: ${HOME}/.lore/"+db+".db\nproject_id: "+pid+"\n")
	return c
}

// oModeBClean fails when git status lists anything in the repo.
func oModeBClean(t *testing.T, c *teamharness.Clone) {
	t.Helper()
	if st := strings.TrimSpace(c.Git("status", "--porcelain", "--untracked-files=all")); st != "" {
		t.Fatalf("lore left files in git status of a private repo:\n%s", st)
	}
}

func TestStoryOModeBFixLinksProject(t *testing.T) {
	s := New(t, "O")
	dev := s.Person("Dev One", "dev1@example.com")
	infra := s.Seed(s.Remote("github.com/acme-co/infra"), func(dir string) { PlainRepo(t, dir) })
	pid := oModeBDB(t, dev.LorePath("acme.db"), "infra")
	c := oModeBRepo(t, dev, infra, "infra", "acme", pid)
	folder := dev.LorePath("stores", "infra", "infra")

	s.Step("D-855/refused-until-fixed", func() {
		dev.LoreIn(c, "memory", "add", "--body", "before the fix").Fails(t, teamharness.ExitRefused, "retired shared-database mode", "lore fix")
	})

	s.Step("D-855/fix-links-the-project-folder", func() {
		dev.LoreIn(c, "fix").OK(t).Says(t, "store shared, folder infra", "projects/"+pid, "at its folder infra")
		if _, err := os.Stat(filepath.Join(folder, "projects", pid+".json")); err != nil {
			t.Fatalf("the project row is not in the project folder: %v", err)
		}
		if _, err := os.Stat(dev.LorePath("stores", "infra", "_store")); !os.IsNotExist(err) {
			t.Fatalf("lore fix wrote rows into the store's root folder (%v)", err)
		}
		if b, err := os.ReadFile(filepath.Join(folder, "_meta.json")); err != nil || !strings.Contains(string(b), pid) {
			t.Fatalf("the folder does not keep the project id: %s %v", b, err)
		}
		if b, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "stores.yaml")); err != nil || !strings.Contains(string(b), "folder: infra") {
			t.Fatalf("stores.yaml does not attach the folder: %s %v", b, err)
		}
		oModeBClean(t, c)
	})

	s.Step("D-855/plain-add-render-doctor", func() {
		dev.LoreIn(c, "memory", "add", "--body", "Deploys freeze on Fridays").OK(t).Says(t, "[shared/infra]")
		dev.LoreIn(c, "rule", "add", "--body", "Never apply without a plan").OK(t).Says(t, "[shared/infra]")
		dev.LoreIn(c, "memory", "list").OK(t).Says(t, "Staging sleeps at night", "Deploys freeze on Fridays")
		dev.LoreIn(c, "render").OK(t)
		dev.LoreIn(c, "doctor").OK(t)
		oModeBClean(t, c)
	})

	// The shape an older lore fix left: rows in the store's _store/, no
	// project row, no folder id, stores.yaml attaching the store whole.
	web := s.Seed(s.Remote("github.com/acme-co/web"), func(dir string) { PlainRepo(t, dir) })
	wpid := oModeBDB(t, dev.LorePath("web.db"), "web")
	wc := oModeBRepo(t, dev, web, "web", "web", wpid)
	wstore := dev.LorePath("stores", "web")
	s.Step("D-855/old-broken-shape-healed-by-fix", func() {
		dev.LoreIn(wc, "fix").OK(t)
		wfolder := filepath.Join(wstore, "web")
		if err := os.RemoveAll(filepath.Join(wfolder, "projects")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(wfolder, "_meta.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(wfolder, filepath.Join(wstore, "_store")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(wc.Dir, ".lore", "stores.yaml"), "stores:\n    - name: shared\n      url: file://"+wstore+"\n")
		dev.LoreIn(wc, "memory", "add", "--body", "still broken").Fails(t, teamharness.ExitNotFound, "this repository has no lore project")

		dev.LoreIn(wc, "fix").OK(t).Says(t, "moved out of the store's root folder", "attached at its folder web")
		dev.LoreIn(wc, "memory", "add", "--body", "Web deploys on Tuesdays").OK(t).Says(t, "[shared/web]")
		dev.LoreIn(wc, "memory", "list").OK(t).Says(t, "Staging sleeps at night", "Web deploys on Tuesdays")
		dev.LoreIn(wc, "render").OK(t)
		dev.LoreIn(wc, "fix").OK(t).Says(t, "Everything is current")
		oModeBClean(t, wc)
	})
}
