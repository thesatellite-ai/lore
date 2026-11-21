//go:build journeys && !windows

package journeys

// private_gitignore_test.go — devco:D-856 (bug hunt H-124): a dev keeps
// lore's notes to themselves by listing .lore/ in the clone's own
// .git/info/exclude. lore init, a plain add, render and doctor --fix all
// appended lore's "# lore" block to the TRACKED .gitignore (and again
// after every revert), so git status was never clean. Now no command
// touches .gitignore in such a repo, and a block an older lore left there
// is named by `lore doctor` and taken out by `lore doctor --fix`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyPrivateRepoKeepsGitignore(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pat := w.Persona("Pat Lindqvist", "pat@acme.test")
	lap := pat.Laptop()
	app := w.Remote("github.com/acme-co/notes-app")
	repo := lap.Clone(app, "notes-app")
	repo.WriteFile(".gitignore", "node_modules/\n")
	repo.CommitAll("gitignore")
	exclude := filepath.Join(repo.Dir, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exclude, []byte(".lore/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clean := func(step string) {
		t.Helper()
		if st := repo.Status(); st != "" {
			t.Errorf("%s: git status not clean in a private repo:\n%s", step, st)
		}
		if gi, _ := repo.ReadFile(".gitignore"); gi != "node_modules/\n" {
			t.Errorf("%s: .gitignore changed:\n%s", step, gi)
		}
	}

	w.Step("private/no-command-writes-gitignore", func() {
		lap.LoreIn(repo, "init").OK(t)
		clean("lore init")
		lap.LoreIn(repo, "memory", "add", "--body", "notes-app: retry the gateway twice").OK(t)
		clean("lore memory add")
		lap.LoreIn(repo, "render").OK(t)
		clean("lore render")
		// doctor is healthy: no "re-includes them in .gitignore" and no
		// "fix: lore render" for a pointer render never writes here
		lap.LoreIn(repo, "doctor").OK(t).Says(t, "private to this machine").Never(t, "holds lore's block", "re-includes", "has no lore pointer")
		lap.LoreIn(repo, "doctor", "--fix").OK(t)
		clean("lore doctor --fix")
	})

	w.Step("private/old-block-named-and-taken-out", func() {
		repo.WriteFile(".gitignore", "node_modules/\n\n# lore\n.lore/lock\n.lore/state/\n")
		repo.CommitAll("block an older lore wrote")
		lap.LoreIn(repo, "doctor").Says(t, "holds lore's block", "`lore doctor --fix` takes them out")
		lap.LoreIn(repo, "doctor", "--fix").Says(t, "took lore's block")
		gi, _ := repo.ReadFile(".gitignore")
		if gi != "node_modules/\n" {
			t.Errorf("doctor --fix left .gitignore as:\n%s", gi)
		}
		if st := repo.Status(); !strings.Contains(st, ".gitignore") || strings.Count(strings.TrimSpace(st), "\n") != 0 {
			t.Errorf("after doctor --fix only .gitignore should wait for a commit, git status:\n%s", st)
		}
		lap.LoreIn(repo, "doctor").Never(t, "holds lore's block")
	})
}
