//go:build stories && !windows

package stories

// GRID_layers_hooks_test.go — feature grid cells (GRID.md) where branch
// layers, a tracked hooks folder, lore 0.1.17 data, moves and migrate meet:
//   - layers x moves: a note of an isolated branch moved with `lore row
//     move` leaves the branch's notes and is read from the target once;
//   - layers x bulk: `lore move --from repo` on an isolated branch takes
//     each branch note once;
//   - layers x hooks: branch notes in a repo whose hooks folder is committed
//     (core.hooksPath) never write into that folder, and committing works;
//   - layers x old: a write on a feature branch of a 0.1.17 repo is refused
//     before any new-format file or branch layer is made;
//   - hooks x moves: a bulk move in a repo with a tracked hooks folder leaves
//     that folder byte-identical;
//   - hooks x migrate: `lore migrate run` leaves the tracked hooks folder
//     byte-identical.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gridHooksBody is the team's own hook, committed with the repo.
const gridHooksBody = "#!/bin/sh\necho team pre-commit\n"

// gridTrackedHooks commits a .githooks/pre-commit to the clone and points
// core.hooksPath at it; it returns the folder's tree for Untouched checks.
func gridTrackedHooks(t *testing.T, g *gridMachine) (string, string) {
	t.Helper()
	writeExec(t, filepath.Join(g.h.Dir, ".githooks", "pre-commit"), gridHooksBody)
	g.h.Git("add", ".githooks")
	g.h.Git("commit", "-q", "-m", "team hooks")
	g.h.Git("config", "core.hooksPath", ".githooks")
	hooks := filepath.Join(g.h.Dir, ".githooks")
	return hooks, TreeOf(t, hooks)
}

// gridHooksSame fails when the tracked hooks folder changed (a file added,
// removed or rewritten, or git sees it modified).
func gridHooksSame(t *testing.T, g *gridMachine, hooks, before, what string) {
	t.Helper()
	if now := TreeOf(t, hooks); now != before {
		t.Errorf("%s changed the tracked hooks folder: %v", what, TreeDiff(before, now))
	}
	if st := g.h.Git("status", "--porcelain", "--untracked-files=all", ".githooks"); strings.TrimSpace(st) != "" {
		t.Errorf("%s left the tracked hooks folder dirty:\n%s", what, st)
	}
}

func TestStoryGRIDLayersSingleMove(t *testing.T) {
	s := New(t, "GRID")
	m, h := blSolo(s)
	g := &gridMachine{m: m, h: h}
	var mem string
	s.Step("layers-x-move/a-note-of-the-branch", func() {
		g.lore("init").OK(t)
		g.personalStore(t)
		h.CommitAll("lore: adopt")
		h.Git("push", "-q", "origin", "HEAD:main")
		h.Git("switch", "-q", "-c", "feat/cache")
		mem = idIn(t, blMemID, g.lore("memory", "add", "--body", "the cache warms at boot on this branch").OK(t))
		g.lore("memory", "list").OK(t).Says(t, "the cache warms at boot")
	})
	s.Step("layers-x-move/move-leaves-the-branch-notes", func() {
		g.lore("row", "move", "--id", mem, "--to", "me", "--yes").OK(t)
		if g.fileOpen(".lore/data/memories/" + mem + ".json") {
			t.Errorf("the moved branch note is still an open file of the branch")
		}
		r := g.lore("show", "--id", mem).OK(t)
		r.Says(t, "the cache warms at boot").Never(t, "E_AMBIGUOUS_ID", "two live rows")
		if n := strings.Count(g.lore("memory", "list", "--json").Out(), mem); n > 1 {
			t.Errorf("the moved branch note is listed %d times", n)
		}
	})
}

func TestStoryGRIDLayersBulkMove(t *testing.T) {
	s := New(t, "GRID")
	m, h := blSolo(s)
	g := &gridMachine{m: m, h: h}
	var mems []string
	s.Step("layers-x-bulk/notes-of-the-branch", func() {
		g.lore("init").OK(t)
		g.personalStore(t)
		h.CommitAll("lore: adopt")
		h.Git("push", "-q", "origin", "HEAD:main")
		h.Git("switch", "-q", "-c", "feat/bulk")
		for _, b := range []string{"the queue drains every minute", "the worker retries three times"} {
			mems = append(mems, idIn(t, blMemID, g.lore("memory", "add", "--body", b).OK(t)))
		}
	})
	s.Step("layers-x-bulk/move-takes-each-once", func() {
		g.lore("move", "--from", "repo", "--to", "me", "--where", "table=memories", "--yes").OK(t)
		list := g.lore("memory", "list", "--json").OK(t).Out()
		for _, id := range mems {
			if g.fileOpen(".lore/data/memories/" + id + ".json") {
				t.Errorf("the moved branch note %s is still an open file of the branch", id)
			}
			if n := strings.Count(list, id); n > 1 {
				t.Errorf("the moved branch note %s is listed %d times", id, n)
			}
			g.lore("show", "--id", id).OK(t).Never(t, "E_AMBIGUOUS_ID", "two live rows")
		}
	})
}

