//go:build machines && !windows

package machines

// storepush_test.go — a real machine whose stores push in batches: on
// each built-in shape, an admin of a team (Start a team, through the
// setup steps) whose personal store and the team's org store are git
// stores on GitHub urls, both switched to push=batch with `lore store
// set`. Several writes then commit locally only (the remotes do not
// move), `lore store show` and `lore doctor` name each store's mode and
// the waiting commits, and one `lore sync` pushes ONE batch commit per
// store naming every row it carries; `lore sync status` lists the modes
// and, as a command that needs the remote current, pushes what waits as
// one batch commit too. The machine
// invariants (CheckMachine: setups, trash, import-scan, config check
// knows every key lore wrote, doctor, file modes) hold before and after.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Names the batch case uses (no shape uses them).
const (
	spSetup = "pushboss"
	// spOrg is the org (and its store) lsCreate starts.
	spOrg = lsOrg
	// spEvery is long enough that no write in the test reaches it.
	spEvery = "10m"
)

// spBatchSubject opens a batch commit's subject.
const spBatchSubject = "lore: batch "

func TestStorePushBatch(t *testing.T) {
	for _, shape := range AllShapes() {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			spBatchMachine(t, shape)
		})
	}
}

func spBatchMachine(t *testing.T, shape string) {
	m := Build(t, shape, loreBin)
	lsGHHosts(t, m)
	gone := goneAtStart(shape)
	repo := m.Repos[RepoWorkApp]

	// The team store and the personal store: GitHub urls, bare repos here.
	teamBare := spBare(t, m, "team", lsTeamURL)
	meBare := spBare(t, m, "me", spMeURL)
	lsCreate(t, m, spSetup, kindAdmin, "boss@example.test", []string{lsFolder}, false, lsTeam{storeURL: lsTeamURL}, lsAlpha)
	lsRun(t, m, repo, "store", "init", "me", spMeURL, "--yes")
	m.CheckMachine(t, shape+"/admin with git stores", gone)

	// Both start as now; set them to batch.
	want := spWant{Push: "manual", PushEvery: "5m", Compact: "off"} // C5: the default
	want.check(t, "before set me", spShow(t, m, repo, "me"))
	want.check(t, "before set "+spOrg, spShow(t, m, repo, spOrg))
	want = spWant{Push: "batch", PushEvery: spEvery, Compact: "off"}
	for _, s := range []string{"me", spOrg} {
		v := spSet(t, m, repo, s, "push=batch", "push_every="+spEvery)
		want.check(t, "set "+s, v)
		if !v.Changed {
			t.Errorf("store set %s: changed=false on a new value", s)
		}
	}
	if v := spShow(t, m, repo, "me"); v.Place != "personal" {
		t.Errorf("me: place %q, want personal", v.Place)
	}
	if v := spShow(t, m, repo, spOrg); v.Place != "org" || v.Org != spOrg || !v.Admin {
		t.Errorf("%s: place %q org %q admin %v, want org %s admin", spOrg, v.Place, v.Org, v.Admin, spOrg)
	}
	m.CheckMachine(t, shape+"/batch set", gone)

	before := map[string][]spCommit{"me": spLog(t, meBare), spOrg: spLog(t, teamBare)}
	bare := map[string]string{"me": meBare, spOrg: teamBare}

	// Several writes: each a local commit, none pushed.
	ids := map[string][]string{}
	for i, body := range []string{"batched fact one", "batched fact two", "batched fact three"} {
		for _, s := range []string{"me", spOrg} {
			ids[s] = append(ids[s], spMemAdd(t, m, repo, s, body+" in "+s))
		}
		if i == 0 {
			// A read between writes sees them at once and pushes nothing.
			if l := lsRun(t, m, repo, "memory", "list"); !strings.Contains(l, ids["me"][0]) || !strings.Contains(l, ids[spOrg][0]) {
				t.Errorf("a read after a batch write misses the row: %s", l)
			}
		}
	}
	for s, b := range bare {
		if got := spLog(t, b); len(got) != len(before[s]) {
			t.Errorf("batch %s: the remote moved before sync: %v (was %v)", s, spSubjects(got), spSubjects(before[s]))
		}
		if v := spShow(t, m, repo, s); v.Unpushed < 1 {
			t.Errorf("store show %s: unpushed %d after 3 batch writes", s, v.Unpushed)
		}
	}

	// doctor names each store's mode and what waits.
	spDoctorLines(t, m, repo, shape+"/waiting", true)

	// One sync: one batch commit per store, naming every row written.
	lsRun(t, m, repo, "sync")
	spBatchPushed(t, m, repo, bare, before, ids, "sync")
	spDoctorLines(t, m, repo, shape+"/synced", false)
	spStatusLines(t, m, repo, shape+"/synced")
	l := lsRun(t, m, repo, "memory", "list")
	for _, s := range []string{"me", spOrg} {
		for _, id := range ids[s] {
			if !strings.Contains(l, id) {
				t.Errorf("after sync memory list misses %s", id)
			}
		}
	}
	// A second sync with nothing waiting adds nothing.
	lsRun(t, m, repo, "sync")
	for s, b := range bare {
		if got := spLog(t, b); len(got) != len(before[s])+1 {
			t.Errorf("a second sync of %s changed the remote: %v", s, spSubjects(got))
		}
	}

	// sync status is a read: it never pushes (plain files 3.3); it shows
	// what waits and that `lore sync` sends it. lore sync then pushes one
	// batch commit per store.
	for s, b := range bare {
		before[s] = spLog(t, b)
		ids[s] = []string{spMemAdd(t, m, repo, s, "status fact one in "+s), spMemAdd(t, m, repo, s, "status fact two in "+s)}
		if got := spLog(t, b); len(got) != len(before[s]) {
			t.Errorf("batch %s: the remote moved before sync status: %v", s, spSubjects(got))
		}
	}
	spDoctorLines(t, m, repo, shape+"/waiting again", true)
	spStatusWaiting(t, m, repo, shape+"/sync status waiting", bare, before)
	lsRun(t, m, repo, "sync")
	spBatchPushed(t, m, repo, bare, before, ids, "sync after sync status")
	spStatusLines(t, m, repo, shape+"/sync status after sync")
	m.CheckMachine(t, shape+"/after sync", gone)
}

