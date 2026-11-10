package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// This file is the root-module twin of the old binary's two-clone time
// merge table (TIME_TRACKING_DESIGN 7.2) and the blocked_by / recur rows of
// OV15 / OV15c: the three sides of one task row are merged with the policy
// of the real built-in tasks table (never hand-built) and read back through
// the same readers task show uses (TimeEntries, TimeOpen). Every case runs
// with ours and theirs swapped, because a merge result must not depend on
// which clone pulls.

// mtJSON wraps raw JSON text as a field value.
func mtJSON(t *testing.T, raw string) value.Value {
	t.Helper()
	out, err := value.JSONOf([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// mtEntries is a time_entries field value.
func mtEntries(t *testing.T, list ...timeentry.Entry) value.Value {
	t.Helper()
	raw, err := timeentry.Encode(list)
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// mtOpen is a time_open field value.
func mtOpen(t *testing.T, list ...timeentry.Open) value.Value {
	t.Helper()
	raw, err := timeentry.EncodeOpen(list)
	if err != nil {
		t.Fatal(err)
	}
	v, err := value.JSONOf(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// mtMerge merges base/ours/theirs both ways round and fails unless the two
// results carry the same field bytes and no conflict.
func mtMerge(t *testing.T, base, ours, theirs row.Row, fieldNames ...string) row.Row {
	t.Helper()
	p := merge.PolicyFor(tasksDef(t))
	a, err := merge.Rows(p, base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := merge.Rows(p, base, theirs, ours)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Clean() || !b.Clean() {
		t.Fatalf("a work-field merge never asks a person: %v / %v", a.Conflicts, b.Conflicts)
	}
	for _, f := range fieldNames {
		if mtString(a.Row.Fields[f]) != mtString(b.Row.Fields[f]) {
			t.Fatalf("%s depends on which side pulls:\n%s\n%s", f, mtString(a.Row.Fields[f]), mtString(b.Row.Fields[f]))
		}
	}
	return a.Row
}

func mtString(v value.Value) string {
	if v == nil {
		return "<absent>"
	}
	return v.String()
}

// mtAt is an RFC 3339 stamp min minutes past mergeT0 in timeentry's layout.
func mtAt(min int) string {
	return mergeT0.Add(time.Duration(min) * time.Minute).UTC().Format(timeentry.TimeLayout)
}

// TestTaskTimeMergeTable is TIME_TRACKING_DESIGN 7.2 on the real tasks
// schema: time entries union by id (add/add keeps both, edit/edit keeps
// the newer edit, a tombstone wins only when newer, delete/delete keeps the
// first delete), and a span closed on one clone while another person
// starts on the other: the closed id is in time_entries, the raw union
// still carries it in time_open, and TimeOpen (what task show reads) drops
// it, leaving only the other person's open span.
func TestTaskTimeMergeTable(t *testing.T) {
	t.Parallel()
	mira := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	tom := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	span := func(note string) timeentry.Entry {
		t.Helper()
		e, err := timeentry.NewSpan(timeentry.Span{
			Kind: timeentry.KindManual, Source: timeentry.SourceManual, Actor: mira,
			Start: mergeT0, End: mergeT0.Add(15 * time.Minute), Note: note,
		})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	edited := func(e timeentry.Entry, note string, min int) timeentry.Entry {
		e.Note, e.UpdatedAt = note, mtAt(min)
		return e
	}
	removed := func(e timeentry.Entry, min int) timeentry.Entry {
		e.DeletedAt, e.UpdatedAt = mtAt(min), mtAt(min)
		return e
	}
	entries := func(r row.Row) map[string]timeentry.Entry {
		out := map[string]timeentry.Entry{}
		for _, e := range opstask.TimeEntries(r) {
			out[e.ID.String()] = e
		}
		return out
	}
	side := func(ms int, by ids.ID[ids.Actor], es ...timeentry.Entry) row.Row {
		return taskSide(t, ms, by, map[string]value.Value{opstask.FieldTimeEntries: mtEntries(t, es...)})
	}

	t.Run("add/add keeps both entries", func(t *testing.T) {
		t.Parallel()
		a1, a2 := span("dev1"), span("dev2")
		got := entries(mtMerge(t, side(0, mira), side(10, mira, a1), side(20, tom, a2), opstask.FieldTimeEntries))
		if len(got) != 2 || got[a1.ID.String()].Note != "dev1" || got[a2.ID.String()].Note != "dev2" {
			t.Fatalf("both clones' new entries must survive: %v", got)
		}
	})
	t.Run("edit/edit keeps the newer edit whole", func(t *testing.T) {
		t.Parallel()
		e := span("base")
		got := entries(mtMerge(t, side(0, mira, e), side(10, mira, edited(e, "dev1 edit", 30)), side(20, tom, edited(e, "dev2 edit", 40)), opstask.FieldTimeEntries))
		if got[e.ID.String()].Note != "dev2 edit" {
			t.Fatalf("the newer edit must win: %+v", got[e.ID.String()])
		}
	})
	t.Run("delete then a later edit: the edit stays", func(t *testing.T) {
		t.Parallel()
		e := span("base")
		got := entries(mtMerge(t, side(0, mira, e), side(10, mira, removed(e, 30)), side(20, tom, edited(e, "edited later", 40)), opstask.FieldTimeEntries))
		if g := got[e.ID.String()]; g.Deleted() || g.Note != "edited later" {
			t.Fatalf("an edit newer than the delete is kept: %+v", g)
		}
	})
	t.Run("edit then a later delete: the delete wins", func(t *testing.T) {
		t.Parallel()
		e := span("base")
		got := entries(mtMerge(t, side(0, mira, e), side(10, mira, edited(e, "dev1 edit", 30)), side(20, tom, removed(e, 40)), opstask.FieldTimeEntries))
		if g := got[e.ID.String()]; !g.Deleted() {
			t.Fatalf("a delete newer than the edit wins: %+v", g)
		}
	})
	t.Run("delete/delete keeps the first delete", func(t *testing.T) {
		t.Parallel()
		e := span("base")
		got := entries(mtMerge(t, side(0, mira, e), side(10, mira, removed(e, 30)), side(20, tom, removed(e, 50)), opstask.FieldTimeEntries))
		if g := got[e.ID.String()]; g.DeletedAt != mtAt(30) {
			t.Fatalf("both deleted: the earlier deleted_at stays on record, got %q", g.DeletedAt)
		}
	})
	t.Run("closed on one clone, another person opens on the other", func(t *testing.T) {
		t.Parallel()
		miraOpen, err := timeentry.NewOpen(mira, mergeT0)
		if err != nil {
			t.Fatal(err)
		}
		miraOpen.UpdatedAt = mtAt(0)
		tomOpen, err := timeentry.NewOpen(tom, mergeT0.Add(20*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		tomOpen.UpdatedAt = mtAt(20)
		closed := miraOpen.Close(mergeT0.Add(30*time.Minute), mira)
		base := taskSide(t, 0, mira, map[string]value.Value{opstask.FieldTimeOpen: mtOpen(t, miraOpen)})
		ours := taskSide(t, 10, mira, map[string]value.Value{opstask.FieldTimeOpen: mtOpen(t), opstask.FieldTimeEntries: mtEntries(t, closed)})
		theirs := taskSide(t, 20, tom, map[string]value.Value{opstask.FieldTimeOpen: mtOpen(t, miraOpen, tomOpen)})
		got := mtMerge(t, base, ours, theirs, opstask.FieldTimeOpen, opstask.FieldTimeEntries)
		if e, ok := entries(got)[closed.ID.String()]; !ok || e.Seconds != 30*60 {
			t.Fatalf("mira's closed span must be in time_entries with 30m: %v", entries(got))
		}
		var open []string
		for _, o := range opstask.TimeOpen(got) {
			open = append(open, o.ActorID.String())
		}
		if !slices.Equal(open, []string{tom.String()}) {
			t.Fatalf("time_open read back = %v, want only tom's span (mira's closed id dropped at read time)", open)
		}
	})
}

// TestTaskBlockedByAndRecurMerge is the OV15 blocked_by set-merge table
// (tasks.blocked_by merges as a three-way set of --after edges, an edge's
// key order never matters) and the OV15c recur row: a recurrence edited on
// both clones takes the newer edit whole, never a field-by-field mix and
// never a conflict.
func TestTaskBlockedByAndRecurMerge(t *testing.T) {
	t.Parallel()
	a := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	b := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	const t1, t2, t3, t4 = "tsk_0000000000000000000000000000b001", "tsk_0000000000000000000000000000b002", "tsk_0000000000000000000000000000b003", "tsk_0000000000000000000000000000b004"
	// edges is a blocked_by value of plain --after edges on the given tasks.
	edges := func(ids ...string) string {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = `{"task_id":"` + id + `"}`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	cases := []struct {
		name               string
		base, ours, theirs string
		want               []string
	}{
		{"both add different edges", edges(t1), edges(t1, t2), edges(t1, t3), []string{t1, t2, t3}},
		{"one side removes, other untouched", edges(t1, t2), edges(t1), edges(t1, t2), []string{t1}},
		{"one removes, other adds a new edge", edges(t1, t2), edges(t1), edges(t1, t2, t4), []string{t1, t4}},
		{"both remove the same edge", edges(t1, t2), edges(t1), edges(t1), []string{t1}},
		{"both add the same edge, keys in another order", edges(t1),
			`[{"task_id":"` + t1 + `"},{"task_id":"` + t2 + `","until":"done"}]`,
			`[{"task_id":"` + t1 + `"},{"until":"done","task_id":"` + t2 + `"}]`, []string{t1, t2}},
	}
	for _, c := range cases {
		t.Run("blocked_by/"+c.name, func(t *testing.T) {
			t.Parallel()
			base := taskSide(t, 0, a, map[string]value.Value{"blocked_by": mtJSON(t, c.base)})
			ours := taskSide(t, 10, a, map[string]value.Value{"blocked_by": mtJSON(t, c.ours)})
			theirs := taskSide(t, 20, b, map[string]value.Value{"blocked_by": mtJSON(t, c.theirs)})
			got := mtMerge(t, base, ours, theirs, "blocked_by")
			raw, err := got.Fields["blocked_by"].MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var list []struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(raw, &list); err != nil {
				t.Fatalf("blocked_by after merge is not a list of edges: %s", raw)
			}
			var ids []string
			for _, e := range list {
				ids = append(ids, e.TaskID)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, c.want) {
				t.Fatalf("blocked_by = %v, want %v (raw %s)", ids, c.want, raw)
			}
		})
	}

	t.Run("recur edited on both sides: newer edit whole", func(t *testing.T) {
		t.Parallel()
		recur := func(rule, until string) value.Value {
			return mtJSON(t, `{"rule":"`+rule+`","series":"rcs_0000000000000000000000000000a001","until":"`+until+`"}`)
		}
		base := taskSide(t, 0, a, map[string]value.Value{schema.FieldTaskRecur: recur("weekly", "2026-12-31")})
		ours := taskSide(t, 10, a, map[string]value.Value{schema.FieldTaskRecur: recur("daily", "2026-12-31")})
		theirs := taskSide(t, 20, b, map[string]value.Value{schema.FieldTaskRecur: recur("weekly", "2027-03-31")})
		got := mtMerge(t, base, ours, theirs, schema.FieldTaskRecur)
		if g, w := mtString(got.Fields[schema.FieldTaskRecur]), mtString(recur("weekly", "2027-03-31")); g != w {
			t.Fatalf("recur = %s, want the newer side whole %s", g, w)
		}
	})
}
