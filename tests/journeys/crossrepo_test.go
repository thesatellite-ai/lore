//go:build journeys && !windows

package journeys

// crossrepo_test.go — Kai has a note in one repo and wants it in another
// (A-15). `lore move --to <the other repo>` is refused saying --to names a
// store, never another repo; looking the id up in the other repo, whose
// branch is up to date, no longer says "it may have landed on main while
// this branch is older" (it never could): it says the row may belong to
// another repo.

import (
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// crMemID matches the id `memory add` prints.
var crMemID = regexp.MustCompile(`mem_[0-9a-f]+`)

func TestJourneyCrossRepoRow(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kai := w.Persona("Kai Brook", "kai@crossrepo.example")
	lap := kai.Laptop()
	var syncRepo, authRepo *teamharness.Clone
	var id string

	w.Step("day1/two-repos-and-a-note", func() {
		syncRepo = lap.Clone(w.Remote("github.com/kai-dev/sync"), "sync")
		authRepo = lap.Clone(w.Remote("github.com/kai-dev/auth"), "auth")
		lap.LoreIn(syncRepo, "init").OK(t)
		lap.LoreIn(authRepo, "init").OK(t)
		r := lap.LoreIn(syncRepo, "memory", "add", "--body", "retries back off exponentially").OK(t)
		if id = crMemID.FindString(r.Out()); id == "" {
			t.Fatalf("memory add printed no id: %s", r)
		}
		for _, c := range []*teamharness.Clone{syncRepo, authRepo} {
			c.CommitAll("lore")
			c.Git("push", "-q", "origin", "HEAD")
		}
	})

	w.Step("day1/move-to-the-other-repo-is-refused-plainly", func() {
		lap.LoreIn(syncRepo, "move", "--id", id, "--to", "auth", "--yes").
			Refused(t, "no store named auth is attached", "--to names a store, never another repo")
	})

	w.Step("day1/the-id-in-the-other-repo", func() {
		lap.LoreIn(authRepo, "memory", "show", "--id", id).
			Refused(t, "E_NOT_FOUND", "the row may belong to another repo", "lore sync peek").Never(t, "while this branch is older")
	})
}
