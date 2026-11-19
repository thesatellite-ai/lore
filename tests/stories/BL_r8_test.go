//go:build stories && !windows

package stories

// BL_r8_test.go — branch-layer stories from the round-8 track V findings
// (tests/stories/BL-branch-layers.md, BL-17 … BL-24): who may move a
// branch's notes to the trash, what "merged" means, landings that merge a
// row field by field, two people on one branch editing one row, stacked
// branches before their first note, the per-shell switch, sharing any
// kind now, refusals that name only what a command takes, honest sync
// counts and layer caches that go with their notes. Every name, email and
// url is made up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStoryBL17UnpushedBranchIsNotOrphaned (BL-17, devco:V-7): a branch
// that lives only on its author's laptop is never judged deleted by a
// teammate's sync, however old its notes are; the author keeps writing to
// the same notes.
func TestStoryBL17UnpushedBranchIsNotOrphaned(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	s.Step("day1/notes-on-a-branch-never-pushed", func() {
		w.aapp.Git("switch", "-q", "-c", "spike/local-only")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the spike measures cold starts").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "spike%2Flocal-only") {
			t.Fatalf("the layer ref is not on the store remote: %q", refs)
		}
	})
	s.Step("day20/a-teammates-sync-keeps-it", func() {
		s.AdvanceDays(19)
		dev.LoreIn(devApp, "sync").OK(t).Never(t, "moved them to the trash")
		dev.LoreIn(devApp, "branch", "list").OK(t).Says(t, "spike/local-only").Never(t, "orphaned", "deleted without merging")
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "spike%2Flocal-only") {
			t.Fatalf("a teammate's sync moved a branch that only lives on its author's laptop: %q", refs)
		}
	})
	s.Step("day20/the-author-keeps-the-same-notes", func() {
		w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "moved them to the trash")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "warm starts are fine").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "cold starts", "warm starts")
	})
}

// TestStoryBL18RestoreSticks (BL-18, devco:V-8): notes of a deleted branch
// that lore moved to the trash and a person restored stay open on the next
// syncs; the trash line says how old the notes really were.
func TestStoryBL18RestoreSticks(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/a-branch-deleted-unmerged", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/gone")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "try a second cache tier").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("branch", "-q", "-D", "feat/gone")
	})
	s.Step("day20/sync-trashes-then-restore-sticks", func() {
		s.AdvanceDays(19)
		w.admin.LoreIn(w.aapp, "sync").OK(t).Says(t, "moved them to the trash", "19 days ago")
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "feat/gone").OK(t).Says(t, "restored", "1 task", "lore branch drop --branch feat/gone")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "moved them to the trash")
		s.AdvanceDays(30)
		w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "moved them to the trash")
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "feat%2Fgone") {
			t.Fatalf("restored notes went back to the trash: %q", refs)
		}
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "feat/gone").OK(t).Says(t, "dropped")
	})
}

// TestStoryBL19ResetIsNotAMerge (BL-19, devco:V-9): a feature branch reset
// to main's tip (its own commits still on origin, or only in the notes'
// record) is not "merged", so nothing offers or lands its notes.
func TestStoryBL19ResetIsNotAMerge(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/a-branch-with-a-commit-of-its-own", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/z")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "ship z").OK(t)
		w.aapp.WriteFile("z.txt", "z\n")
		w.aapp.CommitAll("feat: z")
		w.aapp.Git("push", "-q", "-u", "origin", "feat/z")
		w.aapp.Git("switch", "-q", "main")
		w.aapp.WriteFile("m.txt", "m\n")
		w.aapp.CommitAll("chore: main moves")
		w.aapp.Git("push", "-q", "origin", "main")
		w.aapp.Git("switch", "-q", "feat/z")
		w.aapp.Git("rebase", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "z needs a feature flag").OK(t)
		w.aapp.Git("push", "-q", "--force", "origin", "feat/z")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/reset-to-main-is-not-merged", func() {
		w.aapp.Git("fetch", "-q", "origin")
		w.aapp.Git("reset", "-q", "--hard", "origin/main")
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/z").OK(t).Never(t, "merged")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/z").Never(t, "merged")
	})
	s.Step("day1/also-when-origin-follows", func() {
		// the branch on origin is reset too: only the notes' record knows
		// the commit the branch had
		w.aapp.Git("push", "-q", "--force", "origin", "feat/z")
		w.aapp.Git("fetch", "-q", "origin")
		w.admin.LoreIn(w.aapp, "branch", "show", "--branch", "feat/z").OK(t).Never(t, "merged")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "landed")
	})
}

