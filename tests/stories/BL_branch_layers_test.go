//go:build stories && !windows

package stories

// BL_branch_layers_test.go — the branch-layer stories (tests/stories/
// BL-branch-layers.md, BRANCH_LAYERS_SPEC 18): notes written on a code
// branch of a repo linked to a store folder stay with that branch until it
// lands.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
)

// TestStoryBL1SharedLayer (BL-1): two developers on feat/x share its
// notes through lore sync; a third on main sees none of them, and nothing
// reached the store's main branch or the client repo.
func TestStoryBL1SharedLayer(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	third, thirdApp := w.blJoin(blThird)
	var task, mem string
	var appTree, mainBefore string

	s.Step("day1/a-branch-write-goes-to-the-branch-notes", func() {
		mainBefore = s.RefsOf(w.notes)
		w.aapp.Git("switch", "-q", "-c", "feat/x")
		r := w.admin.LoreIn(w.aapp, "task", "add", "--title", "try the new cache").OK(t).Says(t, "feat/x notes of "+blFolder)
		task = idIn(t, blTaskID, r)
		mem = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the cache needs a warmup").OK(t))
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Says(t, "try the new cache")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "isolated", "step 4")
		// a write pushes nothing (standing rule 1)
		if got := s.RefsOf(w.notes); got != mainBefore {
			t.Fatalf("a branch write pushed to the store:\nbefore %s\nafter %s", mainBefore, got)
		}
	})

	s.Step("day1/sync-shares-the-layer-not-main", func() {
		sr := w.admin.LoreIn(w.aapp, "sync").OK(t)
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "feat%2Fx") {
			t.Fatalf("the layer ref is not on the store remote: %q\n%s", refs, sr)
		}
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "try the new cache")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/x", "1 memory", "1 task")
	})

	s.Step("day2/a-teammate-on-the-branch-sees-and-adds", func() {
		s.AdvanceDays(1)
		devApp.Git("fetch", "-q", "origin")
		devApp.Git("switch", "-q", "-c", "feat/x")
		appTree = TreeOf(t, devApp.Dir) // the person's own git switch is theirs; lore must change nothing after it
		dev.LoreIn(devApp, "sync").OK(t)
		dev.LoreIn(devApp, "task", "list").OK(t).Says(t, "try the new cache")
		dev.LoreIn(devApp, "memory", "show", "--id", mem).OK(t).Says(t, "warmup")
		dev.LoreIn(devApp, "task", "done", "--id", task).OK(t)
		dev.LoreIn(devApp, "rule", "add", "--body", "warm the cache before load tests", "--to", blOrg+"/"+blFolder, "--yes").OK(t)
		dev.LoreIn(devApp, "sync").OK(t)
	})

	s.Step("day2/a-third-person-on-main-sees-nothing", func() {
		third.LoreIn(thirdApp, "sync").OK(t)
		third.LoreIn(thirdApp, "task", "list", "--all").OK(t).Never(t, "try the new cache")
		third.LoreIn(thirdApp, "rule", "list").OK(t).Never(t, "warm the cache")
		third.LoreIn(thirdApp, "branch", "list").OK(t).Says(t, "feat/x")
	})

	s.Step("day2/the-first-developer-gets-the-teammates-edits", func() {
		w.aapp.Git("switch", "-q", "feat/x")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "rule", "list").OK(t).Says(t, "warm the cache")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "done")
	})

	s.Step("day2/rules", func() {
		s.Untouched(t, devApp.Dir, appTree, "branch notes in a client repo")
		fNoLoreIn(t, devApp, "branch notes")
	})
}

