//go:build journeys && !windows

package journeys

// upgrade_pullclean_test.go — decision D7 (B2-20): a repo lore 0.1.17
// wrote is migrated by one person's `lore fix`, committed and pushed. A
// teammate who cloned before that commit and runs `lore sync` (or any
// read) with the new lore first must still `git pull` it cleanly: in a
// repo with unmigrated 0.1.17 rows only lore fix (or a first write's
// auto-fix) changes tracked files.

import (
	"strings"
	"testing"
)

func TestJourneyUpgraderTeammatePullsTheFix(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Admin", "ada@example.test")
	ben := w.Persona("Ben Mate", "ben@example.test")
	repo := w.Remote("github.com/acme/app")
	old := UseOldLore(t, ada.Laptop())
	adaClone := old.Clone(repo, "app")

	w.Step("day1/0117-repo", func() {
		old.Old(adaClone, "init", "--name", "app").OK(t)
		old.Old(adaClone, "memory", "add", "--body", upNoteBranch).OK(t)
		upCommitPush(t, adaClone, "lore data")
	})

	bm := ben.Laptop()
	benClone := bm.Clone(repo, "app") // cloned before the admin's lore fix

	w.AdvanceDays(1)
	w.Step("day2/admin-fixes-and-pushes", func() {
		am := old.Upgrade()
		am.LoreIn(adaClone, "fix", "--yes")
		upCommitPush(t, adaClone, "lore fix")
	})

	w.Step("day2/mate-syncs-then-pulls", func() {
		r := bm.LoreIn(benClone, "sync")
		r.Says(t, "run lore fix", "nothing synced in this repo")
		bm.LoreIn(benClone, "memory", "list").OK(t)
		if st := strings.TrimSpace(benClone.Git("status", "--porcelain")); st != "" {
			t.Fatalf("lore sync and a read changed tracked files in an unmigrated 0.1.17 repo:\n%s", st)
		}
		if r := benClone.TryGit("pull", "--no-rebase", "origin", "main"); r.Code != 0 {
			t.Fatalf("git pull of the admin's lore fix commit: %s", r)
		}
		upLists(t, "mate after pull", bm.LoreIn(benClone, "memory", "list"), []string{upNoteBranch})
		bm.LoreIn(benClone, "memory", "add", "--body", upNoteNewNew).OK(t).Never(t, "rows written by lore 0.1.17")
	})
}
