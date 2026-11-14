//go:build journeys && !windows

package journeys

// pullrebase_test.go — `git pull --rebase` in a repo with lore's hooks
// (B3-20): the docs' own answer to a rejected push. Two people add rules
// on main; the second one's push is refused, they `git pull --rebase`, and
// the rebase finishes: lore's post-checkout hook leaves .lore/LORE.md alone
// while the rebase runs (it used to re-render it at the rebase's first
// checkout, and git stopped with "cannot rebase: You have unstaged
// changes" on a detached HEAD). The post-rewrite that ends the rebase
// leaves the committed render alone too (G-2: rendering it there left the
// tree dirty and the next pull refused); the next commit that carries rows
// renders it with every rule.

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
		// L-3: the person stages only their code; the pre-commit hook adds
		// lore's new row file and says so in one line
		a.Git("add", "main.go")
		r := a.TryGit("commit", "-q", "-m", "pia's rule and code")
		if r.Code != 0 || !strings.Contains(r.Stderr, "lore: added 1 lore row file(s) to this commit: .lore/data/rules/") {
			t.Fatalf("the commit does not say which lore row it added: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		a.CommitAll("anything left")
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
		// G-2: the pull leaves nothing changed, so the next pull works
		if st := a.Status(); st != "" {
			t.Fatalf("the pull left changed files:\n%s", st)
		}
		if r := a.TryGit("pull", "--rebase", "origin", "main"); r.Code != 0 {
			t.Fatalf("a second git pull --rebase: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
		}
		pia.Laptop().LoreIn(a, "rule", "list").OK(t).Says(t, "Rule one from pia", "Rule two from ned", "Rule three from pia")
		a.Git("push", "-q", "origin", "HEAD:main")
	})

	w.Step("day3/the-next-row-commit-renders-every-rule", func() {
		pia.Laptop().LoreIn(a, "rule", "add", "--body", "Rule four from pia").OK(t)
		a.CommitAll("pia's fourth rule")
		md, _ := a.ReadFile(".lore/LORE.md")
		// ned's should rule is search-only: the index counts it
		for _, want := range []string{"Rule one from pia", "Rule three from pia", "Rule four from pia", "4 rules"} {
			if !strings.Contains(md, want) {
				t.Errorf("the committed .lore/LORE.md lacks %q:\n%s", want, md)
			}
		}
		if st := a.Status(); st != "" {
			t.Fatalf("the commit left changed files:\n%s", st)
		}
	})
}
