//go:build userscen

package userscen

// Sub-task trees through the real binary: add under a parent, move a
// subtree (edit --parent / --no-parent), sibling order (--first, --last,
// --before, --after-sibling), every cycle refused with the tree unchanged,
// tasks.max_depth on add and on move, closing a parent with open
// sub-tasks (refused, then --children done|cancel), adding under a closed
// parent (refused, then --reopen-parent), and a big tree of hundreds of
// sub-tasks split in one batch and closed in one batch.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// tasksTreeShow is `lore task show <id> --tree --json`'s tree part.
type tasksTreeShow struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Rollup struct {
		ChildrenTotal int `json:"children_total"`
		ChildrenDone  int `json:"children_done"`
		LeavesTotal   int `json:"leaves_total"`
		LeavesDone    int `json:"leaves_done"`
	} `json:"rollup"`
	Tree []struct {
		ID    string `json:"id"`
		Depth int    `json:"depth"`
	} `json:"tree"`
}

// tasksTree reads the subtree of id, depth first.
func tasksTree(t *testing.T, c *th.Clone, id string) tasksTreeShow {
	t.Helper()
	return th.DecodeJSON[tasksEnvelope[tasksTreeShow]](t, c.Lore("task", "show", id, "--tree", "--json")).Data
}

// tasksTreeIDs is the subtree's ids in show order.
func (v tasksTreeShow) ids() []string {
	out := make([]string, 0, len(v.Tree))
	for _, l := range v.Tree {
		out = append(out, l.ID)
	}
	return out
}

// Every way to make a task its own ancestor is refused E_CYCLE (exit 19)
// with a hint, and the tree stays as it was.
func TestTasksTree_CyclesRefused(t *testing.T) {
	s := newTasksSolo(t)
	root := tasksAdd(t, s.c, "root")
	child := tasksAdd(t, s.c, "child", "--parent", root)
	grand := tasksAdd(t, s.c, "grandchild", "--parent", child)
	for _, tc := range []struct{ name, id, parent, say string }{
		{"self", root, root, "cannot be its own parent"},
		{"under its child", root, child, "a task cannot go under its own sub-task"},
		{"under its grandchild", root, grand, "a task cannot go under its own sub-task"},
		{"middle under its child", child, grand, "a task cannot go under its own sub-task"},
	} {
		s.c.Lore("task", "edit", tc.id, "--parent", tc.parent).Fails(t, th.ExitGuard, "E_CYCLE", tc.say)
		e := tasksJSONErr(t, s.c, "task", "edit", tc.id, "--parent", tc.parent)
		if e.Error.Code != "E_CYCLE" || e.Error.Hint == "" {
			t.Fatalf("%s: --json names E_CYCLE with a hint: %+v", tc.name, e)
		}
	}
	if p := tasksShow(t, s.c, root).ParentID; p != "" {
		t.Fatalf("root stays top level after refused moves: parent %s", p)
	}
	if got := tasksTree(t, s.c, root).ids(); strings.Join(got, ",") != child+","+grand {
		t.Fatalf("the tree is unchanged: %v", got)
	}
	// A parent that does not exist, or is not a task id.
	s.c.Lore("task", "add", "orphan", "--parent", "tsk_nope").Refused(t, "E_INVALID_ID")
	missing := root[:len(root)-6] + "abcdef"
	s.c.Lore("task", "add", "orphan", "--parent", missing).Fails(t, th.ExitNotFound, "E_NOT_FOUND")
}

