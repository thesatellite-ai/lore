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
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Manage tasks (discrete work items)"}
	cmd.AddCommand(newTaskAddCommand())
	cmd.AddCommand(newTaskListCommand())
	cmd.AddCommand(newTaskStartCommand())
	cmd.AddCommand(newTaskDoneCommand())
	cmd.AddCommand(newTaskCancelCommand())
	cmd.AddCommand(newTaskShowCommand())
	cmd.AddCommand(newTaskEditCommand())
	cmd.AddCommand(newTaskSearchCommand())
	cmd.AddCommand(newDeleteCommand(taskDeleteTarget))
	return cmd
}

// newTaskSearchCommand wires `lore task search <query>`. Uses the
// shared FTS5 search helper (see search_shared.go) and eager-loads the
// task's parent tasklist + mission + plan plus created_by / assigned_to
// actor refs so consumers don't need follow-up queries
func newTaskSearchCommand() *cobra.Command {
	var f entitySearchFlags
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across task title + body (FTS5 BM25 ranked)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f.commonFlags)
			if err != nil {
				return err
			}
			defer client.Close()

			hits, err := runFTSAgainst(cmd.Context(), client,
				&projresolveContext{ProjectID: rctx.ProjectID, RepoMount: rctx.RepoMount},
				constants.EntityTask.FTSEntity(), args[0], &f)
			if err != nil {
				return err
			}
			if len(hits) == 0 {
				printSearchEnvelope(f.jsonOutput, constants.EntityTask.SearchKind(), args[0], nil, nil)
				return nil
			}

			// Collect IDs in FTS-rank order, then fetch + reorder
			ids := make([]string, len(hits))
			snippets := make(map[string]string, len(hits))
			scores := make(map[string]float64, len(hits))
			for i, h := range hits {
				ids[i] = h.ID
				snippets[h.ID] = h.Snippet
				scores[h.ID] = h.BM25
			}
			rows, err := client.Task.Query().
				Where(entTask.IDIn(ids...)).
				WithTasklist().
				WithMission().
				WithPlan().
				All(cmd.Context())
			if err != nil {
				return errcodes.New(errcodes.Internal, "fetch task hits").WithCause(err)
			}
			byID := make(map[string]*ent.Task, len(rows))
			for _, r := range rows {
				byID[r.ID] = r
			}

			result := make([]entitySearchHit, 0, len(hits))
			for _, id := range ids {
				t, ok := byID[id]
				if !ok {
					continue
				}
				result = append(result, entitySearchHit{
					ID:      t.ID,
					Score:   scores[id],
					Snippet: snippets[id],
					Row: map[string]any{
						"title":    t.Title,
						"body":     derefStr(t.Body),
						"status":   string(t.Status),
						"priority": string(t.Priority),
						"due_at":   derefTime(t.DueAt),
					},
					Relations: map[string]any{
						"tasklist": briefIfPresent(t.Edges.Tasklist),
						"mission":  briefIfPresentMission(t.Edges.Mission),
						"plan":     briefIfPresentPlan(t.Edges.Plan),
					},
				})
			}

			printSearchEnvelope(f.jsonOutput, constants.EntityTask.SearchKind(), args[0], result,
				func(h entitySearchHit) string {
					row := h.Row.(map[string]any)
					return fmt.Sprintf("%s [%s] %s",
						h.ID, row["status"], row["title"])
				})
			return nil
		},
	}
	bindEntitySearchFlags(cmd, &f)
	return cmd
}

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

type taskEditFlags struct {
	commonFlags
	title           string
	body            string
	priority        string
	status          string
	due             string
	clearDue        bool
	tasklist        string
	mission         string
	clearMission    bool
	plan            string
	clearPlan       bool
	assignedTo      string
	clearAssignedTo bool
}

