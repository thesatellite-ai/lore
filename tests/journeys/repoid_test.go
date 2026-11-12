//go:build journeys && !windows

package journeys

// repoid_test.go — two machines upgrade one 0.1.17 repo (B2-7): Dana runs
// `lore fix` and pushes; Sam clones; both run `lore sync` before either
// commits what it left. Each mints the repo's id on its own, and before
// the id was derived from the project and the address they minted two, so
// after a normal pull the repo had two repos rows. Now both mint one id:
// after the merge the repo has one row and `lore repo list` one repo.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// repoRows lists the repos row files of c.
func repoRows(t testing.TB, c *teamharness.Clone) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(c.Dir, ".lore", "data", "repos"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestJourneyTwoMachinesMintOneRepoID(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	dana := w.Persona("Dana Reyes", "dana@widgets.example")
	sam := w.Persona("Sam Okafor", "sam@widgets.example")
	remote := w.Remote("github.com/widgets/one")
	var dc, sc *teamharness.Clone

	w.Step("day1/dana-fixes-the-old-repo-and-pushes", func() {
		dc = dana.Laptop().Clone(remote, "one")
		fixOldRepo(t, dc)
		dana.Laptop().LoreIn(dc, "fix").OK(t)
		dc.CommitAll("lore fix")
		dc.Git("push", "-q", "origin", "HEAD:main")
		sc = sam.Laptop().Clone(remote, "one")
	})

	w.Step("day1/both-sync-before-either-commits", func() {
		dana.Laptop().LoreIn(dc, "sync").OK(t)
		sam.Laptop().LoreIn(sc, "sync").OK(t)
		if d, s := repoRows(t, dc), repoRows(t, sc); strings.Join(d, ",") != strings.Join(s, ",") || len(d) != 1 {
			t.Fatalf("the two machines minted different repo ids: dana %v, sam %v", d, s)
		}
	})

	w.Step("day1/after-the-merge-one-repo", func() {
		dc.CommitAll("dana: what lore left")
		dc.Git("push", "-q", "origin", "HEAD:main")
		sc.CommitAll("sam: what lore left")
		sc.Git("pull", "-q", "--no-rebase", "origin", "main")
		if rows := repoRows(t, sc); len(rows) != 1 {
			t.Fatalf("repos rows after the merge: %v", rows)
		}
		r := sam.Laptop().LoreIn(sc, "repo", "list").OK(t)
		if n := strings.Count(strings.TrimSpace(r.Stdout), "\n") + 1; n != 1 {
			t.Fatalf("repo list shows %d repos:\n%s", n, r.Stdout)
		}
		sam.Laptop().LoreIn(sc, "doctor").Never(t, "duplicate_repos")
	})
}
