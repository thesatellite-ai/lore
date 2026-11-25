//go:build journeys && !windows

package journeys

// actor_merge_sameday_test.go — devco:D-993 (owner report 2026-11-25): a
// lead adds the same person twice the same day (two emails), then merges
// the second actor into the first. Both have a stint starting that day;
// the merge used to stop at actor_periods E_NAME_TAKEN, its printed rerun
// failed the same, and the merged-away actor read "left" with its stint
// still there. Now the duplicate stint folds into the kept one, the
// contacts move with one primary email, and the rerun has nothing left.

import (
	"strings"
	"testing"
)

func TestJourneyActorMergeSameDayStint(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	second := "bob.b@example.test"

	w.Step("day1/add-bob-twice-then-merge", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "member", "add", "--email", tlBob.Email, "--name", tlBob.Name, "--yes").OK(t)
		tt.lore(tlLee, "member", "add", "--email", second, "--name", "Bob B", "--yes").OK(t)
		roster := tt.roster(t, tlLee)
		keep, dup := roster["human:"+tlBob.Email].ID, roster["human:"+second].ID
		if keep == "" || dup == "" {
			t.Fatalf("both of Bob's actors should be on the roster: %+v", roster)
		}
		r := tt.lore(tlLee, "actor", "merge", "--from", dup, "--into", keep, "--yes").OK(t).
			Says(t, "merged "+dup, "1 of them folded into "+keep).Never(t, "E_NAME_TAKEN", "could not be written")
		t.Logf("actor merge:\n%s", r.Out())
		tt.lore(tlLee, "actor", "merge", "--from", dup, "--into", keep, "--yes").OK(t).Says(t, "no rows name "+dup)
		// the kept actor has one stint and both emails, one of them primary
		show := tt.lore(tlLee, "actor", "show", "--id", keep).OK(t).Says(t, tlBob.Email, second).Out()
		if n := strings.Count(show, "primary"); n != 1 {
			t.Errorf("the kept actor shows %d primary emails, want 1:\n%s", n, show)
		}
		if n := strings.Count(show, "→ now"); n != 1 {
			t.Errorf("the kept actor shows %d open stints, want 1:\n%s", n, show)
		}
		// the merged-away actor is marked and keeps no stint of its own
		tt.lore(tlLee, "actor", "show", "--id", dup).OK(t).Says(t, "left").Never(t, "→ now")
	})
}