// TestStoryBL20LandMergedRowNotHeld (BL-20, devco:V-2): a member lands a
// branch whose rows main changed too (the folder's project row and its
// default task list, made on both sides); the landing merges them field
// by field, and nobody's sync holds the landing back.
func TestStoryBL20LandMergedRowNotHeld(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	s.Step("day1/the-branch-and-main-both-make-the-folder-rows", func() {
		devApp.Git("switch", "-q", "-c", "feat/x")
		dev.LoreIn(devApp, "task", "add", "--title", "a task of the branch").OK(t)
		devApp.WriteFile("x.txt", "x\n")
		devApp.CommitAll("feat: x")
		devApp.Git("push", "-q", "origin", "feat/x")
		dev.LoreIn(devApp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "a task of main").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/the-member-lands", func() {
		devApp.Git("switch", "-q", "main")
		devApp.Git("pull", "-q", "--no-rebase", "origin", "main")
		devApp.Git("merge", "-q", "--no-ff", "-m", "Merge feat/x", "feat/x")
		devApp.Git("push", "-q", "origin", "main")
		dev.LoreIn(devApp, "branch", "land", "--branch", "feat/x", "--pr", "7").OK(t).Says(t, "landed")
	})
	s.Step("day1/nobody-holds-the-landing", func() {
		for i := 0; i < 2; i++ {
			dev.LoreIn(devApp, "sync").OK(t).Never(t, "held back", "claims to be written by")
			w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "held back", "claims to be written by")
		}
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Says(t, "a task of the branch", "a task of main")
		dev.LoreIn(devApp, "doctor").Never(t, "DEGRADED", "held back")
	})
}

// TestStoryBL21TwoPeopleOneRowOnABranch (BL-21, devco:V-12, the layer form
// of D-219): two people on one branch retitle the same task; the second
// sync keeps the newer title, names the one it replaced, and keeps every
// other field each changed.
func TestStoryBL21TwoPeopleOneRowOnABranch(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	var task string
	s.Step("day1/a-task-on-the-shared-branch", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/w")
		w.aapp.Git("push", "-q", "origin", "feat/w")
		task = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "W first title", "--body", "admin body").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		devApp.Git("fetch", "-q", "origin")
		devApp.Git("switch", "-q", "-c", "feat/w", "origin/feat/w")
		dev.LoreIn(devApp, "sync").OK(t)
		dev.LoreIn(devApp, "task", "show", "--id", task).OK(t).Says(t, "W first title")
	})
	s.Step("day1/both-retitle-it", func() {
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--title", "W title of the admin").OK(t)
		dev.LoreIn(devApp, "task", "edit", "--id", task, "--title", "W title of the dev").OK(t)
		dev.LoreIn(devApp, "task", "edit", "--id", task, "--priority", "high").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		r := dev.LoreIn(devApp, "sync").OK(t).Never(t, "E_REFUSED", "E_STALE", "lore sync conflicts")
		r.Says(t, "kept the newer edit", "W title of the admin")
		dev.LoreIn(devApp, "task", "show", "--id", task).OK(t).Says(t, "W title of the dev", "high", "admin body")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "W title of the dev", "high")
	})
}

// TestStoryBL22TakeKeepsOtherFields (BL-22, devco:V-14, V-15): a task
// retitled on the branch and on main stops the landing; --dry-run says so
// first, the refusal names the field and the --take ways out, and
// --take branch settles only the title: main's body stays.
func TestStoryBL22TakeKeepsOtherFields(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var task string
	s.Step("day1/a-task-on-main", func() {
		task = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "bump the runtime to 22").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/both-sides-change-it", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/l")
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--title", "bump the runtime to 22 lts").OK(t)
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--priority", "urgent").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--title", "bump the runtime to 24").OK(t)
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--body", "main body text").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/dry-run-and-refusal-name-the-way-out", func() {
		before := s.RefsOf(w.notes)
		r := w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/l", "--dry-run").Refused(t, "E_LAND_CONFLICT", task, "title", "--take branch --ids "+task, "--take main --ids "+task)
		s.NamedFlags(t, r)
		r = w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/l").Refused(t, "E_LAND_CONFLICT", task, "title", "--take branch --ids "+task)
		s.NamedFlags(t, r)
		if got := s.RefsOf(w.notes); got != before {
			t.Fatalf("a refused landing changed the store remote")
		}
	})
	s.Step("day1/take-branch-keeps-mains-body", func() {
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/l", "--take", "branch", "--ids", task).OK(t).Says(t, "landed")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "bump the runtime to 22 lts", "urgent", "main body text")
	})
}

