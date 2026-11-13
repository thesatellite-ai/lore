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
	"path/filepath"
	"strings"
	"testing"
)

func TestJourneyTeamLeadStoreWipedEmpty(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	clone := lee.LorePath("stores", "acme-co", "lore-team")
	wipe := func() {
		t.Helper()
		if err := os.RemoveAll(tt.store.Dir); err != nil {
			t.Fatal(err)
		}
		w.Git(w.Root, "init", "-q", "--bare", "-b", "main", tt.store.Dir)
	}

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
		wipe()
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

	w.Step("day2/plain-doctor-says-reset", func() {
		// B2-32: lore doctor names the reset, not "run lore sync"
		tt.lore(tlLee, "doctor").Never(t, "a read never pushes: run `lore sync`")
	})

	w.Step("day2/lee-restores-it", func() {
		tt.lore(tlLee, "store", "push", tt.store.SSHURL(), "--restore", "--yes").OK(t).Says(t, "is back at")
		if !tt.store.Has("refs/heads/main") {
			t.Error("--restore did not put the store back")
		}
		tt.lore(tlLee, "sync").OK(t)
		tt.lore(tlLee, "store", "doctor").Never(t, "was reset")
	})

	var backup string
	w.Step("day3/restored-from-a-backup-elsewhere", func() {
		// B2-30: the store is wiped, Lee's lore marks it reset, then the
		// store is put back from a backup (as a teammate's --restore
		// would): Lee's next sync clears the mark by itself
		w.AdvanceDays(1)
		backup = tt.store.Dir + ".backup"
		w.Git(w.Root, "clone", "-q", "--bare", tt.store.Dir, backup)
		wipe()
		tt.lore(tlLee, "sync").Says(t, "was reset")
		if err := os.RemoveAll(tt.store.Dir); err != nil {
			t.Fatal(err)
		}
		w.Git(w.Root, "clone", "-q", "--bare", backup, tt.store.Dir)
		tt.lore(tlLee, "sync").OK(t).Says(t, "is back")
		tt.lore(tlLee, "store", "doctor").Never(t, "was reset")
		tt.lore(tlLee, "memory", "add", "after the store came back", "--to", tlOrg+"/"+tlProject).OK(t).Never(t, "was reset")
	})

	w.Step("day4/restore-as-the-first-command", func() {
		// B2-31: --restore checks the remote itself
		w.AdvanceDays(1)
		wipe()
		tt.lore(tlLee, "store", "push", tt.store.SSHURL(), "--restore", "--yes").OK(t).Says(t, "is back at")
		if !tt.store.Has("refs/heads/main") {
			t.Error("--restore as the first command did not put the store back")
		}
	})

	w.Step("day5/start-fresh-keeps-it-the-orgs-store", func() {
		// B2-33: forget + init in the repo: the store stays the org's
		w.AdvanceDays(1)
		wipe()
		tt.lore(tlLee, "store", "forget", tt.store.SSHURL(), "--yes").OK(t)
		storesFile := filepath.Join(tt.repo[tlLee.Email].Dir, ".lore", "stores.yaml")
		before, _ := os.ReadFile(storesFile)
		r := tt.lore(tlLee, "store", "init", tlOrg, tt.store.SSHURL(), "--yes").OK(t)
		r.Says(t, "org "+tlOrg+"'s store", "lore setup apply --setup "+tlLeadSetup+" --step team", "lore member add").Never(t, "recorded in .lore/stores.yaml")
		if after, _ := os.ReadFile(storesFile); string(after) != string(before) {
			t.Errorf("store init changed the code repo's .lore/stores.yaml:\n%s", after)
		}
		// the step the hint names writes the org file and roster again
		tlApply(t, lee, tt.repo[tlLee.Email].Dir, tlLeadSetup, "team", map[string]any{"org": tlOrg, "store_url": tt.store.SSHURL()})
		tt.lore(tlLee, "member", "list").OK(t).Says(t, tlLee.Email)
	})

	w.Step("day6/an-old-build-clone-never-refills-it", func() {
		// L-2: a clone whose lore kept no remote tip (an older build), its
		// tracking ref pruned by another fetch: its push stamp still tells
		// lore it pushed this store, so the emptied repo stays empty
		w.AdvanceDays(1)
		tt.lore(tlLee, "memory", "add", "a note in the fresh store", "--to", tlOrg+"/"+tlProject).OK(t)
		tt.lore(tlLee, "sync").OK(t)
		if err := os.Remove(filepath.Join(clone, ".git", "lore", "remote-tip")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(clone, ".git", "lore", "push_stamp")); err != nil {
			t.Fatalf("the clone has no push stamp after a push: %v", err)
		}
		wipe()
		lee.OpenClone(tt.store, clone).Git("fetch", "-q", "--prune", "origin")
		if r := tt.lore(tlLee, "sync"); r.Code == 0 {
			t.Errorf("an old-build clone's sync into the emptied store must be refused: %s", r)
		}
		tt.lore(tlLee, "memory", "add", "written after the second wipe", "--to", tlOrg+"/"+tlProject)
		if refs := w.TryGit(tt.store.Dir, "for-each-ref").Stdout; strings.TrimSpace(refs) != "" {
			t.Errorf("an old-build clone refilled the emptied org store: %s", refs)
		}
	})

	w.Step("day7/an-org-link-write-without-a-fetch", func() {
		// the store is put back, then wiped again with no fetch on Lee's
		// clone (its tracking ref still shows the old branch): an org link
		// write asks the remote itself and is refused; the repo stays empty
		w.AdvanceDays(1)
		tt.lore(tlLee, "store", "push", tt.store.SSHURL(), "--restore", "--yes").OK(t)
		tt.lore(tlLee, "sync").OK(t)
		wipe()
		r := tt.lore(tlLee, "org", "link", tlOrg, "--project", tlProject, "--yes")
		if r.Code == 0 {
			t.Errorf("an org link write into the wiped store must be refused: %s", r)
		}
		r.Says(t, "reset")
		if refs := w.TryGit(tt.store.Dir, "for-each-ref").Stdout; strings.TrimSpace(refs) != "" {
			t.Errorf("an org link write refilled the wiped org store: %s", refs)
		}
	})
}
