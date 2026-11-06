//go:build teame2e

package teame2e

// TASK_SYSTEM_DESIGN 10.4 / 10.5 (TSQ12, OV15c) on real git with the real
// binary: two machines finish the same instance of a recurring task while
// offline. Each creates the next instance and its re-created sub-task tree
// under derived ids, with the root's metadata carried, the children's due
// dates moved by the root's shift and the in-subtree --after edge
// re-pointed. When both come back the merge converges: one next instance,
// one copy of each child, the same bytes on origin and on both clones.

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teARecurCaps is what the double completion test needs beyond the repo team.
var teARecurCaps = []th.Cap{
	th.Flag("recur", "task", "add"),
	th.Flag("children", "task", "done"),
	th.Cmd("task", "checklist", "add"),
}

// teALocalRow reads one row file from a clone's own copy of ref (what this
// machine wrote, pushed or not); ok is false when the file is not there.
func teALocalRow(t *testing.T, c *th.Clone, ref, path string) (string, bool) {
	t.Helper()
	r := c.TryGit("show", ref+":"+path)
	if r.Code != 0 {
		return "", false
	}
	return r.Stdout, true
}

// teAChildrenOn lists the ids of the task rows under _root/tasks on ref of
// clone c whose parent_id is parent, sorted.
func teAChildrenOn(t *testing.T, c *th.Clone, ref, parent string) []string {
	t.Helper()
	var out []string
	for _, p := range strings.Fields(c.Git("ls-tree", "-r", "--name-only", ref, rootFolder+"/"+tblTasks)) {
		body, ok := teALocalRow(t, c, ref, p)
		if !ok {
			continue
		}
		if teAField(t, body, "parent_id") == parent {
			out = append(out, strings.TrimSuffix(p[strings.LastIndex(p, "/")+1:], ".json"))
		}
	}
	sort.Strings(out)
	return out
}

// teAField reads one top-level field of a row file as text (a JSON string
// unquoted, anything else as its JSON text; "" when absent or null).
func teAField(t *testing.T, body, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("row is not JSON: %v\n%s", err, body)
	}
	raw, ok := m[field]
	if !ok || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// teADomain is a row file without its envelope and write bookkeeping: what
// the task says (title, dates, recur, tree, edges, metadata, checklist),
// which two machines that create the same instance must agree on byte for
// byte. Dropped: "_…" envelope keys (hlc, version) and who/when wrote it.
func teADomain(t *testing.T, body string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("row is not JSON: %v\n%s", err, body)
	}
	b, err := json.Marshal(teAStrip(m)) // map keys marshal sorted: a stable form
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// teAStrip drops envelope keys and teAWriteBookkeeping from an object and
// from the objects of its list fields (checklist items carry their own
// created/updated stamps).
func teAStrip(m map[string]json.RawMessage) map[string]json.RawMessage {
	for k, raw := range m {
		if strings.HasPrefix(k, "_") || teAWriteBookkeeping[k] {
			delete(m, k)
			continue
		}
		var items []map[string]json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) == 0 || items[0] == nil {
			continue
		}
		for i := range items {
			items[i] = teAStrip(items[i])
		}
		if b, err := json.Marshal(items); err == nil {
			m[k] = b
		}
	}
	return m
}

// teAWriteBookkeeping are the row fields that name the writer or the write
// time rather than the task: they may differ between two machines that
// create the same instance, and the merge settles them like any field.
var teAWriteBookkeeping = map[string]bool{
	"created_at": true, "updated_at": true, "created_by": true, "updated_by": true,
	"created_by_actor_id": true, "updated_by_actor_id": true, "deleted_at": true, "deleted_by": true,
}

// teADoneNext is the part of `task done --json` naming the next instance.
type teADoneNext struct {
	Recur struct {
		ID       string `json:"id"`
		Due      string `json:"due"`
		Index    int    `json:"index"`
		Children int    `json:"children"`
		Existed  bool   `json:"existed"`
	} `json:"recur"`
}

