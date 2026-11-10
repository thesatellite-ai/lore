package task_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// historyOf is a history JSON list of {id, at, field, to} entries.
func historyOf(t *testing.T, entries ...[3]string) value.Value {
	t.Helper()
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = `{"id":"` + e[0] + `","at":"` + e[1] + `","field":"status","to":"` + e[2] + `"}`
	}
	v, err := value.JSONOf([]byte("[" + strings.Join(parts, ",") + "]"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestHistoryUnionOfConcurrentWrites (TASK 17.3, OV22): two clones that
// each appended history entries to the same task merge to the union of
// both (append-only entries by id: nothing is lost, nothing is a
// conflict), whichever side is newer.
func TestHistoryUnionOfConcurrentWrites(t *testing.T) {
	t.Parallel()
	a := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	b := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	p := merge.PolicyFor(tasksDef(t))
	const h0, h1, h2, h3 = "the_00000000000000000000000000000001", "the_00000000000000000000000000000002", "the_00000000000000000000000000000003", "the_00000000000000000000000000000004"
	base := taskSide(t, 0, a, map[string]value.Value{schema.FieldTaskHistory: historyOf(t, [3]string{h0, "2026-11-05T09:00:00Z", "todo"})})
	ours := taskSide(t, 10, a, map[string]value.Value{schema.FieldTaskHistory: historyOf(t,
		[3]string{h0, "2026-11-05T09:00:00Z", "todo"}, [3]string{h1, "2026-11-05T10:00:00Z", "in_progress"})})
	theirs := taskSide(t, 20, b, map[string]value.Value{schema.FieldTaskHistory: historyOf(t,
		[3]string{h0, "2026-11-05T09:00:00Z", "todo"}, [3]string{h2, "2026-11-05T10:30:00Z", "blocked"}, [3]string{h3, "2026-11-05T11:00:00Z", "todo"})})
	for _, swap := range []bool{false, true} {
		o, th := ours, theirs
		if swap {
			o, th = theirs, ours
		}
		res, err := merge.Rows(p, base, o, th)
		if err != nil {
			t.Fatal(err)
		}
		got := res.Row.Fields[schema.FieldTaskHistory].String()
		for _, id := range []string{h0, h1, h2, h3} {
			if strings.Count(got, id) != 1 {
				t.Fatalf("swap=%v: entry %s appears %d times in %s", swap, id, strings.Count(got, id), got)
			}
		}
		if !res.Clean() {
			t.Fatalf("history merge conflicted: %v", res.Conflicts)
		}
	}
}

// TestHistoryClientProjection (TASK 17.3): in a client repo the history of
// a task names who changed it by actor id only (the client projection of
// actors): no session id and no other internal detail reaches the rows
// or task history --json.
func TestHistoryClientProjection(t *testing.T) {
	t.Parallel()
	h := opstest.New(t, repoClient(true))
	ann := newActor(h, "Ann")
	id := add(h, "client task", "")
	h.Must(opstask.OpStart, `{"id":"`+id+`"}`)
	h.Must(opstask.OpEdit, `{"id":"`+id+`","priority":"high","assign":["`+ann+`"]}`)
	_, r := get(h, schema.TableTasks, id)
	entries := opstask.History(r)
	if len(entries) < 3 {
		t.Fatalf("history: %+v", entries)
	}
	for _, e := range entries {
		if e.By != "" && !strings.HasPrefix(e.By, "act_") {
			t.Fatalf("history by %q is not an actor id", e.By)
		}
	}
	js := h.JSON(h.Must(opstask.OpHistory, `{"id":"`+id+`"}`))
	if strings.Contains(js, "ses_") || strings.Contains(field(r, schema.FieldTaskHistory), "ses_") {
		t.Fatalf("a session id in the client history: %s", js)
	}
}

// TestStaleExclusions is the stale exclusions table (TASK 18.5): an open
// task untouched for --days is stale unless it is claimed, in an active
// mission, due in the future, has an open sub-task or recurs; a closed
// task, a recently changed one and a task in a paused mission follow the
// rule as written; --close --dry-run writes nothing and --close cancels
// exactly the stale ones in one batch with close_reason stale.
func TestStaleExclusions(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	var off atomic.Int64
	h := opstest.New(t, func(o *wire.Options) {
		o.Wall = func() time.Time { return base.Add(time.Duration(off.Load())) }
	})
	stale := add(h, "plain old", "")
	claimed := add(h, "claimed", "")
	activeM := h.ID(h.Must("mission.add", `{"title":"active m"}`))
	h.Must("mission.edit", `{"id":"`+activeM+`","status":"active"}`)
	pausedM := h.ID(h.Must("mission.add", `{"title":"paused m"}`))
	h.Must("mission.edit", `{"id":"`+pausedM+`","status":"paused"}`)
	inActive := add(h, "in active mission", `"mission":"`+activeM+`"`)
	inPaused := add(h, "in paused mission", `"mission":"`+pausedM+`"`)
	futureDue := add(h, "due later", `"due":"2027-01-01"`)
	pastDue := add(h, "due long ago", `"due":"2026-01-06"`)
	parent := add(h, "has open child", "")
	child := add(h, "open child", `"parent":"`+parent+`"`)
	recurring := add(h, "recurs", `"recur":"every week","due":"2026-01-06"`)
	closed := add(h, "done", "")
	h.Must(opstask.OpDone, `{"id":"`+closed+`","no_lesson":true}`)
	off.Store(int64(61 * 24 * time.Hour))
	add(h, "fresh", "") // changed today: never stale

	ft := &fakeTeam{board: &fakeBoard{claims: map[string]claim{claimed: {holder: "act_01936f0000007a1b8c2d3e4f5a6b7c8d", active: true}}}}
	ctx := opstask.WithTeam(context.Background(), ft)
	ids := func(res string) []string {
		t.Helper()
		var out struct {
			Tasks []struct {
				ID string `json:"id"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(res), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, b := range out.Tasks {
			got = append(got, b.ID)
		}
		slices.Sort(got)
		return got
	}
	want := []string{stale, inPaused, pastDue, child}
	slices.Sort(want)
	if got := ids(h.JSON(mustCtx(h, ctx, opstask.OpStale, `{"days":60}`))); !slices.Equal(got, want) {
		t.Fatalf("stale: got %v want %v (excluded: claimed %s, active mission %s, future due %s, open child parent %s, recurring %s, done %s)",
			got, want, claimed, inActive, futureDue, parent, recurring, closed)
	}
	// a longer window than the age finds nothing.
	if got := ids(h.JSON(mustCtx(h, ctx, opstask.OpStale, `{"days":90}`))); len(got) != 0 {
		t.Fatalf("--days 90: %v", got)
	}
	if _, err := runCtx(h, ctx, opstask.OpStale, `{"days":60,"dry_run":true}`); err == nil {
		t.Fatal("--dry-run without --close accepted")
	}
	mustCtx(h, ctx, opstask.OpStale, `{"days":60,"close":true,"dry_run":true}`)
	if _, r := get(h, schema.TableTasks, stale); field(r, "status") == schema.TaskStatusCancelled {
		t.Fatal("--dry-run cancelled")
	}
	res := h.JSON(mustCtx(h, opstask.WithTeam(context.Background(), ft), opstask.OpStale, `{"days":60,"close":true}`))
	if !strings.Contains(res, "bat_") {
		t.Fatalf("close: %s", res)
	}
	for _, id := range want {
		if _, r := get(h, schema.TableTasks, id); field(r, "status") != schema.TaskStatusCancelled || field(r, "close_reason") != schema.TaskCloseStale {
			t.Fatalf("%s: status %s reason %s", id, field(r, "status"), field(r, "close_reason"))
		}
	}
	for _, id := range []string{claimed, inActive, futureDue, parent, recurring} {
		if _, r := get(h, schema.TableTasks, id); field(r, "status") == schema.TaskStatusCancelled {
			t.Fatalf("excluded %s was cancelled", id)
		}
	}
}

// TestBoardLayoutSnapshot reads the board's text as behaviour (TASK 16.3,
// G-TASK-1): one block per group, a header "<key> (<n>)" and one
// "  <id>  <title>" line per card, every status group present in the
// closed order even when empty (todo, in_progress, done, cancelled,
// blocked), priority groups highest first; the layout is one column, so a
// 40-column terminal wraps a long title but never reorders or drops a
// card; --json carries the same groups with empty task lists.
func TestBoardLayoutSnapshot(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	add(h, "Release note", `"priority":"high"`)
	b := add(h, "Download button with a title much longer than forty columns", `"priority":"low"`)
	h.Must(opstask.OpStart, `{"id":"`+b+`"}`)
	got := lines(opstest.Text(h.Must(opstask.OpBoard, `{}`)))
	want := "todo (1)\n  tsk_ID  Release note\nin_progress (1)\n  tsk_ID  Download button with a title much longer than forty columns\ndone (0)\ncancelled (0)\nblocked (0)\n"
	if got != want {
		t.Fatalf("status board:\n%s\nwant:\n%s", got, want)
	}
	got = lines(opstest.Text(h.Must(opstask.OpBoard, `{"group_by":"priority"}`)))
	want = "urgent (0)\nhigh (1)\n  tsk_ID  Release note\nmedium (0)\nlow (1)\n  tsk_ID  Download button with a title much longer than forty columns\n"
	if got != want {
		t.Fatalf("priority board:\n%s\nwant:\n%s", got, want)
	}
	var out struct {
		Groups []struct {
			Key   string            `json:"key"`
			Count int               `json:"count"`
			Tasks []json.RawMessage `json:"tasks"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpBoard, `{}`))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 5 || out.Groups[2].Key != "done" || out.Groups[2].Tasks == nil || len(out.Groups[2].Tasks) != 0 {
		t.Fatalf("json groups: %+v", out.Groups)
	}
}