func newTaskEditCommand() *cobra.Command {
	f := &taskEditFlags{}
	cmd := &cobra.Command{
		Use:   "edit <id|T-N>",
		Short: "Edit task fields (only flags you pass are applied)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f.commonFlags); err != nil {
				return err
			}
			rctx, client, err := resolveContext(&f.commonFlags)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			t, err := lookupTask(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			upd := client.Task.UpdateOne(t)
			ch := cmd.Flags().Changed
			if ch(constants.FlagTitle) {
				v, err := textnorm.Normalize(f.title)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
				}
				upd.SetTitle(v)
			}
			if ch(constants.FlagBody) {
				v, err := textnorm.Normalize(f.body)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
				}
				upd.SetBody(v)
			}
			if ch(constants.FlagPriority) {
				upd.SetPriority(taskPriority(f.priority))
			}
			if ch(constants.FlagStatus) {
				v := entTask.Status(f.status)
				if entTask.StatusValidator(v) != nil {
					return errcodes.New(errcodes.InvalidInput, "bad --status "+f.status)
				}
				upd.SetStatus(v)
			}
			if ch(constants.FlagDue) {
				due, err := time.Parse("2006-01-02", f.due)
				if err != nil {
					return errcodes.New(errcodes.InvalidInput, "--due format: YYYY-MM-DD").WithCause(err)
				}
				upd.SetDueAt(due)
			}
			if f.clearDue {
				upd.ClearDueAt()
			}
			if ch(constants.FlagTasklist) {
				upd.SetTasklistID(f.tasklist)
			}
			if ch(constants.FlagMission) {
				upd.SetMissionID(f.mission)
			}
			if f.clearMission {
				upd.ClearMissionID()
			}
			if ch(constants.FlagPlan) {
				upd.SetPlanID(f.plan)
			}
			if f.clearPlan {
				upd.ClearPlanID()
			}
			if ch(constants.FlagAssignedTo) {
				id, err := resolveActorIDFlag(cmd.Context(), client, f.assignedTo)
				if err != nil {
					return err
				}
				upd.SetAssignedToActorID(id)
			}
			if f.clearAssignedTo {
				upd.ClearAssignedToActorID()
			}
			if _, err := upd.Save(cmd.Context()); err != nil {
				return errcodes.New(errcodes.Internal, "update task").WithCause(err)
			}
			fmt.Printf("%s %s updated\n", style.Success("✓"), t.ID)
			return nil
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.title, constants.FlagTitle, "", "new title")
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "new body")
	cmd.Flags().StringVar(&f.priority, constants.FlagPriority, "", "low | medium | high | urgent")
	cmd.Flags().StringVar(&f.status, constants.FlagStatus, "", "todo | in_progress | done | cancelled | blocked")
	cmd.Flags().StringVar(&f.due, constants.FlagDue, "", "due date YYYY-MM-DD")
	cmd.Flags().BoolVar(&f.clearDue, constants.FlagClearDue, false, "remove due date")
	cmd.Flags().StringVar(&f.tasklist, constants.FlagTasklist, "", "move to tasklist_id (tlt_*)")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "attach to mission_id (msn_*)")
	cmd.Flags().BoolVar(&f.clearMission, constants.FlagClearMission, false, "detach from mission")
	cmd.Flags().StringVar(&f.plan, constants.FlagPlan, "", "attach to plan_id (pln_*)")
	cmd.Flags().BoolVar(&f.clearPlan, constants.FlagClearPlan, false, "detach from plan")
	cmd.Flags().StringVar(&f.assignedTo, constants.FlagAssignedTo, "", "actor_id (act_*) to assign to")
	cmd.Flags().BoolVar(&f.clearAssignedTo, constants.FlagClearAssignee, false, "unassign")
	return cmd
}

type taskAddFlags struct {
	commonFlags
	body       string
	priority   string
	due        string
	mission    string
	tasklist   string
	plan       string
	assignedTo string
	createdBy  string
}

func newTaskAddCommand() *cobra.Command {
	f := &taskAddFlags{}
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			return runTaskAdd(cmd.Context(), f, title)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.body, constants.FlagBody, "", "longer description")
	cmd.Flags().StringVar(&f.priority, constants.FlagPriority, "medium", "low | medium | high | urgent")
	cmd.Flags().StringVar(&f.due, constants.FlagDue, "", "due date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "mission_id (msn_*) to attach to")
	cmd.Flags().StringVar(&f.tasklist, constants.FlagTasklist, "", "tasklist_id (tlt_*) — REQUIRED")
	cmd.Flags().StringVar(&f.plan, constants.FlagPlan, "", "plan_id (pln_*) to attach to")
	cmd.Flags().StringVar(&f.assignedTo, constants.FlagAssignedTo, "", "actor_id (act_*) the task is assigned to")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	_ = cmd.MarkFlagRequired(constants.FlagTasklist)
	return cmd
}

func runTaskAdd(ctx context.Context, f *taskAddFlags, title string) error {
	if err := refuseIfReadOnly(&f.commonFlags); err != nil {
		return err
	}
	cleanTitle, err := textnorm.Normalize(title)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "title: "+err.Error())
	}

	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount)
	if err != nil {
		return err
	}

	create := client.Task.Create().
		SetProjectID(projectID).
		SetTitle(cleanTitle).
		SetPriority(taskPriority(f.priority))
	if f.body != "" {
		body, err := textnorm.Normalize(f.body)
		if err != nil {
			return errcodes.New(errcodes.InvalidInput, "body: "+err.Error())
		}
		create.SetBody(body)
	}
	if repoID != "" {
		create.SetRepoID(repoID)
	}
	if f.mission != "" {
		create.SetMissionID(f.mission)
	}
	create.SetTasklistID(f.tasklist)
	if f.plan != "" {
		create.SetPlanID(f.plan)
	}
	if assignedTo, err := resolveActorIDFlag(ctx, client, f.assignedTo); err != nil {
		return err
	} else if assignedTo != "" {
		create.SetAssignedToActorID(assignedTo)
	}
	createdBy, err := resolveActorIDFlag(ctx, client, f.createdBy)
	if err != nil {
		return err
	}
	if createdBy == "" {
		createdBy, err = resolveCurrentActorID(ctx, client)
		if err != nil {
			return err
		}
	}
	create.SetCreatedByActorID(createdBy)
	if f.due != "" {
		t, err := time.Parse("2006-01-02", f.due)
		if err != nil {
			return errcodes.New(errcodes.InvalidInput, "--due format: YYYY-MM-DD").WithCause(err)
		}
		create.SetDueAt(t)
	}

	t, err := create.Save(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "create task").WithCause(err)
	}
	fmt.Printf("%s %s %s — %s [%s]\n",
		style.Success("✓"), t.ID, style.Code(t.ID), cleanTitle, f.priority)
	return nil
}

