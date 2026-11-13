//go:build journeys && !windows

package journeys

// ci_readonly_test.go — a CI job's checks leave its clone alone (C-8).
// Sam's repo has lore notes and a rendered LORE.md just over the warn
// level. A CI runner clones it fresh, never set up for lore, and runs
// `lore render --check`, `lore migrate status` and `lore doctor`: before,
// render --check wired the merge driver and seven hook blocks into .git
// and then failed on the warn level, and migrate status appended to
// .git/info/exclude. Now each writes nothing in .git (config, hooks,
// info) or the work tree, and the warn level is a warning, not a failure.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/untouchedwalk"
)

// ciCloneTree is the clone's tree for the comparison: .git/lore (lore's
// runtime folder: the read fetch throttle and the live lock) and git's own
// FETCH_HEAD are left out, as a read may refresh them.
func ciCloneTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	tree, err := untouchedwalk.Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	for p := range tree {
		if r := filepath.ToSlash(p); r == ".git/lore" || strings.HasPrefix(r, ".git/lore/") || r == ".git/FETCH_HEAD" {
			delete(tree, p)
		}
	}
	return tree
}

func TestJourneyCIChecksLeaveTheCloneAlone(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	sam := w.Persona(soloName, soloEmail)
	lap := sam.Laptop()
	app := w.Remote("github.com/sam-dev/ci-app")

	w.Step("day1/notes-rendered-over-the-warn-level", func() {
		lap.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		repo := lap.Clone(app, "ci-app")
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "rule", "add", "--body", "never log a request body").OK(t)
		lap.LoreIn(repo, "memory", "add", "--body", "the build cache lives in the runner's tmp folder").OK(t)
		lap.LoreIn(repo, "policy", "set", "context.warn_at", "1", "--yes").OK(t)
		lap.LoreIn(repo, "render").OK(t)
		repo.CommitAll("lore: notes")
		repo.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/ci-clone-runs-the-checks", func() {
		w.AdvanceDays(1)
		ci := sam.NewLaptop("ci")
		ci.GitHubAccounts(t, GitHubAccount{Login: soloLogin, Key: "id_ed25519"})
		clone := ci.Clone(app, "ci-app")
		before := ciCloneTree(t, clone.Dir)
		ci.LoreIn(clone, "render", "--check").OK(t).Says(t, "over the warn level").Never(t, "E_REFUSED")
		ci.LoreIn(clone, "migrate", "status").OK(t)
		ci.LoreIn(clone, "doctor")
		if diff := untouchedwalk.Diff(before, ciCloneTree(t, clone.Dir)); diff != "" {
			t.Fatalf("the CI checks wrote into the clone:\n%s", diff)
		}
	})
}
