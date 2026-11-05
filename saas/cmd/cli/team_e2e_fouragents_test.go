//go:build teame2e

package main

// TASK_SYSTEM_DESIGN 21.3: sub-tasks end to end with four agents and one
// person on real git and the real binary: split while holding (6.9), two
// agents racing for one child (exactly one wins each), a coordinator lease
// that does not block children, parent auto-close off/suggest/on (6.7,
// TSQ2), a cancel cascade that fences a child's holder (6.9), and the
// `next` hint in --json run as is.

import (
	"encoding/json"
	"strings"
	"testing"

	th "saas/cmd/cli/internal/teamharness"
)

// subtaskCaps is what the sub-task e2e tests need.
var subtaskCaps = []th.Cap{th.Cmd("task", "split"), th.Flag("children", "task", "cancel"), th.Cmd("policy", "set")}

// splitIDs splits parent on c into titles and returns the new child ids
// in order. `task split --json` prints the result object; an enveloped
// {"data": …} result is read the same way.
func splitIDs(t *testing.T, c *th.Clone, parent string, titles ...string) (th.Result, []string) {
	t.Helper()
	r := c.MustLore(append(append([]string{"task", "split", parent}, titles...), "--json")...)
	type added struct {
		Added []struct {
			ID string `json:"id"`
		} `json:"added"`
	}
	var v struct {
		added
		Data *added `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("split --json: %v\n%s", err, r)
	}
	a := v.added
	if v.Data != nil {
		a = *v.Data
	}
	var ids []string
	for _, x := range a.Added {
		ids = append(ids, x.ID)
	}
	if len(ids) != len(titles) {
		t.Fatalf("split adds %d children, got %v: %s", len(titles), ids, r)
	}
	return r, ids
}

// subtaskStatus reads a task's status on c.
func subtaskStatus(t *testing.T, c *th.Clone, id string) string {
	t.Helper()
	return th.DecodeJSON[struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}](t, c.Lore("task", "show", id, "--json")).Data.Status
}

// doneNext is the part of `task done --json` the auto-close rule fills
// (6.7: the result object itself, no envelope).
type doneNext struct {
	Parent *struct {
		ID           string `json:"id"`
		OpenChildren int    `json:"open_children"`
	} `json:"parent"`
	Next *struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Why     string   `json:"why"`
	} `json:"next"`
}

// bugCancelFence: `task cancel <parent> --children cancel` cancels a
// claimed child but leaves its claim_epoch as it was (1 -> 1), so the
// holder's next fenced write (`task done`) succeeds on the cancelled task
// instead of failing E_CANCELLED (TASK_SYSTEM 6.9). Found by a-task-e2e
// (PASS3 queue #10).
var bugCancelFence = th.Bug{ID: "BUG-CANCEL-FENCE", Owner: "main (ops/task cancel cascade)", Open: true}

// bugLandSession: when an agent closes a task it claimed in a client repo,
// the landing of the row is blocked E_LEAK_BLOCKED for "an agent's tool
// session id" in the row: the row the claim writers build carries the
// holder's session, which the client leak guard refuses, so the task stays
// on the live ref. Found by a-task-e2e (PASS3 queue #10).
var bugLandSession = th.Bug{ID: "BUG-LAND-SESSION", Owner: "main (claim writers / leak guard)", Open: true}

// 6.9 + 21.3: w1 holds a task and splits it: the claim stays on the parent
// as a coordinator lease; w2 and w3 race for the first child and exactly
// one wins; w4 takes the second child although w1 coordinates the parent;
// the last child's done --json carries a `next` that, run as is, closes
// the parent (tasks.parent_autoclose: suggest, the default).
func TestTask_FourAgents_SplitWhileHoldingRaceChildrenSuggestClose(t *testing.T) {
	cr := newCrew(t, subtaskCaps...)
	p := cr.task(t, "invoices export")
	w1, c1 := cr.agent(t, "w-1")
	_, c2 := cr.agent(t, "w-2")
	_, c3 := cr.agent(t, "w-3")
	_, c4 := cr.agent(t, "w-4")

	c1.MustLore("task", "claim", p).Says(t, "claimed")
	sr, kids := splitIDs(t, c1, p, "export query", "download button")
	if sig, ok := cr.claim(t, p); !ok || sig.Session != w1.Env(th.EnvLoreSession) {
		t.Fatalf("after the split w1 still holds the parent (a coordinator lease): %+v found=%v\n%s", sig, ok, sr)
	}

	for _, c := range []*th.Clone{c2, c3, c4} {
		c.MustLore("task", "list")
	}
	rs := cr.w.Simultaneously(c2.Prepare("task", "claim", kids[0]), c3.Prepare("task", "claim", kids[0]))
	wins := 0
	var winner *th.Clone
	for i, r := range rs {
		switch r.Code {
		case th.ExitOK:
			wins++
			winner = []*th.Clone{c2, c3}[i]
		case th.ExitContended:
		default:
			t.Fatalf("a lost child race exits %d (E_CONTENDED): %s", th.ExitContended, r)
		}
	}
	if wins != 1 {
		t.Fatalf("exactly one agent wins the child: %d\n%s\n%s", wins, rs[0], rs[1])
	}
	c4.MustLore("task", "claim", kids[1]).Says(t, "claimed")
	if sig, ok := cr.claim(t, p); !ok || sig.Session != w1.Env(th.EnvLoreSession) {
		t.Fatalf("claiming children leaves the coordinator lease alone: %+v found=%v", sig, ok)
	}

	winner.MustLore("task", "done", kids[0], "--no-lesson")
	c4.MustLore("task", "list")
	dr := c4.Lore("task", "done", kids[1], "--no-lesson", "--json")
	d := th.DecodeJSON[doneNext](t, dr)
	if d.Parent == nil || d.Parent.ID != p || d.Parent.OpenChildren != 0 {
		t.Fatalf("the last child's done --json names its parent with no open children: %s", dr)
	}
	if d.Next == nil || d.Next.Command != "lore" {
		t.Fatalf("the last child's done --json carries the parent close in next: %s", dr)
	}
	want := []string{"task", "done", p}
	if len(d.Next.Args) < len(want) || d.Next.Args[0] != want[0] || d.Next.Args[1] != want[1] || d.Next.Args[2] != want[2] {
		t.Fatalf("next closes the parent: %v, want %v…", d.Next.Args, want)
	}
	if s := subtaskStatus(t, c4, p); s == "done" {
		t.Fatalf("suggest leaves the parent open: %s", s)
	}
	c1.MustLore("task", "list")
	c1.MustLore(d.Next.Args...)
	if s := subtaskStatus(t, cr.toolsC, p); s != "done" {
		t.Fatalf("the next hint, run as is by the coordinator, closes the parent: %s", s)
	}
	bugLandSession.PendingOn(t, dr, "E_LEAK_BLOCKED", "tool session id")
	dr.Never(t, "E_LEAK_BLOCKED")
}

// 6.7 + TSQ2: tasks.parent_autoclose off says nothing and leaves the parent
// open; on closes it in the same write; suggest prints the command.
func TestTask_ParentAutocloseOffSuggestOn(t *testing.T) {
	cr := newCrew(t, subtaskCaps...)
	c := cr.toolsC
	for _, tc := range []struct {
		mode      string
		closes    bool
		hintShown bool
	}{
		{"off", false, false},
		{"suggest", false, true},
		{"on", true, false},
	} {
		c.MustLore("policy", "set", "tasks.parent_autoclose", tc.mode, "--to", "repo", "--yes")
		p := cr.task(t, "parent "+tc.mode)
		_, kids := splitIDs(t, c, p, "only child")
		r := c.MustLore("task", "done", kids[0], "--no-lesson")
		hint := "lore task done " + p
		if got := strings.Contains(r.Out(), hint); got != tc.hintShown {
			t.Fatalf("%s: the close hint shown=%v, want %v:\n%s", tc.mode, got, tc.hintShown, r)
		}
		if s := subtaskStatus(t, c, p); (s == "done") != tc.closes {
			t.Fatalf("%s: the parent is %q after its last child closed (closes=%v):\n%s", tc.mode, s, tc.closes, r)
		}
	}
}

// 6.9: the person cancels a parent with --children cancel while an agent
// holds a child: the cascade fences the holder, whose next write fails
// E_CANCELLED (exit 13) and changes nothing.
func TestTask_CancelCascadeFencesChildHolder(t *testing.T) {
	cr := newCrew(t, subtaskCaps...)
	p := cr.task(t, "to be cancelled")
	_, kids := splitIDs(t, cr.toolsC, p, "held child", "idle child")
	_, wc := cr.agent(t, "w-1")
	wc.MustLore("task", "claim", kids[0]).Says(t, "claimed")
	cr.toolsC.MustLore("task", "list")
	cr.toolsC.MustLore("task", "cancel", p, "--children", "cancel", "--yes")
	if s := subtaskStatus(t, cr.toolsC, kids[0]); s != "cancelled" {
		t.Fatalf("the cascade cancels the held child: %s", s)
	}
	r := wc.Lore("task", "done", kids[0], "--no-lesson")
	bugCancelFence.PendingIf(t, r.Code == th.ExitOK, "the fenced holder's done succeeded on a cancelled child")
	r.Fails(t, th.ExitCancelled, "E_CANCELLED")
	if s := subtaskStatus(t, cr.toolsC, kids[0]); s != "cancelled" {
		t.Fatalf("the fenced holder's done changes nothing: %s", s)
	}
}
