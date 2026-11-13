//go:build journeys && !windows

package journeys

// allow_file_test.go — a finding in an ordinary repo file (owner item):
// Dana starts a repo whose first commit holds a config file with a
// password field (a made-up value built at run time). The commit hook
// refuses it and prints a ready `lore secrets allow --file …` line per
// finding; she runs them, and the commit goes through. A different new
// value in the same file is still refused.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// allowLine is one ready allow command a refusal prints.
var allowLine = regexp.MustCompile(`lore secrets allow --file \S+ --rule \S+ --match-hash sha256:[0-9a-f]+`)

func TestJourneyAllowFileFinding(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	pw := "Zq7" + strings.Repeat("x9K", 6) // built at run time: no password literal in the repo
	pw2 := "Wm4" + "Tq9Lb2Xv7Nc5Rd8Hy3Gk"
	var c *teamharness.Clone

	w.Step("day1/first-commit-is-refused-with-the-allow-line", func() {
		c = lap.Clone(w.Remote("github.com/widgets/conf"), "conf")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("config.yaml", "db:\n  password: \""+pw+"\"\n")
		c.Git("add", "-A")
		r := c.TryGit("commit", "-q", "-m", "first").Refused(t, "E_LEAK_BLOCKED", "config.yaml").Never(t, pw)
		lines := allowLine.FindAllString(r.Out(), -1)
		if len(lines) == 0 {
			t.Fatalf("no ready allow line:\n%s", r.Out())
		}
		for _, l := range lines {
			args := append(strings.Fields(strings.TrimPrefix(l, "lore "))[0:], "--reason", "sample password in a fixture", "--yes")
			lap.LoreIn(c, args...).OK(t).Says(t, "marked not a secret")
		}
	})

	w.Step("day1/the-commit-goes-through", func() {
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "first").OK(t)
	})

	w.Step("day1/a-new-value-in-that-file-is-still-refused", func() {
		c.WriteFile("config.yaml", "db:\n  password: \""+pw+"\"\n  admin_password: \""+pw2+"\"\n")
		c.Git("add", "config.yaml")
		c.TryGit("commit", "-q", "-m", "second").Refused(t, "E_LEAK_BLOCKED", "config.yaml").Never(t, pw2)
	})
}
