//go:build journeys && !windows

package journeys

// migrate_teammatepull_test.go — a teammate's first lore command after a
// migration never blocks their git pull (stories A-1). Bea's team used
// lore 0.1.17. Olga migrates and pushes the migration commit (it changes
// only .lore/data, M-6). Nina clones it and runs her first `lore sync` and
// a note: her clone shows nothing for git, because lore keeps its rules in
// her clone's .git/info files. Olga's next note adds lore's lines to the
// tracked .gitignore and .gitattributes (her clone ran the migration); she
// commits and pushes them, and Nina's plain `git pull` takes the commit.

import (
	"strings"
	"testing"
)

func TestJourneyTeammatePullAfterMigration(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	nina := w.Persona("Nina Park", "nina@example.test").Laptop()
	repo := w.Remote("github.com/acme-tp/app")
	bc := bea.Clone(repo, "app")

	w.Step("day1/team-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "memory", "add", "--body", "the queue drains at night").OK(t)
		fpCommitPush(t, bc, "lore data")
	})

	oc := olga.Clone(repo, "app")
	w.Step("day2/olga-migrates", func() {
		olga.LoreIn(oc, "migrate", "run").OK(t)
		if st := oc.Git("status", "--porcelain", "--", ".gitignore", ".gitattributes"); st != "" {
			t.Fatalf("lore migrate run changed a tracked file outside .lore/data:\n%s", st)
		}
		fpCommitPush(t, oc, "lore: migrate rows to format 2")
	})

	nc := nina.Clone(repo, "app")
	w.Step("day2/nina-first-sync-and-note-leave-git-clean", func() {
		nina.LoreIn(nc, "sync").OK(t)
		nina.LoreIn(nc, "memory", "add", "--body", "retries back off").OK(t)
		if st := nc.Git("status", "--porcelain", "--untracked-files=all", "--", ".gitignore", ".gitattributes"); st != "" {
			t.Fatalf("Nina's first lore commands in the migrated repo edited lore's tracked wiring:\n%s", st)
		}
		if r := nc.TryGit("check-attr", "merge", "--", ".lore/data/memories/x.json"); !strings.Contains(r.Stdout, "merge: lore") {
			t.Errorf("Nina's clone has no lore merge rule for row files: %s", r)
		}
	})

	w.Step("day3/olga-commits-the-wiring-nina-pulls", func() {
		olga.LoreIn(oc, "memory", "add", "--body", "deploys freeze on fridays").OK(t)
		for _, f := range []string{".gitignore", ".gitattributes"} {
			if st := oc.Git("status", "--porcelain", "--", f); st == "" {
				t.Errorf("Olga's write after her migration did not add lore's lines to %s", f)
			}
		}
		fpCommitPush(t, oc, "lore: notes and wiring")
		if r := nc.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
			t.Fatalf("Nina's git pull of Olga's commit aborts: %s", r)
		}
		nina.LoreIn(nc, "memory", "list").OK(t).Says(t, "deploys freeze on fridays")
	})
}
