package lsync

import (
	"strings"
	"testing"

	"saas/pkg/aicoder/canonjson"
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

// F4 on the v0.1.17 field-merge path (tables lsync still writes, such as
// entity_tags): a tombstone on one side and an edit on the other keeps the
// edit. Core-owned tables settle this the other way round, see
// TestMergeDriver_CoreTombstoneNeverResurrected.
func TestMergeDriver_ModifyBeatsDelete(t *testing.T) {
	t.Parallel()
	const id = "etg_01a10237c9e87e4c843d8045973a65a1"
	base := `{"_table":"entity_tags","_v":1,"entity_id":"x","id":"` + id + `","updated_at":"2026-01-01T00:00:00.000000000Z"}`
	tomb := `{"_live":{"deleted":true,"hlc":"2026-11-02T10:00:00.000Z/0000/a"},"id":"` + id + `"}`
	edited := `{"_table":"entity_tags","_v":1,"entity_id":"edited","id":"` + id + `","updated_at":"2026-02-01T00:00:00.000000000Z"}`
	for _, c := range []struct{ name, ours, theirs string }{{"tombstone ours", tomb, edited}, {"tombstone theirs", edited, tomb}} {
		out, doc, _ := runDriver(t, base, c.ours, c.theirs, ".lore/data/entity_tags/x.json")
		if out.Conflicted || doc["entity_id"] != "edited" {
			t.Fatalf("%s: the edit must survive: %+v %v", c.name, out, doc)
		}
		if !strings.Contains(strings.Join(out.AutoResolved, ","), "kept") {
			t.Fatalf("%s: the decision must be reported: %v", c.name, out.AutoResolved)
		}
	}
}

// A core-owned row (tasks, 0.1.18) deleted on one side and edited later on
// the other keeps the tombstone whichever side holds it: rows are never
// resurrected (STORE_SERVER_SPEC 11 rule 3, logic/merge.Rows). The merge is
// not a conflict, and the overruled edit is reported so the merge driver
// tells the teammate.
func TestMergeDriver_CoreTombstoneNeverResurrected(t *testing.T) {
	t.Parallel()
	base := coreTaskDoc(t, "2026-11-02T09:00:00.000Z/0000/", nil)
	// A core tombstone is the header only: no body fields.
	tomb := `{"_deleted":true,"_hlc":"2026-11-02T10:00:00.000Z/0000/","_table":"tasks","_v":2,"id":"tsk_01a10237c9e87e4c843d8045973a65a1"}`
	editedLater := coreTaskDoc(t, "2026-11-02T11:00:00.000Z/0000/", map[string]any{"title": "edited"})
	editedEarlier := coreTaskDoc(t, "2026-11-02T09:30:00.000Z/0000/", map[string]any{"title": "edited"})
	for _, c := range []struct {
		name, ours, theirs string
		reported           bool
	}{
		{"tombstone ours, later edit theirs", tomb, editedLater, true},
		{"later edit ours, tombstone theirs", editedLater, tomb, true},
		{"tombstone ours, earlier edit theirs", tomb, editedEarlier, false},
		{"earlier edit ours, tombstone theirs", editedEarlier, tomb, false},
	} {
		out, doc, raw := runDriver(t, base, c.ours, c.theirs, ".lore/data/tasks/x.json")
		if out.Conflicted || doc["_deleted"] != true {
			t.Fatalf("%s: the tombstone must win: %+v %s", c.name, out, raw)
		}
		if got := strings.Contains(strings.Join(out.AutoResolved, ","), "delete was kept"); got != c.reported {
			t.Fatalf("%s: overruled edit reported=%v, want %v: %v", c.name, got, c.reported, out.AutoResolved)
		}
	}
}

// F4 step 2 on a core-owned table: when both sides carry an hlc it decides
// scalars, not updated_at (a laptop with a wrong clock does not win); a
// v0.1.17 file without an hlc is ordered by its updated_at (row upcast).
func TestMergeDriver_HLCOrdersScalarsOverUpdatedAt(t *testing.T) {
	t.Parallel()
	base := coreTaskDoc(t, "2026-11-02T10:00:00.000Z/0000/", map[string]any{"priority": "medium"})
	ours := coreTaskDoc(t, "2026-11-02T10:20:00.000Z/0000/", map[string]any{"priority": "high", "updated_at": "2026-01-01T00:00:00.000000000Z"})
	theirs := coreTaskDoc(t, "2026-11-02T10:10:00.000Z/0001/", map[string]any{"priority": "low", "updated_at": "2027-01-01T00:00:00.000000000Z"})
	for _, c := range []struct{ name, ours, theirs string }{{"higher hlc ours", ours, theirs}, {"higher hlc theirs", theirs, ours}} {
		out, doc, raw := runDriver(t, base, c.ours, c.theirs, ".lore/data/tasks/x.json")
		if out.Conflicted || doc["priority"] != "high" {
			t.Fatalf("%s: the higher hlc wins scalars: %+v %s", c.name, out, raw)
		}
		if got := strings.Join(out.NewerWins(), ","); got != "priority" {
			t.Fatalf("%s: the losing priority edit must be reported: %q", c.name, got)
		}
	}
	v1Base := taskDoc(t, map[string]any{"priority": "medium"})
	v1Ours := taskDoc(t, map[string]any{"priority": "high", "updated_at": "2026-01-01T00:00:00.000000000Z"})
	v1Theirs := taskDoc(t, map[string]any{"priority": "low", "updated_at": "2027-01-01T00:00:00.000000000Z"})
	_, doc, _ := runDriver(t, v1Base, v1Ours, v1Theirs, ".lore/data/tasks/x.json")
	if doc["priority"] != "low" {
		t.Fatalf("without an hlc on both sides updated_at decides: %v", doc["priority"])
	}
}

// coreTaskDoc is a 0.1.18 core task row document (`_v: 2`, ordered by
// `_hlc`) with kv overriding the defaults.
func coreTaskDoc(t *testing.T, hlc string, kv map[string]any) string {
	t.Helper()
	doc := map[string]any{keyVersion: num(2), keyTable: "tasks", "id": "tsk_01a10237c9e87e4c843d8045973a65a1", "_hlc": hlc,
		"title": "t", "body": "b", "status": "todo", "updated_at": "2026-01-01T00:00:00.000000000Z"}
	for k, v := range kv {
		doc[k] = v
	}
	b, err := canonjson.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
