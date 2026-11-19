//go:build journeys && !windows

package journeys

// nogit_test.go — git missing from PATH (devco:U-5): an agent sandbox or a
// minimal CI image runs lore without git. Every command that needs git
// answers one plain error naming git and the two fixes that exist, never
// E_INTERNAL and never a way to "pass its path" that no flag offers;
// lore doctor says broken with one line naming git; lore version works.

import (
	"testing"
)

func TestJourneyGitMissingFromPath(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	zed := w.Persona("Zed Sandbox", "zed@sandbox.example")
	lap := zed.Laptop()
	site := w.Remote("github.com/zed-dev/agent")
	repo := lap.Clone(site, "agent")

	w.Step("day1/set-up-with-git", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "the sandbox has no network").OK(t)
	})

	w.Step("day1/git-gone-from-path", func() {
		lap.SetEnv("PATH", t.TempDir()) // a folder with no git in it
		defer lap.SetEnv("PATH", "")    // the invariants after the step need git
		for _, args := range [][]string{
			{"memory", "list"},
			{"context", "--for-file", "README.md"},
			{"memory", "add", "--body", "written without git"},
			{"sync"},
		} {
			lap.LoreIn(repo, args...).Fails(t, 23, "E_GIT_MISSING", "no git program on PATH", "install git, or put the folder that holds it on PATH").
				Never(t, "E_INTERNAL", "pass its path")
		}
		lap.LoreIn(repo, "doctor").Fails(t, 2, "Status: BROKEN", "git: lore needs git").Never(t, "pass its path")
		lap.LoreIn(repo, "version").OK(t)
	})
}
