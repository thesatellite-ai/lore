//go:build journeys && !windows

package journeys

// rewrite_test.go — history rewrites on a repo whose notes travel with git
// (bug hunt H-7, H-8, H-14). A teammate's forced push that would take a
// decision off main is stopped by the pre-push hook naming it; once pushed
// anyway, a teammate's `decision show` names the commit and `git
// cherry-pick`, and the author's own `git pull --rebase` (which drops the
// commit as already upstream) says so with the same way back. A note a
// commit removed with `git rm` is listed as removed by that commit's
// author, never "purged by ?", and `trash restore` prints the git checkout
// that brings it back. A rebase moves commit links to the new commits.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// jRewriteIDOut is the id of a write's --json output: top level (a
// decision) or under data (a task).
type jRewriteIDOut struct {
	ID   string `json:"id"`
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

// jRewriteID decodes the id of a successful write's --json output.
func jRewriteID(t *testing.T, r teamharness.Result) string {
	t.Helper()
	var out jRewriteIDOut
	err := json.Unmarshal([]byte(r.Stdout), &out)
	if out.ID == "" {
		out.ID = out.Data.ID
	}
	if err != nil || out.ID == "" {
		t.Fatalf("no id in %s (%v)", r.Stdout, err)
	}
	return out.ID
}

func TestJourneyRewriteKeepsNotes(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	remote := w.Remote("github.com/widgets/rewrite")
	ana := w.Persona("Ana Lind", "ana@widgets.example").Laptop()
	ben := w.Persona("Ben Ortiz", "ben@widgets.example").Laptop()
	var a, b *teamharness.Clone
	var dec, decCommit string

	w.Step("day1/a-repo-with-notes", func() {
		a = ana.Clone(remote, "rewrite")
		ana.LoreIn(a, "init").OK(t)
		a.WriteFile("README.md", "# widgets\n")
		a.CommitAll("start")
		a.Git("push", "-q", "-u", "origin", "HEAD")
		b = ben.Clone(remote, "rewrite")
		ben.LoreIn(b, "sync").OK(t) // wires lore's hooks in this clone
	})

	w.Step("day2/a-decision-is-pushed", func() {
		dec = jRewriteID(t, ana.LoreIn(a, "decision", "add", "--title", "Pin Go 1.23 in CI", "--body", "pin it", "--json").OK(t))
		a.CommitAll("Record Go pin decision")
		decCommit = a.Git("rev-parse", "--short", "HEAD")
		a.Git("push", "-q")
		b.Git("pull", "-q")
		ben.LoreIn(b, "decision", "show", "--id", dec).OK(t)
	})

	w.Step("day2/a-forced-push-that-drops-it-is-stopped", func() {
		b.Git("reset", "-q", "--hard", "HEAD~1")
		b.TryGit("push", "-f", "origin", "HEAD").Refused(t, "E_NEEDS_CONFIRM", dec, decCommit, "LORE_ALLOW_ROW_DROP=1")
		if got := b.Git("ls-remote", "origin", "refs/heads/main"); !strings.HasPrefix(got, a.Git("rev-parse", "HEAD")) {
			t.Fatalf("the refused push moved main: %s", got)
		}
	})

	w.Step("day2/pushed-anyway-the-teammate-is-told-the-way-back", func() {
		b.With("LORE_ALLOW_ROW_DROP=1").Git("push", "-q", "-f", "origin", "HEAD")
		ben.LoreIn(b, "decision", "show", "--id", dec).Refused(t, "E_NOT_FOUND", "git cherry-pick "+decCommit).
			Never(t, "may belong to another repo")
	})

	w.Step("day2/the-authors-pull-rebase-says-it-dropped-the-note", func() {
		a.TryGit("pull", "--rebase").OK(t).Says(t, "this rebase removed 1 lore row", dec, "git cherry-pick "+decCommit)
		a.Git("cherry-pick", decCommit)
		ana.LoreIn(a, "decision", "show", "--id", dec).OK(t)
		a.Git("push", "-q")
	})

	w.Step("day3/a-git-rm-is-named-with-its-author-and-the-way-back", func() {
		b.Git("pull", "-q")
		b.Git("rm", "-q", ".lore/data/decisions/"+dec+".json")
		b.Git("commit", "-q", "-m", "Remove the Go pin decision")
		removed := b.Git("rev-parse", "--short", "HEAD")
		b.Git("push", "-q")
		a.Git("pull", "-q")
		back := "git checkout " + removed + "^ -- :/.lore/data/decisions/" + dec + ".json"
		ana.LoreIn(a, "decision", "show", "--id", dec).Refused(t, "E_NOT_FOUND", back, "Ben Ortiz")
		ana.LoreIn(a, "trash", "list", "--from-history").OK(t).Says(t, dec, "removed", "by Ben Ortiz", back).Never(t, "by ?", "never come back")
		ana.LoreIn(a, "trash", "restore", "--id", dec).Refused(t, "E_NOT_FOUND", back)
		a.Git(strings.Fields(strings.TrimPrefix(back, "git "))...)
		ana.LoreIn(a, "decision", "show", "--id", dec).OK(t)
		a.CommitAll("Bring the Go pin decision back")
		a.Git("push", "-q")
	})

	w.Step("day4/a-rebase-moves-commit-links", func() {
		task := jRewriteID(t, ana.LoreIn(a, "task", "add", "--title", "One devbox binary", "--commitment", "accepted", "--json").OK(t))
		a.WriteFile("main.go", "package main\n")
		a.CommitAll("One devbox binary work")
		ana.LoreIn(a, "link", "add", "--entity", task, "--commit", "HEAD").OK(t)
		a.CommitAll("link the work")
		a.TryGit("rebase", "--force-rebase", "HEAD~2").OK(t).Says(t, "moved 1 commit link(s)")
		rewritten := a.Git("rev-parse", "HEAD~1")
		ana.LoreIn(a, "commit", "show", "--id", rewritten).OK(t).Says(t, "tasks/"+task)
		ana.LoreIn(a, "doctor").Never(t, "commit_links:")
	})
}
