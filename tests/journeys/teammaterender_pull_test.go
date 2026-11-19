//go:build journeys && !windows

package journeys

// teammaterender_pull_test.go — a teammate's lore render in a freshly
// inited repo never blocks their git pull (devco N-7, the render side of
// stories A-1). Ida inits the project and pushes it without its render.
// Teo clones it and runs `lore render`: his .lore/LORE.md and LORE.full.md
// go to his clone's .git/info/exclude. Ida adds a note, renders and commits
// the render; Teo's plain `git pull` takes it.

import (
	"strings"
	"testing"
)

func TestJourneyTeammateRenderBeforeCommittedRender(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ida := w.Persona("Ida Lund", "ida@example.test").Laptop()
	teo := w.Persona("Teo Marsh", "teo@example.test").Laptop()
	repo := w.Remote("github.com/acme-tr/app")

	ic := ida.Clone(repo, "app")
	w.Step("day1/ida-inits-without-render", func() {
		ida.LoreIn(ic, "init", "--name", "app").OK(t)
		ic.Git("rm", "-q", "--cached", "--ignore-unmatch", ".lore/LORE.md", ".lore/LORE.full.md")
		fpCommitPush(t, ic, "lore init")
	})

	tc := teo.Clone(repo, "app")
	w.Step("day1/teo-renders", func() {
		teo.LoreIn(tc, "render").OK(t).Says(t, "git add -f .lore/LORE.md")
		if st := tc.Git("status", "--porcelain", "--untracked-files=all", "--", ".lore"); strings.Contains(st, "LORE") {
			t.Fatalf("Teo's render left untracked render files a pull of Ida's render will abort on:\n%s", st)
		}
	})

	w.Step("day2/ida-commits-render-teo-pulls", func() {
		ida.LoreIn(ic, "memory", "add", "--body", "the queue drains at night").OK(t)
		ida.LoreIn(ic, "render").OK(t)
		fpCommitPush(t, ic, "note and render")
		if ic.Git("ls-files", "--", ".lore/LORE.md") == "" {
			t.Fatal("Ida's commit does not carry her render: the clone that ran lore init must keep it in git status")
		}
		if r := tc.TryGit("pull", "-q", "--no-rebase", "origin", "main"); r.Code != 0 {
			t.Fatalf("Teo's git pull of Ida's render aborts: %s", r)
		}
	})
}
