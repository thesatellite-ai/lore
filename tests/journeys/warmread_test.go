//go:build journeys && !windows

package journeys

// warmread_test.go — Sam's reads trust git (perf3): once the rows are
// committed a warm read asks git's index what changed instead of looking
// at every row file, so it must still see what Sam changes by hand: a
// committed task file edited in an editor, the same file edited again
// after git add, and the edit committed.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyWarmReadSeesHandEdits(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	repo := lap.Clone(w.Remote("github.com/sam-dev/warm-app"), "warm-app")
	var file string
	edit := func(old, repl string) {
		t.Helper()
		body := jRead(t, file)
		if !strings.Contains(body, old) {
			t.Fatalf("%s lacks %q", file, old)
		}
		jWrite(t, file, strings.Replace(body, old, repl, 1))
	}

	w.Step("day1/tasks-committed", func() {
		repo.WriteFile("main.go", "package main\n")
		repo.CommitAll("code")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "task", "add", "--title", "Ship the parser", "--commitment", "accepted", "--to", "repo", "--no-live").OK(t)
		lap.LoreIn(repo, "task", "add", "--title", "Write the docs", "--commitment", "accepted", "--to", "repo", "--no-live").OK(t)
		repo.CommitAll("lore: tasks")
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "Ship the parser", "Write the docs")
		files, err := filepath.Glob(filepath.Join(repo.Dir, ".lore", "data", "tasks", "*.json"))
		if err != nil || len(files) != 2 {
			t.Fatalf("task files: %v %v", files, err)
		}
		for _, f := range files {
			if strings.Contains(jRead(t, f), "Ship the parser") {
				file = f
			}
		}
	})

	w.Step("day2/hand-edit-is-read", func() {
		w.AdvanceDays(1)
		lap.LoreIn(repo, "task", "list").OK(t) // warm: nothing changed
		edit("Ship the parser", "Ship the parser v2")
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "Ship the parser v2", "Write the docs")
	})

	w.Step("day2/edit-after-git-add-is-read", func() {
		repo.Git("add", "-A", ".lore")
		edit("Ship the parser v2", "Ship the parser v3")
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "Ship the parser v3").Never(t, "parser v2")
		repo.Git("add", "-A", ".lore")
		repo.Git("commit", "-q", "-m", "lore: parser title")
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "Ship the parser v3", "Write the docs")
	})
}
