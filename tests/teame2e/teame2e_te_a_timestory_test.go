//go:build teame2e

package teame2e

// TIME_TRACKING_DESIGN build step 11 on real git with the real binary: the
// three-clone story. mira (alice, Northwind's own repo), tomas (dev1, his
// own clone) and tomas via codex (an agent of dev1, a third clone): start,
// claim, run, the agent dies mid-claim, tomas reclaims, both people log
// time by hand while offline, everyone merges, tomas finishes, the task
// lands, the live ref is compacted, the task moves to a store project and
// on into the client repo. After every step every clone that can see the
// task gives the same numbers in every time report.

import (
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teAStoryCaps is what the story needs beyond the crew.
var teAStoryCaps = []th.Cap{
	capTime, capLand, th.Cmd("task", "heartbeat"), th.Cmd("live", "compact"), th.Cmd("move"),
}

// teAReports are the time reports compared on every clone: the entries
// view (canon) and the per-person, per-source and per-day groupings.
var teAReports = [][]string{
	{"--group-by", "person"},
	{"--group-by", "source"},
	{"--group-by", "day", "--tz", "UTC"},
}

// teAReport is every time report of task id on clone c as one text.
func teAReport(t *testing.T, c *th.Clone, id string) string {
	t.Helper()
	out := []string{timeShow(t, c, id).canon(t)}
	for _, extra := range teAReports {
		r := c.Lore(append([]string{"task", "time", "show", id, "--json"}, extra...)...)
		if r.Code != th.ExitOK {
			t.Fatalf("task time show %v: %s", extra, r)
		}
		out = append(out, r.Stdout)
	}
	return strings.Join(out, "\n")
}

// teASameEverywhere syncs every clone at one clock (each actor's) and
// asserts they all give the same reports for id; it returns the view.
func teASameEverywhere(t *testing.T, step, id string, at time.Time, actors []*th.Actor, cs ...*th.Clone) timeView {
	t.Helper()
	for _, a := range actors {
		a.SetNow(at)
	}
	for _, c := range cs {
		c.Lore("sync")
	}
	first := teAReport(t, cs[0], id)
	for _, c := range cs[1:] {
		if got := teAReport(t, c, id); got != first {
			t.Fatalf("%s: every clone gives the same time reports:\n%s\n---\n%s", step, first, got)
		}
	}
	return timeShow(t, cs[0], id)
}

// teASources sums the not removed entries of v by source.
func teASources(v timeView) map[string]int {
	out := map[string]int{}
	for _, e := range v.Data.Entries {
		if e.DeletedAt == "" {
			out[e.Source] += e.Seconds
		}
	}
	return out
}

func TestTeA_TimeStory_ThreeClones(t *testing.T) {
	cr := newCrew(t, teAStoryCaps...)
	mira, miraC := cr.alice, cr.toolsC
	tomas := cr.dev1
	tomas.SetNow(t0)
	tomasC := tomas.Clone(cr.tools, "tools-tomas")
	tomasC.MustLore("task", "list")
	codex := startAgent(t, tomas, tomasC, "codex-1", "dev1")
	codex.SetNow(t0)
	codexC := codex.Clone(cr.tools, "tools-codex")
	codexC.MustLore("task", "list")
	people := []*th.Actor{mira, tomas, codex}
	all := []*th.Clone{miraC, tomasC, codexC}

	// Start: mira adds the task; codex claims it and runs one renewal.
	id := cr.task(t, "nightly export")
	codexC.MustLore("task", "list")
	codexC.MustLore("task", "claim", id).Says(t, "claimed")
	codex.SetNow(t0.Add(leaseRenew))
	codexC.MustLore("task", "heartbeat", id)
	tomas.SetNow(t0.Add(leaseRenew + time.Minute))
	tomasC.MustLore("task", "list")

	// The agent dies mid-claim; past ttl + grace tomas reclaims it himself.
	reclaimAt := t0.Add(leaseRenew + leaseTTL + leaseGrace + time.Minute)
	tomas.SetNow(reclaimAt)
	tomasC.MustLore("task", "claim", id).Says(t, "claimed")
	v := teASameEverywhere(t, "after the reclaim", id, reclaimAt, people, all...)
	if s := v.claimSpans(); len(s) != 1 || s[0].Seconds != int(leaseRenew.Seconds()) {
		t.Fatalf("the dead agent's span ends at its last heartbeat (%v): %s", leaseRenew, v.canon(t))
	}

	// Offline manual logs on two machines, then everyone merges.
	offAt := reclaimAt.Add(time.Minute)
	mira.SetNow(offAt)
	tomas.SetNow(offAt)
	miraC.MustLore("task", "list")
	mira.Offline()
	tomas.Offline()
	miraC.MustLore("task", "time", "log", id, "30m", "--note", "review call")
	tomas.SetNow(offAt.Add(time.Minute))
	tomasC.MustLore("task", "time", "log", id, "45m", "--note", "pairing")
	mira.Online()
	tomas.Online()
	mergeAt := offAt.Add(5 * time.Minute)
	v = teASameEverywhere(t, "after the offline logs merged", id, mergeAt, people, all...)
	if got := teASources(v)["manual"]; got != int((75 * time.Minute).Seconds()) {
		t.Fatalf("both offline manual logs survive the merge (75m): %s", v.canon(t))
	}

	// tomas keeps his lease alive and finishes: his claim span runs from
	// the reclaim to done.
	for _, at := range []time.Duration{15 * time.Minute, 30 * time.Minute} {
		tomas.SetNow(reclaimAt.Add(at))
		tomasC.MustLore("task", "heartbeat", id)
	}
	doneAt := reclaimAt.Add(40 * time.Minute)
	tomas.SetNow(doneAt)
	tomasC.MustLore("task", "list")
	tomasC.MustLore("task", "done", id, "--no-lesson")
	v = teASameEverywhere(t, "after done", id, doneAt, people, all...)
	claims := v.claimSpans()
	want := map[int]bool{int(leaseRenew.Seconds()): true, int((40 * time.Minute).Seconds()): true}
	if len(claims) != 2 || !want[claims[0].Seconds] || !want[claims[1].Seconds] || claims[0].Seconds == claims[1].Seconds {
		t.Fatalf("two claim spans: the agent's %v and tomas's 40m: %s", leaseRenew, v.canon(t))
	}
	before := v.canon(t)

	// Land, compact the live ref: the same numbers everywhere, unchanged.
	miraC.MustLore("land", "--all")
	miraC.MustLore("live", "compact")
	for _, step := range []string{"after land", "after live compact"} {
		if got := teASameEverywhere(t, step, id, doneAt.Add(time.Minute), people, all...).canon(t); got != before {
			t.Fatalf("%s the time is what it was at done:\n%s\n%s", step, before, got)
		}
	}

	// Move to a store project (same id): mira's acme/app checkout and
	// tomas's see the same numbers as before the move.
	miraC.MustLore("move", id, "--to", orgNorthwind+"/"+folderAcme, "--yes")
	if got := teASameEverywhere(t, "after the move to the store project", id, doneAt.Add(2*time.Minute), people, cr.aliceApp, cr.dev1App).canon(t); got != before {
		t.Fatalf("the move to another project keeps every entry:\n%s\n%s", before, got)
	}

	// Move on into the client repo with per-worker totals: every clone of
	// the client repo, the client's own included, reports the same effort.
	// The own repo's task list stays behind on the first hop (F-MOVE-1);
	// the move is no leak (12a955d8, BUG-MOVE-CLIENT-LEAK); the entries
	// stay behind and the carried totals are the effort (F-MOVE-3).
	cr.aliceApp.MustLore("move", id, "--to", "repo", "--with-time", "totals", "--yes")
	clientV := teASameEverywhere(t, "after the move to the client repo", id, doneAt.Add(3*time.Minute), append(people, cr.acmedev), cr.aliceApp, cr.dev1App, cr.acmeApp)
	if clientV.Data.Effort != v.Data.Effort {
		t.Fatalf("the client repo's totals give the same effort as before (%d): %s", v.Data.Effort, clientV.canon(t))
	}
}
