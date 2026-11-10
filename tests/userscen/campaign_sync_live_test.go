//go:build userscen

package userscen

// campaign_sync_live_test.go — live rows, landing and sync between two
// clones of one remote, the way a team meets them:
//
//   - offline remote: a write is kept locally (sync status and live diff
//     name it), land / compact / restore refuse with E_OFFLINE (exit 15) and
//     a hint, the teammate does not see it; back online, `sync run` sends
//     it once (a second run sends nothing) and the teammate reads it
//   - live off (LORE_LIVE=0): every live command and land answer
//     E_UNSUPPORTED (exit 23) with a hint; sync status says live is off
//     here; writes still work
//   - landing: land run --dry-run writes nothing, land run <id> lands the
//     row on main, a second land is "nothing to land" (exit 0), the
//     teammate reads the landed row after pulling main
//   - two people writing live rows at the same moment: both writes
//     succeed and each clone reads both rows

import (
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// camSyncDiffOut is `lore live diff --json`.
type camSyncDiffOut struct {
	Pending []string `json:"pending"`
}

// camSyncAddTask adds a live task in c and returns its id.
func camSyncAddTask(t *testing.T, st *syncTeam, c *th.Clone, title string) string {
	t.Helper()
	return th.DecodeJSON[syncShowOut](t, c.MustLore("task", "add", title, "--tasklist", st.tasklist, "--json")).Data.ID
}

// TestCampaignSync_LiveOfflineRemote: an unreachable remote keeps live
// writes here, refuses what needs the remote, and sends them once online.
func TestCampaignSync_LiveOfflineRemote(t *testing.T) {
	st, a, b := camSyncPair(t)
	alice := st.people[0]
	alice.Offline()
	id := camSyncAddTask(t, st, a, "send the March invoices — offline 🚀")
	a.MustLore("sync", "status").Says(t, "live not pushed:  1 write(s) waiting", id)
	if d := th.DecodeJSON[camSyncDiffOut](t, a.MustLore("live", "diff", "--json")); len(d.Pending) != 1 || !strings.Contains(d.Pending[0], id) {
		t.Fatalf("live diff --json names the one waiting write: %+v", d)
	}
	rev := st.app.Rev(syncRefLive)
	for _, args := range [][]string{{"land", "run", "--all"}, {"live", "compact"}, {"live", "restore"}} {
		r := a.Lore(args...)
		r.Fails(t, th.ExitOffline, "[E_OFFLINE]")
		if camSyncHint(r) == "" {
			t.Errorf("lore %s offline: a hint names the next step:\n%s", strings.Join(args, " "), r.Out())
		}
	}
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("nothing reaches the remote while offline")
	}
	b.MustLore("task", "list").Never(t, id)

	alice.Online()
	a.MustLore("sync", "run").Says(t, "pushed waiting live writes")
	if d := th.DecodeJSON[camSyncDiffOut](t, a.MustLore("live", "diff", "--json")); len(d.Pending) != 0 {
		t.Fatalf("after the sync nothing waits: %+v", d)
	}
	rev = st.app.Rev(syncRefLive)
	a.MustLore("sync", "run").Never(t, "pushed waiting live writes")
	if st.app.Rev(syncRefLive) != rev {
		t.Fatal("a second sync with nothing waiting must not write the live ref")
	}
	b.MustLore("task", "list").Says(t, id)
}

// TestCampaignSync_LiveOff: LORE_LIVE=0 turns live rows off for one
// command; every live verb says so with a next step, writes still work.
func TestCampaignSync_LiveOff(t *testing.T) {
	st, a, _ := camSyncPair(t)
	camSyncAddTask(t, st, a, "a live task before live is turned off")
	off := a.With("LORE_LIVE=0")
	for _, args := range [][]string{
		{"live", "show"}, {"live", "diff"}, {"live", "log"}, {"live", "compact"},
		{"live", "restore"}, {"live", "purge", "tsk_01a1b7b892587de4895ee2ad4e285e1c"}, {"land", "run", "--all"},
	} {
		camSyncRefuses(t, off, camSyncRefusal{args: args, code: "E_UNSUPPORTED", exit: th.ExitRefused, says: "live rows are not available here"})
	}
	off.MustLore("sync", "status").Says(t, "live rows:        off here")
	id := th.DecodeJSON[syncShowOut](t, off.MustLore("task", "add", "written with live off", "--tasklist", st.tasklist, "--json")).Data.ID
	off.MustLore("task", "list").Says(t, id)
}

// TestCampaignSync_LandRoundTrip: a named row lands once; the dry run and
// the second land change nothing; the teammate reads it from main.
func TestCampaignSync_LandRoundTrip(t *testing.T) {
	st, a, b := camSyncPair(t)
	id := camSyncAddTask(t, st, a, "close the books for Q1")
	a.MustLore("task", "done", id)
	mainRev, liveRev := st.app.Rev(syncRefMain), st.app.Rev(syncRefLive)
	before := camSyncTree(t, a.Dir)
	a.MustLore("land", "run", id, "--dry-run")
	camSyncSameTree(t, "land run --dry-run", before, camSyncTree(t, a.Dir))
	if st.app.Rev(syncRefMain) != mainRev || st.app.Rev(syncRefLive) != liveRev {
		t.Fatal("land run --dry-run must not write the remote")
	}
	a.MustLore("land", "run", id)
	if fs := st.app.FilesMatching(syncRefMain, id); len(fs) != 1 {
		t.Fatalf("the landed row is on main once: %v", fs)
	}
	mainRev = st.app.Rev(syncRefMain)
	a.MustLore("land", "run", id).Says(t, "nothing to land")
	if st.app.Rev(syncRefMain) != mainRev {
		t.Fatal("a second land of the same row must not write main")
	}
	b.Git("pull", "-q", "--no-rebase", "origin", "main")
	b.MustLore("task", "show", id).Says(t, "close the books for Q1")
}

// TestCampaignSync_TwoWritersAtOnce: two people add live rows at the same
// moment; both land in live and each clone reads both.
func TestCampaignSync_TwoWritersAtOnce(t *testing.T) {
	st, a, b := camSyncPair(t)
	rs := st.w.Simultaneously(
		a.Prepare("task", "add", "alice's task", "--tasklist", st.tasklist, "--json"),
		b.Prepare("task", "add", "bob's task", "--tasklist", st.tasklist, "--json"),
	)
	var got []string
	for _, r := range rs {
		got = append(got, th.DecodeJSON[syncShowOut](t, r.OK(t)).Data.ID)
	}
	for _, c := range []*th.Clone{a, b} {
		c.MustLore("sync", "run")
	}
	for _, c := range []*th.Clone{a, b} {
		c.MustLore("task", "list").Says(t, got...)
	}
}
