//go:build journeys && !windows

package journeys

// migrate_nopush_test.go — lore migrate run writes nothing to the code
// repo's remote unless asked (A-7, C-4), and a write in a format-1 repo is
// refused for its format before any live-ref check (A-6). Bea's team used
// lore 0.1.17 (a live row on refs/lore/live). Olga, on 0.1.18, read it once;
// then the server's live ref is deleted. Olga's write is refused with the
// format line, never sent to `lore live restore` (a push). Once the ref is
// back, Olga's plain `lore migrate run` leaves every ref of the remote as
// it was and says it holds only this machine's lock; a later
// `lore migrate run --team-lock` says it pushes a hold to refs/lore/signals
// on the remote and leaves no signals ref there when it ends.

import (
	"slices"
	"testing"
)

func TestJourneyMigrateWritesNothingToTheRemote(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	bea := UseOldLore(t, w.Persona("Bea Builder", "bea@example.test").Laptop())
	olga := w.Persona(fpOwnerName, fpOwnerEmail).Laptop()
	repo := w.Remote("github.com/acme-mn/app")
	bc := bea.Clone(repo, "app")
	var liveTip string

	w.Step("day1/team-on-0.1.17", func() {
		bea.Old(bc, "init", "--name", "app").OK(t)
		bea.Old(bc, "task", "add", "Write the export job").OK(t)
		bea.Old(bc, "memory", "add", "--live", "--body", "staging is reset every monday").OK(t)
		fpCommitPush(t, bc, "lore data")
		if liveTip = repo.Rev("refs/lore/live"); liveTip == "" {
			t.Fatal("setup: 0.1.17 shared no live ref")
		}
	})

	oc := olga.Clone(repo, "app")
	w.Step("day2/format-refusal-comes-before-the-live-ref", func() {
		olga.LoreIn(oc, "memory", "list").OK(t).Says(t, "staging is reset every monday")
		fpGit(t, repo.Dir, "update-ref", "-d", "refs/lore/live")
		w.AdvanceDays(1) // past the fetch window: the write fetches and finds no live ref
		olga.LoreIn(oc, "task", "add", "Schedule the export", "--commitment", "accepted").
			Fails(t, fpFormatOld, fpOldLine).Never(t, "E_READ_ONLY", "live restore", "deleted or rewritten")
		if repo.Has("refs/lore/live") {
			t.Fatal("a refused write pushed a live ref")
		}
		fpGit(t, repo.Dir, "update-ref", "refs/lore/live", liveTip)
	})

	w.Step("day3/migrate-run-pushes-nothing", func() {
		w.AdvanceDays(1)
		before := repo.Refs("refs/")
		olga.LoreIn(oc, "migrate", "run").OK(t).
			Says(t, "team lock: none (this machine's migration lock only; nothing written to the remote for it)").
			Never(t, "team lock released", "pushed a hold")
		if after := repo.Refs("refs/"); !slices.Equal(before, after) || repo.Has("refs/lore/signals") {
			t.Fatalf("lore migrate run changed the remote's refs:\nbefore %v\nafter  %v", before, after)
		}
	})

	w.Step("day3/team-lock-is-asked-for-and-cleaned-up", func() {
		olga.LoreIn(oc, "migrate", "run", "--team-lock").OK(t).
			Says(t, "team lock: pushed a hold to refs/lore/signals on the remote origin", "removed from the remote when the run ends", "team lock released")
		if repo.Has("refs/lore/signals") {
			t.Fatal("the team lock's signals ref stayed on the remote after the run")
		}
	})
}
