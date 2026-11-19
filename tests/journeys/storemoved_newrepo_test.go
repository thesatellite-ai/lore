//go:build journeys && !windows

package journeys

// storemoved_newrepo_test.go — the org's store moves to a NEW repo while
// the old one still exists (devco D-698, D-699; owner use pass 5 weak 4
// and 5). Lee (the admin) runs `lore store set-url` to an empty new repo,
// then `lore sync`. Ann (a member) still has the old address.
//
//	D-699: set-url promised "every teammate's lore follows it after their
//	       next sync", but the move commit went only to the new address:
//	       Ann pulled the old repo and stayed there. Lee's sync now sends
//	       the move to the old address too, and Ann's next sync follows.
//	D-698: the sync line of a push with no note rows counts the commits
//	       it sent, as the push line does (it said "sent 0" under a push
//	       line of "(1 commit)").

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyTeamLeadStoreMovedToNewRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var ann *Machine
	var annCode string
	moved := w.Remote("github.com/acme-co/team-notes-new") // a new repo; the old one stays

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// D-694: a member adds notes only in a folder of a team they are on
		// (org/ is the admin's): Ann's invite puts her on @web
		tt.lore(tlLee, "team", "add", "--handle", "@web", "--members", "@lee").OK(t)
		tt.lore(tlLee, "store", "folder", "add", "--store", tlOrg, "--folder", "web").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email, "--team", "@web")
		ann = tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		annCode = filepath.Join(ann.Home, "code")
		ann.Lore(annCode, "sync").OK(t)
	})

	w.Step("day2/the-admin-moves-the-store-to-a-new-repo", func() {
		w.AdvanceDays(1)
		w.Git(tt.store.Dir, "push", "-q", "--mirror", moved.Dir) // the history copied to the new repo, the old one left as it is
		r := lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		r.Says(t, "is now at "+moved.SSHURL(), "lore-org.yaml of "+tlOrg+" now names "+moved.SSHURL(),
			"also sends the move to the old address "+tt.store.SSHURL(), "lore store set-url --name "+tlOrg+" --url "+moved.SSHURL()).
			Never(t, "every teammate's lore follows it after their next sync")
		s := lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t)
		// D-699: the old repo gets the move once the new one has it
		s.Says(t, "forwarded 1 commit to the old address "+tt.store.SSHURL()+" (") // its own line and count; "sent 1" is the new address's
		oldHead := w.Git(tt.store.Dir, "rev-parse", "refs/heads/main")
		if newHead := w.Git(moved.Dir, "rev-parse", "refs/heads/main"); oldHead != newHead {
			t.Fatalf("[devco:D-699] old repo main %s, new repo main %s: the move did not reach the old address", oldHead, newHead)
		}
		// D-698: every store line that pushed counts commits, never "sent 0" under a push line
		if strings.Contains(s.Stdout, "pushed (sent 0") {
			t.Errorf("[devco:D-698] a push line read sent 0:\n%s", s.Stdout)
		}
		// said once: the next sync owes the old address nothing
		lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t).Never(t, "old address")
	})

	w.Step("day2/the-teammate-follows", func() {
		ann.Lore(annCode, "sync").OK(t)
		cfg, err := os.ReadFile(ann.LorePath("config.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(cfg), moved.SSHURL()) {
			t.Fatalf("[devco:D-699] Ann's config does not follow the move to %s:\n%s", moved.SSHURL(), cfg)
		}
		tt.lore(tlAnn, "memory", "add", "--body", "written after the move", "--to", tlOrg+"/web", "--yes").OK(t)
		ann.Lore(annCode, "sync").OK(t)
		if got := w.Git(moved.Dir, "log", "-5", "--format=%ae", "refs/heads/main"); !strings.Contains(got, tlAnn.Email) {
			t.Fatalf("[devco:D-699] Ann's note did not reach the new repo (authors %q)", got)
		}
	})
}

// TestJourneyTeamLeadStoreMovedToEmptyRepo: the admin moves the store to a
// new EMPTY repo (nothing copied over). The first explicit sync after
// set-url fills it (devco D-699): before, the clone still held what it knew
// of the old repo (its tracking ref and remote-tip note), so the empty new
// repo looked like the old one deleted and made again, and the sync was
// refused with "lore store push --restore".
func TestJourneyTeamLeadStoreMovedToEmptyRepo(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var ann *Machine
	var annCode string
	empty := w.Remote("github.com/acme-co/team-notes-empty")

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "sync").OK(t)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		ann = tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		annCode = filepath.Join(ann.Home, "code")
		ann.Lore(annCode, "sync").OK(t)
		tt.lore(tlLee, "sync").OK(t) // the admin has the member's join before the move
	})

	w.Step("day2/set-url-to-an-empty-repo-then-sync-fills-it", func() {
		w.AdvanceDays(1)
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", empty.SSHURL(), "--yes").OK(t)
		s := lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t)
		s.Never(t, "--restore").Says(t, "to the old address "+tt.store.SSHURL()+" (branch main, now at commit ")
		newHead := w.Git(empty.Dir, "rev-parse", "refs/heads/main")
		if oldHead := w.Git(tt.store.Dir, "rev-parse", "refs/heads/main"); oldHead != newHead {
			t.Fatalf("[devco:D-699] old repo main %s, new repo main %s after the first sync", oldHead, newHead)
		}
	})

	w.Step("day2/the-teammate-follows-to-the-filled-repo", func() {
		ann.Lore(annCode, "sync").OK(t)
		cfg, err := os.ReadFile(ann.LorePath("config.yaml"))
		if err != nil || !strings.Contains(string(cfg), empty.SSHURL()) {
			t.Fatalf("[devco:D-699] Ann's config does not follow the move to %s (%v):\n%s", empty.SSHURL(), err, cfg)
		}
	})
}

