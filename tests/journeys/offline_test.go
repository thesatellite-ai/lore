//go:build journeys && !windows

package journeys

// offline_test.go — working offline is calm and clear (B3-13, B3-14): one
// person writes on a train, reads, commits, syncs; later the repo's
// origin points at a path with no repository.
//
//	day 1  online: init, a task (live.autopush is off by default: the
//	       write says it is kept here), Oli turns live.autopush on, commit
//	       and push
//	day 2  offline: a write says once what waits and what happens next;
//	       reads and the git hooks stay quiet; sync says it once, its way
//	day 3  origin points nowhere: lore names the remote and the fix, not
//	       "offline"; doctor warns; fixing origin sends everything

import (
	"path/filepath"
	"strings"
	"testing"
)

// offlineWaiting is the live line's start while writes wait.
const offlineWaiting = "(live, not pushed yet:"

func TestJourneyOfflineCalm(t *testing.T) {
	t.Parallel()
	w := NewWorld(t)
	oli := w.Persona("Oli Offline", "oli@offline.example")
	lap := oli.Laptop()
	site := w.Remote("github.com/oli-dev/site")
	repo := lap.Clone(site, "site")
	originURL := strings.TrimSpace(repo.Git("remote", "get-url", "origin"))

	w.Step("day1/online-start", func() {
		lap.LoreIn(repo, "init").OK(t)
		lap.LoreIn(repo, "task", "add", "set up the site").OK(t).Never(t, offlineWaiting).
			Says(t, "(live, kept here: `lore sync` or your next `git push` sends live writes")
		// The rest of this journey is about writes lore pushes by itself
		// while offline, so Oli opts in (U-2: pushes are opt-in; with them
		// off an offline write tries no push and has nothing to wait on).
		lap.LoreIn(repo, "config", "set", "--key", "live.autopush", "--value", "true").OK(t)
		repo.CommitAll("add lore")
		repo.Git("push", "-q", "origin", "HEAD")
	})

	w.Step("day2/offline-says-once", func() {
		w.AdvanceDays(1)
		lap.Offline()
		r := lap.LoreIn(repo, "task", "add", "written on the train").OK(t)
		r.Says(t, offlineWaiting+" 1 waiting; offline: the next lore command sends them once origin", "answers, or run `lore sync`")
		// reads do not repeat it
		lap.LoreIn(repo, "task", "list").OK(t).Says(t, "written on the train").Never(t, offlineWaiting)
		lap.LoreIn(repo, "memory", "list").OK(t).Never(t, offlineWaiting)
		// nor do git's hooks (post-commit and pre-push used to say it twice)
		repo.CommitAll("offline work")
		if out := repo.TryGit("commit", "-q", "--allow-empty", "-m", "again"); strings.Contains(out.Stdout+out.Stderr, offlineWaiting) {
			t.Errorf("a git commit repeated the live line:\n%s%s", out.Stdout, out.Stderr)
		}
		// sync says what waits once, in its own words
		s := lap.LoreIn(repo, "sync").OK(t)
		// devco L-10: the line names the remote and git's reason, and the
		// --json says it too, so a script can tell a failed sync
		s.Says(t, "live: 1 write(s) not pushed (offline: git push to origin (", "): kept here, sent on the next sync").Never(t, offlineWaiting)
		lap.LoreIn(repo, "sync", "--json").OK(t).Says(t, `"live_offline": true`, `"live_error": "git push to origin (`)
		// a second write changes the count: said again, once
		lap.LoreIn(repo, "task", "add", "second on the train").OK(t).Says(t, offlineWaiting+" 2 waiting; offline")
	})

	w.Step("day3/missing-origin-is-named", func() {
		w.AdvanceDays(1)
		lap.Online()
		gone := filepath.Join(w.Root, "nowhere", "site.git")
		repo.Git("remote", "set-url", "origin", gone)
		r := lap.LoreIn(repo, "task", "add", "while origin is wrong").OK(t)
		r.Says(t, "no git repository at origin ("+gone+"): fix the address with `git remote set-url origin <url>`").Never(t, "waiting; offline")
		lap.LoreIn(repo, "sync").OK(t).Says(t, "not pushed (no git repository at origin ("+gone+"))")
		lap.LoreIn(repo, "doctor").Says(t, "live writes cannot be pushed: no git repository at origin ("+gone+")", "does not appear to be a git repository")
		// fixed: everything goes out and doctor stops warning
		repo.Git("remote", "set-url", "origin", originURL)
		lap.LoreIn(repo, "sync").OK(t).Never(t, "not pushed")
		lap.LoreIn(repo, "doctor").Never(t, "live writes cannot be pushed")
	})
}
