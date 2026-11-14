//go:build journeys && !windows

package journeys

// hookstaged_leak_test.go — a key pasted by hand into a row file that is
// not staged (D-224). The pre-commit hook stages lore's row files into the
// commit; the commit-msg block's first check refused the key but put the
// index back, so its second check found nothing and passed, and git
// committed the row with the key in it. Now the commit is refused, the
// index is as the person staged it, and the key never reaches a commit.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyHookStagedRowKeepsAKeyOut(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
	var c *teamharness.Clone
	var rel, clean string

	w.Step("day1/a-pushed-repo-with-a-rule", func() {
		c = lap.Clone(w.Remote("github.com/widgets/hookstaged"), "hookstaged")
		lap.LoreIn(c, "init").OK(t)
		lap.LoreIn(c, "rule", "add", "--body", "clean rule").OK(t)
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "start").OK(t)
		c.Git("push", "-q", "-u", "origin", "HEAD")
		files, err := filepath.Glob(filepath.Join(c.Dir, ".lore", "data", "rules", "*.json"))
		if err != nil || len(files) != 1 {
			t.Fatalf("one rule row file: %v %v", files, err)
		}
		rel, _ = filepath.Rel(c.Dir, files[0])
		b, err := os.ReadFile(filepath.Clean(files[0]))
		if err != nil {
			t.Fatal(err)
		}
		clean = string(b)
	})

	w.Step("day1/a-key-in-an-unstaged-row-file-is-refused", func() {
		c.WriteFile(rel, strings.Replace(clean, "clean rule", "key "+key, 1))
		c.WriteFile("README.md", "# widgets\n")
		c.Git("add", "README.md")
		head := c.Head()
		c.TryGit("commit", "-q", "-m", "readme only").Refused(t, "E_LEAK_BLOCKED", rel).Never(t, key)
		if c.Head() != head {
			t.Fatal("the refused commit made a commit")
		}
		if staged := c.Git("diff", "--cached", "--name-only"); staged != "README.md" {
			t.Fatalf("the index is as Dana staged it, got %q", staged)
		}
	})

	w.Step("day1/without-the-key-the-commit-goes-through", func() {
		c.WriteFile(rel, clean)
		c.TryGit("commit", "-q", "-m", "readme only").OK(t)
		if got := c.Git("log", "-p", "@{u}..HEAD"); strings.Contains(got, key) {
			t.Fatal("a commit holds the key")
		}
	})
}
