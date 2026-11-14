//go:build journeys && !windows

package journeys

// teamlead_storemoved_test.go — the org's store repo is renamed on the
// host (bug hunt H-10, H-11, H-12, G-16, G-19). Ann (a member) syncs from her home
// folder: lore must say the address now holds no store and name `lore
// store set-url` and the restore, never "check the network"; `lore sync
// status` works from there as `lore sync` does. Ann follows the move on
// her machine with set-url, which leaves the org's lore-org.yaml to an
// admin; Lee (the admin) runs set-url from his home folder, which moves
// the org's file and names the old clone left behind.
//
// Named TestJourneyTeamLead… so the teamlead gate step runs it.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJourneyTeamLeadStoreMoved(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	var ann *Machine
	var annCode string // ~/code: in Ann's setup folders, not a repo
	moved := w.Remote("github.com/acme-co/team-notes")

	w.Step("day1/team-with-a-member", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "sync").OK(t)
		_, line := tlInvite(t, lee, tt.repo[tlLee.Email].Dir, tlAnn.Email)
		ann = tt.person(tlAnn)
		tlJoin(t, ann, tt.repo[tlAnn.Email].Dir, tlTeamSetup, line, tlAnn, []string{tlFolders})
		annCode = filepath.Join(ann.Home, "code")
		ann.Lore(annCode, "sync").OK(t)
	})

	w.Step("day1/sync-status-outside-a-repo", func() {
		// H-10: it refused E_NOT_A_STORE outside a repo while `lore sync` worked
		ann.Lore(annCode, "sync", "status").OK(t).Says(t, "no code repo here", "stores:", tlOrg+":")
	})

	w.Step("day2/the-store-repo-is-renamed", func() {
		w.AdvanceDays(1)
		if err := os.RemoveAll(moved.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tt.store.Dir, moved.Dir); err != nil {
			t.Fatal(err)
		}
		// H-11: not "offline: check the network"
		r := ann.Lore(annCode, "sync")
		if r.Code == 0 {
			t.Errorf("a sync to a store that is gone must fail: %s", r)
		}
		r.Says(t, "no git repository at "+tt.store.SSHURL(), "lore store set-url --name "+tlOrg+" --url <new-url>", "--restore --yes").
			Never(t, "check the network", "offline:")
		// G-16: org refresh said "retry when the store is reachable"
		tt.lore(tlAnn, "org", "refresh", tlOrg).Says(t, "there is no repository", "lore store set-url --name").
			Never(t, "retry when the store is reachable")
	})

	w.Step("day2/the-member-follows-the-move", func() {
		// H-12: a member's set-url moves her machine, not the org's file
		r := tt.lore(tlAnn, "store", "set-url", tlOrg, moved.SSHURL(), "--yes").OK(t)
		r.Says(t, "is now at "+moved.SSHURL(), "lore-org.yaml of "+tlOrg+" left as it is", "only an admin of "+tlOrg)
		ann.Lore(annCode, "sync").OK(t)
		// G-16: lore-org.yaml still names the old address: a refresh
		// keeps her move instead of copying the old url back
		tt.lore(tlAnn, "org", "refresh", tlOrg).OK(t).Says(t, "kept "+tt.store.SSHURL()+" → "+moved.SSHURL())
		ann.Lore(annCode, "sync").OK(t)
		// G-19: store doctor names the clone left at the old address
		tt.lore(tlAnn, "store", "doctor").OK(t).Says(t, "the old clone", "lore store forget --url "+tt.store.SSHURL()).
			Never(t, "v0.1.17 org stores")
	})

	w.Step("day2/the-admin-moves-it-from-home", func() {
		// H-12: it refused outside a repo ("it edits .lore/stores.yaml")
		r := lee.Lore(lee.Home, "store", "set-url", tlOrg, moved.SSHURL(), "--yes").OK(t)
		r.Says(t, "is now at "+moved.SSHURL(), "lore-org.yaml of "+tlOrg+" now names "+moved.SSHURL(), "the old clone", "lore store forget --url "+tt.store.SSHURL()).
			Never(t, "left as it is")
		lee.Lore(filepath.Join(lee.Home, "code"), "sync").OK(t)
		old := lee.LorePath("stores", "acme-co", "lore-team")
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("the old clone the line names is not there: %v", err)
		}
		lee.Lore(lee.Home, "store", "forget", "--url", tt.store.SSHURL(), "--yes").OK(t)
		if _, err := os.Stat(old); !os.IsNotExist(err) {
			t.Errorf("forget left the old clone %s: %v", filepath.Base(old), err)
		}
	})
}
