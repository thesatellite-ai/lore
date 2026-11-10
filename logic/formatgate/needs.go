package formatgate

import (
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// Table names the first-use rule looks at. They are file format (the
// schema package declares the same strings: schema.TablePolicies,
// schema.TablePlans, schema.KindTablePrefix; schema_test pins them equal,
// because logic may not import schema at the same layer rank).
const (
	TablePolicies   = "policies"
	TablePlans      = "plans"
	KindTablePrefix = "res_"
	// FieldDeletedAt marks a row in the trash (logic/trash.FieldDeletedAt;
	// same layer rank, so the string is repeated, not imported).
	FieldDeletedAt = "deleted_at"
	// FieldTimeEntries is tasks.time_entries (ops/task.FieldTimeEntries; repeated, same layer rule as above).
	FieldTimeEntries = "time_entries"
	// TableTasks is schema.TableTasks (repeated, same layer rule).
	TableTasks = "tasks"
)

// TaskTreeFields lists the task fields of the task_tree feature
// (FORMAT_EVOLUTION step list row 4, TASK_SYSTEM_DESIGN 6 to 17: parent,
// rank, close reason, checklist, assignees, watchers, estimate,
// recurrence, history); the schema.FieldTask* names, repeated under the
// same layer rule. The lead stays in v0.1.17's assigned_to_actor_id, which
// every reader knows, so it is not one of them.
func TaskTreeFields() []string {
	return []string{"parent_id", "rank", "close_reason", "checklist", "assignees", "watchers", "estimate", "recur", "history"}
}

// Needs lists the features a row written by this binary uses, sorted
// (FORMAT_EVOLUTION rule 2, "How features is set the first time"): hlc for
// every row (the _v: 2 format), meta for a row with metadata, trash for a
// tombstone, policy for a policies row, resources for a kind row,
// plan_status for a plans row (its status is the closed TSQ9 set), time for
// a task with time entries, and task_tree for a task with a TaskTreeFields
// value.
func Needs(r row.Row) []string {
	out := []string{HLC}
	if len(r.Meta) > 0 {
		out = append(out, Meta)
	}
	if _, trashed := r.Fields[FieldDeletedAt]; r.Deleted || trashed {
		out = append(out, Trash) // a tombstone, or a row in the trash (DELETION 3.1)
	}
	if _, ok := r.Fields[FieldTimeEntries]; ok {
		out = append(out, Time) // a task with time entries (TIME_TRACKING_DESIGN 13 step 1)
	}
	if r.Table.String() == TableTasks && usesTaskTree(r) {
		out = append(out, TaskTree) // a task with a tree, checklist, people or recurrence field (T14)
	}
	switch t := r.Table.String(); {
	case t == TablePolicies:
		out = append(out, Policy)
	case t == TablePlans:
		out = append(out, PlanStatus)
	case strings.HasPrefix(t, KindTablePrefix):
		out = append(out, Resources)
	}
	slices.Sort(out)
	return out
}

// usesTaskTree reports a task row holding a non-null TaskTreeFields value
// (a null is what an old reader writes for a field it does not know).
func usesTaskTree(r row.Row) bool {
	for _, f := range TaskTreeFields() {
		if v, ok := r.Fields[f]; ok && v != nil && v.Kind() != value.KindNull {
			return true
		}
	}
	return false
}

// NeedsAll is the sorted union of Needs over rows.
func NeedsAll(rows []row.Row) []string {
	var out []string
	for _, r := range rows {
		for _, f := range Needs(r) {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	slices.Sort(out)
	return out
}
