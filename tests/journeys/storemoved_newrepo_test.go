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
		tt.lore(tlLee, "sync").OK(t)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
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
		s.Says(t, "at its old address "+tt.store.SSHURL()+" too")
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
		tt.lore(tlAnn, "memory", "add", "--body", "written after the move", "--to", tlOrg+"/org", "--yes").OK(t)
		ann.Lore(annCode, "sync").OK(t)
		if got := w.Git(moved.Dir, "log", "-5", "--format=%ae", "refs/heads/main"); !strings.Contains(got, tlAnn.Email) {
			t.Fatalf("[devco:D-699] Ann's note did not reach the new repo (authors %q)", got)
		}
	})
}