// spBatchPushed: since before, each remote got exactly one batch commit
// naming every row in ids, and store show has nothing waiting.
func spBatchPushed(t *testing.T, m *Machine, repo string, bare map[string]string, before map[string][]spCommit, ids map[string][]string, by string) {
	t.Helper()
	for s, b := range bare {
		got := spLog(t, b)
		if len(got) < len(before[s]) {
			t.Errorf("%s %s: the remote lost history: %v", by, s, spSubjects(got))
			continue
		}
		added := got[:len(got)-len(before[s])]
		if len(added) != 1 || !strings.HasPrefix(added[0].Subject, spBatchSubject) {
			t.Errorf("%s %s: want one %q commit on the remote, got %v", by, s, spBatchSubject, spSubjects(added))
			continue
		}
		for _, id := range ids[s] {
			if !strings.Contains(added[0].Body, "memories "+id) {
				t.Errorf("%s %s: the batch commit does not name %s:\n%s", by, s, id, added[0].Body)
			}
		}
		if want := fmt.Sprintf("memories %d", len(ids[s])); !strings.Contains(added[0].Subject, want) {
			t.Errorf("%s %s: batch subject %q does not count %s", by, s, added[0].Subject, want)
		}
		if v := spShow(t, m, repo, s); v.Unpushed != 0 || v.LastPush == "" {
			t.Errorf("store show %s after %s: unpushed %d, last push %q", s, by, v.Unpushed, v.LastPush)
		}
	}
}

// spModeLine is the batch stores' mode as doctor and sync status print it.
const spModeLine = "push batch every " + spEvery + ", compact off"

