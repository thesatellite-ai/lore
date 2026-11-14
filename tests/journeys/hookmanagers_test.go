//go:build journeys && !windows

package journeys

// hookmanagers_test.go — lore's hooks next to a person's own git habits and
// hook managers (H-15, H-18, H-19).
//
//   - `git commit -a` with nothing but a new lore row: git decides there is
//     nothing to commit before its hooks run, so the hook no longer prints
//     "added 1 lore row file(s)" right before git's "nothing added"; it
//     leaves the row out and says how to commit it.
//   - husky takes the hooks over (core.hooksPath .husky, a tracked folder):
//     a lore write adds its block to the hook file already there but
//     creates no new file in .husky, and says which it left out; doctor's
//     first warning is the leak guard being off, with `lore fix`; a commit
//     with a key made meanwhile is named by the next lore command; `lore fix`
//     (which asks) creates the hook files and says they are new files of
//     the repo; the push is then refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyHookManagers(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	kim := w.Persona("Kim Ardent", "kim@gadgets.example")
	lap := kim.Laptop()
	key := "AKIA" + strings.Repeat("Q", 16) // built at run time: no key-shaped literal in the repo
	var c *teamharness.Clone

	w.Step("day1/a-pushed-repo", func() {
		c = lap.Clone(w.Remote("github.com/gadgets/hooks"), "hooks")
		lap.LoreIn(c, "init").OK(t)
		c.WriteFile("README.md", "# gadgets\n")
		c.Git("add", "-A")
		c.TryGit("commit", "-q", "-m", "start").OK(t)
		c.Git("push", "-q", "-u", "origin", "HEAD")
	})

	w.Step("day1/commit-a-with-only-a-new-row-claims-nothing", func() {
		lap.LoreIn(c, "memory", "add", "--body", "dry runs first").OK(t)
		r := c.TryGit("commit", "-q", "-a", "-m", "note")
		r.Refused(t, "left out of this commit", "git add .lore/data && git commit").Never(t, "to this commit:")
		c.Git("add", ".lore/data")
		c.TryGit("commit", "-q", "-m", "note").OK(t)
	})

	husky := filepath.Join(c.Dir, ".husky")
	w.Step("day2/husky-takes-the-hooks-over", func() {
		c.WriteFile(".husky/pre-commit", "#!/bin/sh\necho team check\n")
		if err := os.Chmod(filepath.Join(husky, "pre-commit"), 0o755); err != nil { // #nosec G302 -- a hook git must run
			t.Fatal(err)
		}
		c.Git("add", ".husky")
		c.TryGit("commit", "-q", "-m", "husky").OK(t)
		c.Git("config", "core.hooksPath", ".husky")
		lap.LoreIn(c, "memory", "add", "--body", "husky runs the lint").OK(t).
			Says(t, "lore did not create commit-msg, post-checkout, post-commit, post-merge, post-rewrite, pre-push in "+husky, "leak guard off in this clone")
		if st := c.Git("status", "--porcelain", "--untracked-files=all", "--", ".husky"); strings.Contains(st, "??") {
			t.Fatalf("a lore write created files in the tracked .husky:\n%s", st)
		}
		pc, err := os.ReadFile(filepath.Join(husky, "pre-commit"))
		if err != nil || !strings.Contains(string(pc), "lore-sync") || !strings.Contains(string(pc), "echo team check") {
			t.Fatalf("lore's block is not in the existing .husky/pre-commit beside the team's line: %v\n%s", err, pc)
		}
	})

	w.Step("day2/doctor-says-the-guard-is-off-first", func() {
		r := lap.LoreIn(c, "doctor")
		out := r.Out()
		i := strings.Index(out, "Warnings:")
		if i < 0 || !strings.HasPrefix(strings.TrimSpace(out[i+len("Warnings:"):]), "- live: leak guard off in this clone") || !strings.Contains(out, "run `lore fix` to restore them") {
			t.Fatalf("doctor's first warning is not the leak guard with lore fix:\n%s", r)
		}
	})

	w.Step("day2/a-key-slips-through-and-the-next-command-names-it", func() {
		c.WriteFile("deploy/aws.env", "AWS_ACCESS_KEY_ID="+key+"\n")
		c.Git("add", "deploy/aws.env")
		c.TryGit("commit", "-q", "-m", "probe").OK(t)
		lap.LoreIn(c, "memory", "list").OK(t).Says(t, "leak guard off in this clone", "deploy/aws.env:1").Never(t, key)
	})

	w.Step("day2/lore-fix-creates-the-hook-files-and-says-so", func() {
		lap.LoreIn(c, "fix", "--yes").Says(t, "new files in "+husky+", a folder committed with the repo", "restored now").Never(t, key)
		c.TryGit("push", "-q").Refused(t, "E_LEAK_BLOCKED", "deploy/aws.env").Never(t, key)
	})
}