// TestStoryBL2NormalMergeLands (BL-2): the branch merges into main; lore
// branch list says so; lore branch land makes one commit on the store's main,
// the layer ref goes and a trash copy stays; landing again is a no-op.
func TestStoryBL2NormalMergeLands(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	third, thirdApp := w.blJoin(blThird)

	s.Step("day1/notes-on-a-branch", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/merge-me")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "ship the badge").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "badges render server side").OK(t)
		w.aapp.WriteFile("badge.txt", "badge\n")
		w.aapp.CommitAll("feat: badge")
		w.aapp.Git("push", "-q", "origin", "feat/merge-me")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})

	s.Step("day2/the-pr-merges-with-a-merge-commit", func() {
		s.AdvanceDays(1)
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("merge", "-q", "--no-ff", "-m", "Merge feat/merge-me", "feat/merge-me")
		w.aapp.Git("push", "-q", "origin", "main")
		w.aapp.Git("fetch", "-q", "origin")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "merged", "lore branch land --branch feat/merge-me")
	})

	var landing string
	s.Step("day2/land", func() {
		r := w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/merge-me", "--pr", "12").OK(t).Says(t, "landed", "pushed")
		landing = strings.TrimSpace(w.s.Git(w.notes.Dir, "log", "-1", "--format=%s", "main"))
		if !strings.Contains(landing, "lore: land feat/merge-me") || !strings.Contains(landing, "#12") {
			t.Fatalf("landing commit subject %q (%s)", landing, r)
		}
		// one landing commit on main's first-parent line; its second parent is
		// the layer tip, so every row keeps its writer's own commit
		if parents := strings.Fields(w.s.Git(w.notes.Dir, "log", "-1", "--format=%P", "main")); len(parents) != 2 {
			t.Fatalf("the landing is not one merge commit of main and the layer: parents %v", parents)
		}
		if refs := w.storeRefs("refs/lore/layer/"); refs != "" {
			t.Fatalf("the layer ref stayed on the remote: %s", refs)
		}
		if refs := w.storeRefs("refs/lore/layer-trash/"); !strings.Contains(refs, "feat%2Fmerge-me@") {
			t.Fatalf("no trash copy on the remote: %q", refs)
		}
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/merge-me").OK(t).Says(t, "already landed")
	})

	s.Step("day2/everyone-on-main-sees-the-landed-notes", func() {
		third.LoreIn(thirdApp, "sync").OK(t)
		third.LoreIn(thirdApp, "task", "list").OK(t).Says(t, "ship the badge")
		third.LoreIn(thirdApp, "memory", "list").OK(t).Says(t, "badges render server side")
	})

	s.Step("day3/restore-brings-the-notes-back", func() {
		s.AdvanceDays(1)
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "feat/merge-me").OK(t).Says(t, "restored")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/merge-me")
	})
}

// TestStoryBL3SquashMerge (BL-3): a squash-merged pull request is landed
// by the CI step on a runner with no lore config (--store --folder
// --repo); another branch deleted after a squash merge is landed by hand.
func TestStoryBL3SquashMerge(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)

	s.Step("day1/notes-on-two-branches", func() {
		for _, b := range []string{"feat/squashed", "feat/by-hand"} {
			w.aapp.Git("switch", "-q", "-c", b, "main")
			w.admin.LoreIn(w.aapp, "memory", "add", "--body", "a note of "+b).OK(t)
			w.aapp.WriteFile(strings.ReplaceAll(b, "/", "-")+".txt", b+"\n")
			w.aapp.CommitAll("feat: " + b)
			w.aapp.Git("push", "-q", "origin", b)
		}
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})

	s.Step("day2/squash-merge-and-delete-the-branches", func() {
		s.AdvanceDays(1)
		w.aapp.Git("switch", "-q", "main")
		for _, b := range []string{"feat/squashed", "feat/by-hand"} {
			w.aapp.Git("merge", "-q", "--squash", b)
			w.aapp.Git("commit", "-q", "-m", "squash "+b)
			w.aapp.Git("branch", "-q", "-D", b)
			w.aapp.Git("push", "-q", "origin", "--delete", b)
		}
		w.aapp.Git("push", "-q", "origin", "main")
		w.aapp.Git("fetch", "-q", "--prune", "origin")
		// a squash merge is not detected: the old code head is not in main
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/squashed", "orphaned")
	})

	s.Step("day2/the-ci-step-lands-without-a-config", func() {
		ci := s.Persona("CI Runner", "ci@example.test").Laptop()
		journeys.GitHubLogin(t, s.World, ci, blAdmin.Login)
		work := ci.Clone(w.app, "checkout").Dir // actions/checkout: the code repo, no lore config
		r := ci.Lore(work, "branch", "land", "--store", w.notes.SSHURL(), "--folder", blFolder, "--repo", blAppSlug,
			"--branch", "feat/squashed", "--pr", "7").OK(t).Says(t, "landed", "pushed")
		s.NamedFlags(t, r)
		if subj := s.Git(w.notes.Dir, "log", "-1", "--format=%s", "main"); !strings.Contains(subj, "lore: land feat/squashed") || !strings.Contains(subj, "#7") {
			t.Fatalf("landing subject %q", subj)
		}
		// a branch that wrote no notes: nothing to land, exit 0
		ci.Lore(work, "branch", "land", "--store", w.notes.SSHURL(), "--folder", blFolder, "--repo", blAppSlug, "--branch", "feat/no-notes").OK(t).Says(t, "nothing to land")
	})

	s.Step("day2/the-other-is-landed-by-hand", func() {
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/by-hand").OK(t).Says(t, "landed")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "a note of feat/squashed", "a note of feat/by-hand")
		if refs := w.storeRefs("refs/lore/layer/"); refs != "" {
			t.Fatalf("layer refs left: %s", refs)
		}
	})
}

