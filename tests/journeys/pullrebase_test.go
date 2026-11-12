//go:build journeys && !windows

package journeys

// pullrebase_test.go — `git pull --rebase` in a repo with lore's hooks
// (B3-20): the docs' own answer to a rejected push. Two people add rules
// on main; the second one's push is refused, they `git pull --rebase`, and
// the rebase finishes: lore's post-checkout hook leaves .lore/LORE.md alone
// while the rebase runs (it used to re-render it at the rebase's first
// checkout, and git stopped with "cannot rebase: You have unstaged
// changes" on a detached HEAD); the post-rewrite that ends the rebase
// renders it with both rules.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestJourneyPullRebase(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	pia := w.Persona("Pia Park", "pia@widgets.example")
	ned := w.Persona("Ned North", "ned@widgets.example")
	remote := w.Remote("github.com/widgets/api")
	var a, b *teamharness.Clone

	w.Step("day1/a-project-that-renders", func() {
		a = pia.Laptop().Clone(remote, "api")
		a.WriteFile("README.md", "api\n")
		a.CommitAll("init")
		a.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "init", "--non-interactive", "--name=api").OK(t)
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule one from pia").OK(t)
		pia.Laptop().LoreIn(a, "render").OK(t)
		a.CommitAll("lore")
		a.Git("push", "-q", "origin", "HEAD:main")
		b = ned.Laptop().Clone(remote, "api")
	})

	w.Step("day2/both-add-rules-on-main", func() {
		w.AdvanceDays(1)
		ned.Laptop().LoreIn(b, "rule", "add", "--body", "Rule two from ned", "--severity", "should").OK(t)
		b.CommitAll("ned's rule")
		b.Git("push", "-q", "origin", "HEAD:main")
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule three from pia").OK(t)
		a.WriteFile("main.go", "package main\n")
		a.CommitAll("pia's rule and code")
		if s := a.Status(); s != "" {
			t.Fatalf("pia's clone is not clean before the pull:\n%s", s)
		}
	})

	w.Step("day2/pull-rebase-finishes", func() {
		r := a.TryGit("pull", "--rebase", "origin", "main")
		if r.Code != 0 {
			t.Fatalf("git pull --rebase: exit %d\n%s%s\nstatus:\n%s", r.Code, r.Stdout, r.Stderr, a.TryGit("status").Stdout)
		}
		gitDir := a.Git("rev-parse", "--absolute-git-dir")
		for _, m := range []string{"rebase-merge", "rebase-apply"} {
			if _, err := os.Stat(filepath.Join(gitDir, m)); err == nil {
				t.Fatalf("a rebase is still in progress (%s)", m)
			}
		}
		if br := a.Git("symbolic-ref", "--short", "HEAD"); br != "main" {
			t.Fatalf("HEAD after the pull is %q, want main", br)
		}
		// only the rendered files may differ (rendered at the rebase's end); no row file
		for _, l := range strings.Split(a.Status(), "\n") {
			if l != "" && !strings.HasSuffix(l, ".lore/LORE.md") && !strings.HasSuffix(l, ".lore/LORE.full.md") {
				t.Errorf("the pull left a changed file: %q", l)
			}
		}
		// the post-rewrite that ended the rebase rendered the pulled rows
		md, _ := a.ReadFile(".lore/LORE.md")
		pia.Laptop().LoreIn(a, "render").OK(t)
		if again, _ := a.ReadFile(".lore/LORE.md"); again != md {
			t.Errorf(".lore/LORE.md was not rendered after the rebase:\n%s\n--- lore render writes ---\n%s", md, again)
		}
		pia.Laptop().LoreIn(a, "rule", "list").OK(t).Says(t, "Rule one from pia", "Rule two from ned", "Rule three from pia")
		a.Git("push", "-q", "origin", "HEAD:main")
	})
}
