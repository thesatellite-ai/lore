//go:build journeys && !windows

package journeys

// amend_leak_test.go — `git commit --amend` after a key slipped in with
// --no-verify (bug L-1). The commit check counts as history only what
// reached a remote: an amend replaces HEAD, so comparing with HEAD let a
// key already in the commit being amended through as "not new", even
// when the amend touched another file. Now the amend is refused until the
// key is gone; a key already pushed stays history and does not block.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyAmendKeepsAKeyOut(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
	var c *teamharness.Clone

	w.Step("day1/a-pushed-repo", func() {
		c = lap.Clone(w.Remote("github.com/widgets/amend"), "amend")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("README.md", "# widgets\n")
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "start").OK(t)
		c.Git("push", "-q", "-u", "origin", "HEAD")
	})

	w.Step("day1/a-key-slips-in-with-no-verify", func() {
		c.WriteFile("notes.md", "token "+key+"\n")
		c.Git("add", "notes.md")
		c.TryGit("commit", "-q", "-m", "notes").Refused(t, "E_LEAK_BLOCKED").Never(t, key)
		c.Git("commit", "-q", "--no-verify", "-m", "notes")
	})

	w.Step("day1/an-amend-of-another-file-is-refused", func() {
		c.WriteFile("README.md", "# widgets\n\nmore\n")
		c.Git("add", "README.md")
		c.TryGit("commit", "-q", "--amend", "--no-edit").Refused(t, "E_LEAK_BLOCKED", "notes.md").Never(t, key)
	})

	w.Step("day1/an-amend-of-that-file-is-refused", func() {
		c.WriteFile("notes.md", "token "+key+"\nand a line\n")
		c.Git("add", "notes.md")
		c.TryGit("commit", "-q", "--amend", "--no-edit").Refused(t, "E_LEAK_BLOCKED", "notes.md").Never(t, key, "not new")
	})

	w.Step("day1/the-amend-without-the-key-goes-through", func() {
		c.WriteFile("notes.md", "token ${secret:widgets/token}\n")
		c.Git("add", "notes.md")
		c.TryGit("commit", "-q", "--amend", "--no-edit").OK(t)
		if got := c.Git("log", "-p", "@{u}..HEAD"); strings.Contains(got, key) {
			t.Fatal("the amended commit still holds the key")
		}
	})
}
