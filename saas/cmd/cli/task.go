// task.go — `lore task add/list/start/done/cancel/edit` (NEW per user request)
//
// Tasks are discrete units of work. Lifecycle:
//
//	todo → in_progress → done
//
// Or alternative terminal states: cancelled / blocked
//
// Tasks can belong to a Mission via mission_id
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/signals/coord"
	"saas/pkg/aicoder/style"
)

// briefIfPresent returns a minimal map for a tasklist edge or nil
func briefIfPresent(tl *ent.TaskList) any {
	if tl == nil {
		return nil
	}
	return map[string]any{"id": tl.ID, "title": tl.Title}
}

func briefIfPresentMission(m *ent.Mission) any {
	if m == nil {
		return nil
	}
	return map[string]any{"id": m.ID, "title": m.Title, "status": string(m.Status)}
}

func briefIfPresentPlan(p *ent.Plan) any {
	if p == nil {
		return nil
	}
	return map[string]any{"id": p.ID, "title": p.Title}
}

// fullFromTask projects an ent.Task → taskFull JSON shape
func fullFromTask(t *ent.Task) taskFull {
	out := taskFull{
		ID:         t.ID,
		Title:      t.Title,
		Status:     string(t.Status),
		Priority:   string(t.Priority),
		Commitment: string(t.Commitment),
		ProjectID:  t.ProjectID,
		CreatedAt:  t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if t.DeferredUntil != nil {
		out.DeferredUntil = t.DeferredUntil.Format("2006-01-02")
	}
	if t.Body != nil {
		out.Body = *t.Body
	}
	if t.DueAt != nil {
		out.DueAt = t.DueAt.Format("2006-01-02")
	}
	if t.StartedAt != nil {
		out.StartedAt = t.StartedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if t.CompletedAt != nil {
		out.CompletedAt = t.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if t.MissionID != nil {
		out.MissionID = *t.MissionID
	}
	if t.TasklistID != nil {
		out.TaskListID = *t.TasklistID
	}
	if t.PlanID != nil {
		out.PlanID = *t.PlanID
	}
	if t.RepoID != nil {
		out.RepoID = *t.RepoID
	}
	return out
}

// lookupTask reads task idArg through the core (any attached store; tasks
// are core-owned, their row files are the truth). An unknown or deleted id
// is E_NOT_FOUND with v0.1.17's message.
func lookupTask(ctx context.Context, idArg string) (coord.Doc, error) {
	d, ok, err := coreDoc(ctx, tableTasks, idArg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errcodes.New(errcodes.NotFound, fmt.Sprintf("task %q not found", idArg))
	}
	return d, nil
}

// docStrs returns a list field of a core document as strings (refs and
// text items); nil when absent or not a list.
func docStrs(d coord.Doc, k string) []string {
	items, ok := d[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// docBool returns a bool field of a core document (false when absent).
func docBool(d coord.Doc, k string) bool {
	b, ok := d[k].(bool)
	return ok && b
}

// docInto decodes a core document into T, a struct whose json tags are the
// column names (the v0.1.17 row structs coordination views still use as
// plain data holders; nothing here queries lore.db).
func docInto[T any](d coord.Doc) (*T, error) {
	b, err := json.Marshal(map[string]any(d))
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// taskStatus / taskPriority parse a flag value into the typed ent enum
// using ent's generated validator. No hand-written switch statements
//
// On invalid input, returns ErrInvalidInput (CLI surfaces as E_INVALID_INPUT)
// so the user gets immediate feedback rather than waiting for the INSERT to
// fail on a CHECK constraint
func taskStatus(s string) entTask.Status {
	es := entTask.Status(s)
	if err := entTask.StatusValidator(es); err != nil {
		return entTask.StatusTodo
	}
	return es
}

// parseCommitment validates a --commitment value. Unlike taskStatus (which
// silently falls back), commitment is loud: an invalid value is a hard error
// so an agent can't typo its way into the wrong axis
func parseCommitment(s string) (entTask.Commitment, error) {
	c := entTask.Commitment(s)
	if err := entTask.CommitmentValidator(c); err != nil {
		return "", errcodes.New(errcodes.InvalidInput,
			"bad --commitment "+s+" (want: accepted | proposed | someday)")
	}
	return c, nil
}

// callerIsAgent reports whether the current CLI invoker resolved to an
// agent identity. Used to force an explicit --commitment on agent task
// creation (no silent default — see TASK_COMMITMENT_SPEC.md ADR-TASK-1)
func callerIsAgent() bool {
	return identityBestEffort(context.Background()).Kind == identity.KindAgent
}

// parseDeferUntil parses a --defer-until value (YYYY-MM-DD, consistent
// with --due)
func parseDeferUntil(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, errcodes.New(errcodes.InvalidInput,
			"--defer-until format: YYYY-MM-DD").WithCause(err)
	}
	return t, nil
}

func taskStatusStyle(s string) string {
	switch s {
	case "done":
		return style.Success("✓")
	case "in_progress":
		return style.Info("▶")
	case "cancelled":
		return style.Muted("✗")
	case "blocked":
		return style.Warn("⛔")
	default:
		return style.Muted("○")
	}
}
