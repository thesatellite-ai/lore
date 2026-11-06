//go:build teame2e

package main

// TASK_SYSTEM_DESIGN 6.13 and 6.15 end to end on real git with the real
// binary: two developers change the sub-task tree offline, both sync, and
// both clones must show the same tree and the same `lore task tree check`
// findings; the check repairs nothing on read, and `--fix` repairs only the
// parent cycle, which the other clone then sees after its sync.

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	th "saas/cmd/cli/internal/teamharness"
)

// teBTreeFinding is one finding of `lore task tree check --json`
// (ops/task TreeFinding).
type teBTreeFinding struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Parent  string `json:"parent"`
	Message string `json:"message"`
	Fix     string `json:"fix"`
	Fixed   bool   `json:"fixed"`
}

// teBTreeCheck is the whole `lore task tree check --json` document (no
// envelope: the result object itself).
type teBTreeCheck struct {
	Tasks    int              `json:"tasks"`
	Findings []teBTreeFinding `json:"findings"`
	Fixed    int              `json:"fixed"`
}

// teBTreeCheckOn runs the tree check on c (extra: --fix).
func teBTreeCheckOn(t *testing.T, c *th.Clone, extra ...string) teBTreeCheck {
	t.Helper()
	r := c.MustLore(append([]string{"task", "tree", "check", "--json"}, extra...)...)
	var v teBTreeCheck
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("task tree check --json: %v\n%s", err, r)
	}
	return v
}

