package task_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/logic/merge"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// tasksDef is the built-in tasks table (the merge policy under test is
// read from it, never hand-built).
func tasksDef(t *testing.T) schema.TableDef {
	t.Helper()
	for _, d := range schema.Builtin() {
		if d.Name == schema.TableTasks {
			return d
		}
	}
	t.Fatal("no built-in tasks table")
	return schema.TableDef{}
}

// mergeT0 and the two actors stamp the three sides of a merge.
var mergeT0 = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func mergeActor(t *testing.T, s string) ids.ID[ids.Actor] {
	t.Helper()
	a, err := ids.Parse[ids.Actor](s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// taskSide is one version of the same task row at ms past mergeT0.
func taskSide(t *testing.T, ms int, by ids.ID[ids.Actor], fields map[string]value.Value) row.Row {
	t.Helper()
	id, err := ids.ParseAny("tsk_01936f3300007d4e8f5a6b7c8d9e0f1a")
	if err != nil {
		t.Fatal(err)
	}
	return row.Row{
		Table: schema.TableTasks, ID: id, CreatedAt: mergeT0, CreatedBy: by,
		HLC:    hlc.HLC{Wall: mergeT0.Add(time.Duration(ms) * time.Millisecond), Actor: by},
		Fields: fields,
	}
}

// textList is a list of text items (assignees / watchers).
func textList(t *testing.T, items ...string) value.Value {
	t.Helper()
	vs := make([]value.Value, len(items))
	for i, s := range items {
		vs[i] = text(t, s)
	}
	l, err := value.ListOf(value.KindText, vs...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// checklistOf is a checklist JSON list of {id, text, updated_at} items.
func checklistOf(t *testing.T, items ...[3]string) value.Value {
	t.Helper()
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = `{"id":"` + it[0] + `","text":"` + it[1] + `","updated_at":"` + it[2] + `"}`
	}
	v, err := value.JSONOf([]byte("[" + strings.Join(parts, ",") + "]"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestTaskPeopleAndChecklistMerge is OV12's merge table on the real tasks
// schema (TASK 14.4, 7.2): assignees and watchers are three-way sets (an
// add on either side kept, a removal kept unless the other side added the
// actor anew), the checklist merges item by item (an item both sides
// added is kept, an item edited on both sides takes the newer edit).
func TestTaskPeopleAndChecklistMerge(t *testing.T) {
	a := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8d")
	b := mergeActor(t, "act_01936f0000007a1b8c2d3e4f5a6b7c8e")
	p := merge.PolicyFor(tasksDef(t))
	const ann, bob, cy, dee = "act_0000000000000000000000000000a001", "act_0000000000000000000000000000a002", "act_0000000000000000000000000000a003", "act_0000000000000000000000000000a004"

	people := []struct {
		name               string
		base, ours, theirs []string
		want               []string
	}{
		{"both add different people", []string{ann}, []string{ann, bob}, []string{ann, cy}, []string{ann, bob, cy}},
		{"one side removes, other untouched", []string{ann, bob}, []string{ann}, []string{ann, bob}, []string{ann}},
		{"one removes, other adds someone new", []string{ann, bob}, []string{ann}, []string{ann, bob, dee}, []string{ann, dee}},
		{"both remove the same", []string{ann, bob}, []string{ann}, []string{ann}, []string{ann}},
		{"empty base, both add the same", nil, []string{cy}, []string{cy}, []string{cy}},
		{"padded id is the same person", []string{ann}, []string{ann, " " + bob + " "}, []string{ann, bob}, []string{ann, bob}},
	}
	for _, field := range []string{schema.FieldTaskAssignees, schema.FieldTaskWatchers} {
		for _, c := range people {
			t.Run(field+"/"+c.name, func(t *testing.T) {
				base := taskSide(t, 0, a, map[string]value.Value{field: textList(t, c.base...)})
				ours := taskSide(t, 10, a, map[string]value.Value{field: textList(t, c.ours...)})
				theirs := taskSide(t, 20, b, map[string]value.Value{field: textList(t, c.theirs...)})
				for _, swap := range []bool{false, true} {
					o, th := ours, theirs
					if swap {
						o, th = theirs, ours
					}
					res, err := merge.Rows(p, base, o, th)
					if err != nil {
						t.Fatal(err)
					}
					got := value.ItemStrings(res.Row.Fields[field])
					for i := range got {
						got[i] = strings.TrimSpace(got[i])
					}
					slices.Sort(got)
					if !slices.Equal(got, c.want) {
						t.Fatalf("swap=%v: got %v, want %v", swap, got, c.want)
					}
					if !res.Clean() {
						t.Fatalf("a set merge never conflicts: %v", res.Conflicts)
					}
				}
			})
		}
	}

	t.Run("checklist by item id", func(t *testing.T) {
		const c1, c2, c3 = "chk_0000000000000000000000000000c001", "chk_0000000000000000000000000000c002", "chk_0000000000000000000000000000c003"
		const old, mid, late = "2026-11-05T10:00:00Z", "2026-11-05T11:00:00Z", "2026-11-05T12:00:00Z"
		base := taskSide(t, 0, a, map[string]value.Value{schema.FieldTaskChecklist: checklistOf(t, [3]string{c1, "one", old})})
		ours := taskSide(t, 10, a, map[string]value.Value{schema.FieldTaskChecklist: checklistOf(t,
			[3]string{c1, "one (ours)", mid}, [3]string{c2, "two", mid})})
		theirs := taskSide(t, 20, b, map[string]value.Value{schema.FieldTaskChecklist: checklistOf(t,
			[3]string{c1, "one (theirs)", late}, [3]string{c3, "three", mid})})
		res, err := merge.Rows(p, base, ours, theirs)
		if err != nil {
			t.Fatal(err)
		}
		got := res.Row.Fields[schema.FieldTaskChecklist].String()
		for _, want := range []string{c2, c3, "one (theirs)"} {
			if !strings.Contains(got, want) {
				t.Fatalf("checklist merge lost %q: %s", want, got)
			}
		}
		if strings.Contains(got, "one (ours)") {
			t.Fatalf("the older edit of one item won: %s", got)
		}
	})
}

// TestClientViewOfPeople (TASK 14.4, OV12): in a client repo the lead,
// assignees and watchers of a task are actor ids only (the client
// projection), and task show --json carries no internal session id.
func TestClientViewOfPeople(t *testing.T) {
	h := opstest.New(t, repoClient(true))
	ann := newActor(h, "Ann")
	bob := newActor(h, "Bob")
	id := add(h, "client work", `"assign":["`+ann+`","`+bob+`"],"lead":"`+bob+`"`)
	var out struct {
		Data struct {
			Lead      string   `json:"lead"`
			Assignees []string `json:"assignees"`
			Watchers  []string `json:"watchers"`
		} `json:"data"`
	}
	js := h.JSON(h.Must(opstask.OpShow, `{"id":"`+id+`"}`))
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.Lead != bob || !slices.Contains(out.Data.Assignees, ann) || !slices.Contains(out.Data.Assignees, bob) {
		t.Fatalf("people: %s", js)
	}
	for _, p := range slices.Concat([]string{out.Data.Lead}, out.Data.Assignees, out.Data.Watchers) {
		if !strings.HasPrefix(p, "act_") {
			t.Fatalf("a non-actor id %q in the client view: %s", p, js)
		}
	}
	if strings.Contains(js, "ses_") {
		t.Fatalf("a session id in the client view: %s", js)
	}
}
