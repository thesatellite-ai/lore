//go:build teame2e

package teame2e

// Merge tables on real git with the real binary (TIME_TRACKING_DESIGN 7.2,
// TASK_SYSTEM_DESIGN 7.5 and 14.5): two developers go offline, each changes
// the same task row, both come back online, and both clones must show the
// same merged result. Every case runs on its own task so one world covers a
// whole table; the clocks put dev2's offline edits one minute after dev1's,
// so "the newer edit wins" has a known answer.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// capTime is what the time tests need beyond the repo team.
var capTime = th.Cmd("task", "time", "log")

// timeEntry is one entry of `lore task time show --entries --deleted --json`.
type timeEntry struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	ActorID   string `json:"actor_id"`
	Seconds   int    `json:"seconds"`
	Note      string `json:"note"`
	DeletedAt string `json:"deleted_at"`
}

// timeView is the part of `lore task time show --json` the tests compare.
type timeView struct {
	Data struct {
		Effort  int         `json:"effort_seconds"`
		Elapsed int         `json:"elapsed_seconds"`
		Manual  int         `json:"manual_seconds"`
		Entries []timeEntry `json:"entries"`
		Open    []timeEntry `json:"open"`
	} `json:"data"`
}

// timeShow reads a task's time with every entry, removed ones included.
func timeShow(t *testing.T, c *th.Clone, id string) timeView {
	t.Helper()
	return th.DecodeJSON[timeView](t, c.Lore("task", "time", "show", id, "--entries", "--deleted", "--json"))
}

// entry finds one entry by id; ok is false when the merged row lost it.
func (v timeView) entry(id string) (timeEntry, bool) {
	for _, e := range v.Data.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return timeEntry{}, false
}

// canon is a stable text form of a time view: equal on two clones means
// both show the same numbers and the same entries.
func (v timeView) canon(t *testing.T) string {
	t.Helper()
	es := append([]timeEntry{}, v.Data.Entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].ID < es[j].ID })
	op := append([]timeEntry{}, v.Data.Open...)
	sort.Slice(op, func(i, j int) bool { return op[i].ID < op[j].ID })
	b, err := json.Marshal(struct {
		E, M     int
		Entries  []timeEntry
		OpenSpan []timeEntry
	}{v.Data.Effort, v.Data.Manual, es, op})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// offlineRound runs dev1's then dev2's offline writes (dev2 one minute
// later on the clock), then brings both back: dev1 pushes, dev2 merges
// and pushes, dev1 pulls the merge.
func (rt *repoTeam) offlineRound(t *testing.T, at time.Time, dev1, dev2 func()) {
	t.Helper()
	rt.dev1.SetNow(at)
	rt.dev2.SetNow(at)
	rt.c2.MustLore("task", "list")
	rt.dev1.Offline()
	rt.dev2.Offline()
	rt.dev1.SetNow(at.Add(time.Minute))
	dev1()
	rt.dev2.SetNow(at.Add(2 * time.Minute))
	dev2()
	rt.dev1.SetNow(at.Add(3 * time.Minute))
	rt.dev2.SetNow(at.Add(3 * time.Minute))
	rt.dev1.Online()
	rt.dev2.Online()
	rt.c1.MustLore("sync")
	rt.c2.MustLore("sync")
	rt.c1.MustLore("sync")
}

