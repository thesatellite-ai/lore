//go:build journeys && !windows

package journeys

// ci_shallow_rohome_test.go — a CI job the way GitHub Actions runs it
// (F-6, F-7, F-8, D-9): a depth-1 clone, a HOME the job cannot write, no
// git identity, CI=true. Before, every read (list, search, render --check)
// failed E_READ_ONLY_FS because the cache folder under ~/.lore could not
// be made, and doctor read DEGRADED for the shallow history alone. Now the
// reads keep their caches in a temp folder removed at exit, doctor reads
// HEALTHY, and neither the clone (git status --ignored, every file) nor
// the home changes.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
)

func TestJourneyCIShallowReadOnlyHome(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/ci-shallow")

	w.Step("day1/notes-rendered-and-pushed", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		repo := lap.Clone(app, "ci-shallow")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "rule", "add", "--body", "never log a request body").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "the test fixtures live in testdata").OK(t)
		lap.LoreIn(repo, "render").OK(t)
		repo.CommitAll("lore: notes")
		repo.Git("push", "-q", "origin", "HEAD")
		repo.WriteFile("main.go", "package main\n")
		repo.CommitAll("code: main")
		repo.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/ci-job-reads", func() {
		w.AdvanceDays(1)
		ci := sam.NewLaptop("runner")
		clone := ci.Clone(app, "ci-shallow")
		clone.Git("fetch", "-q", "--depth", "1", "origin", "main")
		clone.Git("reset", "-q", "--hard", "FETCH_HEAD")
		if got := clone.Git("rev-parse", "--is-shallow-repository"); got != "true" {
			t.Fatalf("the CI clone is not shallow: %q", got)
		}
		// no git identity on the runner
		gc := filepath.Join(ci.Home, ".gitconfig")
		for _, k := range []string{"user.name", "user.email"} {
			w.TryGit(ci.Home, "config", "--file", gc, "--unset", k)
		}
		ci.SetEnv("CI", "true")
		ci.SetEnv("GITHUB_ACTIONS", "true")
		status := clone.Git("status", "--porcelain", "--ignored")
		tree := ciCloneTree(t, clone.Dir)
		home, err := untouchedwalk.Tree(ci.Home)
		if err != nil {
			t.Fatal(err)
		}
		readOnlyTree(t, ci.Home, true)
		defer readOnlyTree(t, ci.Home, false)

		for _, ro := range []string{"", "LORE_READ_ONLY=1"} {
			c := clone
			if ro != "" {
				c = clone.With(ro)
			}
			ci.LoreIn(c, "render", "--check").OK(t).Says(t, "rendered files match their rows").Never(t, "E_READ_ONLY_FS", "E_REFUSED")
			ci.LoreIn(c, "rule", "list").OK(t).Says(t, "never log a request body")
			ci.LoreIn(c, "memory", "list").OK(t).Says(t, "testdata")
			ci.LoreIn(c, "search", "--query", "fixtures").OK(t).Says(t, "testdata")
			ci.LoreIn(c, "doctor").OK(t).Says(t, "Status: HEALTHY", "shallow clone").Never(t, "DEGRADED", "E_READ_ONLY_FS")
		}

		readOnlyTree(t, ci.Home, false)
		if got := clone.Git("status", "--porcelain", "--ignored"); got != status {
			t.Fatalf("git status changed in the CI clone:\nbefore:\n%s\nafter:\n%s", status, got)
		}
		if diff := untouchedwalk.Diff(tree, ciCloneTree(t, clone.Dir)); diff != "" {
			t.Fatalf("the CI reads wrote into the clone:\n%s", diff)
		}
		after, err := untouchedwalk.Tree(ci.Home)
		if err != nil {
			t.Fatal(err)
		}
		if diff := untouchedwalk.Diff(home, after); diff != "" {
			t.Fatalf("the CI reads wrote into the read-only home:\n%s", diff)
		}
	})
}

// readOnlyTree takes write permission off every folder under dir (on), or
// gives it back to the owner (off), as a CI runner's read-only HOME.
func readOnlyTree(t *testing.T, dir string, on bool) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.IsDir() {
			return err
		}
		mode := fi.Mode().Perm() | 0o200
		if on {
			mode = fi.Mode().Perm() &^ 0o222
		}
		return os.Chmod(p, mode)
	})
	if err != nil {
		t.Fatal(err)
	}
}