// TestStoryBL4Orphaned (BL-4): a branch deleted without merging leaves an
// orphaned layer; after 14 days lore sync moves it to the trash (a read
// never does); restore brings it back.
func TestStoryBL4Orphaned(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)

	s.Step("day1/notes-on-a-branch-that-dies", func() {
		w.aapp.Git("switch", "-q", "-c", "fix-login")
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "try oauth device flow").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("branch", "-q", "-D", "fix-login")
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "fix-login", "orphaned", "lore branch drop --branch fix-login")
		w.admin.LoreIn(w.aapp, "doctor").Says(t, "branch layer(s) open", "fix-login: 1 task", "orphaned")
	})

	s.Step("day3/reads-and-an-early-sync-keep-it", func() {
		s.AdvanceDays(2)
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "orphaned")
		w.admin.LoreIn(w.aapp, "sync").OK(t).Never(t, "moved them to the trash")
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "fix-login") {
			t.Fatalf("the layer went too early: %q", refs)
		}
	})

	s.Step("day16/sync-moves-it-to-the-trash", func() {
		s.AdvanceDays(13)
		w.admin.LoreIn(w.aapp, "sync").OK(t).Says(t, "moved them to the trash")
		if refs := w.storeRefs("refs/lore/layer/"); refs != "" {
			t.Fatalf("layer still open: %q", refs)
		}
		w.admin.LoreIn(w.aapp, "branch", "list", "--trash").OK(t).Says(t, "fix-login", "dropped")
	})

	s.Step("day16/restore-and-drop-again-and-purge", func() {
		w.admin.LoreIn(w.aapp, "branch", "restore", "--branch", "fix-login").OK(t).Says(t, "restored", "1 task")
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "fix-login", "--dry-run").OK(t).Says(t, "would drop")
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "fix-login").OK(t).Says(t, "dropped")
		w.admin.LoreIn(w.aapp, "branch", "purge", "--branch", "fix-login").Refused(t, "E_NEEDS_CONFIRM")
		w.admin.LoreIn(w.aapp, "branch", "purge", "--branch", "fix-login", "--confirm").OK(t).Says(t, "purged")
		if refs := w.storeRefs("refs/lore/layer-trash/"); refs != "" {
			t.Fatalf("trash refs left after purge: %q", refs)
		}
	})
}

// TestStoryBL5GitCommands (BL-5): plain git commands on the code branch
// never lose a note and never move one: checkout, commit, reset --hard,
// rebase, merging main in, and a rename followed by lore branch rename.
func TestStoryBL5GitCommands(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	notes := []string{}
	add := func(body string) {
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", body).OK(t)
		notes = append(notes, body)
	}
	seesAll := func(what string) {
		r := w.admin.LoreIn(w.aapp, "memory", "list").OK(t)
		for _, n := range notes {
			if !strings.Contains(r.Out(), n) {
				t.Fatalf("after %s the branch lost %q:\n%s", what, n, r)
			}
		}
	}
	s.Step("day1/every-git-command", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/git")
		add("note one")
		w.aapp.WriteFile("a.txt", "a\n")
		w.aapp.CommitAll("a")
		add("note two")
		seesAll("git commit")
		w.aapp.WriteFile("b.txt", "b\n")
		w.aapp.CommitAll("b")
		w.aapp.Git("reset", "-q", "--hard", "HEAD~1")
		seesAll("git reset --hard")
		// main moves on; the branch rebases and merges main in
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, "note one", "note two")
		w.aapp.WriteFile("m.txt", "m\n")
		w.aapp.CommitAll("m")
		w.aapp.Git("switch", "-q", "feat/git")
		w.aapp.Git("rebase", "-q", "main")
		seesAll("git rebase main")
		w.aapp.Git("merge", "-q", "--no-edit", "main")
		seesAll("git merge main")
		add("note three")
		// a rename: the new name starts with no notes until lore branch rename
		w.aapp.Git("branch", "-m", "feat/git", "feat/renamed")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Never(t, "note one")
		w.admin.LoreIn(w.aapp, "branch", "rename", "--from", "feat/git", "--to", "feat/renamed").OK(t).Says(t, "now belong to feat/renamed")
		seesAll("lore branch rename")
		if refs := w.storeRefs("refs/lore/layer/"); !strings.Contains(refs, "feat%2Frenamed") || strings.Contains(refs, "feat%2Fgit") {
			t.Fatalf("remote layer refs after the rename: %q", refs)
		}
	})
}