// Moving a subtree carries its sub-tasks; --no-parent makes it top level;
// a sub-task inherits its parent's tasklist; placement flags order the
// siblings the way show --tree prints them.
func TestTasksTree_MoveAndOrder(t *testing.T) {
	s := newTasksSolo(t)
	a := tasksAdd(t, s.c, "parent A")
	b := tasksAdd(t, s.c, "parent B")
	mid := tasksAdd(t, s.c, "moving subtree", "--parent", a)
	leaf := tasksAdd(t, s.c, "its leaf", "--parent", mid)

	s.c.MustLore("task", "edit", mid, "--parent", b)
	if got := tasksTree(t, s.c, b).ids(); strings.Join(got, ",") != mid+","+leaf {
		t.Fatalf("the subtree moved under B with its leaf: %v", got)
	}
	if got := tasksTree(t, s.c, a).Tree; len(got) != 0 {
		t.Fatalf("A has no sub-tasks left: %v", got)
	}
	if p := tasksShow(t, s.c, leaf).ParentID; p != mid {
		t.Fatalf("the leaf keeps its own parent: %s", p)
	}
	s.c.MustLore("task", "edit", mid, "--no-parent")
	if p := tasksShow(t, s.c, mid).ParentID; p != "" {
		t.Fatalf("--no-parent detaches: %s", p)
	}
	if !tasksHas(tasksList(t, s.c, "--top"), mid) || tasksHas(tasksList(t, s.c, "--top"), leaf) {
		t.Fatal("--top lists the detached subtree root, not its leaf")
	}
	if tl := tasksShow(t, s.c, leaf).TaskListID; tl == "" || tl != tasksShow(t, s.c, a).TaskListID {
		t.Fatalf("a sub-task lives in its parent's tasklist: %s", tl)
	}

	// Sibling order: 1 2 3 added in order, then rearranged.
	p := tasksAdd(t, s.c, "ordered parent")
	one := tasksAdd(t, s.c, "one", "--parent", p)
	two := tasksAdd(t, s.c, "two", "--parent", p)
	three := tasksAdd(t, s.c, "three", "--parent", p)
	order := func(want ...string) {
		t.Helper()
		if got := tasksTree(t, s.c, p).ids(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("sibling order %v, want %v", got, want)
		}
	}
	order(one, two, three)
	s.c.MustLore("task", "edit", three, "--first")
	order(three, one, two)
	s.c.MustLore("task", "edit", three, "--last")
	order(one, two, three)
	s.c.MustLore("task", "edit", one, "--after-sibling", two)
	order(two, one, three)
	s.c.MustLore("task", "edit", three, "--before", two)
	order(three, two, one)
	// Placing next to a task that is not a sibling is refused.
	s.c.Lore("task", "edit", one, "--before", leaf).Refused(t)
	order(three, two, one)
	s.c.MustLore("task", "tree", "check").Never(t, "shared_rank", "parent_cycle")
}

// tasks.max_depth (default 4) bounds both a new sub-task and a moved
// subtree (E_TREE_DEPTH, exit 23, the chain and the policy hint); raising
// the policy lets the same add through.
func TestTasksTree_MaxDepth(t *testing.T) {
	s := newTasksSolo(t)
	chain := []string{tasksAdd(t, s.c, "level 1")}
	for i := 2; i <= 4; i++ {
		chain = append(chain, tasksAdd(t, s.c, "level "+strconv.Itoa(i), "--parent", chain[len(chain)-1]))
	}
	e := tasksJSONErr(t, s.c, "task", "add", "level 5", "--parent", chain[3])
	if e.Error.Code != "E_TREE_DEPTH" || e.Error.Exit != th.ExitRefused || !strings.Contains(e.Error.Message, "max_depth 4") || !strings.Contains(e.Error.Hint, "lore policy set tasks.max_depth") {
		t.Fatalf("a 5th level is refused with the policy hint: %+v", e)
	}
	// Moving a two-level subtree under level 3 makes 5 levels: refused.
	sub := tasksAdd(t, s.c, "sub root")
	tasksAdd(t, s.c, "sub leaf", "--parent", sub)
	s.c.Lore("task", "edit", sub, "--parent", chain[2]).Fails(t, th.ExitRefused, "E_TREE_DEPTH")
	if p := tasksShow(t, s.c, sub).ParentID; p != "" {
		t.Fatalf("a refused move leaves the subtree where it was: %s", p)
	}
	s.c.MustLore("policy", "set", "tasks.max_depth", "5", "--yes")
	tasksAdd(t, s.c, "level 5", "--parent", chain[3])
	if got := len(tasksTree(t, s.c, chain[0]).Tree); got != 4 {
		t.Fatalf("level 1 has 4 descendants after the policy allows 5 levels: %d", got)
	}
}

