//go:build userscen

package userscen

// Knowledge campaign: knowledge rows between two people on one bare
// remote, and two processes writing at once.
//   - every kind: a row added by alice reaches bob through git (commit,
//     push, pull) with the same id and body; bob's edit and delete come
//     back to alice the same way; restore travels too;
//   - memory and rule with --live reach bob at once; written while alice
//     is offline they wait, `lore sync` delivers them once she is back;
//     once landed on main a later edit travels through git;
//   - two processes adding rows of one kind at the same moment both land,
//     with distinct ids, and list shows every row once.

import (
	"strconv"
	"sync"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

func TestCampaignKnowledge_TwoClonesEveryKind(t *testing.T) {
	scenParallel(t)
	for _, k := range campKnowKinds() {
		t.Run(k.noun, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			id := campMustAdd(t, a, k, "Shared "+k.noun, "shared body of "+k.noun)
			a.CommitAll("knowledge")
			a.AdminPush("origin", "main")
			b.Git("pull", "-q", "--no-rebase", "origin", "main")
			if got := knowShowRow(t, b, k, id).Body; got != "shared body of "+k.noun {
				t.Fatalf("bob sees body %q after the pull", got)
			}
			if !knowListRows(t, b, k).has(id) {
				t.Fatalf("bob's list does not hold %s", id)
			}
			if k.canEdit {
				b.Lore(k.noun, "edit", id, "--body", "bob edited it").OK(t)
				b.CommitAll("edit")
				b.AdminPush("origin", "main")
				a.Git("pull", "-q", "--no-rebase", "origin", "main")
				if got := knowShowRow(t, a, k, id).Body; got != "bob edited it" {
					t.Fatalf("alice sees body %q after bob's edit", got)
				}
			}
			b.Lore(k.noun, "delete", id, "--yes").OK(t)
			b.CommitAll("delete")
			b.AdminPush("origin", "main")
			a.Git("pull", "-q", "--no-rebase", "origin", "main")
			a.Lore(k.noun, "show", id).Fails(t, 23, "E_IN_TRASH", "lore trash restore")
			a.Lore("trash", "restore", id, "--yes").OK(t)
			a.CommitAll("restore")
			a.AdminPush("origin", "main")
			b.Git("pull", "-q", "--no-rebase", "origin", "main")
			if !knowListRows(t, b, k).has(id) {
				t.Fatalf("bob does not see the restored row")
			}
		})
	}
}

func TestCampaignKnowledge_LiveRowsOnlineOffline(t *testing.T) {
	scenParallel(t)
	for _, noun := range []string{"memory", "rule"} {
		t.Run(noun, func(t *testing.T) {
			st := newSyncTeam(t, 2)
			a, b := st.clones[0], st.clones[1]
			k := knowKindByNoun(t, noun)
			live := th.DecodeJSON[knowAdded](t, a.Lore(noun, "add", "--body", "live at once", "--live", "--json")).ID
			if !knowListRows(t, b, k).has(live) {
				t.Fatalf("bob does not see the live %s at once", noun)
			}
			kept := th.DecodeJSON[knowAdded](t, a.Lore(noun, "add", "--body", "kept with the code", "--no-live", "--json")).ID
			if knowListRows(t, b, k).has(kept) {
				t.Fatalf("a --no-live %s reached bob before any push", noun)
			}

			st.people[0].Offline()
			waiting := th.DecodeJSON[knowAdded](t, a.Lore(noun, "add", "--body", "written offline", "--live", "--json")).ID
			a.Lore("sync").Says(t, "not pushed")
			if knowListRows(t, b, k).has(waiting) {
				t.Fatalf("a write that never left alice's machine reached bob")
			}
			st.people[0].Online()
			a.Lore("sync").OK(t)
			if !knowListRows(t, b, k).has(waiting) {
				t.Fatalf("bob does not see the offline write after alice's sync")
			}
			// a finished live row lands on main at the sync and leaves live
			// (LORE_TEAM_SPEC F1), so a later edit is a committed change
			// that travels through git
			// (editing before the pull would write an untracked copy that
			// git pull then refuses to overwrite: see the campaign report)
			b.Lore("explain", live).OK(t).Says(t, "origin/main: present")
			b.Git("pull", "-q", "--no-rebase", "origin", "main")
			b.Lore(noun, "edit", live, "--body", "bob changed the landed row").OK(t)
			b.CommitAll("edit")
			b.AdminPushLanding("origin", "main") // bob's finished live rows land with his push (U-2)
			a.Git("pull", "-q", "--no-rebase", "origin", "main")
			if got := knowShowRow(t, a, k, live).Body; got != "bob changed the landed row" {
				t.Fatalf("alice sees %q after bob's pushed edit", got)
			}
		})
	}
}

func TestCampaignKnowledge_ConcurrentAdds(t *testing.T) {
	scenParallel(t)
	for _, noun := range []string{"memory", "decision", "tastepref"} {
		t.Run(noun, func(t *testing.T) {
			_, _, c := knowProject(t)
			k := knowKindByNoun(t, noun)
			const writers = 6
			ids := make([]string, writers)
			results := make([]th.Result, writers)
			var wg sync.WaitGroup
			for i := 0; i < writers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					results[i] = c.Lore(campAddArgs(k, "at once "+strconv.Itoa(i), "written at once "+strconv.Itoa(i))...)
				}(i)
			}
			wg.Wait()
			seen := map[string]bool{}
			for i, r := range results {
				if r.Code != 0 {
					t.Fatalf("writer %d: %s", i, r)
				}
				ids[i] = th.DecodeJSON[knowAdded](t, r).ID
				if seen[ids[i]] {
					t.Fatalf("two writers got id %s", ids[i])
				}
				seen[ids[i]] = true
			}
			l := knowListRows(t, c, k)
			if l.Count != writers {
				t.Fatalf("list holds %d rows after %d concurrent adds: %v", l.Count, writers, l.ids())
			}
			for _, id := range ids {
				if got := knowShowRow(t, c, k, id); got.ID != id {
					t.Fatalf("show %s: %+v", id, got)
				}
			}
		})
	}
}

func TestCampaignKnowledge_ConcurrentLiveAdds(t *testing.T) {
	st := newSyncTeam(t, 2)
	a, b := st.clones[0], st.clones[1]
	k := knowKindByNoun(t, "memory")
	const writers = 5
	results := make([]th.Result, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = a.Lore("memory", "add", "--body", "live at once "+strconv.Itoa(i), "--live", "--json")
		}(i)
	}
	wg.Wait()
	var ids []string
	for i, r := range results {
		if r.Code != 0 {
			t.Fatalf("writer %d: %s", i, r)
		}
		ids = append(ids, th.DecodeJSON[knowAdded](t, r).ID)
	}
	a.Lore("sync").OK(t)
	l := knowListRows(t, b, k)
	for _, id := range ids {
		if !l.has(id) {
			t.Fatalf("bob misses %s of %d concurrent live adds: %v", id, writers, l.ids())
		}
	}
}
