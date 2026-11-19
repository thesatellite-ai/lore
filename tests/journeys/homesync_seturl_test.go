//go:build journeys && !windows

package journeys

// homesync_seturl_test.go — owner use pass 1 (devco D-561, D-562, D-564,
// D-565): what a home-folder `lore sync` and an org store's `lore store
// set-url` say must be what happened.
//
//	D-561: a home sync named one org store "pushed (sent 0 …); pushed local
//	       commit(s) with no note rows" when only a leftover branch in its
//	       clone was ahead (nothing was pushed), and never said that no
//	       setup covers the home folder
//	D-562: set-url of an org store by its admin rewrote the org's shared
//	       lore-org.yaml with no confirmation, said nothing about a local
//	       path no teammate can reach, and counted "1 unpushed commit(s)"
//	       where git counted 2
//	D-564: whoami named a link in a path store "link in _local/<path> …"
//	D-565: set-url to a path and back left two move commits that cancel
//	       out, and an empty stores/_local/ folder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyHomeSyncAndStoreMoveSayWhatHappened(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	clone := lee.LorePath("stores", "acme-co", "lore-team")
	moved := filepath.Join(w.Root, "elsewhere", "lore-team-moved.git")

	w.Step("day1/team-store-and-a-leftover-branch", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "org", "link", "--org", tlOrg, "--project", "app", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		// a branch someone made by hand in the store clone, ahead of the remote
		w.Git(clone, "branch", "leftover-test")
		w.Git(clone, "-c", "user.name=Lee Lead", "-c", "user.email=lee@example.test", "commit", "--allow-empty", "-q", "-m", "scratch")
		w.Git(clone, "branch", "-f", "leftover-test", "HEAD")
		w.Git(clone, "reset", "-q", "--hard", "HEAD~1")
	})

	w.Step("day1/home-sync-claims-no-push", func() {
		r := lee.Lore(lee.Home, "sync").OK(t)
		r.Says(t, "no setup covers this folder", "lore sync --setup <name>", "yours: "+tlLeadSetup).
			Never(t, "pushed (sent", "with no note rows")
		lee.Lore(lee.Home, "sync", "--setup", tlLeadSetup).OK(t).Says(t, "store "+tlOrg+": up to date").
			Never(t, "no setup covers this folder")
	})

	w.Step("day2/set-url-to-a-path-asks-first", func() {
		w.Git(w.Root, "clone", "-q", "--bare", tt.store.Dir, moved)
		head := w.Git(clone, "rev-parse", "HEAD")
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved).
			Refused(t, "E_NEEDS_CONFIRM", "for everyone in "+tlOrg, "lore-org.yaml", "is a path on this machine", "--yes")
		if got := w.Git(clone, "rev-parse", "HEAD"); got != head {
			t.Errorf("the refused set-url committed in the store clone: %s -> %s", head, got)
		}
		if got := w.Git(clone, "remote", "get-url", "origin"); strings.Contains(got, "elsewhere") {
			t.Errorf("the refused set-url re-pointed the clone at %s", got)
		}
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved, "--yes").OK(t).
			Says(t, "is now at "+moved, "teammates cannot fetch from it")
	})

	w.Step("day2/whoami-shows-the-path", func() {
		// the path as lore keys a store (no .git), never the internal _local/ key
		at := strings.TrimSuffix(moved, ".git")
		tt.lore(tlLee, "whoami").OK(t).Says(t, "link in "+at+" app/", "internal   "+at+" app/").Never(t, "_local/")
	})

	w.Step("day2/a-note-then-back-counts-right", func() {
		tt.lore(tlLee, "memory", "add", "--body", "written while the store sat at a path", "--to", tlOrg+"/app").OK(t)
		r := lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", tt.store.SSHURL(), "--yes").OK(t)
		ahead := w.Git(clone, "rev-list", "--count", "origin/main..HEAD")
		r.Says(t, "("+ahead+" unpushed commit(s))")
		if _, err := os.Stat(lee.LorePath("stores", "_local")); !os.IsNotExist(err) {
			t.Errorf("set-url back left stores/_local behind: %v", err)
		}
	})

	w.Step("day3/a-round-trip-leaves-no-commit", func() {
		lee.Lore(lee.Home, "sync", "--setup", tlLeadSetup).OK(t)
		head := w.Git(clone, "rev-parse", "HEAD")
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved, "--yes").OK(t)
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", tt.store.SSHURL(), "--yes").OK(t).
			Says(t, "both move commits were dropped")
		if got := w.Git(clone, "rev-parse", "HEAD"); got != head {
			t.Errorf("a set-url round trip left commits: %s -> %s\n%s", head, got, w.Git(clone, "log", "--format=%s", head+"..HEAD"))
		}
		if _, err := os.Stat(lee.LorePath("stores", "_local")); !os.IsNotExist(err) {
			t.Errorf("the round trip left stores/_local behind: %v", err)
		}
	})
}
