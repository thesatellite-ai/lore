package lsync

import (
	"strings"
	"testing"

	"saas/pkg/aicoder/merge3"
)

// C12 / F4 step 1: the driver settles the file first. Add/add with an
// empty base merges field by field against an empty base.
func TestMergeDriver_AddAddMergesAgainstEmptyBase(t *testing.T) {
	t.Parallel()
	ours := taskDoc(t, map[string]any{"title": "same title", "status": "todo"})
	theirs := taskDoc(t, map[string]any{"title": "same title", "status": "done", "updated_at": "2026-02-01T00:00:00.000000000Z"})
	out, doc, _ := runDriver(t, "", ours, theirs, ".lore/data/tasks/x.json")
	if out.Conflicted || doc["status"] != "done" || doc["title"] != "same title" {
		t.Fatalf("add/add: %+v %v", out, doc)
	}
}

// F4: a tombstone on one side and an edit on the other keeps the edit.
func TestMergeDriver_ModifyBeatsDelete(t *testing.T) {
	t.Parallel()
	base := taskDoc(t, nil)
	tomb := `{"_live":{"deleted":true,"hlc":"2026-11-02T10:00:00.000Z/0000/a"},"id":"tsk_01a10237c9e87e4c843d8045973a65a1"}`
	edited := taskDoc(t, map[string]any{"title": "edited"})
	for _, c := range []struct{ name, ours, theirs string }{{"tombstone ours", tomb, edited}, {"tombstone theirs", edited, tomb}} {
		out, doc, _ := runDriver(t, base, c.ours, c.theirs, ".lore/data/tasks/x.json")
		if out.Conflicted || doc["title"] != "edited" {
			t.Fatalf("%s: the edit must survive: %+v %v", c.name, out, doc)
		}
		if !strings.Contains(strings.Join(out.AutoResolved, ","), "kept") {
			t.Fatalf("%s: the decision must be reported: %v", c.name, out.AutoResolved)
		}
	}
}

// F4 step 2: when both sides carry an hlc, it decides scalars, not
// updated_at (a laptop with a wrong clock does not win).
func TestMergeDriver_HLCOrdersScalarsOverUpdatedAt(t *testing.T) {
	t.Parallel()
	base := taskDoc(t, map[string]any{"priority": "medium"})
	ours := taskDoc(t, map[string]any{"priority": "high", "updated_at": "2026-01-01T00:00:00.000000000Z",
		"_live": map[string]any{"hlc": "2026-11-02T10:20:00.000Z/0000/dev1"}})
	theirs := taskDoc(t, map[string]any{"priority": "low", "updated_at": "2027-01-01T00:00:00.000000000Z",
		"_live": map[string]any{"hlc": "2026-11-02T10:10:00.000Z/0001/dev2"}})
	_, doc, _ := runDriver(t, base, ours, theirs, ".lore/data/tasks/x.json")
	if doc["priority"] != "high" {
		t.Fatalf("the higher hlc wins scalars: %v", doc["priority"])
	}
	noHLC := taskDoc(t, map[string]any{"priority": "low", "updated_at": "2027-01-01T00:00:00.000000000Z"})
	_, doc, _ = runDriver(t, base, ours, noHLC, ".lore/data/tasks/x.json")
	if doc["priority"] != "low" {
		t.Fatalf("without an hlc on both sides updated_at decides: %v", doc["priority"])
	}
}

// F4 / F11: _meta.json links merge as a set; concurrent links never
// produce markers, and a removal on one side is honoured.
func TestMergeDriver_MetaLinksAreASet(t *testing.T) {
	t.Parallel()
	base := `{"_v":1,"links":["a","b"],"project_id":"prj_01a10237c9e87e4c843d8045973a65a1"}`
	ours := `{"_v":1,"links":["a","b","c"],"project_id":"prj_01a10237c9e87e4c843d8045973a65a1"}`
	theirs := `{"_v":1,"links":["b","d"],"project_id":"prj_01a10237c9e87e4c843d8045973a65a1"}`
	out, doc, raw := runDriver(t, base, ours, theirs, ".lore/data/_meta.json")
	if out.Conflicted || strings.Contains(raw, "<<<<") {
		t.Fatalf("links must never conflict: %s", raw)
	}
	got := map[string]bool{}
	for _, v := range doc["links"].([]any) {
		got[v.(string)] = true
	}
	if !got["b"] || !got["c"] || !got["d"] || got["a"] {
		t.Fatalf("set merge: adds kept, removal of a honoured: %v", doc["links"])
	}
}

// Set-like list columns (mission approvers) merge as sets.
func TestStrategyForSetColumns(t *testing.T) {
	t.Parallel()
	for _, table := range []string{"missions", "teams"} {
		s := StrategyForTable(table)
		if s == nil {
			t.Fatalf("%s is synced", table)
		}
		for _, col := range []string{"approvers", "member_actor_ids"} {
			if got := s(col); got != merge3.StrategySet {
				t.Fatalf("%s.%s: %v", table, col, got)
			}
		}
	}
	if StrategyForTable("no_such_table") != nil {
		t.Fatal("unknown table: nil")
	}
}
