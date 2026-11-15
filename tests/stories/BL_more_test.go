//go:build stories && !windows

package stories

// BL_more_test.go — branch-layer stories BL-10 to BL-15 (tests/stories/
// BL-branch-layers.md): code-repo mode, land's options, stacked branches,
// restore --revert, adopt, and the CI workflow.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// blSolo is a person in no org with their own repo (its notes in its own
// .lore/data after lore init), cloned at ~/code/solo-app.
func blSolo(s *Story) (*journeys.Machine, *teamharness.Clone) {
	s.T.Helper()
	m := s.Persona("Sol Solo", "sol@example.test").Laptop()
	journeys.GitHubLogin(s.T, s.World, m, "sol-gh")
	r := s.Seed(s.Remote("github.com/sol-co/app"), func(dir string) { PlainRepo(s.T, dir) })
	return m, fCodeClone(s, m, r, "solo-app")
}

// TestStoryBL10CodeRepoMode (BL-10): in a repo whose notes live in its own
// .lore/data, a new task on an isolated branch stays with that branch (a
// file of the branch, not the repo-wide live ref) and reaches main with the
// merge; main and --live / --no-layer keep today's live rows.
func TestStoryBL10CodeRepoMode(t *testing.T) {
	s := New(t, "BL")
	solo, h := blSolo(s)
	w := &blWorld{s: s, admin: solo}
	var branchTask string
	s.Step("day1/an-isolated-branch-keeps-its-new-task", func() {
		w.admin.LoreIn(h, "init").OK(t)
		h.CommitAll("lore: adopt")
		h.Git("push", "-q", "origin", "HEAD:main")
		w.admin.LoreIn(h, "task", "add", "--title", "a task for everyone").OK(t)
		h.Git("switch", "-q", "-c", "feat/local")
		branchTask = idIn(t, blTaskID, w.admin.LoreIn(h, "task", "add", "--title", "a task of the branch").OK(t))
		if _, ok := h.ReadFile(".lore/data/tasks/" + branchTask + ".json"); !ok {
			t.Fatalf("the branch task is not a file of the branch")
		}
		if strings.Contains(h.Git("ls-tree", "-r", "--name-only", "refs/lore/live"), branchTask) {
			t.Fatal("the branch task went to the live ref")
		}
		w.admin.LoreIn(h, "task", "list").OK(t).Says(t, "a task of the branch", "a task for everyone")
		// --live and --no-layer still share at once
		live := idIn(t, blTaskID, w.admin.LoreIn(h, "task", "add", "--title", "urgent for all", "--live").OK(t))
		nolayer := idIn(t, blTaskID, w.admin.LoreIn(h, "task", "add", "--title", "also for all", "--no-layer").OK(t))
		tree := h.Git("ls-tree", "-r", "--name-only", "refs/lore/live")
		if !strings.Contains(tree, live) || !strings.Contains(tree, nolayer) {
			t.Fatalf("--live / --no-layer rows are not live:\n%s", tree)
		}
	})
	s.Step("day1/main-does-not-see-it-until-the-merge", func() {
		h.CommitAll("feat: local work")
		h.Git("switch", "-q", "main")
		w.admin.LoreIn(h, "task", "list").OK(t).Says(t, "a task for everyone", "urgent for all").Never(t, "a task of the branch")
		h.Git("merge", "-q", "--no-ff", "-m", "Merge feat/local", "feat/local")
		w.admin.LoreIn(h, "task", "list").OK(t).Says(t, "a task of the branch")
	})
	s.Step("day1/a-main-like-branch-keeps-live-rows", func() {
		h.Git("switch", "-q", "-c", "dev")
		dev := idIn(t, blTaskID, w.admin.LoreIn(h, "task", "add", "--title", "dev is shared").OK(t))
		if !strings.Contains(h.Git("ls-tree", "-r", "--name-only", "refs/lore/live"), dev) {
			t.Fatal("a task on dev (main-like) is not live")
		}
	})
	s.Step("day1/the-repos-own-policy-shares-a-branch", func() {
		h.Git("switch", "-q", "main")
		w.admin.LoreIn(h, "policy", "set", "branches.shared", "integration", "--yes").OK(t)
		h.Git("switch", "-q", "-c", "integration")
		in := idIn(t, blTaskID, w.admin.LoreIn(h, "task", "add", "--title", "integration is shared").OK(t))
		if !strings.Contains(h.Git("ls-tree", "-r", "--name-only", "refs/lore/live"), in) {
			t.Fatal("a branch the repo's policy shares did not get a live task")
		}
	})
}

// TestStoryBL11LandOptions (BL-11): --kinds lands some tables and keeps
// the rest open; --review without a terminal is refused, with answers
// drops the rows answered no; --take settles a same-text clash.
func TestStoryBL11LandOptions(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var mem string
	s.Step("day1/kinds", func() {
		mem = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "shared base text").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "-c", "feat/kinds")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "task stays").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "memory lands").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/kinds", "--kinds", "memories").OK(t).Says(t, "landed 1 memory", "stay with feat/kinds")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/kinds", "1 task").Never(t, "1 memory, ")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "memory lands")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "task stays")
	})
	s.Step("day1/review", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/review")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "keep me").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "drop me").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/review", "--review").Refused(t, "E_NEEDS_CONFIRM", "no terminal")
		// rows are reviewed in id order: the first written first
		w.aapp.With("LORE_REVIEW_ANSWERS=y,n").Lore("branch", "land", "--branch", "feat/review", "--review").OK(t).Says(t, "landed 1 memory", "1 memory dropped")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "keep me").Never(t, "drop me")
	})
	s.Step("day2/take", func() {
		s.AdvanceDays(1)
		w.aapp.Git("switch", "-q", "-c", "feat/take")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", mem, "--body", "branch text wins").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", mem, "--body", "main text").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/take").Refused(t, "E_LAND_CONFLICT", mem)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/take", "--take", "branch", "--ids", mem).OK(t).Says(t, "landed")
		w.admin.LoreIn(w.aapp, "memory", "show", "--id", mem).OK(t).Says(t, "branch text wins")
	})
}

