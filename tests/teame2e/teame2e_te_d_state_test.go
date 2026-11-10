//go:build teame2e

package teame2e

// later.md 229 (r3 STATE e2e) on cmd/lore: the task list STATE column a
// person sees in her own clone is built from what agents did in theirs:
// a live claim, a claim past its lease, a pending gate, a done task whose
// commit is not on main yet, and the same task once it is merged.

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teDStateLine is the `task list --all` line naming id.
func teDStateLine(t *testing.T, list th.Result, id string) string {
	t.Helper()
	for _, l := range strings.Split(list.Stdout, "\n") {
		if strings.Contains(l, id) {
			return l
		}
	}
	t.Fatalf("task list --all has no line for %s:\n%s", id, list)
	return ""
}

// TestTeD_StateColumnAcrossClones (LORE_TEAM_SPEC §3.5 STATE, r3).
func TestTeD_StateColumnAcrossClones(t *testing.T) {
	cr := newCrew(t, th.Cmd("gate", "request"), th.Flag("commit", "task", "done"), th.Flag("all", "task", "list"))
	working := cr.task(t, "working task")
	expiring := cr.task(t, "expiring task")
	gated := cr.task(t, "gated task")
	shipped := cr.task(t, "shipped task")
	plain := cr.task(t, "nobody touched")

	a1, w1 := cr.agent(t, "w-1")
	w1.MustLore("task", "claim", working)
	_, w2 := cr.agent(t, "w-2")
	w2.MustLore("task", "claim", expiring)
	cr.toolsC.MustLore("gate", "request", gated)

	_, w3 := cr.agent(t, "w-3")
	w3.MustLore("task", "claim", shipped)
	w3.Git("checkout", "-q", "-b", "agent/w-3/ship")
	w3.WriteFile("ship.go", "package tools\n")
	w3.CommitAll("ship it")
	sha := w3.Head()
	w3.Git("push", "-q", "-u", "origin", "agent/w-3/ship")
	w3.MustLore("task", "done", shipped, "--commit", sha)

	list := cr.toolsC.MustLore("task", "list", "--all")
	cases := []struct {
		id, want string
	}{
		{working, "working: "},
		{expiring, "working: "},
		{gated, "gate: "},
		{shipped, "done · not merged yet"},
	}
	for _, c := range cases {
		if l := teDStateLine(t, list, c.id); !strings.Contains(l, c.want) {
			t.Errorf("%s: want %q in %q", c.id, c.want, l)
		}
	}
	if l := teDStateLine(t, list, plain); strings.Contains(l, "working") || strings.Contains(l, "gate") || strings.Contains(l, "EXPIRED") || strings.Contains(l, "done ·") {
		t.Errorf("an untouched task has no STATE: %q", l)
	}

	// The commit reaches main: alice's next look (still at t0) records
	// the merge.
	cr.toolsC.Git("pull", "-q")
	cr.toolsC.Git("merge", "-q", "--no-ff", "--no-edit", "origin/agent/w-3/ship")
	cr.toolsC.AdminPush("origin", "main")
	list = cr.toolsC.MustLore("task", "list", "--all")
	if l := teDStateLine(t, list, shipped); !strings.Contains(l, "done · merged ") {
		t.Errorf("merged: %q", l)
	}

	// w-1 keeps renewing, w-2 went silent; alice looks again just past
	// w-2's lease and grace (a reader shows EXPIRED only past the grace).
	a1.SetNow(t0.Add(3 * leaseRenew))
	w1.MustLore("task", "heartbeat", working)
	cr.alice.SetNow(t0.Add(leaseTTL + leaseGrace + time.Minute))
	list = cr.toolsC.MustLore("task", "list", "--all")
	cases = []struct {
		id, want string
	}{
		{working, "working: "},
		{expiring, "EXPIRED"},
		{gated, "gate: "},
		{shipped, "done · merged "},
	}
	for _, c := range cases {
		if l := teDStateLine(t, list, c.id); !strings.Contains(l, c.want) {
			t.Errorf("%s: want %q in %q", c.id, c.want, l)
		}
	}
	// STATE is human output only: --json carries no STATE text.
	cr.toolsC.MustLore("task", "list", "--all", "--json").Never(t, "working: ", "not merged yet", "EXPIRED")
}
