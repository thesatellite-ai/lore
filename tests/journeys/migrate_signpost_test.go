//go:build journeys && !windows

package journeys

// migrate_signpost_test.go — the migration commit goes through lore's own
// commit hook (D-154). `lore migrate run` leaves .lore/data/
// .0-needs-lore-0.1.18-or-newer, a plain-text note for teammates still on
// lore 0.1.17. The pre-commit hook checked every changed file under
// .lore/data as a JSON row and refused it ("refusing to commit broken lore
// files: … (invalid JSON)") whenever the migrated files were not the ones
// this machine's own migration wrote: here Dan, whose clone has lore's
// hooks, brings his branch from before the migration up to Olga's
// migration branch with a squash merge and commits it.

import (
	"strings"
	"testing"
)

func TestJourneyMigrationCommitPassesTheHook(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	dan := w.Persona("Dan Dev", "dan@example.test").Laptop()
	repo := w.Remote("github.com/acme-sp/app")
	bc := bea.Clone(repo, "app")

	w.Step("day1/team-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "task", "add", "Write the export job").OK(t)
		bea.Old(bc, "memory", "add", "--body", "staging is reset every monday").OK(t)
		fpCommitPush(t, bc, "lore data")
	})

	oc := olga.Clone(repo, "app")
	w.Step("day2/olga-migrates-on-a-branch-with-hooks-on", func() {
		w.AdvanceDays(1)
		olga.LoreIn(oc, "task", "list").OK(t) // wires lore's hooks into the clone
		oc.Git("switch", "-q", "-c", "lore-format-2")
		olga.LoreIn(oc, "migrate", "run").OK(t)
		if !strings.Contains(oc.Git("status", "--porcelain", "--untracked-files=all"), ".0-needs-lore-0.1.18-or-newer") {
			t.Fatal("setup: the migration left no signpost for lore 0.1.17")
		}
		oc.Git("add", "-A", ".lore/data")
		if r := oc.TryGit("commit", "-q", "-m", "lore: migrate rows to format 2"); r.Code != 0 {
			t.Fatalf("Olga's migration commit was refused by lore's hook: %s", r)
		}
		oc.Git("push", "-q", "origin", "lore-format-2")
	})

	dc := dan.Clone(repo, "app")
	w.Step("day3/dan-brings-an-old-branch-up-to-the-migration", func() {
		w.AdvanceDays(1)
		dc.Git("switch", "-q", "-c", "format-2", "origin/lore-format-2")
		dan.LoreIn(dc, "sync").OK(t) // on the migrated branch: wires lore's hooks into the clone
		if strings.TrimSpace(dc.Git("status", "--porcelain")) != "" {
			dc.CommitAll("lore: dan joins")
		}
		dc.Git("switch", "-q", "main") // his branch from before the migration
		dc.Git("merge", "-q", "--squash", "format-2")
		r := dc.TryGit("commit", "-q", "-m", "lore: format 2 (squashed)")
		if r.Code != 0 || strings.Contains(r.Out(), "invalid JSON") {
			t.Fatalf("Dan's commit of the migration was refused by lore's hook: %s", r)
		}
		if got := dc.Git("show", "HEAD:.lore/data/.0-needs-lore-0.1.18-or-newer"); !strings.Contains(got, "lore 0.1.18") {
			t.Fatalf("the signpost was not committed as it is: %q", got)
		}
		dan.LoreIn(dc, "task", "list").OK(t).Says(t, "Write the export job")
	})
}