// canon is a stable text form of the findings (kind, id, parent), so two
// clones can be compared; messages carry the same ids.
func (v teBTreeCheck) canon() string {
	var parts []string
	for _, f := range v.Findings {
		parts = append(parts, f.Kind+":"+f.ID+"<"+f.Parent)
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}

// of returns the findings of one kind.
func (v teBTreeCheck) of(kind string) []teBTreeFinding {
	var out []teBTreeFinding
	for _, f := range v.Findings {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// sharedUnder returns the shared_rank findings among parent's children
// ("" = top level).
func (v teBTreeCheck) sharedUnder(parent string) []teBTreeFinding {
	var out []teBTreeFinding
	for _, f := range v.of("shared_rank") {
		if f.Parent == parent {
			out = append(out, f)
		}
	}
	return out
}

// teBTopIDs is `lore task list --top --all --json` as ids.
func teBTopIDs(t *testing.T, c *th.Clone) []string {
	t.Helper()
	l := th.DecodeJSON[taskListed](t, c.Lore("task", "list", "--top", "--all", "--json"))
	var out []string
	for _, d := range l.Data {
		out = append(out, d.ID)
	}
	return out
}

// teBParentOf reads a task's stored parent_id on c ("" = top level).
func teBParentOf(t *testing.T, c *th.Clone, id string) string {
	t.Helper()
	return th.DecodeJSON[struct {
		Data struct {
			ParentID string `json:"parent_id"`
		} `json:"data"`
	}](t, c.Lore("task", "show", id, "--json")).Data.ParentID
}

// 6.13 merge matrix, two clones (rows 1, 3, 4, 5 and 6 of the table): every
// row file merges on its own, so the merged tree has a cycle (X under P on
// one side, P under X on the other), a child of a parent trashed on the
// other side, an open child of a parent closed on the other side, a subtree
// pushed over tasks.max_depth (default 4) and two siblings sharing a rank.
// Both clones show the same findings and the same top level (the smallest
// id of the cycle); the check writes nothing; --fix writes only the cycle's
// shown result, and the other clone sees it after its sync.
func TestTeB_TreeMergeMatrixTwoClones(t *testing.T) {
	rt := newRepoTeam(t, th.Cmd("task", "tree", "check"), th.Flag("parent", "task", "add"), th.Flag("top", "task", "list"))
	rt.dev1.SetNow(t0)
	rt.dev2.SetNow(t0)
	c1, c2 := rt.c1, rt.c2
	x := rt.addTask(t, c1, "cycle x")
	p := rt.addTask(t, c1, "cycle p")
	q := rt.addTask(t, c1, "rank parent")
	rt.addTask(t, c1, "rank first", "--parent", q)
	r := rt.addTask(t, c1, "parent trashed on one side")
	s := rt.addTask(t, c1, "parent closed on one side")
	a := rt.addTask(t, c1, "depth a")
	b := rt.addTask(t, c1, "depth b", "--parent", a)
	cc := rt.addTask(t, c1, "depth c", "--parent", b)
	e := rt.addTask(t, c1, "depth e")
	before := teBTreeCheckOn(t, c1)
	if len(before.Findings) != 0 {
		t.Fatalf("premise: a clean tree before the round: %+v", before.Findings)
	}

	var k1, k2, rChild, sChild, d string
	rt.offlineRound(t, t0.Add(time.Hour), func() {
		c1.MustLore("task", "edit", x, "--parent", p)
		k1 = rt.addTask(t, c1, "rank dev1", "--parent", q)
		c1.MustLore("task", "delete", r, "--reason", "not needed", "--yes")
		c1.MustLore("task", "done", s, "--no-lesson")
		d = rt.addTask(t, c1, "depth d", "--parent", cc) // depth 4: allowed
	}, func() {
		c2.MustLore("task", "edit", p, "--parent", x)
		k2 = rt.addTask(t, c2, "rank dev2", "--parent", q)
		rChild = rt.addTask(t, c2, "child of the trashed", "--parent", r)
		sChild = rt.addTask(t, c2, "child of the closed", "--parent", s)
		c2.MustLore("task", "edit", a, "--parent", e) // c at depth 4: allowed
	})

	liveBefore := rt.app.Rev(refLive)
	v1, v2 := teBTreeCheckOn(t, c1), teBTreeCheckOn(t, c2)
	if v1.canon() != v2.canon() {
		t.Fatalf("both clones report the same findings after the merge:\ndev1 %s\ndev2 %s", v1.canon(), v2.canon())
	}
	top := min(x, p)
	cyc := v1.of("parent_cycle")
	if len(cyc) != 1 || cyc[0].ID != top {
		t.Fatalf("one parent_cycle finding whose top is the smallest id %s: %+v\n%s", top, cyc, v1.canon())
	}
	if o := v1.of("orphan"); len(o) != 1 || o[0].ID != rChild || o[0].Parent != r || !strings.Contains(o[0].Message, "trash") {
		t.Fatalf("the child of the trashed parent is an orphan whose parent is in the trash: %+v", o)
	}
	if pc := v1.of("parent_closed"); len(pc) != 1 || pc[0].ID != sChild || pc[0].Parent != s {
		t.Fatalf("the open child of the closed parent is parent_closed: %+v", pc)
	}
	if od := v1.of("over_depth"); len(od) != 1 || od[0].ID != d {
		t.Fatalf("the merge pushes %s to depth 5 (over the default 4): %+v", d, od)
	}
	// Two shared ranks: q's children added on both sides, and at top level
	// the cycle's top and the orphan (each was its old parent's first child,
	// rank a1, and both are now shown at top level).
	sr := v1.sharedUnder(q)
	if len(sr) != 1 || !strings.Contains(sr[0].Message, k1) || !strings.Contains(sr[0].Message, k2) {
		t.Fatalf("the two siblings added on both sides share a rank: %+v", v1.of("shared_rank"))
	}
	if root := v1.sharedUnder(""); len(root) != 1 || !strings.Contains(root[0].Message, top) || !strings.Contains(root[0].Message, rChild) {
		t.Fatalf("at top level the cycle's top and the orphan share rank a1: %+v", v1.of("shared_rank"))
	}
	if rt.app.Rev(refLive) != liveBefore {
		t.Fatal("a tree check without --fix writes nothing (no repair on read)")
	}
	for _, c := range []*th.Clone{c1, c2} {
		tops := teBTopIDs(t, c)
		if !slices.Contains(tops, top) || slices.Contains(tops, max(x, p)) {
			t.Fatalf("every clone shows the cycle's smallest id %s at top level and not the other: %v", top, tops)
		}
		if !slices.Contains(tops, rChild) {
			t.Fatalf("the orphan is shown at top level: %v", tops)
		}
		if got := teBParentOf(t, c, top); got != "" {
			t.Fatalf("task show reads the cycle's top as top level, as every view does: parent %q", got)
		}
	}
	for id, want := range map[string]string{x: p, p: x} {
		if row, ok := rt.liveTask(t, id); !ok || rawString(t, row, "parent_id") != want {
			t.Fatalf("before --fix the stored parent_id of %s stays as merged (%s): %v", id, want, row.Raw["parent_id"])
		}
	}

	fixed := teBTreeCheckOn(t, c1, "--fix")
	if fixed.Fixed != 1 || len(fixed.of("parent_cycle")) != 1 || !fixed.of("parent_cycle")[0].Fixed {
		t.Fatalf("--fix repairs the one cycle: %+v", fixed)
	}
	c1.MustLore("sync")
	c2.MustLore("sync")
	after := teBTreeCheckOn(t, c2)
	if len(after.of("parent_cycle")) != 0 {
		t.Fatalf("the other clone sees the repaired cycle after its sync: %s", after.canon())
	}
	for _, kind := range []string{"orphan", "parent_closed", "over_depth"} {
		if len(after.of(kind)) != 1 {
			t.Fatalf("--fix leaves the %s finding alone (report only): %s", kind, after.canon())
		}
	}
	if len(after.sharedUnder(q)) != 1 || len(after.sharedUnder("")) != 1 {
		t.Fatalf("--fix leaves both shared ranks alone: %s", after.canon())
	}
	if teBParentOf(t, c2, top) != "" || teBParentOf(t, c2, max(x, p)) != top {
		t.Fatalf("the fix writes the shown result: %s top level, the other under it", top)
	}
	if row, ok := rt.liveTask(t, top); !ok || rawString(t, row, "parent_id") != "" {
		t.Fatalf("the shared row of %s has no parent_id after --fix: %v", top, row.Raw["parent_id"])
	}
}