type taskListFlags struct {
	commonFlags
	status     string
	mission    string
	all        bool
	jsonOutput bool
}

func newTaskListCommand() *cobra.Command {
	f := &taskListFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskList(cmd.Context(), f)
		},
	}
	bindCommonFlags(cmd, &f.commonFlags)
	cmd.Flags().StringVar(&f.status, constants.FlagStatus, "", "filter: todo | in_progress | done | cancelled | blocked (empty = default open set, see --all)")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "filter by mission_id")
	cmd.Flags().BoolVar(&f.all, constants.FlagAll, false, "include done + cancelled tasks (default: hide them)")
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output")
	return cmd
}

func runTaskList(ctx context.Context, f *taskListFlags) error {
	rctx, client, err := resolveContext(&f.commonFlags)
	if err != nil {
		return err
	}
	defer client.Close()

	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}

	q := client.Task.Query().Where(entTask.ProjectID(projectID))
	if f.status != "" {
		q = q.Where(entTask.StatusEQ(taskStatus(f.status)))
	} else if !f.all {
		// Default: hide done + cancelled. --all overrides; --status= overrides
		q = q.Where(entTask.StatusNotIn(entTask.StatusDone, entTask.StatusCancelled))
	}
	if f.mission != "" {
		q = q.Where(entTask.MissionID(f.mission))
	}
	rows, err := q.Order(ent.Asc(entTask.FieldID)).All(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "list tasks").WithCause(err)
	}

	if f.jsonOutput {
		out := make([]taskBrief, 0, len(rows))
		for _, t := range rows {
			out = append(out, briefFromTask(t))
		}
		printJSON(constants.KindTaskList, out, len(out))
		return nil
	}

	if len(rows) == 0 {
		fmt.Println(style.Muted("(no tasks)"))
		return nil
	}
	for _, t := range rows {
		statusBadge := taskStatusStyle(string(t.Status))
		due := ""
		if t.DueAt != nil {
			due = " due:" + t.DueAt.Format("2006-01-02")
		}
		fmt.Printf("%s T-%-3s %s [%s]%s\n",
			statusBadge, t.ID, t.Title, t.Priority, due)
	}
	return nil
}

func newTaskStartCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "start <id>",
		Short: "Mark task as in_progress",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateTaskStatus(cmd.Context(), &f, args[0], entTask.StatusInProgress, "started")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newTaskDoneCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark task as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateTaskStatus(cmd.Context(), &f, args[0], entTask.StatusDone, "completed")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newTaskCancelCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Mark task as cancelled",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateTaskStatus(cmd.Context(), &f, args[0], entTask.StatusCancelled, "cancelled")
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
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
	case entTask.StatusDone:
		upd.SetCompletedAt(now)
	}
	if _, err := upd.Save(ctx); err != nil {
		return errcodes.New(errcodes.Internal, "update task").WithCause(err)
	}
	fmt.Printf("%s %s %s\n", style.Success("✓"), t.ID, label)
	return nil
}

func newTaskShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rctx, client, err := resolveContext(&f)
			if err != nil {
				return err
			}
			defer client.Close()
			projectID, err := resolveProjectID(cmd.Context(), client, rctx.ProjectID)
			if err != nil {
				return err
			}
			t, err := lookupTask(cmd.Context(), client, projectID, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				printJSON(constants.KindTaskShow, fullFromTask(t), 0)
				return nil
			}
			fmt.Printf("%s %s\n", t.ID, style.Code(t.ID))
			fmt.Printf("  title:    %s\n", t.Title)
			fmt.Printf("  status:   %s\n", t.Status)
			fmt.Printf("  priority: %s\n", t.Priority)
			if t.DueAt != nil {
				fmt.Printf("  due:      %s\n", t.DueAt.Format("2006-01-02"))
			}
			if t.MissionID != nil {
				fmt.Printf("  mission:  %s\n", *t.MissionID)
			}
			if t.Body != nil && *t.Body != "" {
				fmt.Println()
				fmt.Println(*t.Body)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	return cmd
}

// fullFromTask projects an ent.Task → taskFull JSON shape
func fullFromTask(t *ent.Task) taskFull {
	out := taskFull{
		ID:        t.ID,
		Title:     t.Title,
		Status:    string(t.Status),
		Priority:  string(t.Priority),
		ProjectID: t.ProjectID,
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
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