// TestStoryBL6SameRowBothSides (BL-6): a task edited on the branch (its
// title) and on main (its status) merges field by field on landing; a
// memory whose text both sides changed stops the landing with
// E_LAND_CONFLICT and changes nothing.
func TestStoryBL6SameRowBothSides(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	var task, mem string
	s.Step("day1/rows-on-main", func() {
		task = idIn(t, blTaskID, w.admin.LoreIn(w.aapp, "task", "add", "--title", "cache headers").OK(t))
		mem = idIn(t, blMemID, w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the cdn caches for an hour").OK(t))
		w.admin.LoreIn(w.aapp, "sync").OK(t)
	})
	s.Step("day1/edited-on-both-sides", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/both")
		w.admin.LoreIn(w.aapp, "task", "edit", "--id", task, "--title", "cache headers for images").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "task", "start", "--id", task).OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		w.aapp.Git("switch", "-q", "feat/both")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "cache headers for images")
	})
	s.Step("day1/different-fields-merge", func() {
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/both").OK(t).Says(t, "landed")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "task", "show", "--id", task).OK(t).Says(t, "cache headers for images", "in_progress")
	})
	s.Step("day2/the-same-text-stops-the-landing", func() {
		s.AdvanceDays(1)
		w.aapp.Git("switch", "-q", "-c", "feat/text")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", mem, "--body", "the cdn caches for a day").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "edit", "--id", mem, "--body", "the cdn caches for ten minutes").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		before := s.RefsOf(w.notes)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/text").Refused(t, "E_LAND_CONFLICT", mem)
		if got := s.RefsOf(w.notes); got != before {
			t.Fatalf("a refused landing changed the store remote")
		}
		w.admin.LoreIn(w.aapp, "branch", "list").OK(t).Says(t, "feat/text")
		w.admin.LoreIn(w.aapp, "memory", "show", "--id", mem).OK(t).Says(t, "ten minutes")
	})
}

// TestStoryBL7WorktreesDetachedReuse (BL-7): two worktrees (two agents in
// parallel) keep two layers; a detached HEAD refuses a store write with
// E_DETACHED unless --no-layer; a reused branch name with an older layer
// is refused with E_LAYER_STALE; promote shares one row at once.
func TestStoryBL7WorktreesDetachedReuse(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	s.Step("day1/two-worktrees-two-layers", func() {
		a := s.Worktree(t, w.aapp.Dir, "agent-a")
		b := s.Worktree(t, w.aapp.Dir, "agent-b")
		w.admin.Lore(a, "task", "add", "--title", "agent a work").OK(t).Says(t, "agent-a notes")
		w.admin.Lore(b, "task", "add", "--title", "agent b work").OK(t).Says(t, "agent-b notes")
		w.admin.Lore(a, "task", "list").OK(t).Says(t, "agent a work").Never(t, "agent b work")
		w.admin.Lore(b, "task", "list").OK(t).Says(t, "agent b work").Never(t, "agent a work")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "agent a work", "agent b work")
	})
	s.Step("day1/promote-shares-one-row-now", func() {
		a := filepath.Join(filepath.Dir(w.aapp.Dir), filepath.Base(w.aapp.Dir)+"-agent-a")
		r := w.admin.Lore(a, "rule", "add", "--body", "never log card numbers", "--to", blOrg+"/"+blFolder, "--yes").OK(t)
		rule := idIn(t, blRuleID, r)
		w.admin.Lore(a, "branch", "promote", "--ids", rule).OK(t).Says(t, "shared notes")
		w.admin.LoreIn(w.aapp, "rule", "list").OK(t).Says(t, "never log card numbers")
	})
	s.Step("day1/detached-head", func() {
		w.aapp.Git("switch", "-q", "--detach", "HEAD")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "bisect finding").Refused(t, "E_DETACHED", "--no-layer")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "bisect finding", "--no-layer").OK(t)
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Never(t, "agent a work")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "bisect finding")
	})
	s.Step("day1/a-reused-name", func() {
		w.aapp.Git("switch", "-q", "-c", "spike")
		w.aapp.WriteFile("spike.txt", "1\n")
		w.aapp.CommitAll("spike 1")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "first spike").OK(t)
		w.aapp.Git("switch", "-q", "main")
		w.aapp.Git("branch", "-q", "-D", "spike")
		w.aapp.WriteFile("main2.txt", "2\n")
		w.aapp.CommitAll("main moves")
		w.aapp.Git("switch", "-q", "--orphan", "spike")
		w.aapp.WriteFile("other.txt", "x\n")
		w.aapp.CommitAll("an unrelated spike")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "second spike").Refused(t, "E_LAYER_STALE", "lore branch drop --branch spike")
		w.admin.LoreIn(w.aapp, "branch", "drop", "--branch", "spike").OK(t)
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "second spike").OK(t)
	})
}

