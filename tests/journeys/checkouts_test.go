//go:build journeys && !windows

package journeys

// checkouts_test.go — lore in the checkouts a repo's people really make
// (round 2 track D). Sam's repo had history before lore was added. A
// worktree at a commit from before lore used to say "lore isn't set up,
// run lore init" (which makes a second, empty project); now it names the
// branch that carries the project. `lore init` in a package folder used to
// make a second project with the root's name and say nothing about the
// root; now the sub-project gets its own name and init says the root holds
// one. A hand edit lore refused used to ride into the next commit; now the
// commit hook refuses it as the pick-up did.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyCheckoutsOfALoreRepo(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/checkouts-app")
	repo := lap.Clone(app, "checkouts-app")

	w.Step("day1/history-then-lore", func() {
		repo.WriteFile("src/main.go", "package main\n")
		repo.CommitAll("first code")
		repo.WriteFile("src/util.go", "package main\n")
		repo.CommitAll("more code")
		lap.LoreIn(repo, "init").OK(t)
		repo.CommitAll("lore: adopt")
	})

	w.Step("day2/worktree-from-before-lore-names-the-branch", func() {
		w.AdvanceDays(1)
		det := filepath.Join(filepath.Dir(repo.Dir), "checkouts-old")
		repo.Git("worktree", "add", "-q", "--detach", det, "HEAD~1")
		old := repo.In(filepath.Join("..", "checkouts-old"))
		lap.LoreIn(old, "task", "list").Refused(t, "from before lore was added", "git switch").Never(t, "lore init sets this repo up")
		lap.LoreIn(old, "task", "add", "--title", "x", "--commitment", "accepted").Refused(t, "from before lore was added").Never(t, "run `lore init`")
		if _, err := os.Stat(filepath.Join(det, ".lore")); err == nil {
			t.Fatal("a refused command made .lore in the old checkout")
		}
	})

	w.Step("day2/package-init-is-a-named-sub-project", func() {
		pkg := repo.In(filepath.Join("workers", "extraction"))
		lap.LoreIn(pkg, "init", "--non-interactive").OK(t).Says(t, "name:       checkouts-app-extraction", "already holds a lore project in the repo root")
	})

	w.Step("day3/refused-hand-edit-is-not-committed", func() {
		w.AdvanceDays(1)
		repo.Git("add", "-A")
		repo.Git("commit", "-q", "-m", "lore: package project")
		files, err := filepath.Glob(filepath.Join(repo.Dir, ".lore", "data", "projects", "*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no project row file: %v", err)
		}
		b, err := os.ReadFile(files[0])
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(string(b), "\n}", ",\n  \"my_note\": \"x\"\n}", 1)
		jWrite(t, files[0], edited)
		repo.Git("add", "-A", ".lore")
		r := repo.TryGit("commit", "-q", "-m", "odd hand edit")
		if r.Code == 0 || !strings.Contains(r.Out(), "unknown field my_note") {
			t.Fatalf("a refused hand edit was committed: %s", r)
		}
	})
}
