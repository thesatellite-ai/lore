//go:build journeys && !windows

package journeys

// migrate_stale_test.go — a checkout behind a pushed migration (K-11).
// Olga migrated the team's repo and pushed; Dan's checkout is from before
// it, with origin/main fetched. His `lore migrate run` migrated again and
// made a second migration commit that conflicts with Olga's in every row
// file, with no word that the team already did it. Now lore reads the
// fetched origin/main, says who migrated and that git pull is the step,
// and changes nothing; after the pull everything is on format 2.

import (
	"strings"
	"testing"
)

func TestJourneyStaleCheckoutPullsInsteadOfMigrating(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	dan := w.Persona("Dan Dev", "dan@example.test").Laptop()
	repo := w.Remote("github.com/acme-sp/stale")
	bc := bea.Clone(repo, "app")

	w.Step("day1/team-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "task", "add", "Write the export job").OK(t)
		fpCommitPush(t, bc, "lore data")
	})

	dc := dan.Clone(repo, "app")
	oc := olga.Clone(repo, "app")
	w.Step("day2/olga-migrates-and-pushes", func() {
		w.AdvanceDays(1)
		olga.LoreIn(oc, "migrate", "run").OK(t)
		oc.Git("add", "-A", ".lore")
		oc.Git("commit", "-q", "-m", "lore: migrate rows to format 2")
		oc.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day3/dan-fetched-but-did-not-pull", func() {
		w.AdvanceDays(1)
		dc.Git("fetch", "-q", "origin")
		dan.LoreIn(dc, "migrate", "run", "--dry-run").Refused(t, "E_REFUSED", "origin/main is already on the lore 0.1.18 format", fpOwnerName, "git pull first")
		dan.LoreIn(dc, "migrate", "run").Refused(t, "git pull first", "do not migrate here")
		dan.LoreIn(dc, "fix").Refused(t, "git pull first")
		if st := strings.TrimSpace(dc.Git("status", "--porcelain", "--", ".lore")); st != "" {
			t.Fatalf("the refused runs changed .lore:\n%s", st)
		}
	})

	w.Step("day3/dan-pulls", func() {
		dc.Git("pull", "-q", "--no-rebase", "origin", "main")
		if got := dc.Git("show", "HEAD:.lore/data/_meta.json"); !strings.Contains(got, `"format": 2`) {
			t.Fatalf("after the pull the checkout is not on format 2: %s", got)
		}
		dan.LoreIn(dc, "task", "list").OK(t).Says(t, "Write the export job")
		dan.LoreIn(dc, "migrate", "run", "--dry-run").Never(t, "git pull first")
	})
}