// TIME_TRACKING 7.2 test table, end to end: union by entry id on
// time_entries and time_open across two offline clones.
func TestTime_MergeTable_TwoClonesOffline(t *testing.T) {
	rt := newRepoTeam(t, capTime, th.Cmd("task", "time", "edit"), th.Cmd("task", "time", "rm"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	logged := func(c *th.Clone, id, d string, extra ...string) string {
		return mustID(t, c.MustLore(append([]string{"task", "time", "log", id, d}, extra...)...), "tme")
	}
	tAddAdd := rt.addTask(t, rt.c1, "add/add")
	tAddEdit := rt.addTask(t, rt.c1, "add/edit")
	eAddEdit := logged(rt.c1, tAddEdit, "15m", "--note", "base")
	tEditEdit := rt.addTask(t, rt.c1, "edit/edit")
	eEditEdit := logged(rt.c1, tEditEdit, "15m", "--note", "base")
	tRmEdit := rt.addTask(t, rt.c1, "delete then edit")
	eRmEdit := logged(rt.c1, tRmEdit, "15m", "--note", "base")
	tEditRm := rt.addTask(t, rt.c1, "edit then delete")
	eEditRm := logged(rt.c1, tEditRm, "15m", "--note", "base")
	tRmRm := rt.addTask(t, rt.c1, "delete/delete")
	eRmRm := logged(rt.c1, tRmRm, "15m", "--note", "base")
	tClose := rt.addTask(t, rt.c1, "close on one side, open on the other")
	rt.c1.MustLore("task", "start", tClose)

	var a1, a2 string
	rt.offlineRound(t, t0.Add(time.Hour), func() {
		a1 = logged(rt.c1, tAddAdd, "10m", "--note", "dev1")
		logged(rt.c1, tAddEdit, "5m", "--note", "dev1 new")
		rt.c1.MustLore("task", "time", "edit", eEditEdit, "--note", "dev1 edit")
		rt.c1.MustLore("task", "time", "rm", eRmEdit, "--yes")
		rt.c1.MustLore("task", "time", "edit", eEditRm, "--note", "dev1 edit")
		rt.c1.MustLore("task", "time", "rm", eRmRm, "--yes")
		rt.c1.MustLore("task", "done", tClose, "--no-lesson")
	}, func() {
		a2 = logged(rt.c2, tAddAdd, "20m", "--note", "dev2")
		rt.c2.MustLore("task", "time", "edit", eAddEdit, "--duration", "25m")
		rt.c2.MustLore("task", "time", "edit", eEditEdit, "--note", "dev2 edit")
		rt.c2.MustLore("task", "time", "edit", eRmEdit, "--note", "edited later")
		rt.c2.MustLore("task", "time", "rm", eEditRm, "--yes")
		rt.c2.MustLore("task", "time", "rm", eRmRm, "--yes")
		rt.c2.MustLore("task", "edit", tClose, "--priority", "high")
	})

	for _, id := range []string{tAddAdd, tAddEdit, tEditEdit, tRmEdit, tEditRm, tRmRm, tClose} {
		v1, v2 := timeShow(t, rt.c1, id), timeShow(t, rt.c2, id)
		if v1.canon(t) != v2.canon(t) {
			t.Fatalf("%s: both clones must show the same time after the merge:\ndev1 %s\ndev2 %s", id, v1.canon(t), v2.canon(t))
		}
	}
	// add/add: both entries survive.
	v := timeShow(t, rt.c1, tAddAdd)
	if _, ok := v.entry(a1); !ok || v.Data.Manual != 30*60 {
		t.Fatalf("add/add keeps both entries (%s, %s): %s", a1, a2, v.canon(t))
	}
	if _, ok := v.entry(a2); !ok {
		t.Fatalf("add/add keeps dev2's entry %s: %s", a2, v.canon(t))
	}
	// add/edit: the new entry and the edit both land.
	v = timeShow(t, rt.c1, tAddEdit)
	if e, ok := v.entry(eAddEdit); !ok || e.Seconds != 25*60 || v.Data.Manual != 30*60 || len(v.Data.Entries) != 2 {
		t.Fatalf("add/edit keeps dev1's new entry and dev2's edit: %s", v.canon(t))
	}
	// edit/edit: the newer edit (dev2's) wins the whole entry.
	if e, _ := timeShow(t, rt.c1, tEditEdit).entry(eEditEdit); e.Note != "dev2 edit" {
		t.Fatalf("edit/edit: the newer edit wins: %+v", e)
	}
	// delete, then a newer edit: modify beats an older delete.
	if e, ok := timeShow(t, rt.c1, tRmEdit).entry(eRmEdit); !ok || e.DeletedAt != "" || e.Note != "edited later" {
		t.Fatalf("an edit newer than the removal keeps the entry: %+v (found %v)", e, ok)
	}
	// edit, then a newer delete: the tombstone wins and is kept.
	v = timeShow(t, rt.c1, tEditRm)
	if e, ok := v.entry(eEditRm); !ok || e.DeletedAt == "" || v.Data.Manual != 0 {
		t.Fatalf("a removal newer than the edit wins, kept as a tombstone: %+v (found %v) %s", e, ok, v.canon(t))
	}
	// delete/delete: one tombstone, no time.
	v = timeShow(t, rt.c1, tRmRm)
	if e, ok := v.entry(eRmRm); !ok || e.DeletedAt == "" || len(v.Data.Entries) != 1 || v.Data.Manual != 0 {
		t.Fatalf("delete/delete: one tombstone and no time: %s", v.canon(t))
	}
	// Closed on dev1, still open in dev2's copy: the span is closed once and
	// not open any more (OV7-2: the read drops an open span whose id is closed).
	v = timeShow(t, rt.c1, tClose)
	if len(v.Data.Open) != 0 {
		t.Fatalf("a span closed on one side is not open after the merge: %s", v.canon(t))
	}
	n := 0
	for _, e := range v.Data.Entries {
		if e.Source == "status" && e.DeletedAt == "" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the closed status span is in time_entries exactly once, got %d: %s", n, v.canon(t))
	}
}

// checklistView is `lore task checklist list --json`.
type checklistView struct {
	Data struct {
		Done  int `json:"done"`
		Total int `json:"total"`
		Items []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
			Done bool   `json:"done"`
		} `json:"items"`
	} `json:"data"`
}

func checklistOf(t *testing.T, c *th.Clone, id string) checklistView {
	t.Helper()
	return th.DecodeJSON[checklistView](t, c.Lore("task", "checklist", "list", id, "--json"))
}

// canon is the items as "text=done" in list order.
func (v checklistView) canon() string {
	var parts []string
	for _, it := range v.Data.Items {
		mark := " "
		if it.Done {
			mark = "x"
		}
		parts = append(parts, "["+mark+"] "+it.Text)
	}
	return strings.Join(parts, " | ")
}

// TASK_SYSTEM 7.5 list-by-id merge table, end to end: add/add, tick/untick
// both orders, edit/delete both orders. Both sides start from the same
// unticked item, so for "tick/untick" each side really changes it: one side
// ticks, the other ticks then unticks; the side whose last change is newer
// wins (an untick of an unticked item writes nothing and cannot win). The reorder/reorder row has no
// command in 0.1.18 (checklist items keep their order; no reorder verb in
// CLI_REFERENCE), so it is not run here.
func TestTask_ChecklistMergeTable_TwoClones(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("task", "checklist", "add"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	items := func(id string) []string {
		var ids []string
		for _, it := range checklistOf(t, rt.c1, id).Data.Items {
			ids = append(ids, it.ID)
		}
		return ids
	}
	tAdd := rt.addTask(t, rt.c1, "add/add")
	rt.c1.MustLore("task", "checklist", "add", tAdd, "base")
	tTick := rt.addTask(t, rt.c1, "tick then untick")
	rt.c1.MustLore("task", "checklist", "add", tTick, "a")
	tUntick := rt.addTask(t, rt.c1, "untick then tick")
	rt.c1.MustLore("task", "checklist", "add", tUntick, "a")
	tEditRm := rt.addTask(t, rt.c1, "edit then delete")
	rt.c1.MustLore("task", "checklist", "add", tEditRm, "a", "--item", "b")
	tRmEdit := rt.addTask(t, rt.c1, "delete then edit")
	rt.c1.MustLore("task", "checklist", "add", tRmEdit, "a", "--item", "b")
	iTick, iUntick := items(tTick)[0], items(tUntick)[0]
	iEditRm, iRmEdit := items(tEditRm)[0], items(tRmEdit)[0]

	rt.offlineRound(t, t0.Add(time.Hour), func() {
		rt.c1.MustLore("task", "checklist", "add", tAdd, "from dev1")
		rt.c1.MustLore("task", "checklist", "tick", tTick, iTick)
		rt.c1.MustLore("task", "checklist", "tick", tUntick, iUntick)
		rt.c1.MustLore("task", "checklist", "untick", tUntick, iUntick)
		rt.c1.MustLore("task", "checklist", "edit", tEditRm, iEditRm, "a edited")
		rt.c1.MustLore("task", "checklist", "rm", tRmEdit, iRmEdit)
	}, func() {
		rt.c2.MustLore("task", "checklist", "add", tAdd, "from dev2")
		rt.c2.MustLore("task", "checklist", "tick", tTick, iTick)
		rt.c2.MustLore("task", "checklist", "untick", tTick, iTick)
		rt.c2.MustLore("task", "checklist", "tick", tUntick, iUntick)
		rt.c2.MustLore("task", "checklist", "rm", tEditRm, iEditRm)
		rt.c2.MustLore("task", "checklist", "edit", tRmEdit, iRmEdit, "a edited later")
	})

	want := map[string]string{
		tAdd:    "[ ] base | [ ] from dev1 | [ ] from dev2",
		tTick:   "[ ] a",                      // dev2's later untick wins over dev1's tick
		tUntick: "[x] a",                      // dev2's later tick wins over dev1's untick
		tEditRm: "[ ] b",                      // the later removal wins
		tRmEdit: "[ ] a edited later | [ ] b", // an edit newer than the removal keeps the item
	}
	for id, w := range want {
		v1, v2 := checklistOf(t, rt.c1, id).canon(), checklistOf(t, rt.c2, id).canon()
		if v1 != v2 {
			t.Fatalf("%s: both clones must show the same checklist:\ndev1 %s\ndev2 %s", id, v1, v2)
		}
		if id == tAdd {
			// add/add: both items survive; their relative order is the
			// merge's (by id), so compare as a set.
			got := strings.Split(v1, " | ")
			sort.Strings(got)
			if strings.Join(got, " | ") != w {
				t.Fatalf("add/add keeps both items: %s", v1)
			}
			continue
		}
		if v1 != w {
			t.Fatalf("%s: got %q want %q", id, v1, w)
		}
	}
}

// peopleView is the assignee part of `lore task show --json`.
type peopleView struct {
	Data struct {
		Lead      string   `json:"lead"`
		Assignees []string `json:"assignees"`
	} `json:"data"`
}

// TASK_SYSTEM 14.5 people merge table, end to end: the assignee list is a
// set merge (add/add keeps both; a removal on one side and an add of
// someone else on the other keep both changes), and a lead removed from the
// list on the other side is put back by the reader (14.4: the lead is
// always an assignee).
func TestTask_PeopleMergeTable_TwoClones(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("add-assignee", "task", "edit"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	dev3 := rt.w.Human("dev3", "dev3@acme.example")
	dev3.WriteConfig(th.Config{Live: th.FreshFetch()})
	dev3.SetNow(t0)
	c3 := dev3.Clone(rt.app, "app")
	for _, c := range []*th.Clone{rt.c1, rt.c2, c3, rt.c1, rt.c2} {
		c.MustLore("actor", "register")
	}
	// Actor ids, not handles: a handle a teammate's clone assigned first is
	// not found on this clone (BUG-ASSIGN-HANDLE, pinned by
	// TestTask_AssignHandleAfterTeammateAssigned); this table is about the merge.
	d1, d2, d3 := actorIDOf(t, rt.c1, "dev1"), actorIDOf(t, rt.c1, "dev2"), actorIDOf(t, rt.c2, "dev3")
	tAdd := rt.addTask(t, rt.c1, "add/add")
	tAddRm := rt.addTask(t, rt.c1, "add/remove")
	rt.c1.MustLore("task", "edit", tAddRm, "--assign", d1, "--assign", d2)
	tLead := rt.addTask(t, rt.c1, "lead vs list")
	rt.c1.MustLore("task", "edit", tLead, "--assign", d1, "--assign", d2)

	rt.offlineRound(t, t0.Add(time.Hour), func() {
		rt.c1.MustLore("task", "edit", tAdd, "--add-assignee", d1)
		rt.c1.MustLore("task", "edit", tAddRm, "--remove-assignee", d2)
		rt.c1.MustLore("task", "edit", tLead, "--lead", d2)
	}, func() {
		rt.c2.MustLore("task", "edit", tAdd, "--add-assignee", d2)
		rt.c2.MustLore("task", "edit", tAddRm, "--add-assignee", d3)
		rt.c2.MustLore("task", "edit", tLead, "--remove-assignee", d2, "--lead", d1)
	})

	show := func(c *th.Clone, id string) peopleView {
		return th.DecodeJSON[peopleView](t, c.Lore("task", "show", id, "--json"))
	}
	for _, id := range []string{tAdd, tAddRm, tLead} {
		p1, p2 := show(rt.c1, id), show(rt.c2, id)
		sort.Strings(p1.Data.Assignees)
		sort.Strings(p2.Data.Assignees)
		if p1.Data.Lead != p2.Data.Lead || strings.Join(p1.Data.Assignees, ",") != strings.Join(p2.Data.Assignees, ",") {
			t.Fatalf("%s: both clones show the same people: %+v vs %+v", id, p1.Data, p2.Data)
		}
		// The lead is always one of the assignees (14.4).
		if l := p1.Data.Lead; l != "" && !hasItem(p1.Data.Assignees, l) {
			t.Fatalf("%s: the lead %s must be an assignee after the merge: %v", id, l, p1.Data.Assignees)
		}
	}
	if p := show(rt.c1, tAdd); len(p.Data.Assignees) != 2 {
		t.Fatalf("add/add keeps both assignees: %+v", p.Data)
	}
	if p := show(rt.c1, tAddRm); len(p.Data.Assignees) != 2 || !hasItem(p.Data.Assignees, d1) || !hasItem(p.Data.Assignees, d3) {
		t.Fatalf("add/remove: dev2 removed on one side, dev3 added on the other, dev1 kept: %+v", p.Data)
	}
}

// actorIDOf returns the act_ id of the person with handle h on c's roster.
func actorIDOf(t *testing.T, c *th.Clone, h string) string {
	t.Helper()
	l := th.DecodeJSON[struct {
		Data []struct {
			ID     string `json:"id"`
			Handle string `json:"handle"`
		} `json:"data"`
	}](t, c.Lore("actor", "list", "--json"))
	for _, a := range l.Data {
		if a.Handle == h {
			return a.ID
		}
	}
	t.Fatalf("no actor @%s on the roster: %+v", h, l.Data)
	return ""
}

// bugAssignHandle: on dev2's clone, `--add-assignee @dev1` is E_NOT_FOUND
// once dev1's clone has assigned @dev1 and @dev2 to a task, although
// `lore actor list` there shows dev1 with that handle. Without the earlier
// assign the same command works. Found by a-task-e2e (PASS3 queue #10).
var bugAssignHandle = th.Bug{ID: "BUG-ASSIGN-HANDLE", Owner: "main (roster/live actors)", Open: true}

// A handle resolves on every clone that lists the person, whatever a
// teammate assigned before (TASK_SYSTEM 14: `--assign @handle`).
func TestTask_AssignHandleAfterTeammateAssigned(t *testing.T) {
	rt := newRepoTeam(t, th.Flag("add-assignee", "task", "edit"))
	rt.c1.MustLore("actor", "register")
	rt.c2.MustLore("actor", "register")
	id := rt.addTask(t, rt.c1, "x")
	rt.c1.MustLore("task", "edit", id, "--assign", "@dev1", "--assign", "@dev2")
	other := rt.addTask(t, rt.c1, "y")
	rt.c2.MustLore("task", "list")
	if !strings.Contains(rt.c2.MustLore("actor", "list").Stdout, "dev1") {
		t.Fatal("control: dev2's clone lists dev1")
	}
	r := rt.c2.Lore("task", "edit", other, "--add-assignee", "@dev1")
	bugAssignHandle.PendingOn(t, r, "E_NOT_FOUND", "@dev1")
	r.OK(t)
}

// TASK_SYSTEM 6.15 E2E: two clones re-parent at once into a cycle, sync,
// both show the same tree and the same warning; `task tree check --fix`
// repairs it the same way everywhere.
func TestTask_TreeTwoClonesReparentAtOnce(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("task", "tree", "check"), th.Flag("parent", "task", "edit"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	a := rt.addTask(t, rt.c1, "A")
	b := rt.addTask(t, rt.c1, "B")
	rt.offlineRound(t, t0.Add(time.Hour), func() {
		rt.c1.MustLore("task", "edit", a, "--parent", b)
	}, func() {
		rt.c2.MustLore("task", "edit", b, "--parent", a)
	})
	r1 := rt.c1.Lore("task", "tree", "check")
	r2 := rt.c2.Lore("task", "tree", "check")
	if r1.Stdout != r2.Stdout || r1.Code != r2.Code {
		t.Fatalf("both clones show the same tree warning:\ndev1 %s\ndev2 %s", r1, r2)
	}
	r1.Says(t, "cycle")
	rt.c1.MustLore("task", "tree", "check", "--fix")
	rt.c2.MustLore("sync")
	for _, c := range []*th.Clone{rt.c1, rt.c2} {
		c.Lore("task", "tree", "check").OK(t).Never(t, "cycle")
	}
	// The smallest id becomes top level (6.13).
	low, high := a, b
	if high < low {
		low, high = high, low
	}
	parentOf := func(id string) string {
		return th.DecodeJSON[struct {
			Data struct {
				Parent string `json:"parent_id"`
			} `json:"data"`
		}](t, rt.c2.Lore("task", "show", id, "--json")).Data.Parent
	}
	if p := parentOf(low); p != "" {
		t.Fatalf("after --fix the smallest id %s is top level, parent %s", low, p)
	}
	if p := parentOf(high); p != low {
		t.Fatalf("after --fix %s stays under %s, parent %q", high, low, p)
	}
}
