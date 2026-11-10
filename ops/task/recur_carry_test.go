package task_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/settings"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/wire"
)

// recurDone closes id (children done, checklist ticked) and returns the
// next instance's id and the human output.
func recurDone(h *opstest.H, id string) (string, string) {
	h.T.Helper()
	res := h.Must(opstask.OpDone, `{"id":"`+id+`","no_lesson":true,"children":"done","tick_all":true}`)
	next := between(h.JSON(res), `"recur":{"id":"`)
	if next == "" {
		h.T.Fatalf("no next instance: %s", h.JSON(res))
	}
	return next, opstest.Text(res)
}

// childrenOf is the ids of id's direct sub-tasks, by title.
func childrenOf(h *opstest.H, id string) map[string]string {
	h.T.Helper()
	var out struct {
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpList, `{"parent":"`+id+`","all":true}`))), &out); err != nil {
		h.T.Fatal(err)
	}
	m := map[string]string{}
	for _, d := range out.Data {
		m[d.Title] = d.ID
	}
	return m
}

// TestRecurCarry (TASK 10.4, OV15c): the next instance holds the root's
// metadata; each child's due and deferred dates move by the root's shift;
// an --after edge between two tasks of the subtree points at the new
// children, an edge leaving the subtree is dropped.
func TestRecurCarry(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	outside := add(h, "outside work", "")
	root := add(h, "Monthly close", `"due":"2026-11-30","recur":"every month on last","meta":["client=acme"]`)
	first := add(h, "collect", `"parent":"`+root+`","due":"2026-11-25","defer_until":"2026-11-20"`)
	add(h, "post", `"parent":"`+root+`","after":["`+first+`"]`)
	add(h, "report", `"parent":"`+root+`","after":["`+outside+`"]`)

	next, _ := recurDone(h, root)
	_, oldRoot := get(h, schema.TableTasks, root)
	_, newRoot := get(h, schema.TableTasks, next)
	if len(newRoot.Meta) == 0 || !maps.EqualFunc(oldRoot.Meta, newRoot.Meta, func(a, b value.JSON) bool { return a.String() == b.String() }) {
		t.Errorf("metadata not carried: old %v new %v", oldRoot.Meta, newRoot.Meta)
	}
	if got := field(newRoot, "due_at"); !strings.HasPrefix(got, "2026-12-31") {
		t.Errorf("root due %q", got)
	}

	kids := childrenOf(h, next)
	if len(kids) != 3 {
		t.Fatalf("children %v", kids)
	}
	// Root moved 2026-11-30 → 2026-12-31: 31 days.
	_, collect := get(h, schema.TableTasks, kids["collect"])
	if got := field(collect, "due_at"); !strings.HasPrefix(got, "2026-12-26") {
		t.Errorf("child due %q, want 2026-12-26 (shifted 31 days)", got)
	}
	if got := field(collect, "deferred_until"); !strings.HasPrefix(got, "2026-12-21") {
		t.Errorf("child deferred %q, want 2026-12-21", got)
	}
	_, post := get(h, schema.TableTasks, kids["post"])
	if got := field(post, "blocked_by"); !strings.Contains(got, kids["collect"]) || strings.Contains(got, first) {
		t.Errorf("in-subtree edge %q, want the new collect %s", got, kids["collect"])
	}
	_, report := get(h, schema.TableTasks, kids["report"])
	if got := field(report, "blocked_by"); strings.Contains(got, outside) {
		t.Errorf("edge leaving the subtree was copied: %q", got)
	}
}

// TestRecurCarryRefusedMeta (10.4 + METADATA 3.19): a carried value that
// today's key rules refuse never blocks closing the instance: the next
// instance is written without it and the output says so.
func TestRecurCarryRefusedMeta(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	h.MustYes(settings.OpMetaAdd, `{"name":"points","type":"number"}`)
	// Not strict yet: a text value is a warning, so the root keeps it.
	root := add(h, "Quarterly", `"due":"2026-11-30","recur":"every month on last","meta":["points=lots"]`)
	// The key turns strict after the root was written.
	h.MustYes(settings.OpMetaEdit, `{"id":"points","strict":true}`)

	next, human := recurDone(h, root)
	if !strings.Contains(human, "metadata of "+root+" not carried") {
		t.Errorf("no refusal line:\n%s", human)
	}
	_, r := get(h, schema.TableTasks, next)
	if len(r.Meta) != 0 {
		t.Errorf("refused metadata was written: %v", r.Meta)
	}
	if field(r, "status") != schema.TaskStatusTodo {
		t.Errorf("next instance status %q", field(r, "status"))
	}
}

// TestRecurNextZoneFree (OV15c, TSQ11): the next instance's id and dates
// are byte-equal whatever the machine's time zone, so two machines that
// both finish the same instance write the same row.
func TestRecurNextZoneFree(t *testing.T) {
	// A wall clock late in the UTC day: in +14 it is already the next day,
	// in -11 still the same day; neither may move the series.
	wall := time.Date(2026, 11, 30, 22, 30, 0, 0, time.UTC)
	rules := []string{"every month on last", "every! 3 days", "every mon", "every day"}
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	type result struct{ next, due, kidDue string }
	var want []result
	for zi, zone := range []*time.Location{time.UTC, time.FixedZone("plus14", 14*3600), time.FixedZone("minus11", -11*3600)} {
		time.Local = zone
		var got []result
		for _, rule := range rules {
			h := opstest.New(t, func(o *wire.Options) { o.Wall = func() time.Time { return wall } })
			// The same pinned project and series inputs on each machine:
			// the root is added with --key so its id is the same too.
			root := add(h, "zone "+rule, `"key":"zone-root","due":"2026-11-30","recur":`+opstest.Quote(rule))
			add(h, "kid", `"parent":"`+root+`","due":"2026-11-29T10:00:00Z"`)
			next, _ := recurDone(h, root)
			_, r := get(h, schema.TableTasks, next)
			_, k := get(h, schema.TableTasks, childrenOf(h, next)["kid"])
			got = append(got, result{next: next, due: field(r, "due_at"), kidDue: field(k, "due_at")})
		}
		if zi == 0 {
			want = got
			continue
		}
		for i := range got {
			if got[i].due != want[i].due || got[i].kidDue != want[i].kidDue {
				t.Errorf("%s %q: %+v, UTC gave %+v", zone, rules[i], got[i], want[i])
			}
		}
	}
}