// spDoctorLines: lore doctor notes one line per store with its mode, and
// says commits wait exactly when they do.
func spDoctorLines(t *testing.T, m *Machine, repo, step string, waiting bool) {
	t.Helper()
	d := m.Lore(t, repo, "doctor")
	if !slices.Contains(doctorExits, d.Code) {
		t.Errorf("[%s] doctor: %s", step, d)
	}
	for _, s := range []string{"me", spOrg} {
		var found string
		for _, l := range strings.Split(d.Stdout, "\n") {
			if strings.Contains(l, "store "+s+": "+spModeLine) {
				found = l
			}
		}
		switch {
		case found == "":
			t.Errorf("[%s] doctor has no line \"store %s: %s\":\n%s", step, s, spModeLine, d.Stdout)
		case waiting && !strings.Contains(found, "waiting"):
			t.Errorf("[%s] doctor's line for %s does not say commits wait: %q", step, s, found)
		case !waiting && strings.Contains(found, "waiting"):
			t.Errorf("[%s] doctor's line for %s says commits wait after a push: %q", step, s, found)
		}
		if v := spShow(t, m, repo, s); waiting != (v.Unpushed > 0) {
			t.Errorf("[%s] store show %s: unpushed %d, waiting=%v", step, s, v.Unpushed, waiting)
		}
	}
}

// spStatusLines: lore sync status names both stores' push modes on their
// lines under "stores:" in text, and in JSON's data.stores with nothing
// waiting (sync status pushes first).
// spStatusWaiting: sync status with commits waiting names each store's
// waiting commits and `lore sync`, and pushes nothing.
func spStatusWaiting(t *testing.T, m *Machine, repo, step string, bare map[string]string, before map[string][]spCommit) {
	t.Helper()
	st := m.Lore(t, repo, "sync", "status")
	if st.Code != 0 {
		t.Errorf("[%s] sync status: %s", step, st)
	}
	_, block, _ := strings.Cut(st.Stdout, "stores:")
	for _, s := range []string{"me", spOrg} {
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(s) + `: ahead [1-9].*; ` + regexp.QuoteMeta(spModeLine) + " \\(`lore sync` pushes the waiting commits\\)$").MatchString(block) {
			t.Errorf("[%s] sync status has no line \"%s: ahead N, …; %s (`lore sync` pushes the waiting commits)\":\n%s", step, s, spModeLine, st.Stdout)
		}
	}
	for s, b := range bare {
		if got := spLog(t, b); len(got) != len(before[s]) {
			t.Errorf("[%s] sync status, a read, pushed %s: %v", step, s, spSubjects(got))
		}
	}
}

func spStatusLines(t *testing.T, m *Machine, repo, step string) {
	t.Helper()
	st := m.Lore(t, repo, "sync", "status")
	if st.Code != 0 {
		t.Errorf("[%s] sync status: %s", step, st)
	}
	_, block, ok := strings.Cut(st.Stdout, "stores:")
	if !ok {
		t.Errorf("[%s] sync status has no stores block:\n%s", step, st.Stdout)
	}
	for _, s := range []string{"me", spOrg} {
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(s) + `: ahead 0,.*; ` + regexp.QuoteMeta(spModeLine) + `$`).MatchString(block) {
			t.Errorf("[%s] sync status has no line \"%s: ahead 0, …; %s\":\n%s", step, s, spModeLine, st.Stdout)
		}
	}
	js := spSyncStatus(t, m, repo)
	for _, s := range []string{"me", spOrg} {
		v, ok := js[s]
		switch {
		case !ok:
			t.Errorf("[%s] sync status --json has no data.stores entry for %s: %v", step, s, js)
		case v.Push != "batch" || v.Problem != "" || v.Summary != spModeLine:
			t.Errorf("[%s] sync status --json %s: push %q summary %q problem %q", step, s, v.Push, v.Summary, v.Problem)
		case v.Ahead != 0:
			t.Errorf("[%s] sync status --json %s: ahead %d after sync status pushed", step, s, v.Ahead)
		}
	}
}