// TestJourneyTeamLeadStoreMovedTeammatePushedToOldRepo: Ann pushes a note
// to the old repo, and Lee (the admin) runs set-url to the new repo
// without syncing first; Ann then pushes another note to the old repo
// before Lee's next sync (devco D-699 follow-up). Before, Lee's clone never
// fetched the old address: Ann's commits never reached the new repo, and
// the move sent to the old address was refused as "a teammate pushed there
// after it" (Ann pushed before following). Now set-url takes in Ann's first
// note, Lee's sync takes in the second and sends both to the new repo, the
// move reaches the old repo as a fast-forward, and Ann follows in one sync,
// run from home (SyncSetup) in one run and from her code repo (SyncStores)
// in the other (her repo holds a .lore folder, so `lore sync` there is the
// repo sync): the repo sync refreshed her org entry only at open, before
// its fetch brought the move, so she followed only at a later command.
func TestJourneyTeamLeadStoreMovedTeammatePushedToOldRepo(t *testing.T) {
	t.Parallel()
	for _, follow := range []string{"home-sync", "repo-sync"} {
		t.Run(follow, func(t *testing.T) {
			t.Parallel()
			storeMovedTeammatePushedToOldRepo(t, follow)
		})
	}
}

// storeMovedTeammatePushedToOldRepo is the journey above; follow names the
// sync Ann follows with: "home-sync" or "repo-sync".
func storeMovedTeammatePushedToOldRepo(t *testing.T, follow string) {
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var ann *Machine
	var annCode string
	moved := w.Remote("github.com/acme-co/team-notes-moved")

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		// D-694: a member adds notes only in a folder of a team they are on
		// (org/ is the admin's): Ann's invite puts her on @web
		tt.lore(tlLee, "team", "add", "--handle", "@web", "--members", "@lee").OK(t)
		tt.lore(tlLee, "store", "folder", "add", "--store", tlOrg, "--folder", "web").OK(t)
		// the folder's first write also gives it its project row (its own
		// commit), so Ann's one note below is one commit
		tt.lore(tlLee, "memory", "add", "--body", "web folder opened", "--to", tlOrg+"/web", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email, "--team", "@web")
		ann = tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		annCode = filepath.Join(ann.Home, "code")
		if follow == "repo-sync" {
			// a repo with its own .lore folder: its `lore sync` is the
			// repo sync (SyncStores), not the setup sync of a repo linked
			// by its remote only; Ann's notes below are written from her
			// code folder (a member's write in a lore project of its own
			// is refused for the company folder)
			tt.lore(tlAnn, "init", "--non-interactive", "--name=app").OK(t)
		}
		ann.Lore(annCode, "sync").OK(t)
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day2/the-teammate-pushes-then-the-admin-moves-without-a-sync", func() {
		w.AdvanceDays(1)
		w.Git(tt.store.Dir, "push", "-q", "--mirror", moved.Dir) // the new repo: a copy from before Ann's note
		ann.Lore(annCode, "memory", "add", "--body", "pushed to the old repo before the move", "--to", tlOrg+"/web", "--yes").OK(t)
		ann.Lore(annCode, "sync").OK(t)
		r := lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		r.Says(t, "took in 1 commit a teammate pushed to the old address "+tt.store.SSHURL()+" before following the move")
		ann.Lore(annCode, "memory", "add", "--body", "pushed to the old repo after the move", "--to", tlOrg+"/web", "--yes").OK(t)
		ann.Lore(annCode, "sync").OK(t)
	})

	w.Step("day2/the-admin-syncs-every-commit-reaches-the-new-repo", func() {
		s := lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t)
		// one line per remote the forward moved, each with its own count
		s.Says(t, "to the new address "+moved.SSHURL()+" (branch main, now at commit ", "took in 1 commit a teammate pushed to its old address before following the move",
			"to the old address "+tt.store.SSHURL()+" (branch main, now at commit ").
			Never(t, "refused the move", "did not reach its old address", "commit(s) a teammate")
		oldHead := w.Git(tt.store.Dir, "rev-parse", "refs/heads/main")
		if newHead := w.Git(moved.Dir, "rev-parse", "refs/heads/main"); oldHead != newHead {
			t.Fatalf("[devco:D-699] old repo main %s, new repo main %s: the move did not reach the old address", oldHead, newHead)
		}
		bodies := w.Git(moved.Dir, "grep", "-l", "-e", "pushed to the old repo", "refs/heads/main")
		if strings.Count(bodies, "\n")+1 < 2 {
			t.Fatalf("[devco:D-699] the new repo lacks Ann's notes pushed to the old repo; files holding them:\n%s", bodies)
		}
	})

	w.Step("day2/the-teammate-follows-from-a-"+follow, func() {
		if cfg, err := os.ReadFile(ann.LorePath("config.yaml")); err != nil || strings.Contains(string(cfg), moved.SSHURL()) {
			t.Fatalf("[devco:D-699] Ann's config names %s before her sync after the move reached the old repo (%v):\n%s", moved.SSHURL(), err, cfg)
		}
		if follow == "repo-sync" {
			tt.lore(tlAnn, "sync").OK(t)
		} else {
			ann.Lore(annCode, "sync").OK(t)
		}
		cfg, err := os.ReadFile(ann.LorePath("config.yaml"))
		if err != nil || !strings.Contains(string(cfg), moved.SSHURL()) {
			t.Fatalf("[devco:D-699] Ann's config does not follow the move to %s after one %s (%v):\n%s", moved.SSHURL(), follow, err, cfg)
		}
	})
}
