package formatgate

import (
	"slices"
	"testing"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TestNeedsTaskTree (FORMAT_EVOLUTION step list row 4, later 410): a task
// row declares task_tree as soon as it holds any TaskTreeFields value; a
// plain task, a null value (what an old reader writes back) and the same
// field on another table do not; the other rules keep their features.
func TestNeedsTaskTree(t *testing.T) {
	txt := func(s string) value.Value {
		v, err := value.TextOf(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	base := map[string]value.Value{"title": txt("t")}
	with := func(k string, v value.Value) map[string]value.Value {
		m := map[string]value.Value{"title": txt("t")}
		m[k] = v
		return m
	}
	cases := []struct {
		name   string
		table  string
		fields map[string]value.Value
		want   bool
	}{
		{"plain task", TableTasks, base, false},
		{"null parent", TableTasks, with("parent_id", value.Null{}), false},
		{"memory with a rank field", "memories", with("rank", txt("a0")), false},
		{"time entries only", TableTasks, with(FieldTimeEntries, txt("[]")), false},
	}
	for _, f := range TaskTreeFields() {
		cases = append(cases, struct {
			name   string
			table  string
			fields map[string]value.Value
			want   bool
		}{"task with " + f, TableTasks, with(f, txt("x")), true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Needs(row.Row{Table: row.Table(c.table), Fields: c.fields})
			if slices.Contains(got, TaskTree) != c.want {
				t.Fatalf("Needs = %v, task_tree want %v", got, c.want)
			}
			if !slices.Contains(got, HLC) || !slices.IsSorted(got) {
				t.Fatalf("Needs = %v: hlc always, sorted", got)
			}
		})
	}
	if got := NeedsAll([]row.Row{{Table: TableTasks, Fields: with("parent_id", txt("tsk_1"))}, {Table: TableTasks, Fields: with(FieldTimeEntries, txt("[]"))}}); !slices.Equal(got, []string{HLC, TaskTree, Time}) {
		t.Fatalf("NeedsAll = %v", got)
	}
}
