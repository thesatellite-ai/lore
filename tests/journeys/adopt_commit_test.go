//go:build journeys && !windows

package journeys

// adopt_commit_test.go — devco:D-710 (owner, adopting real repos into a
// store with 0.1.18): `lore store adopt --remove-source` moved a repo's
// rows into a store folder and deleted their row files, and the next `git
// commit` was refused by lore's own pre-commit: "E_NEEDS_CONFIRM refusing
// to commit: this commit deletes 204 lore rows by removing the file, not
// with lore". A command of lore must never leave a change lore refuses.
// Lee adopts a 0.1.18 repo (hooks wired) into a team folder, runs the
// command adopt printed, through the real hooks, pushes, and a fresh
// clone holds none of the rows while the folder holds all of them.

import (
	"path/filepath"
	"strings"
	"testing"
)

const (
	jAdcSlug   = "github.com/acme-co/notes-app"
	jAdcFolder = "client_notes"
	jAdcWord   = "quillborough"
)

func TestJourneyAdoptRemoveSourceCommits(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	place := tlOrg + "/" + jAdcFolder
	mainRef := "refs/heads/main"
	var repo = jPlainRepo(t, lee, w.Remote(jAdcSlug), filepath.Join(parent, "notes-app"))
	var printed string

	w.Step("day1/a-repo-with-its-own-rows", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jAdcFolder).OK(t)
		lee.LoreIn(repo, "init", "--non-interactive", "--name=notes-app").OK(t)
		lee.LoreIn(repo, "rule", "add", "--body", "Run the linter before every push", "--severity", "must", "--to", "repo", "--yes").OK(t)
		lee.LoreIn(repo, "memory", "add", "--body", "the "+jAdcWord+" cache warms at boot", "--to", "repo").OK(t)
		repo.CommitAll("lore notes")
		repo.Git("push", "-q", "origin", "HEAD:main")
		if r := repo.TryGit("hook", "run", "pre-commit"); strings.Contains(r.Out(), "cannot find a hook") {
			t.Fatalf("the repo has no pre-commit hook: the story would prove nothing\n%s", r.Out())
		}
	})

	w.Step("day2/adopt-and-remove-the-source", func() {
		w.AdvanceDays(1)
		r := lee.LoreIn(repo, "store", "adopt", place, "--remove-source", "--yes").OK(t).Says(t, "removed ")
		// the next step adopt prints: two git commands for this repo
		for _, l := range strings.Split(r.Out(), "\n") {
			if _, cmd, ok := strings.Cut(l, "review and commit it ("); ok {
				printed, _, _ = strings.Cut(cmd, "); ")
			}
		}
		want := "git -C " + repo.Dir + " add -A .lore/data && git -C " + repo.Dir + " commit -m \"lore: move the notes to " + place + "\""
		if printed != want {
			t.Fatalf("adopt printed %q, want %q\n%s", printed, want, r.Out())
		}
		lee.LoreIn(repo, "doctor").Never(t, "by removing the file").Says(t, "live in "+place+" now")
	})

	w.Step("day2/commit-it-as-printed-through-the-hooks", func() {
		repo.Git("add", "-A", ".lore/data")
		r := repo.TryGit("commit", "-m", "lore: move the notes to "+place)
		if r.Code != 0 || strings.Contains(r.Out(), "by removing the file") {
			t.Fatalf("lore's hook refused the removal adopt made: exit %d\n%s", r.Code, r.Out())
		}
		if !strings.Contains(r.Out(), "`lore store adopt --remove-source` moved to "+place) {
			t.Errorf("the commit does not say where the rows went:\n%s", r.Out())
		}
		repo.Git("push", "-q", "origin", "HEAD:main")
		lee.Lore(parent, "sync").OK(t)
		// the next commit lets go of the marker: a later hand delete of a
		// row file is refused again
		repo.CommitAll("after")
	})

	w.Step("day3/a-fresh-clone-and-the-folder", func() {
		w.AdvanceDays(1)
		peer := lee.Clone(w.Remote(jAdcSlug), "notes-peer")
		if out := peer.Git("ls-files", ".lore/data/rules", ".lore/data/memories"); out != "" {
			t.Errorf("a fresh clone still holds adopted row files:\n%s", out)
		}
		var rules, mems int
		for _, p := range tt.store.FilesMatching(mainRef, jAdcFolder+"/") {
			switch {
			case strings.Contains(p, "/rules/"):
				rules++
			case strings.Contains(p, "/memories/"):
				mems++
			}
		}
		if rules != 1 || mems != 1 {
			t.Errorf("the folder holds %d rules and %d memories, want 1 and 1", rules, mems)
		}
	})
}
