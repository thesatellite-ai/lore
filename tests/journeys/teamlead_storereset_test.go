//go:build journeys && !windows

package journeys

// teamlead_storereset_test.go — the lead wipes the org store (bug hunt
// B2-28, B2-29): a row leaked into it, so Lee deletes the store repo and
// makes it again EMPTY. Before he picks what to do, his own routine
// `lore sync` (and a write) must not refill it with the whole old
// history, even when another fetch of the clone already pruned the
// tracking ref; `lore store doctor` says the store was reset and names
// `lore store push --restore` and `lore store forget`, as sync does.
//
// Named TestJourneyTeamLead… so the teamlead gate step runs it.

import (
	"os"
	"strings"
	"testing"
)

func TestJourneyTeamLeadStoreWipedEmpty(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	clone := lee.LorePath("stores", "acme-co", "lore-team")

	w.Step("day1/team-with-a-project-note", func() {
		tlStartTeam(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		tt.lore(tlLee, "init").OK(t)
		tt.lore(tlLee, "memory", "add", "a row that leaked", "--to", tlOrg+"/"+tlProject, "--create").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		if !tt.store.Has("refs/heads/main") {
			t.Fatal("the org store has no main branch")
		}
	})

	w.Step("day2/store-repo-made-again-empty", func() {
		w.AdvanceDays(1)
		if err := os.RemoveAll(tt.store.Dir); err != nil {
			t.Fatal(err)
		}
		w.Git(w.Root, "init", "-q", "--bare", "-b", "main", tt.store.Dir)
		// another fetch of the clone (a read's refresh, the org link
		// index) prunes the tracking ref before the sync looks
		lee.OpenClone(tt.store, clone).Git("fetch", "-q", "--prune", "origin")
		r := tt.lore(tlLee, "sync")
		if r.Code == 0 {
			t.Errorf("a plain sync into the emptied org store must be refused: %s", r)
		}
		r.Says(t, "was reset", "lore store push --restore", "lore store forget")
		tt.lore(tlLee, "memory", "add", "written after the wipe", "--to", tlOrg+"/"+tlProject)
		if refs := w.TryGit(tt.store.Dir, "for-each-ref").Stdout; strings.TrimSpace(refs) != "" {
			t.Errorf("lore refilled the emptied org store: %s", refs)
		}
	})

	w.Step("day2/store-doctor-says-reset", func() {
		r := tt.lore(tlLee, "store", "doctor")
		r.Says(t, "was reset", "lore store push --restore", "lore store forget").
			Never(t, "the next lore command that reaches the store pushes them")
	})

	w.Step("day2/lee-restores-it", func() {
		tt.lore(tlLee, "store", "push", tt.store.SSHURL(), "--restore", "--yes").OK(t).Says(t, "is back at")
		if !tt.store.Has("refs/heads/main") {
			t.Error("--restore did not put the store back")
		}
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "store", "doctor").Never(t, "was reset")
	})
}