func TestStoryGRIDLayersTrackedHooks(t *testing.T) {
	s := New(t, "GRID")
	m, h := blSolo(s)
	g := &gridMachine{m: m, h: h}
	hooks, before := gridTrackedHooks(t, g)
	s.Step("layers-x-hooks/branch-notes-never-touch-the-tracked-folder", func() {
		g.lore("init").OK(t)
		h.Git("add", "-A")
		h.Git("commit", "-q", "-m", "lore: adopt")
		h.Git("push", "-q", "origin", "HEAD:main")
		h.Git("switch", "-q", "-c", "feat/hooks")
		task := idIn(t, blTaskID, g.lore("task", "add", "--title", "a task of the hooked branch").OK(t))
		if _, ok := h.ReadFile(".lore/data/tasks/" + task + ".json"); !ok {
			t.Fatalf("the branch task is not a file of the branch: the cell proves nothing")
		}
		g.lore("sync").OK(t)
		gridHooksSame(t, g, hooks, before, "branch notes and sync")
	})
	s.Step("layers-x-hooks/commit-on-the-branch-works", func() {
		h.Git("add", "-A")
		out := h.TryGit("commit", "-q", "-m", "branch work").OK(t).Out()
		if !strings.Contains(out, "team pre-commit") {
			t.Errorf("the team's own hook did not run:\n%s", out)
		}
		gridHooksSame(t, g, hooks, before, "the commit")
	})
}

func TestStoryGRIDLayersOldFormat(t *testing.T) {
	s := New(t, "GRID")
	m := s.Persona("Old Owl", "owl@example.test").Laptop()
	r := s.Seed(s.Remote("github.com/owl-co/old-app"), func(dir string) {
		PlainRepo(t, dir)
		V017Repo(t, dir, "old-app", SmallShape)
	})
	h := fCodeClone(s, m, r, "old-app")
	g := &gridMachine{m: m, h: h}
	h.Git("switch", "-q", "-c", "feat/old")
	before := TreeOf(t, h.Dir)
	refs := s.RefsOf(r)
	s.Step("layers-x-old/write-on-a-branch-is-refused-format-first", func() {
		res := g.lore("memory", "add", "--body", "a note on the old branch")
		if res.Code == 0 {
			t.Errorf("a write on a branch of a 0.1.17 repo was accepted:\n%s", res.Out())
		}
		if !strings.Contains(res.Out(), "migrate run") {
			t.Errorf("the refusal does not name lore migrate run:\n%s", res.Out())
		}
		if f := NewFormatFiles(t, h.Dir); len(f) > 0 {
			t.Errorf("the refused write left new-format files: %v", f)
		}
		s.Untouched(t, h.Dir, before, "a refused branch write in a 0.1.17 repo")
		s.NoPushSince(t, r, refs, "a refused branch write")
		if out := h.TryGit("for-each-ref", "--format=%(refname)", "refs/lore/").Stdout; strings.Contains(out, "layer") {
			t.Errorf("a branch layer ref was made for a refused write:\n%s", out)
		}
	})
}

func TestStoryGRIDHooksBulkMove(t *testing.T) {
	s := New(t, "GRID")
	m, h := blSolo(s)
	g := &gridMachine{m: m, h: h}
	hooks, before := gridTrackedHooks(t, g)
	s.Step("hooks-x-bulk/memories-in-the-repo", func() {
		g.lore("init").OK(t)
		g.personalStore(t)
		for _, b := range []string{"the build caches modules", "the deploy runs at noon"} {
			g.lore("memory", "add", "--body", b, "--to", "repo").OK(t)
		}
		h.Git("add", "-A")
		h.Git("commit", "-q", "-m", "lore notes")
	})
	s.Step("hooks-x-bulk/move-leaves-the-tracked-folder", func() {
		g.lore("move", "--from", "repo", "--to", "me", "--where", "table=memories", "--yes").OK(t)
		gridHooksSame(t, g, hooks, before, "the bulk move")
		if out := h.Git("status", "--porcelain", "--untracked-files=all"); strings.Contains(out, ".githooks") {
			t.Errorf("the bulk move touched the tracked hooks folder:\n%s", out)
		}
	})
}

func TestStoryGRIDHooksMigrate(t *testing.T) {
	s := New(t, "GRID")
	m := s.Persona("Old Owl", "owl@example.test").Laptop()
	r := s.Seed(s.Remote("github.com/owl-co/hooked-old"), func(dir string) {
		PlainRepo(t, dir)
		V017Repo(t, dir, "hooked-old", SmallShape)
		writeExec(t, filepath.Join(dir, ".githooks", "pre-commit"), gridHooksBody)
	})
	h := fCodeClone(s, m, r, "hooked-old")
	h.Git("config", "core.hooksPath", ".githooks")
	g := &gridMachine{m: m, h: h}
	hooks := filepath.Join(h.Dir, ".githooks")
	before := TreeOf(t, hooks)
	s.Step("hooks-x-migrate/migrate-leaves-the-tracked-folder", func() {
		g.lore("migrate", "run").OK(t)
		gridHooksSame(t, g, hooks, before, "lore migrate run")
		if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err != nil {
			t.Errorf("the team's own hook is gone: %v", err)
		}
	})
}
