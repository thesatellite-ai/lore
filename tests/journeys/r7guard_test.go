//go:build journeys && !windows

package journeys

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// regexpRuleID finds a rule id in command output.
var regexpRuleID = regexp.MustCompile(`rul_[0-9a-f]{32}`)

// TestJourneyAgentMistakesAreCaught (devco S-9, S-8): an agent in an own
// repo with no org. A key it puts into a note file with `git commit
// --no-verify`, after an ordinary commit that changed the same file, is
// refused by a plain `git push` (the push used to check only the oldest
// version of the file it carried, so the key reached the remote); a rule
// file it removes by hand is refused at commit, naming the rule and both
// ways on.
func TestJourneyAgentMistakesAreCaught(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Quill", "ada@widgets.example")
	remote := w.Remote("github.com/widgets/guarded")
	var c *teamharness.Clone
	lore := func(args ...string) teamharness.Result { return ada.Laptop().LoreIn(c, args...) }
	key := "AKIA" + strings.Repeat("W", 16) // made-up, built at run time

	w.Step("day1/no-verify-key-is-refused-at-push", func() {
		c = ada.Laptop().Clone(remote, "guarded")
		c.WriteFile("README.md", "guarded\n")
		c.CommitAll("init")
		c.Git("push", "-q", "origin", "HEAD:main")
		lore("init", "--non-interactive", "--name=guarded").OK(t)
		id := mgMemID.FindString(lore("memory", "add", "--body", "retry the flaky job once", "--no-live", "--yes").OK(t).Out())
		if id == "" {
			t.Fatal("memory add printed no id")
		}
		c.CommitAll("lore: memory")
		c.Git("push", "-q", "origin", "HEAD:main")
		rel := filepath.Join(".lore", "data", "memories", id+".json")
		body, ok := c.ReadFile(rel)
		if !ok {
			t.Fatalf("no row file %s", rel)
		}
		c.WriteFile(rel, strings.Replace(body, "retry the flaky job once", "retry the flaky job twice", 1))
		c.CommitAll("tidy memory")
		c.WriteFile(rel, strings.Replace(body, "retry the flaky job once", "retry the flaky job twice, prod key "+key, 1))
		c.Git("commit", "-q", "--no-verify", "-am", "tidy memory again")
		c.TryGit("push", "-q", "origin", "HEAD:main").Refused(t, "E_LEAK_BLOCKED", id, "lore secrets allow").Never(t, key)
	})

	w.Step("day1/a-rule-file-removed-by-hand-is-refused", func() {
		c.Git("reset", "-q", "--hard", "origin/main")
		rid := regexpRuleID.FindString(lore("rule", "add", "--summary", "tests green before push", "--body", "Run the tests before every push", "--severity", "must", "--no-live", "--yes").OK(t).Out())
		if rid == "" {
			t.Fatal("rule add printed no id")
		}
		c.CommitAll("lore: rule")
		c.Git("rm", "-q", filepath.ToSlash(filepath.Join(".lore", "data", "rules", rid+".json")))
		c.TryGit("commit", "-q", "-m", "remove stale file").Refused(t, "E_NEEDS_CONFIRM", `must rule "tests green before push"`, rid, "git checkout HEAD --", "lore rule delete --id "+rid)
		lore("doctor").Says(t, rid)
	})
}
