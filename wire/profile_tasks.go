package wire

import (
	"context"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	opsperiod "github.com/thesatellite-ai/lore/ops/period"
	"github.com/thesatellite-ai/lore/schema"
)

// The open tasks of a leaving person (TEAM_PROFILES 5.1 step 3, TP-17):
// App implements the ops/period TaskReader upgrade over every store this
// App has attached (the repo, its project and org stores, personal `me`),
// read-only. The offboarding preview lists them with the line that
// reassigns them; nothing here, or in the op, changes a task.

var _ opsperiod.TaskReader = (*App)(nil)

// Task columns the reader matches on (schema builtin_work; fieldTaskLead is
// doctor_checks.go's assigned_to_actor_id).
const (
	profTaskFieldStatus = "status"
	profTaskFieldTitle  = "title"
)

// openTaskStatuses are the task statuses a person is still on: everything
// but done and cancelled.
func openTaskStatuses() []string {
	return []string{schema.TaskStatusTodo, schema.TaskStatusInProgress, schema.TaskStatusBlocked}
}

// OpenTasks implements opsperiod.TaskReader: every live open task of actor
// in every attached store, where actor is the lead (assigned_to_actor_id)
// or in assignees, sorted by store then id. A task seen in two stores (the
// same id) is listed once, from the first. A store that cannot be listed
// fails the read (the preview says so rather than show a partial list as
// complete). A task the person is neither lead nor assignee of is listed
// when they hold an active claim on it (TaskOnClaim): a claim lives on the
// signals ref, not on the row, and `lore task claim` does not make the
// holder an assignee. The claims are read as `lore task list --claimed-by`
// reads them (localClaims: this repo's signals ref as last fetched; none
// outside a git repo); a ref that cannot be read returns the other tasks in
// an *opsperiod.ClaimsUnreadError so the preview says the claims are
// missing.
func (a *App) OpenTasks(ctx context.Context, actor ids.AnyID) ([]opsperiod.OpenTask, error) {
	st, err := a.state(ctx)
	if err != nil {
		return nil, err
	}
	board, cerr := a.localClaims(ctx)
	who := actor.String()
	seen := map[ids.AnyID]bool{}
	out := []opsperiod.OpenTask{}
	for _, src := range st.eng.Stores() {
		rows, lerr := st.eng.List(ctx, src, schema.TableTasks)
		if lerr != nil {
			return nil, lerr
		}
		for _, r := range rows {
			if r.Deleted || seen[r.ID] || !slices.Contains(openTaskStatuses(), fieldText(r, profTaskFieldStatus)) {
				continue
			}
			how := ""
			switch {
			case fieldText(r, fieldTaskLead) == who:
				how = opsperiod.TaskOnLead
			case slices.Contains(taskItems(r.Fields[schema.FieldTaskAssignees]), who):
				how = opsperiod.TaskOnAssignee
			case cerr == nil && claimHeldBy(board, r.ID.String(), who):
				how = opsperiod.TaskOnClaim
			default:
				continue
			}
			seen[r.ID] = true
			out = append(out, opsperiod.OpenTask{ID: r.ID.String(), Title: fieldText(r, profTaskFieldTitle), Store: string(src), How: how})
		}
	}
	slices.SortStableFunc(out, func(x, y opsperiod.OpenTask) int {
		if c := strings.Compare(x.Store, y.Store); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	if cerr != nil {
		return nil, &opsperiod.ClaimsUnreadError{Tasks: out, Reason: cerr.Error()}
	}
	return out, nil
}

// claimHeldBy reports whether actor holds an active claim on task id on
// board (an expired claim is nobody's: anyone may take it).
func claimHeldBy(board localBoard, id, actor string) bool {
	holder, claimed, active := board.Claim(id)
	return claimed && active && holder == actor
}

// taskItems is a list field's items (none for null or absent).
func taskItems(v value.Value) []string {
	if v == nil || v.Kind() == value.KindNull {
		return nil
	}
	return value.ItemStrings(v)
}