// TestStoryBL12Stacked (BL-12): feat/b made from feat/a reads feat/a's
// notes under its own; landing feat/b --into feat/a moves its notes
// there; when feat/a lands, feat/c (also stacked) moves onto the shared
// notes and then lands alone.
func TestStoryBL12Stacked(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	commit := func(name string) {
		w.aapp.WriteFile(name+".txt", name+"\n")
		w.aapp.CommitAll("feat: " + name)
	}
	s.Step("day1/a-stack", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/a")
		commit("a")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of a").OK(t)
		w.aapp.Git("switch", "-q", "-c", "feat/b")
		commit("b")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of b").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of a", "note of b")
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/b").OK(t).Says(t, "note of b").Never(t, "note of a")
		w.aapp.Git("switch", "-q", "feat/a")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of a").Never(t, "note of b")
		w.aapp.Git("switch", "-q", "-c", "feat/c")
		commit("c")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of c").OK(t)
	})
	s.Step("day1/b-lands-into-a", func() {
		w.aapp.Git("switch", "-q", "feat/a")
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "merge b", "feat/b")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/b", "--into", "feat/a").OK(t).Says(t, "into the notes of feat/a")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of a", "note of b")
	})
	s.Step("day1/a-lands-and-c-moves-onto-main", func() {
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/a").OK(t).Says(t, "landed 2 memories", "feat/c")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of a", "note of b").Never(t, "note of c")
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/c").OK(t).Says(t, "note of c").Never(t, "note of a")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/c").OK(t).Says(t, "landed 1 memory")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "note of c")
	})
}

// TestStoryBL13RestoreRevert (BL-13): notes landed by mistake come back
// with restore --revert, which also takes them off the shared notes with a
// new commit (no history rewrite).
func TestStoryBL13RestoreRevert(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/land-then-undo", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/oops")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "landed too early").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/oops").OK(t)
		before := s.Git(w.notes.Dir, "rev-parse", "main")
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "feat/oops", "--revert").OK(t).Says(t, "reverted their landing")
		if !strings.Contains(s.Git(w.notes.Dir, "log", "--format=%P", "-1", "main"), strings.TrimSpace(before)) {
			t.Fatal("the revert is not a new commit on top of the landing")
		}
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "landed too early") // still on the branch
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, "landed too early")
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "feat/oops").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "feat/oops", "--revert").Refused(t, "dropped, never landed")
	})
}

// TestStoryBL14Adopt (BL-14): rows a branch wrote into the shared notes
// before it had a layer move into its layer with lore branch adopt; main
// no longer shows them; landing the branch brings them back.
func TestStoryBL14Adopt(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/old-branch-notes-in-the-shared-notes", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/old")
		w.aapp.With("LORE_LAYER=off").Lore("memory", "add", "--body", "written before layers").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "written before layers")
		w.admin.LoreIn(w.aapp, "branch", "adopt", "--branch", "feat/old", "--dry-run").OK(t).Says(t, "would move 1 memory")
		w.admin.LoreIn(w.aapp, "branch", "adopt", "--branch", "feat/old").OK(t).Says(t, "moved 1 memory")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, "written before layers")
		w.aapp.Git("switch", "-q", "feat/old")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "written before layers")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/old").OK(t).Says(t, "landed")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "written before layers")
	})
}

// TestStoryBL15InstallAction (BL-15): the CI workflow is offered in an own
// repo with branch notes, written only with --yes, never in a client repo.
func TestStoryBL15InstallAction(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/client-repo-never", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/ci")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "x").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Never(t, "install-action")
		w.admin.LoreIn(w.aapp, "branch", "install-action", "--yes").Refused(t, "E_LEAK_BLOCKED")
	})
	s.Step("day1/own-repo-asks-first", func() {
		solo, h := blSolo(s)
		notes := s.Remote("github.com/sol-co/notes")
		solo.LoreIn(h, "init").OK(t)
		solo.LoreIn(h, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		solo.LoreIn(h, "memory", "add", "--body", "the app folder starts", "--to", "notes/app", "--yes").OK(t)
		h.CommitAll("lore: adopt")
		h.Git("push", "-q", "origin", "HEAD:main")
		w.admin = solo
		h.Git("switch", "-q", "-c", "feat/ci")
		w.admin.LoreIn(h, "memory", "add", "--body", "y", "--to", "notes/app").OK(t).Says(t, "feat/ci notes")
		w.admin.LoreIn(h, "branch", "list").OK(t).Says(t, "lore branch install-action")
		w.admin.LoreIn(h, "branch", "install-action", "--dry-run").OK(t).Says(t, "lore branch land --store", "--folder app")
		w.admin.LoreIn(h, "branch", "install-action").Refused(t, "E_NEEDS_CONFIRM")
		wf := filepath.Join(h.Dir, ".github", "workflows", "lore-branch-land.yml")
		if _, err := os.Stat(wf); err == nil {
			t.Fatal("the workflow was written without a yes")
		}
		w.admin.LoreIn(h, "branch", "install-action", "--yes").OK(t).Says(t, "LORE_STORE_TOKEN")
		if _, err := os.Stat(wf); err != nil {
			t.Fatal("no workflow after --yes")
		}
		w.admin.LoreIn(h, "branch", "list").OK(t).Never(t, "install-action adds")
	})
}
