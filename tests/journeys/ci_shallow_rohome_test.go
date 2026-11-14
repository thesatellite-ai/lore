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
//
// devco K-15: the remote also holds refs/lore/live. Before, check leaks
// --ci fetched it into the CI clone (.git/lore/*, new objects, the ref),
// render --check and doctor rewrote .git/lore/lore-live.seen* and
// .git/index, and each said "lore sync … sets up merge drivers". Now every
// CI command leaves .git byte-identical, .git/lore and FETCH_HEAD
// included, and none tells CI to run lore sync. K-14: in a repo with no
// lore data render --check passes with one line, as check leaks --ci and
// doctor do. K-9: lore fix --all --dry-run (and lore fix --all in a
// 0.1.17-format repo it leaves as it is) writes nothing in either .git.

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
	plainApp := w.Remote("github.com/sam-dev/ci-plain")
	oldApp := w.Remote("github.com/sam-dev/ci-old")

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
		// K-15: a live row, so the remote holds refs/lore/live
		lap.LoreIn(repo, "task", "add", "ship the retry budget", "--commitment", "accepted").OK(t)
		lap.LoreIn(repo, "sync").OK(t)
		if repo.Git("ls-remote", "origin", "refs/lore/live") == "" {
			t.Fatal("setup: the remote has no refs/lore/live")
		}
		plain := lap.Clone(plainApp, "ci-plain")
		plain.WriteFile("README.md", "# no lore here\n")
		plain.CommitAll("docs: readme")
		plain.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day1/fix-all-dry-run-writes-nothing", func() {
		// K-9: one repo on the current format, one still on 0.1.17
		code := filepath.Join(filepath.Dir(lap.Home), "fixall", "code")
		if err := os.MkdirAll(code, 0o750); err != nil {
			t.Fatal(err)
		}
		cur := lap.Clone(app, "ci-shallow-cur")
		old := lap.Clone(oldApp, "ci-old")
		fixOldRepo(t, old)
		lap.MoveClone(t, cur, filepath.Join(code, "cur"))
		lap.MoveClone(t, old, filepath.Join(code, "old"))
		w.AdvanceDays(1) // a fetch is due in both
		curGit, oldGit := gitTree(t, cur.Dir), gitTree(t, old.Dir)
		lap.Lore("", "fix", "--all", "--dirs", code, "--dry-run").OK(t).Says(t, "on the lore 0.1.17 format")
		if diff := untouchedwalk.Diff(curGit, gitTree(t, cur.Dir)); diff != "" {
			t.Fatalf("lore fix --all --dry-run wrote into .git:\n%s", diff)
		}
		if diff := untouchedwalk.Diff(oldGit, gitTree(t, old.Dir)); diff != "" {
			t.Fatalf("lore fix --all --dry-run wrote into the 0.1.17-format repo's .git:\n%s", diff)
		}
		// the real run leaves the 0.1.17-format repo as it is too
		lap.Lore("", "fix", "--all", "--dirs", code).OK(t).Says(t, "on the lore 0.1.17 format")
		if diff := untouchedwalk.Diff(oldGit, gitTree(t, old.Dir)); diff != "" {
			t.Fatalf("lore fix --all wrote into the 0.1.17-format repo it left as it is:\n%s", diff)
		}
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
		plain := ci.Clone(plainApp, "ci-plain")
		status := clone.Git("status", "--porcelain", "--ignored")
		// K-15: every file, .git/lore and FETCH_HEAD included
		tree, err := untouchedwalk.Tree(clone.Dir)
		if err != nil {
			t.Fatal(err)
		}
		plainTree, err := untouchedwalk.Tree(plain.Dir)
		if err != nil {
			t.Fatal(err)
		}
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
			ci.LoreIn(c, "check", "leaks", "--ci").OK(t).Says(t, "every row in .lore/data belongs to this project").Never(t, "lore sync")
			ci.LoreIn(c, "render", "--check").OK(t).Says(t, "rendered files match their rows").Never(t, "E_READ_ONLY_FS", "E_REFUSED", "lore sync")
			ci.LoreIn(c, "rule", "list").OK(t).Says(t, "never log a request body")
			ci.LoreIn(c, "memory", "list").OK(t).Says(t, "testdata")
			ci.LoreIn(c, "search", "--query", "fixtures").OK(t).Says(t, "testdata")
			ci.LoreIn(c, "doctor").OK(t).Says(t, "Status: HEALTHY", "shallow clone").Never(t, "DEGRADED", "E_READ_ONLY_FS", "lore sync (or")
		}
		// K-14: the three CI commands agree in a repo with no lore data
		ci.LoreIn(plain, "check", "leaks", "--ci").OK(t).Says(t, "has no .lore folder")
		ci.LoreIn(plain, "render", "--check").OK(t).Says(t, "has no .lore folder").Never(t, "E_NOT_A_STORE")
		ci.LoreIn(plain, "doctor").OK(t).Says(t, "Status: HEALTHY")

		readOnlyTree(t, ci.Home, false)
		if got := clone.Git("status", "--porcelain", "--ignored"); got != status {
			t.Fatalf("git status changed in the CI clone:\nbefore:\n%s\nafter:\n%s", status, got)
		}
		if diff := untouchedwalk.Diff(tree, mustTree(t, clone.Dir)); diff != "" {
			t.Fatalf("the CI reads wrote into the clone:\n%s", diff)
		}
		if got := clone.Git("for-each-ref", "refs/lore"); got != "" {
			t.Fatalf("the CI reads fetched lore's refs into the clone:\n%s", got)
		}
		if diff := untouchedwalk.Diff(plainTree, mustTree(t, plain.Dir)); diff != "" {
			t.Fatalf("the CI reads wrote into the repo with no lore data:\n%s", diff)
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

// mustTree is every file under dir (untouchedwalk.Tree), .git included.
func mustTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree, err := untouchedwalk.Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// gitTree is every file under the clone's .git, lore's own .git/lore
// included (K-9: a dry run writes nothing there either).
func gitTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	return mustTree(t, filepath.Join(dir, ".git"))
}
