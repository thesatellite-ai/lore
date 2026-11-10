package wire

// Ports of saas/cmd/cli task_coord_history_test.go TestAppendReassignHistory
// and task_corerows_test.go TestCoreDocOfAuthor (B11) to cmd/lore's
// coordination host (coord_standalone_more.go, coord_standalone_world.go).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// TestAppendReassignHistory (TASK 17.2, OV59): reassign records one entry
// for the lead and one for the assignees (sorted, comma-joined) on top of
// the existing history; an unchanged field records nothing.
func TestAppendReassignHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	read := func(d coord.Doc) []opstask.HistoryEntry {
		s, err := canonjson.CompactValue(d[schema.FieldTaskHistory])
		if err != nil {
			t.Fatal(err)
		}
		var out []opstask.HistoryEntry
		if err = json.Unmarshal([]byte(s), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	d := coord.Doc{}
	if err := appendReassignHistory(d, "act_x", now, "", nil, "act_a"); err != nil {
		t.Fatal(err)
	}
	if err := appendReassignHistory(d, "act_x", now, "act_a", []string{"act_b", "act_a"}, "act_c"); err != nil {
		t.Fatal(err)
	}
	got := read(d)
	if len(got) != 4 {
		t.Fatalf("history = %+v, want 4 entries", got)
	}
	last := got[3]
	if last.Field != schema.FieldTaskAssignees || last.From != "act_a,act_b" || last.To != "act_c" || last.By != "act_x" || last.At != "2026-11-05T09:00:00Z" {
		t.Fatalf("assignees entry = %+v", last)
	}
	if got[2].Field != fieldTaskLead || got[2].From != "act_a" || got[2].To != "act_c" {
		t.Fatalf("lead entry = %+v", got[2])
	}
	if err := appendReassignHistory(d, "act_x", now, "act_c", []string{"act_c"}, "act_c"); err != nil {
		t.Fatal(err)
	}
	if n := len(read(d)); n != 4 {
		t.Fatalf("unchanged reassign added entries: %d", n)
	}
}

// TestDocOfRowAuthor: a v0.1.17 row's own created_by_actor_id column wins;
// a core row (no such column, or an empty one) gets the envelope's
// created_by, so the inbox and the mission owner see one author for both
// writers (F07 poison task).
func TestDocOfRowAuthor(t *testing.T) {
	t.Parallel()
	env, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	col, err := ids.New[ids.Actor]()
	if err != nil {
		t.Fatal(err)
	}
	colRef, err := value.RefOf(col.Any())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := value.TextOf("")
	if err != nil {
		t.Fatal(err)
	}
	task, err := ids.ParseAny("tsk_01a1a69973047a1f8526a3ee26fdba38")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		envBy  ids.ID[ids.Actor]
		column value.Value // nil = no column
		want   string
	}{
		{"core row: envelope only", env, nil, env.String()},
		{"v1 row: column only", ids.ID[ids.Actor]{}, colRef, col.String()},
		{"both: the column wins", env, colRef, col.String()},
		{"empty column: the envelope fills it", env, empty, env.String()},
		{"neither: no author", ids.ID[ids.Actor]{}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := row.Row{Table: schema.TableTasks, ID: task, CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0), CreatedBy: tc.envBy, Fields: map[string]value.Value{}}
			if tc.column != nil {
				r.Fields[docKeyAuthor] = tc.column
			}
			d, err := docOfRow(r)
			if err != nil {
				t.Fatal(err)
			}
			if got := d.Str(docKeyAuthor); got != tc.want {
				t.Fatalf("created_by_actor_id = %q, want %q", got, tc.want)
			}
			if d.Str(docKeyID) != task.String() {
				t.Fatalf("id = %q", d.Str(docKeyID))
			}
		})
	}
}
