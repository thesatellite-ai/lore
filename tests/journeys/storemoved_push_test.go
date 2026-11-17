//go:build journeys && !windows

package journeys

// storemoved_push_test.go — an admin moves the org's store to another
// GitHub org with `lore store set-url` from the home folder, then pushes
// it with `lore store push --store <org>` (devco D-516, D-517). From the
// home folder the push refused the org entry's name ("no store named …
// in this repo"; only the url worked), and a push that sent a commit said
// "sent 0".

import (
	"os"
	"testing"
)

func TestJourneyStoreMovedPushFromHome(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	moved := w.Remote("github.com/acme-new/lore-team")

	w.Step("day1/team", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "sync").OK(t)
	})

	w.Step("day2/move-then-push-from-home", func() {
		w.AdvanceDays(1)
		// the GitHub transfer: the old address holds nothing now
		if err := os.RemoveAll(moved.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tt.store.Dir, moved.Dir); err != nil {
			t.Fatal(err)
		}
		lee.Lore(lee.Home, "store", "set-url", "--name", tlOrg, "--url", moved.SSHURL(), "--yes").OK(t)
		// D-516: show and push took only the url outside a repo
		lee.Lore(lee.Home, "store", "show", "--store", tlOrg).OK(t).Says(t, "waiting   1 local commit(s)")
		// D-517: it printed "sent 0" after pushing the set-url commit
		lee.Lore(lee.Home, "store", "push", "--store", tlOrg, "--json").OK(t).Says(t, `"sent": 1`).Never(t, `"sent": 0`)
		if got := moved.Log("main"); len(got) == 0 {
			t.Fatalf("the push left the moved remote empty")
		}
		lee.Lore(lee.Home, "store", "push", "--store", tlOrg).OK(t).Says(t, "nothing to send").Never(t, "sent 0")
		// a setup's name stands for its org's store
		lee.Lore(lee.Home, "store", "push", "--store", tlLeadSetup).OK(t).Says(t, "nothing to send")
		lee.Lore(lee.Home, "store", "push", "--store", "nosuch").Refused(t, "no org, org store or setup is named nosuch", tlOrg, tlLeadSetup).
			Never(t, "in this repo")
		lee.Lore(lee.Home, "store", "forget", "--url", "nosuch").Refused(t, "no org, org store or setup is named nosuch").Never(t, "in this repo")
	})
}
