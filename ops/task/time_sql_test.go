package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/value"
	opssql "github.com/thesatellite-ai/lore/ops/sql"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// timeSQL runs sql.run and returns its rows as "a|b" lines.
func timeSQL(t *testing.T, h *opstest.H, q string) []string {
	t.Helper()
	in, err := json.Marshal(opssql.RunIn{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	var out opssql.RunOut
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opssql.OpRun, string(in)))), &out); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(out.Rows))
	for _, r := range out.Rows {
		parts := make([]string, 0, len(r.Cells))
		for _, c := range r.Cells {
			parts = append(parts, c.Value)
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	return lines
}

// TestTimeSpentSampleQueries runs every sample report of
// TIME_TRACKING_DESIGN 9 against a fixture (later 478, OV16): hours per
// owner per week, hours per client this month (the client column from
// the time.client_key metadata key), the per-task worker-kind share and
// the long spans; a task in the trash drops out of time_spent.
func TestTimeSpentSampleQueries(t *testing.T) {
	t.Setenv("LORE_HUMAN", "1")
	base := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, repoClient(false), func(o *wire.Options) { // time data lives in internal stores only (PC-LEAK-1)
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	mira := newActor(h, "Mira")
	codex := create(h, schema.TableActors, map[string]value.Value{
		"kind": text(t, schema.ActorKindAgent), "display_name": text(t, "codex"), "stable_key": text(t, "agent:codex"), "owner_actor_id": ref(t, mira),
	})
	acme := add(h, "acme work", `"meta":["client=acme"]`)
	beta := add(h, "beta work", `"meta":["client=beta"]`)
	plain := add(h, "no client", "")
	gone := add(h, "trashed", `"meta":["client=acme"]`)
	h.Must(opstask.OpTimeLog, `{"id":"`+acme+`","duration":"2h","at":"2026-11-02","by":"`+mira+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+beta+`","duration":"30m","at":"2026-11-02","by":"`+codex+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+gone+`","duration":"5h","at":"2026-11-02","by":"`+mira+`"}`)
	h.Must(opstask.OpTimeLog, `{"id":"`+acme+`","duration":"1h","at":"2026-10-20","by":"`+mira+`"}`) // last month
	// a status span of 11h on plain: over time.long_span (600 minutes).
	h.Must(opstask.OpStart, `{"id":"`+plain+`"}`)
	off.Store(int64(11 * time.Hour))
	h.Must(opstask.OpTimeStop, `{"id":"`+plain+`"}`)
	h.MustYes(opstask.OpDelete, `{"id":"`+gone+`"}`)

	// hours per person per ISO week (agents folded into their owner).
	weeks := timeSQL(t, h, `SELECT owner_label, strftime('%Y-%W', day) AS week, round(sum(owner_counted_seconds)/3600.0, 1) AS hours FROM time_spent GROUP BY 1, 2 ORDER BY 2, 1`)
	// week 44: mira 2h + codex 0.5h folded into mira; the 11h span is the
	// harness caller's (no actor row here, so no label).
	if want := []string{"Mira|2026-42|1", "|2026-44|11", "Mira|2026-44|2.5"}; !slices.Equal(weeks, want) {
		t.Fatalf("per week: %q", weeks)
	}
	// hours per client this month: the trashed acme task is not counted.
	clients := timeSQL(t, h, `SELECT coalesce(client, '(none)') AS client, round(sum(counted_seconds)/3600.0, 1) AS hours FROM time_spent WHERE day >= '2026-11-01' GROUP BY 1 ORDER BY 1`)
	if want := []string{"(none)|11", "acme|2", "beta|0.5"}; !slices.Equal(clients, want) {
		t.Fatalf("per client: %q, want %q", clients, want)
	}
	// agent vs person share per task.
	share := timeSQL(t, h, `SELECT task_id, worker_kind, round(sum(counted_seconds)/3600.0, 2) AS hours FROM time_spent WHERE task_id IN ('`+acme+`', '`+beta+`') GROUP BY 1, 2 ORDER BY 1`)
	want := []string{acme + "|human|3", beta + "|agent|0.5"}
	slices.Sort(want) // ORDER BY task_id
	if !slices.Equal(share, want) {
		t.Fatalf("share: %q, want %q", share, want)
	}
	// spans flagged long: only the 11h status span.
	long := timeSQL(t, h, `SELECT task_id, worker_label, start_at, end_at, seconds/3600.0 AS h FROM time_spent WHERE flags LIKE '%long_span%'`)
	if len(long) != 1 || !strings.HasPrefix(long[0], plain+"|") || !strings.HasSuffix(long[0], "|11") {
		t.Fatalf("long spans: %q", long)
	}
}

// TestMoveWithTimeIntoClientRepo is the lore move --with-time smoke
// (later 478, TQ3) with a client repo and its org store: by default a task
// moved into the client repo keeps its time back (no entries, no totals,
// a "kept back" line); time.client_share = totals on the client repo
// writes per-worker totals and says the policy chose it; --with-time
// none overrides the policy; the entries never reach the client row.
func TestMoveWithTimeIntoClientRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, policy, withTime string
		totals                 bool
		line                   string
	}{
		{"default none", "", "", false, "kept back: time (1 entries)"},
		{"policy totals", "totals", "", true, "time: totals only (policy time.client_share = totals)"},
		{"flag none over policy", "totals", "none", false, "kept back: time (1 entries)"},
		{"flag totals", "", "totals", true, "time: totals only (Mira 2h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := opstest.New(t, repoClient(true), withOrg(t))
			if c.policy != "" {
				h.MustYes("policy.set", `{"key":"time.client_share","value":"`+c.policy+`"}`)
			}
			mira := create(h, schema.TableActors, map[string]value.Value{
				"kind": text(t, schema.ActorKindHuman), "display_name": text(t, "Mira"), "stable_key": text(t, "human:mira"),
			})
			// the task's list is the client repo's (an org list would be an
			// internal pointer the move refuses, F15).
			list := h.ID(h.Must("tasklist.add", `{"title":"client list","body":"b"}`))
			id := addID(h, h.Must(opstask.OpAdd, `{"title":"billable","to":"`+orgStore+`","tasklist":"`+list+`"}`))
			h.Must(opstask.OpTimeLog, `{"id":"`+id+`","duration":"2h","at":"2026-11-02","by":"`+mira+`"}`)
			in := `{"id":"` + id + `","to":"repo"`
			if c.withTime != "" {
				in += `,"with_time":"` + c.withTime + `"`
			}
			got := h.JSON(h.MustYes("row.move", in+"}"))
			if !strings.Contains(got, c.line) {
				t.Fatalf("move output lacks %q: %s", c.line, got)
			}
			src, r := get(h, schema.TableTasks, id)
			if src != "repo" {
				t.Fatalf("task is in %s", src)
			}
			if len(opstask.TimeEntries(r)) != 0 || len(opstask.TimeOpen(r)) != 0 {
				t.Fatal("time entries reached the client repo")
			}
			tt := field(r, "time_totals")
			if c.totals != (tt != "") || (c.totals && !strings.Contains(tt, "7200")) {
				t.Fatalf("time_totals %q, want totals %v", tt, c.totals)
			}
		})
	}
}
