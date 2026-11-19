//go:build journeys && !windows

package journeys

// revertrows_test.go — a `git revert` of a code commit never deletes a
// note by itself (devco M-6). A decision written before a code change rides
// into that code commit (the pre-commit hook stages lore's rows); reverting
// the code commit deleted the decision's file, the push sent the deletion
// and a teammate's `decision show` said not found, with nothing printed at
// the revert, the push or the sync. Now the revert puts the file back in
// the work tree and names it, a push before it is committed back is
// refused naming the row and the commands, and after the commit the
// teammate sees the decision.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyRevertKeepsRows(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	remote := w.Remote("github.com/widgets/revertrows")
	ana := w.Persona("Ana Lind", "ana@widgets.example").Laptop()
	ben := w.Persona("Ben Ortiz", "ben@widgets.example").Laptop()
	var a, b *teamharness.Clone
	var dec string

	w.Step("day1/a-repo-with-notes", func() {
		a = ana.Clone(remote, "revertrows")
		ana.LoreIn(a, "init").OK(t)
		a.WriteFile("packages/dock/README.md", "# dock\n")
		a.CommitAll("start")
		a.Git("push", "-q", "-u", "origin", "HEAD")
	})

	w.Step("day2/a-decision-rides-a-code-commit-that-is-reverted", func() {
		dec = jRewriteID(t, ana.LoreIn(a, "decision", "add", "--title", "Dock popout keeps one window per panel", "--body", "one window", "--json").OK(t))
		a.WriteFile("packages/dock/README.md", "# dock\nfocus\n")
		a.Git("add", "packages/dock/README.md")
		a.Git("commit", "-q", "-m", "dock: restore focus after popout")
		r := a.TryGit("revert", "--no-edit", "HEAD")
		if r.Code != 0 || !strings.Contains(r.Out(), "this revert deleted 1 lore row") || !strings.Contains(r.Out(), dec) {
			t.Fatalf("the revert said nothing about the row: exit %d\n%s", r.Code, r.Out())
		}
		ana.LoreIn(a, "decision", "show", "--id", dec).OK(t) // back in the work tree
	})

	w.Step("day2/the-push-before-it-is-committed-back-is-refused", func() {
		a.TryGit("push", "-q").Refused(t, "E_NEEDS_CONFIRM", "git revert", dec, "git checkout", "LORE_ALLOW_ROW_DROP=1")
	})

	w.Step("day2/committed-back-a-teammate-sees-it", func() {
		a.CommitAll("lore: keep rows a revert removed")
		a.Git("push", "-q")
		b = ben.Clone(remote, "revertrows")
		ben.LoreIn(b, "decision", "show", "--id", dec).OK(t)
	})
}
