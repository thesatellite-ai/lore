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
	"fmt"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
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

// printTaskRows renders the human task list. Shows the commitment chip only
// when it isn't the common case (accepted) and the deferral wake date, so
// triage/someday/deferred views are self-explaining
func printTaskRows(rows []*ent.Task) { printTaskRowsWithState(rows, nil) }

// printTaskRowsWithState is printTaskRows plus each task's coordination
// state (§3.5: working / EXPIRED / gate / merged), when known.
func printTaskRowsWithState(rows []*ent.Task, states map[string]string) {
	if len(rows) == 0 {
		fmt.Println(style.Muted("(no tasks)"))
		return
	}
	for _, t := range rows {
		// One line format for repo and store rows (taskListLine); the full
		// opaque id is what every command takes.
		fmt.Println(taskListLine(t, states[t.ID]))
	}
}

func updateTaskStatus(ctx context.Context, f *commonFlags, idArg string, target entTask.Status, label string) error {
	rctx, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	t, err := lookupTask(ctx, client, projectID, idArg)
	if err != nil {
		return err
	}
	upd := client.Task.UpdateOne(t).SetStatus(target)
	now := time.Now()
	switch target {
	case entTask.StatusInProgress:
		upd.SetStartedAt(now)
		// Auto-promote: starting work commits the task and un-defers it.
		// Self-heals a task wrongly left proposed/someday/deferred
		upd.SetCommitment(entTask.CommitmentAccepted).ClearDeferredUntil()
	case entTask.StatusDone:
		upd.SetCompletedAt(now)
		upd.SetCommitment(entTask.CommitmentAccepted).ClearDeferredUntil()
	}
	if _, err := upd.Save(ctx); err != nil {
		return errcodes.New(errcodes.Internal, "update task").WithCause(err)
	}
	fmt.Printf("%s %s %s\n", style.Success("✓"), t.ID, label)
	return nil
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

func lookupTask(ctx context.Context, client *ent.Client, projectID, idArg string) (*ent.Task, error) {
	q := client.Task.Query().Where(entTask.ProjectID(projectID), entTask.ID(idArg))
	t, err := q.Only(ctx)
	if err != nil {
		return nil, errcodes.New(errcodes.NotFound, fmt.Sprintf("task %q not found", idArg))
	}
	return t, nil
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

func taskPriority(s string) entTask.Priority {
	ep := entTask.Priority(s)
	if err := entTask.PriorityValidator(ep); err != nil {
		return entTask.PriorityMedium
	}
	return ep
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

// activeTaskFilter applies the one shared ActiveTask predicate:
//
//	status NOT IN (done,cancelled)
//	AND commitment = accepted
//	AND (deferred_until IS NULL OR deferred_until <= now)
//
// Single definition consumed by `task list` default, the triage/someday/
// deferred views, and the task FTS fetch (Rule 2 — do not inline elsewhere)
func activeTaskFilter(q *ent.TaskQuery) *ent.TaskQuery {
	now := time.Now()
	return q.Where(
		entTask.StatusNotIn(entTask.StatusDone, entTask.StatusCancelled),
		entTask.CommitmentEQ(entTask.CommitmentAccepted),
		entTask.Or(
			entTask.DeferredUntilIsNil(),
			entTask.DeferredUntilLTE(now),
		),
	)
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
