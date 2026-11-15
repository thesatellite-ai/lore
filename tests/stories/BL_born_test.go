//go:build stories && !windows

package stories

// BL_born_test.go — BL-16 (D-392): notes found, and trashed, by the branch
// they were created on (their provenance `_o.git.branch`), on the branch
// and after it landed; the provenance survives every way a branch's notes
// move (land, stacked land --into, adopt, restore --revert).

import (
	"regexp"
	"testing"
)

// bornOn asserts lore show --origin names branch as id's git branch.
func (w *blWorld) bornOn(t *testing.T, id, branch string) {
	t.Helper()
	w.admin.LoreIn(w.aapp, "show", "--id", id, "--origin").OK(t).Says(t, "branch "+branch+" at")
}

// TestStoryBL16BornBranch (BL-16): notes created on feat/x are merged and
// landed; --born-branch lists them on main; trash add --born-branch moves
// only the tasks, one comes back with trash restore, and the rest of main
// is untouched; the client repo stays byte-identical.
func TestStoryBL16BornBranch(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var mem, tsk, tsk2, rul, tree string
	s.Step("day1/notes-on-feat-x-land", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/x")
		w.aapp.WriteFile("x.txt", "x\n")
		w.aapp.CommitAll("feat: x")
		mem = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "x keeps its cache warm").OK(t))
		tsk = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "wire the x cache").OK(t))
		tsk2 = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "measure the x cache").OK(t))
		rul = idIn(t, blRuleID, w.admin.LoreIn(w.aapp, "rule", "add", "--body", "x reads go through the cache").OK(t))
		w.bornOn(t, mem, "feat/x")
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "merge x", "feat/x")
		tree = TreeOf(t, w.aapp.Dir)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/x").OK(t).Says(t, "landed")
		// D-392-1: the provenance survives the land
		w.bornOn(t, mem, "feat/x")
		w.bornOn(t, tsk, "feat/x")
		w.bornOn(t, rul, "feat/x")
	})
	s.Step("day1/list-by-born-branch", func() {
		// D-392-2: every list and search narrows to the branch's rows
		w.admin.LoreIn(w.aapp, "memory", "list", "--born-branch", "feat/x").OK(t).Says(t, mem).Never(t, "port 8080")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, mem, "port 8080")
		w.admin.LoreIn(w.aapp, "task", "list", "--born-branch", "feat/x").OK(t).Says(t, "wire the x cache", "measure the x cache")
		w.admin.LoreIn(w.aapp, "rule", "list", "--born-branch", "feat/x").OK(t).Says(t, rul)
		w.admin.LoreIn(w.aapp, "memory", "list", "--born-branch", "feat/none").OK(t).Never(t, mem)
		w.admin.LoreIn(w.aapp, "search", "--query", "cache", "--born-branch", "feat/x").OK(t).Says(t, mem)
		w.admin.LoreIn(w.aapp, "search", "--query", "port", "--born-branch", "feat/x").OK(t).Never(t, "port 8080")
		w.admin.LoreIn(w.aapp, "memory", "list", "--born-branch", "feat x").Refused(t, "E_INVALID_INPUT")
		// D-392-4: branch show of a landed branch counts its rows on main
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/x").OK(t).
			Says(t, "on main", "2 tasks", "1 memory", "1 rule", "lore trash add --born-branch feat/x")
	})
	var batch string
	s.Step("day1/trash-only-the-tasks-restore-one", func() {
		// D-392-3: counts first, a yes before any write, one batch
		w.admin.LoreIn(w.aapp, "trash", "add", "--born-branch", "feat/x", "--kind", "task", "--dry-run").OK(t).
			Says(t, "would move 2 rows", "2 tasks", tsk, tsk2).Never(t, mem)
		w.admin.LoreIn(w.aapp, "trash", "add", "--born-branch", "feat/x", "--kind", "task").Refused(t, "E_NEEDS_CONFIRM")
		w.admin.LoreIn(w.aapp, "task", "list", "--born-branch", "feat/x").OK(t).Says(t, "wire the x cache")
		r := w.admin.LoreIn(w.aapp, "trash", "add", "--born-branch", "feat/x", "--kind", "task", "--yes").OK(t).Says(t, "moved 2 rows", "lore trash restore --id bat_")
		batch = regexpBatch.FindString(r.Stdout)
		if batch == "" {
			t.Fatalf("no batch id in:\n%s", r)
		}
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "wire the x cache", "measure the x cache")
		w.admin.LoreIn(w.aapp, "trash", "list", "--born-branch", "feat/x").OK(t).Says(t, tsk, tsk2).Never(t, mem)
		w.admin.LoreIn(w.aapp, "trash", "restore", "--id", tsk).OK(t)
		w.admin.LoreIn(w.aapp, "task", "list", "--born-branch", "feat/x").OK(t).Says(t, "wire the x cache").Never(t, "measure the x cache")
		// the rest of main is untouched
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, mem, "port 8080")
		w.admin.LoreIn(w.aapp, "rule", "list").OK(t).Says(t, rul)
		w.admin.LoreIn(w.aapp, "trash", "add", "--born-branch", "feat/none", "--yes").OK(t).Says(t, "nothing moved")
		s.Untouched(t, w.aapp.Dir, tree, "find and trash by born branch in a client repo")
	})
}

// TestStoryBL16OriginKept (BL-16, D-392-1): a row's born branch survives
// a stacked land --into, an adopt and a restore --revert.
func TestStoryBL16OriginKept(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	commit := func(name string) {
		w.aapp.WriteFile(name+".txt", name+"\n")
		w.aapp.CommitAll("feat: " + name)
	}
	s.Step("day1/stacked-into", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/a")
		commit("a")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of a").OK(t)
		w.aapp.Git("switch", "-q", "-c", "feat/b")
		commit("b")
		b := idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "note of b").OK(t))
		w.aapp.Git("switch", "-q", "feat/a")
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "merge b", "feat/b")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/b", "--into", "feat/a").OK(t)
		w.bornOn(t, b, "feat/b")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/a").OK(t)
		w.bornOn(t, b, "feat/b")
		w.admin.LoreIn(w.aapp, "memory", "list", "--born-branch", "feat/b").OK(t).Says(t, "note of b").Never(t, "note of a")
	})
	s.Step("day1/adopt", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/old")
		m := idIn(t, blMemID, w.aapp.With("LORE_LAYER=off").Lore("memory", "add", "--body", "written before layers").OK(t))
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "branch", "adopt", "--branch", "feat/old").OK(t).Says(t, "moved 1 memory")
		w.aapp.Git("switch", "-q", "feat/old")
		w.bornOn(t, m, "feat/old")
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/old").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.bornOn(t, m, "feat/old")
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/old").OK(t).Says(t, "1 memory", "created on feat/old")
	})
	s.Step("day1/restore-revert", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/oops")
		m := idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "landed too early").OK(t))
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/oops").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "feat/oops", "--revert").OK(t)
		w.bornOn(t, m, "feat/oops")
		w.admin.LoreIn(w.aapp, "memory", "list", "--born-branch", "feat/oops").OK(t).Says(t, "landed too early")
	})
}

// regexpBatch matches a batch id in an output.
var regexpBatch = regexp.MustCompile(`bat_[0-9a-f]{32}`)