// TestStoryBL23StackedBeforeTheFirstNote (BL-23, devco:V-11): feat/b made
// from feat/a reads feat/a's notes before it writes any of its own, even
// when feat/a's notes were written before feat/a's first commit.
func TestStoryBL23StackedBeforeTheFirstNote(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/a-then-b", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/a")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "task of a").OK(t)
		w.aapp.WriteFile("a.txt", "a\n")
		w.aapp.CommitAll("feat: a")
		w.aapp.Git("switch", "-q", "-c", "feat/b")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Says(t, "task of a")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "task of b").OK(t)
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Says(t, "task of a", "task of b")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "task of a", "task of b")
	})
}

// TestStoryBL24ShareNowAndHonestCounts (BL-24): on an isolated branch,
// LORE_LAYER=OFF and LORE_LAYER=0 write to the shared notes and a value
// lore does not know is refused naming LORE_LAYER=off (devco:V-4); a
// decision is shared at once with --no-layer (devco:V-5); a bad id to
// promote names why and lore branch show (devco:V-6); E_DETACHED names
// only LORE_LAYER=off, which works for every command (devco:V-10); a sync
// reports the rows a layer carried and then "up to date", never the whole
// folder (devco:V-13); dropping a branch's notes removes its layer cache
// (devco:V-17).
func TestStoryBL24ShareNowAndHonestCounts(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/lore-layer-any-case", func() {
		w.aapp.Git("switch", "-q", "-c", "hotfix/1")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "hotfix layer note").OK(t).Says(t, "hotfix/1 notes")
		w.aapp.With("LORE_LAYER=OFF").Lore("memory", "add", "--body", "shared by OFF").OK(t).Never(t, "hotfix/1 notes")
		w.aapp.With("LORE_LAYER=0").Lore("memory", "add", "--body", "shared by zero").OK(t).Never(t, "hotfix/1 notes")
		r := w.aapp.With("LORE_LAYER=nope").Lore("memory", "add", "--body", "never written").Refused(t, "E_INVALID_INPUT", "LORE_LAYER=nope", "LORE_LAYER=off")
		s.NamedFlags(t, r)
	})
	s.Step("day1/decision-shared-now", func() {
		w.admin.LoreIn(w.aapp, "decision", "add", "--title", "layer decision", "--body", "stays on the branch").OK(t).Says(t, "hotfix/1 notes")
		w.admin.LoreIn(w.aapp, "decision", "add", "--title", "team decision", "--body", "for everyone now", "--no-layer").OK(t).Never(t, "hotfix/1 notes")
		r := w.admin.LoreIn(w.aapp, "branch", "promote", "--ids", "mem_doesnotexist").Refused(t, "E_INVALID_ID", "is not a row id", "lore branch show --branch hotfix/1")
		s.NamedFlags(t, r)
	})
	s.Step("day1/detached-names-only-what-works", func() {
		w.aapp.Git("switch", "-q", "--detach", "HEAD")
		r := w.admin.LoreIn(w.aapp, "decision", "add", "--title", "bisect", "--body", "found it").Refused(t, "E_DETACHED", "LORE_LAYER=off").Never(t, "--no-layer")
		s.NamedFlags(t, r)
		w.aapp.With("LORE_LAYER=off").Lore("decision", "add", "--title", "bisect", "--body", "found it").OK(t)
		w.admin.LoreIn(w.aapp, "decision", "add", "--title", "bisect two", "--body", "found more", "--no-layer").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "shared by OFF", "shared by zero").Never(t, "hotfix layer note")
		w.admin.LoreIn(w.aapp, "decision", "list").OK(t).Says(t, "team decision", "bisect", "bisect two").Never(t, "layer decision")
	})
	s.Step("day1/sync-counts-what-moved", func() {
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "hotfix/1")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "one more on the branch").OK(t).Says(t, "hotfix/1 notes")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Says(t, "pushed (sent 1,")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Says(t, "up to date").Never(t, "pushed", "sent ")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Says(t, "up to date").Never(t, "pushed", "sent ")
	})
	s.Step("day1/drop-takes-the-layer-cache", func() {
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "hotfix layer note")
		pattern := w.admin.LorePath("cache", "stores", "*", "*", "_layers", "*", "hotfix%2F1")
		if got, _ := filepath.Glob(pattern); len(got) == 0 {
			t.Fatalf("no layer cache for hotfix/1 under %s", pattern)
		}
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "hotfix/1").OK(t).Says(t, "dropped")
		if got, _ := filepath.Glob(pattern); len(got) != 0 {
			t.Fatalf("the layer cache of dropped hotfix/1 stayed: %v", got)
		}
		readme, err := os.ReadFile(w.admin.LorePath("README.md"))
		if err != nil || !strings.Contains(string(readme), "_layers/<repo key>/<branch key>/") {
			t.Fatalf("the lore home README does not explain the layer caches (%v)", err)
		}
	})
}