// Closing a parent with open sub-tasks: refused (E_OPEN_CHILDREN, the open
// ones listed, nothing closed) until --children done|cancel closes the
// whole subtree in the same batch. Adding under a closed parent is
// refused until --reopen-parent, which reopens it with no close fields.
func TestTasksTree_CloseParentAndReopen(t *testing.T) {
	s := newTasksSolo(t)
	for _, verb := range []string{"done", "cancel"} {
		root := tasksAdd(t, s.c, verb+" root")
		kid := tasksAdd(t, s.c, verb+" kid", "--parent", root)
		grand := tasksAdd(t, s.c, verb+" grandkid", "--parent", kid)
		closedKid := tasksAdd(t, s.c, verb+" already done", "--parent", root)
		s.c.MustLore("task", "done", closedKid, "--no-lesson")
		args := []string{"task", verb, root}
		if verb == "done" {
			args = append(args, "--no-lesson")
		}
		e := tasksJSONErr(t, s.c, args...)
		if e.Error.Code != "E_OPEN_CHILDREN" || !strings.Contains(e.Error.Message, kid) || strings.Contains(e.Error.Message, closedKid) || !strings.Contains(e.Error.Hint, "--children") {
			t.Fatalf("%s with open sub-tasks: E_OPEN_CHILDREN naming only the open one: %+v", verb, e)
		}
		if st := tasksShow(t, s.c, root).Status; st != "todo" {
			t.Fatalf("a refused %s closes nothing: %s", verb, st)
		}
		s.c.Lore("task", verb, root, "--children", "bogus").Refused(t)
		want := map[string]string{"done": "done", "cancel": "cancelled"}[verb]
		s.c.MustLore(append(args, "--children", verb)...)
		for _, id := range []string{root, kid, grand} {
			if st := tasksShow(t, s.c, id).Status; st != want {
				t.Fatalf("%s --children %s closes the whole subtree: %s is %s", verb, verb, id, st)
			}
		}
		if st := tasksShow(t, s.c, closedKid).Status; st != "done" {
			t.Fatalf("an already closed sub-task keeps its status: %s", st)
		}
		// Under a closed parent.
		s.c.Lore("task", "add", "late", "--parent", root).Fails(t, th.ExitRefused, "E_PARENT_CLOSED", "--reopen-parent")
		late := tasksAdd(t, s.c, "late", "--parent", root, "--reopen-parent")
		got := tasksShow(t, s.c, root)
		if got.Status != "in_progress" || got.CompletedAt != "" || got.CloseReason != "" {
			t.Fatalf("--reopen-parent reopens the parent to in_progress with no close fields: %+v", got)
		}
		if tasksShow(t, s.c, late).ParentID != root {
			t.Fatal("the late sub-task is under the reopened parent")
		}
	}
}

// A big tree: 201 sub-tasks from a file are over tasks.max_children 200
// (E_TREE_WIDTH, nothing added); 150 split in one batch are listed and
// rolled up; closing them all is over batch.preview_rows, so it asks
// first (E_NEEDS_CONFIRM, exit 26, nothing closed) and --yes closes the
// whole tree in one batch.
func TestTasksTree_BigSplit(t *testing.T) {
	s := newTasksSolo(t)
	root := tasksAdd(t, s.c, "big release")
	steps := func(name string, n int) string {
		var b strings.Builder
		for i := range n {
			b.WriteString("step " + strconv.Itoa(i+1) + "\n")
		}
		file := filepath.Join(s.w.Root, name)
		if err := os.WriteFile(file, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return file
	}
	s.c.Lore("task", "split", root, "--from", steps("wide.txt", 201)).Fails(t, th.ExitRefused, "E_TREE_WIDTH", "max_children 200", "lore policy set tasks.max_children")
	if got := len(tasksTree(t, s.c, root).Tree); got != 0 {
		t.Fatalf("a refused split adds nothing: %d sub-tasks", got)
	}
	const n = 150
	file := steps("steps.txt", n)
	s.c.MustLore("task", "split", root, "--from", file, "--dry-run").Says(t, "step 1", "step 150")
	if got := len(tasksTree(t, s.c, root).Tree); got != 0 {
		t.Fatalf("--dry-run writes nothing: %d sub-tasks", got)
	}
	s.c.MustLore("task", "split", root, "--from", file)
	v := tasksTree(t, s.c, root)
	if len(v.Tree) != n || v.Rollup.ChildrenTotal != n || v.Rollup.ChildrenDone != 0 {
		t.Fatalf("150 sub-tasks, rolled up: %d in tree, rollup %+v", len(v.Tree), v.Rollup)
	}
	for _, l := range v.Tree {
		if l.Depth != 1 {
			t.Fatalf("split adds direct children: depth %d", l.Depth)
		}
	}
	if got := len(tasksList(t, s.c)); got != n+1 {
		t.Fatalf("task list shows the parent and every sub-task: %d", got)
	}
	s.c.Lore("task", "done", root, "--children", "done", "--no-lesson").Fails(t, th.ExitNeedsConfirm, "E_NEEDS_CONFIRM", "batch.preview_rows", "--yes")
	if st := tasksShow(t, s.c, v.Tree[0].ID).Status; st != "todo" {
		t.Fatalf("an unconfirmed batch closes nothing: %s", st)
	}
	s.c.MustLore("task", "done", root, "--children", "done", "--no-lesson", "--yes").Says(t, strconv.Itoa(n)+" sub-task(s) done")
	v = tasksTree(t, s.c, root)
	if v.Status != "done" || v.Rollup.ChildrenDone != n || v.Rollup.LeavesDone != n {
		t.Fatalf("one batch closes all 150: %+v", v.Rollup)
	}
	if got := tasksList(t, s.c); len(got) != 0 {
		t.Fatalf("closed tasks leave the default list: %d left", len(got))
	}
}
