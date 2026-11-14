//go:build journeys && !windows

package journeys

// firstcommit_binary_test.go — mac:firstcommit-staged-binary: Dana starts
// a repo, runs `lore init`, builds the program and stages everything, the
// compiled binary included. The first commit goes through (it was still
// checked for secrets), and the commit hook names the compiled program,
// its size and the way to keep build output out of git. A later commit
// that only changes source says nothing about it.

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// fcELF is a made-up compiled program: an ELF header and some padding.
func fcELF() string {
	return "\x7fELF\x02\x01\x01\x00" + strings.Repeat("\x00", 56) + strings.Repeat("code", 4096)
}

func TestJourneyFirstCommitNamesStagedBinary(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	lap := dana.Laptop()
	var c *teamharness.Clone

	w.Step("day1/first-commit-with-the-built-program", func() {
		c = lap.Clone(w.Remote("github.com/widgets/tool"), "tool")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("main.go", "package main\n\nfunc main() {}\n")
		c.WriteFile("tool", fcELF())
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "first").OK(t).
			Says(t, "this commit adds compiled programs: tool (ELF, 16 KB)", ".gitignore", "git rm --cached -- tool", "git commit --amend").
			Never(t, "main.go (")
		if got := c.Git("ls-files", "--", "tool"); got != "tool" {
			t.Fatalf("the binary is not committed: %q", got)
		}
	})

	w.Step("day1/a-source-only-commit-is-quiet", func() {
		c.WriteFile("main.go", "package main\n\nfunc main() { println(1) }\n")
		c.Git("add", "main.go")
		c.TryGit("commit", "-q", "-m", "second").OK(t).Never(t, "compiled program")
	})
}