// TestStoryBL8StandingRules (BL-8): a write never pushes; the client repo
// stays byte-identical; the first layer declares branch_layers (lore
// 0.1.19) in the folder's _meta.json on the store's main so a 0.1.18 lore
// refuses to write there; a fake secret on a branch is refused and never
// printed; LORE_LAYER=off writes to the shared notes.
func TestStoryBL8StandingRules(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	key := FakeSecret("BL-8")
	s.Step("day1/rules", func() {
		before := s.RefsOf(w.notes)
		tree := ""
		w.aapp.Git("switch", "-q", "-c", "feat/rules")
		tree = TreeOf(t, w.aapp.Dir)
		r1 := w.admin.LoreIn(w.aapp, "memory", "add", "--body", "branch only").OK(t)
		r2 := w.admin.LoreIn(w.aapp, "memory", "add", "--body", "the token is "+key).Refused(t, "secret")
		s.NotPrinted(t, key, r1, r2)
		s.NamedFlags(t, r1)
		if got := s.RefsOf(w.notes); got != before {
			t.Fatal("a write pushed to the store")
		}
		s.Untouched(t, w.aapp.Dir, tree, "branch writes")
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		meta, ok := w.notes.Read("refs/heads/main", blFolder+"/_meta.json")
		if !ok || !strings.Contains(meta, "branch_layers") || !strings.Contains(meta, "0.1.19") {
			t.Fatalf("the folder does not declare branch_layers for lore 0.1.19: %q", meta)
		}
		// LORE_LAYER=off for one shell writes to the shared notes
		w.aapp.With("LORE_LAYER=off").Lore("memory", "add", "--body", "shared on purpose").OK(t).Never(t, "feat/rules notes")
	})
	s.Step("day1/share-and-isolate-a-branch-for-the-team", func() {
		w.aapp.Git("switch", "-q", "-c", "integration")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "isolated")
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "integration").OK(t).Says(t, "shared notes")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "shared", "step 1")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "integration writes to everyone").OK(t).Never(t, "integration notes")
		// a shared work branch is how a team coordinates on a branch: its tasks reach everyone at once (devco:D-391-docs)
		w.admin.LoreIn(w.aapp, "task", "add", "--title", "integration task for everyone").OK(t).Never(t, "integration notes")
		w.admin.LoreIn(w.aapp, "branch", "share", "--branch", "integration", "--undo").OK(t).Says(t, "removed")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "isolated")
		// dev is main-like by default; isolate makes it keep its own notes
		w.aapp.Git("switch", "-q", "-c", "dev")
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "shared", "step 3")
		w.admin.LoreIn(w.aapp, "branch", "isolate", "--branch", "dev").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "mode").OK(t).Says(t, "isolated", "step 1")
		w.aapp.Git("switch", "-q", "main")
		w.admin.LoreIn(w.aapp, "memory", "list").OK(t).Says(t, "integration writes to everyone", "shared on purpose").Never(t, "branch only")
		w.admin.LoreIn(w.aapp, "task", "list").OK(t).Says(t, "integration task for everyone")
	})
}

// TestStoryBL9TwoLandings (BL-9): two people land the same branch; the
// first makes the one commit, the second finds it landed and exits 0.
func TestStoryBL9TwoLandings(t *testing.T) {
	s := New(t, "BL")
	w := blStart(s)
	dev, devApp := w.blJoin(blDev)
	s.Step("day1/both-land", func() {
		w.aapp.Git("switch", "-q", "-c", "feat/twice")
		w.admin.LoreIn(w.aapp, "memory", "add", "--body", "landed once").OK(t)
		w.admin.LoreIn(w.aapp, "sync").OK(t)
		dev.LoreIn(devApp, "sync").OK(t)
		w.admin.LoreIn(w.aapp, "branch", "land", "--branch", "feat/twice").OK(t).Says(t, "landed")
		dev.LoreIn(devApp, "branch", "land", "--branch", "feat/twice").OK(t).Says(t, "already landed")
		n := strings.Count(s.Git(w.notes.Dir, "log", "--format=%s", "main"), "lore: land feat/twice")
		if n != 1 {
			t.Fatalf("%d landing commits, want 1", n)
		}
	})
}
