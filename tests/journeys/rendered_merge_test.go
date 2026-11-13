//go:build journeys && !windows

package journeys

// rendered_merge_test.go — the files lore render commits, over a week of
// two people adding rules (devco D-19, D-22, D-23). Ada and Ben each add
// rules to the same repo and commit them with the rendered files; Ben's
// plain `git pull --no-rebase` merges with no conflict in
// .lore/LORE.full.md (it had no merge rule and conflicted every time).
// A package of the repo gets its own project, and its AGENTS.md names the
// package's LORE.md, not the root's. A repo whose CLAUDE.md is a link to
// AGENTS.md gets one pointer block serving both tools, written once.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Placeholder remotes of this journey.
const (
	jRenderSlug     = "github.com/acme-test/render-app"
	jRenderLinkSlug = "github.com/acme-test/render-linked"
)

func TestJourneyRenderedFiles(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	ada := w.Persona("Ada Rowe", "ada.rowe@example.test").Laptop()
	ben := w.Persona("Ben Hale", "ben.hale@example.test").Laptop()
	app := w.Remote(jRenderSlug)
	linked := w.Remote(jRenderLinkSlug)
	a := ada.Clone(app, "app")

	w.Step("day1/init-and-render", func() {
		a.WriteFile("README.md", "# app\n")
		ada.LoreIn(a, "init").OK(t)
		ada.LoreIn(a, "render").OK(t)
		a.CommitAll("lore: init")
		a.Git("push", "-q", "origin", "HEAD:main")
	})
	b := ben.Clone(app, "app")

	w.Step("day2/both-add-rules-and-pull", func() {
		w.AdvanceDays(1)
		for i := range 4 {
			for _, who := range []string{"ada", "ben"} {
				w.Advance(time.Minute) // the two people's rules interleave in LORE.full.md
				m, c := ada, a
				if who == "ben" {
					m, c = ben, b
				}
				m.LoreIn(c, "rule", "add", "--to", "repo", "--no-live", "--body", "rule "+strconv.Itoa(i)+" by "+who+": check input "+strconv.Itoa(i)).OK(t)
			}
		}
		a.CommitAll("ada: rules")
		b.CommitAll("ben: rules")
		a.Git("push", "-q", "origin", "HEAD:main")
		r := b.TryGit("pull", "-q", "--no-rebase", "origin", "main")
		if strings.Contains(r.Out(), "CONFLICT") || strings.Contains(b.Status(), "UU ") {
			t.Fatalf("pull conflicted in a rendered file:\n%s\n%s", r.Out(), b.Status())
		}
		ben.LoreIn(b, "doctor").Says(t, "open conflicts:   0").Never(t, "has a git merge conflict")
	})

	w.Step("day3/package-pointer", func() {
		w.AdvanceDays(1)
		pkg := filepath.Join("workers", "extraction")
		a.WriteFile(filepath.Join(pkg, "README.md"), "# extraction\n")
		ada.LoreIn(a.In(pkg), "init").OK(t)
		got, ok := a.ReadFile(filepath.Join(pkg, "AGENTS.md"))
		if !ok || !strings.Contains(got, "Read `workers/extraction/.lore/LORE.md`") {
			t.Fatalf("package AGENTS.md does not name the package's LORE.md:\n%s", got)
		}
	})

	w.Step("day4/claude-linked-to-agents", func() {
		w.AdvanceDays(1)
		l := ada.Clone(linked, "linked")
		l.WriteFile("AGENTS.md", "# agents\n")
		if err := os.Symlink("AGENTS.md", filepath.Join(l.Dir, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
		ada.LoreIn(l, "init").OK(t)
		ada.LoreIn(l, "render").OK(t)
		ada.LoreIn(l, "render").OK(t).Says(t, "AGENTS.md (pointer present)", "one pointer block serves both").Never(t, "written through the link")
		got, _ := l.ReadFile("AGENTS.md")
		if !strings.Contains(got, "@.lore/LORE.md\n") || !strings.Contains(got, "Read `.lore/LORE.md`") {
			t.Fatalf("AGENTS.md lacks the @import or the sentence:\n%s", got)
		}
	})
}