// teANextOf decodes the next instance a done --json printed; an enveloped
// {"data": …} result is read the same way.
func teANextOf(t *testing.T, r th.Result) teADoneNext {
	t.Helper()
	var v struct {
		teADoneNext
		Data *teADoneNext `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("done --json: %v\n%s", err, r)
	}
	if v.Data != nil {
		return *v.Data
	}
	return v.teADoneNext
}

// TestTeA_RecurDoubleCompletion_TwoMachinesByteEqual runs the double
// completion in both reconnect orders (dev1 syncs first, dev2 syncs first).
func TestTeA_RecurDoubleCompletion_TwoMachinesByteEqual(t *testing.T) {
	for _, order := range []string{"dev1 first", "dev2 first"} {
		t.Run(order, func(t *testing.T) {
			teARecurDoubleCompletion(t, order == "dev2 first")
		})
	}
}

func teARecurDoubleCompletion(t *testing.T, dev2First bool) {
	rt := newRepoTeam(t, teARecurCaps...)
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	c1, c2 := rt.c1, rt.c2

	// The series: a weekly root due Monday 2026-11-02 with metadata and a
	// checklist item, two children, the second --after the first.
	root := rt.addTask(t, c1, "weekly report", "--recur", "every week", "--due", "2026-11-02", "--meta", "sprint=s1")
	kidA := rt.addTask(t, c1, "gather numbers", "--parent", root, "--due", "2026-11-03")
	kidB := rt.addTask(t, c1, "write summary", "--parent", root, "--after", kidA)
	c1.MustLore("task", "checklist", "add", root, "proofread")
	c2.MustLore("task", "list")

	// Both machines finish instance #1 offline, an hour apart.
	rt.dev1.Offline()
	rt.dev2.Offline()
	done := func(a *th.Actor, c *th.Clone, at time.Time) teADoneNext {
		a.SetNow(at)
		r := c.MustLore("task", "done", root, "--children", "done", "--force", "--no-lesson", "--json")
		return teANextOf(t, r)
	}
	n1 := done(rt.dev1, c1, t0.Add(time.Hour))
	n2 := done(rt.dev2, c2, t0.Add(2*time.Hour))
	next := n1.Recur.ID
	if next == "" || n1.Recur.ID != n2.Recur.ID {
		t.Fatalf("both machines derive the same next instance id: dev1 %+v, dev2 %+v", n1, n2)
	}
	if n1.Recur.Index != 2 || n2.Recur.Index != 2 || n1.Recur.Due != "2026-11-09" || n2.Recur.Due != "2026-11-09" {
		t.Fatalf("instance #2 due 2026-11-09 on both machines: dev1 %+v, dev2 %+v", n1, n2)
	}
	if n1.Recur.Children != 2 || n2.Recur.Children != 2 {
		t.Fatalf("each machine re-creates the two sub-tasks: dev1 %+v, dev2 %+v", n1, n2)
	}

	// Offline, each machine's own copy of the new tree says the same thing.
	p := liveRowPath(rootFolder, tblTasks, next)
	b1, ok1 := teALocalRow(t, c1, refLive, p)
	b2, ok2 := teALocalRow(t, c2, refLive, p)
	if !ok1 || !ok2 {
		t.Fatalf("each machine wrote the next instance on its own live ref: dev1 %v, dev2 %v", ok1, ok2)
	}
	if d1, d2 := teADomain(t, b1), teADomain(t, b2); d1 != d2 {
		t.Fatalf("the two offline copies of the next instance agree field for field:\ndev1 %s\ndev2 %s", d1, d2)
	}
	k1 := teAChildrenOn(t, c1, refLive, next)
	k2 := teAChildrenOn(t, c2, refLive, next)
	if len(k1) != 2 || strings.Join(k1, ",") != strings.Join(k2, ",") {
		t.Fatalf("both machines derive the same two child ids: dev1 %v, dev2 %v", k1, k2)
	}
	for _, k := range k1 {
		kp := liveRowPath(rootFolder, tblTasks, k)
		x1, _ := teALocalRow(t, c1, refLive, kp)
		x2, _ := teALocalRow(t, c2, refLive, kp)
		if d1, d2 := teADomain(t, x1), teADomain(t, x2); d1 != d2 {
			t.Fatalf("the two offline copies of child %s agree field for field:\ndev1 %s\ndev2 %s", k, d1, d2)
		}
	}

	// Back online, in the order under test.
	rt.dev1.SetNow(t0.Add(3 * time.Hour))
	rt.dev2.SetNow(t0.Add(3 * time.Hour))
	rt.dev1.Online()
	rt.dev2.Online()
	first, second := c1, c2
	if dev2First {
		first, second = c2, c1
	}
	first.MustLore("sync")
	second.MustLore("sync")
	first.MustLore("sync")

	// One next instance and one copy of each child on origin, with the
	// carried fields; origin and both clones hold the same bytes.
	onOrigin, ok := rt.liveTask(t, next)
	if !ok {
		t.Fatalf("the next instance is on origin's live ref after the merge")
	}
	if onOrigin.Status != statusTodo && onOrigin.Status != "" {
		t.Fatalf("the merged next instance is open: %s", onOrigin.Status)
	}
	originBody, _ := rt.app.Read(refLive, p)
	if got := teAField(t, originBody, "due_at"); !strings.HasPrefix(got, "2026-11-09") {
		t.Fatalf("the merged next instance is due 2026-11-09: %s", originBody)
	}
	if !strings.Contains(originBody, "sprint") || !strings.Contains(originBody, "s1") {
		t.Fatalf("the root's metadata is carried to the merged next instance: %s", originBody)
	}
	if !strings.Contains(originBody, "proofread") {
		t.Fatalf("the checklist is copied to the merged next instance: %s", originBody)
	}
	kids := teAChildrenOn(t, c1, refLive, next)
	if strings.Join(kids, ",") != strings.Join(k1, ",") {
		t.Fatalf("after the merge the next instance has exactly the two derived children %v, got %v", k1, kids)
	}
	var newA, newB string
	for _, k := range kids {
		kb, _ := rt.app.Read(refLive, liveRowPath(rootFolder, tblTasks, k))
		switch teAField(t, kb, "title") {
		case "gather numbers":
			newA = k
			if due := teAField(t, kb, "due_at"); !strings.HasPrefix(due, "2026-11-10") {
				t.Fatalf("the child's due moves by the root's shift (2026-11-03 + 7 days): %s", kb)
			}
		case "write summary":
			newB = k
		}
	}
	if newA == "" || newB == "" || newA == kidA || newB == kidB {
		t.Fatalf("the merged instance holds two new children: %v (old %s %s)", kids, kidA, kidB)
	}
	bb, _ := rt.app.Read(refLive, liveRowPath(rootFolder, tblTasks, newB))
	if bl := teAField(t, bb, "blocked_by"); !strings.Contains(bl, newA) || strings.Contains(bl, kidA) {
		t.Fatalf("the in-subtree --after edge points at the new child %s, not the old %s: %s", newA, kidA, bb)
	}
	for _, id := range append([]string{next}, kids...) {
		path := liveRowPath(rootFolder, tblTasks, id)
		ob, _ := rt.app.Read(refLive, path)
		for _, c := range []*th.Clone{c1, c2} {
			cb, ok := teALocalRow(t, c, refLive, path)
			if !ok || cb != ob {
				t.Fatalf("%s: the clone's live copy is byte-equal to origin's after the merge:\norigin %s\nclone  %s", id, ob, cb)
			}
		}
	}
	s1 := c1.MustLore("task", "show", next, "--json").Stdout
	s2 := c2.MustLore("task", "show", next, "--json").Stdout
	if s1 != s2 {
		t.Fatalf("both clones show the same merged next instance:\ndev1 %s\ndev2 %s", s1, s2)
	}
	for _, c := range []*th.Clone{c1, c2} {
		c.MustLore("task", "show", next).Never(t, "conflict")
	}
}
