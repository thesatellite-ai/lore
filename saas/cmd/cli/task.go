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
	"os"
	"saas/pkg/constants"
	"strings"
	"time"

	"dbent/gen/ent"
	entTask "dbent/gen/ent/task"
	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/identity"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/style"
	"saas/pkg/aicoder/textnorm"

	"github.com/spf13/cobra"
)

func newTaskCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Manage tasks (discrete work items)"}
	cmd.AddCommand(newTaskAddCommand())
	cmd.AddCommand(newTaskListCommand())
	cmd.AddCommand(newTaskTriageCommand())
	cmd.AddCommand(newTaskSomedayCommand())
	cmd.AddCommand(newTaskDeferredCommand())
	cmd.AddCommand(newTaskStartCommand())
	cmd.AddCommand(newTaskDoneCommand())
	cmd.AddCommand(newTaskCancelCommand())
	cmd.AddCommand(newTaskShowCommand())
	cmd.AddCommand(newTaskEditCommand())
	cmd.AddCommand(newTaskSearchCommand())
	cmd.AddCommand(newDeleteCommand(taskDeleteTarget))
	cmd.AddCommand(taskCoordCommands()...)
	return cmd
}

// newTaskSearchCommand wires `lore task search <query>`. Uses the
// shared FTS5 search helper (see search_shared.go) and eager-loads the
// task's parent tasklist + mission + plan plus created_by / assigned_to
// actor refs so consumers don't need follow-up queries
func newTaskSearchCommand() *cobra.Command {
	var f entitySearchFlags
	var includeAll bool
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
			fq := client.Task.Query().Where(entTask.IDIn(ids...))
			if !includeAll {
				// Default: only surface ActiveTask hits (hide proposed/
				// someday/deferred/done/cancelled). --all bypasses
				fq = activeTaskFilter(fq)
			}
			rows, err := fq.
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
	cmd.Flags().BoolVar(&includeAll, constants.FlagAll, false,
		"include proposed/someday/deferred/done/cancelled hits (default: active only)")
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
	commitment      string
	deferUntil      string
	clearDefer      bool
	due             string
	clearDue        bool
	tasklist        string
	mission         string
	clearMission    bool
	plan            string
	clearPlan       bool
	assignedTo      string
	clearAssignedTo bool
	team            teamTaskFlags
	from            string
}

func newTaskEditCommand() *cobra.Command {
	f := &taskEditFlags{}
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit task fields (only flags you pass are applied)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := refuseIfReadOnly(&f.commonFlags); err != nil {
				return err
			}
			if f.from != "" && f.from != toRepo {
				return editTaskFrom(cmd, args[0], f)
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
			if ch(constants.FlagCommitment) {
				c, err := parseCommitment(f.commitment)
				if err != nil {
					return err
				}
				upd.SetCommitment(c)
			}
			if ch(constants.FlagDeferUntil) {
				du, err := parseDeferUntil(f.deferUntil)
				if err != nil {
					return err
				}
				upd.SetDeferredUntil(du)
			}
			if f.clearDefer {
				upd.ClearDeferredUntil()
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
			if ch(flagAfter) {
				deps, err := parseAfter(cmd.Context(), client, projectID, f.team.after, deref(t.RepoID))
				if err != nil {
					return err
				}
				if err := checkNoCycle(cmd.Context(), client, projectID, t.ID, deps); err != nil {
					return err
				}
				if len(deps) == 0 {
					upd.ClearBlockedBy()
				} else {
					upd.SetBlockedBy(deps)
				}
			}
			if ch(flagAssign) {
				a, err := resolveAssign(cmd.Context(), client, projectID, f.team.assign)
				if err != nil {
					return err
				}
				if a.ActorID != "" {
					upd.SetAssignedToActorID(a.ActorID).ClearAssignedTeam()
				} else {
					upd.SetAssignedTeam(a.TeamID).ClearAssignedToActorID()
				}
			}
			if ch(flagTimebox) {
				tb, err := timeboxSeconds(f.team.timebox)
				if err != nil {
					return err
				}
				if tb == 0 {
					upd.ClearTimeboxSeconds()
				} else {
					upd.SetTimeboxSeconds(tb)
				}
			}
			if ch(flagRef) {
				refs, rowRefs, err := parseTaskRefs(f.team.refs)
				if err != nil {
					return err
				}
				if err := checkReferences(cmd.Context(), writeRoute{Repo: true}, rowRefs); err != nil {
					return err
				}
				if err := saveTaskRefs(cmd.Context(), client, projectID, t.ID, refs, false, ""); err != nil {
					return err
				}
			}
			if ch(flagRole) {
				if f.team.role == "" {
					upd.ClearRole()
				} else {
					upd.SetRole(f.team.role)
				}
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
	cmd.Flags().StringVar(&f.commitment, constants.FlagCommitment, "", "accepted | proposed | someday")
	cmd.Flags().StringVar(&f.deferUntil, constants.FlagDeferUntil, "", "snooze until date YYYY-MM-DD")
	cmd.Flags().BoolVar(&f.clearDefer, constants.FlagClearDefer, false, "un-defer (clear deferred_until)")
	cmd.Flags().StringVar(&f.due, constants.FlagDue, "", "due date YYYY-MM-DD")
	cmd.Flags().BoolVar(&f.clearDue, constants.FlagClearDue, false, "remove due date")
	cmd.Flags().StringVar(&f.tasklist, constants.FlagTasklist, "", "move to tasklist_id (tlt_*)")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "attach to mission_id (msn_*)")
	cmd.Flags().BoolVar(&f.clearMission, constants.FlagClearMission, false, "detach from mission")
	cmd.Flags().StringVar(&f.plan, constants.FlagPlan, "", "attach to plan_id (pln_*)")
	cmd.Flags().BoolVar(&f.clearPlan, constants.FlagClearPlan, false, "detach from plan")
	cmd.Flags().StringVar(&f.assignedTo, constants.FlagAssignedTo, "", "actor_id (act_*) to assign to")
	cmd.Flags().BoolVar(&f.clearAssignedTo, constants.FlagClearAssignee, false, "unassign")
	bindTeamTaskFlags(cmd, &f.team, false)
	bindFromFlag(cmd, &f.from)
	return cmd
}

type taskAddFlags struct {
	commonFlags
	body       string
	priority   string
	commitment string
	deferUntil string
	due        string
	mission    string
	tasklist   string
	plan       string
	assignedTo string
	createdBy  string
	team       teamTaskFlags
	to         toFlags
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
	cmd.Flags().StringVar(&f.commitment, constants.FlagCommitment, "", "accepted | proposed | someday (REQUIRED for agent callers)")
	cmd.Flags().StringVar(&f.deferUntil, constants.FlagDeferUntil, "", "snooze until date (YYYY-MM-DD); hides from active list until then")
	cmd.Flags().StringVar(&f.due, constants.FlagDue, "", "due date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "mission_id (msn_*) to attach to")
	cmd.Flags().StringVar(&f.tasklist, constants.FlagTasklist, "", "tasklist_id (tlt_*); default: the project's default list (created on demand)")
	cmd.Flags().StringVar(&f.plan, constants.FlagPlan, "", "plan_id (pln_*) to attach to")
	cmd.Flags().StringVar(&f.assignedTo, constants.FlagAssignedTo, "", "actor_id (act_*) the task is assigned to")
	cmd.Flags().StringVar(&f.createdBy, constants.FlagCreatedBy, "", "actor_id (act_*); defaults to current identity")
	bindTeamTaskFlags(cmd, &f.team, true)
	bindToFlags(cmd, &f.to, false)
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
	// F14: which project the task goes to (this repo, a store project, or
	// personal); for anything but this repo it points f at that project.
	repoName := f.commonFlags.flagRepo // routeWrite clears it for a store project
	here := checkoutProjectID()
	// People handles resolve on the roster BEFORE routing: routeWrite holds
	// the store mirror's lock until the command ends, and the roster read
	// would wait on it.
	preAssign := preResolveHandle(ctx, f.team.assign)
	// A team handle is looked up on the roster too, for the same reason:
	// in a linked client repo the routed project holds no teams, and the
	// TEAM-030 refusal below must still see that @handle is a team.
	var preTeam string
	if preAssign == "" && strings.HasPrefix(strings.TrimSpace(f.team.assign), handlePrefix) {
		if preTeam, err = resolveHandleTeam(ctx, f.team.assign); err != nil {
			return err
		}
	}
	route, err := routeWrite(ctx, &f.commonFlags, f.to, kindTask, cleanTitle)
	if err != nil {
		return err
	}
	// routeWrite checked the title; the body goes to the same repo row, so
	// it is checked before the row exists too (trial F8).
	if err := leakCheckRouteWrite(ctx, route, kindTask, f.body); err != nil {
		return err
	}
	refs, rowRefs, err := parseTaskRefs(f.team.refs)
	if err != nil {
		return err
	}
	if err := checkReferences(ctx, route, rowRefs); err != nil {
		return err
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
	if !route.Repo && repoName != "" {
		if repoID, err = repoIDFor(ctx, client, projectID, repoName, true); err != nil {
			return err
		}
	}

	// Commitment axis (orthogonal to status). Agent callers MUST pass
	// --commitment explicitly — no silent default in either direction
	// (TASK_COMMITMENT_SPEC.md ADR-TASK-1). Non-agent / manual callers
	// fall back to the schema default (accepted)
	// ds:def id=task-commitment-agent-rule-lxfnerf7 owner=@khanakia stability=stable desc="agents must pass --commitment on task add"
	if f.commitment == "" && callerIsAgent() {
		return errcodes.New(errcodes.InvalidInput,
			"--commitment is required for agent callers").
			WithHint("accepted = user asked / starting now; " +
				"proposed = your speculative idea; someday = parking lot")
	}

	if existing, err := findByKey(ctx, client, projectID, f.mission, f.team.key); err != nil {
		return err
	} else if existing != nil {
		if f.team.json {
			printJSON(constants.KindTaskShow, fullFromTask(existing), 0)
			return nil
		}
		fmt.Printf("%s %s — %s (already added with --key %s)\n", style.Success("✓"), style.Code(existing.ID), existing.Title, f.team.key)
		return nil
	}
	// --assign resolves before anything is written (the default task list
	// below, the task): a refused handle must leave the project, and so the
	// store, untouched, because the flush stores whatever was written even
	// when the command fails.
	assign := assignTarget{ActorID: preAssign}
	if preAssign == "" {
		assign, err = resolveAssign(ctx, client, projectID, f.team.assign)
		switch {
		case err != nil && preTeam != "" && errcodes.IsCode(err, errcodes.NotFound):
			// Not in the routed project, but a team on the roster.
			assign = assignTarget{TeamID: preTeam}
		case err != nil:
			return err
		}
	}
	if assign.TeamID != "" && route.Repo && route.Situation != nil && route.Situation.Result.Role == orgresolve.RoleClient {
		return errcodes.New(errcodes.InvalidInput, "a team cannot be assigned in a linked client repo: team names are internal (TEAM-030)").
			WithHint("assign a person (--assign @handle), or keep the task internal with --to <org>")
	}
	if f.tasklist == "" {
		if f.tasklist, err = defaultTasklist(ctx, client, projectID); err != nil {
			return err
		}
	}
	deps, err := parseAfter(ctx, client, projectID, f.team.after, repoID)
	if err != nil {
		return err
	}
	if len(f.team.after) > 0 {
		if err := checkReferences(ctx, route, afterIDs(f.team.after)); err != nil {
			return err
		}
	}
	tb, err := timeboxSeconds(f.team.timebox)
	if err != nil {
		return err
	}

	create := client.Task.Create().
		SetProjectID(projectID).
		SetTitle(cleanTitle).
		SetPriority(taskPriority(f.priority))
	if f.commitment != "" {
		c, err := parseCommitment(f.commitment)
		if err != nil {
			return err
		}
		create.SetCommitment(c)
	}
	if f.deferUntil != "" {
		du, err := parseDeferUntil(f.deferUntil)
		if err != nil {
			return err
		}
		create.SetDeferredUntil(du)
	}
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
	if len(deps) > 0 {
		create.SetBlockedBy(deps)
	}
	if assign.ActorID != "" {
		create.SetAssignedToActorID(assign.ActorID)
	}
	if assign.TeamID != "" {
		create.SetAssignedTeam(assign.TeamID)
	}
	if tb > 0 {
		create.SetTimeboxSeconds(tb)
	}
	if f.team.role != "" {
		create.SetRole(f.team.role)
	}
	if f.team.key != "" {
		create.SetIdempotencyKey(f.team.key)
	}
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
	if err := saveTaskRefs(ctx, client, projectID, t.ID, refs, !route.Repo, here); err != nil {
		return err
	}
	if f.team.json {
		printJSON(constants.KindTaskShow, fullFromTask(t), 0)
		return nil
	}
	label := ""
	if route.Label != "" {
		label = " → " + route.Label
	}
	fmt.Printf("%s %s — %s [%s]%s\n", style.Success("✓"), style.Code(t.ID), cleanTitle, f.priority, label)
	return nil
}

type taskListFlags struct {
	commonFlags
	status          string
	commitment      string
	mission         string
	assignee        string
	all             bool
	includeProposed bool
	includeSomeday  bool
	includeDeferred bool
	jsonOutput      bool
	team            taskListTeamFlags
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
	cmd.Flags().StringVar(&f.status, constants.FlagStatus, "", "filter: todo | in_progress | done | cancelled | blocked (empty = default active set, see --all)")
	cmd.Flags().StringVar(&f.commitment, constants.FlagCommitment, "", "filter: accepted | proposed | someday")
	cmd.Flags().StringVar(&f.mission, constants.FlagMission, "", "filter by mission_id")
	cmd.Flags().StringVar(&f.assignee, constants.FlagAssignee, "", "filter by assignee: actor_id (act_*), "+constants.ActorRefMe+", stable_key or display name")
	cmd.Flags().BoolVar(&f.all, constants.FlagAll, false, "show everything: done/cancelled + proposed/someday + deferred")
	cmd.Flags().BoolVar(&f.includeProposed, constants.FlagIncludeProposed, false, "also include commitment=proposed")
	cmd.Flags().BoolVar(&f.includeSomeday, constants.FlagIncludeSomeday, false, "also include commitment=someday")
	cmd.Flags().BoolVar(&f.includeDeferred, constants.FlagIncludeDeferred, false, "also include deferred (deferred_until in the future)")
	cmd.Flags().BoolVar(&f.jsonOutput, constants.FlagJSON, false, "JSON output")
	bindTaskListTeamFlags(cmd, &f.team)
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
	if repoID, err := resolveRepoID(ctx, client, projectID, rctx.RepoMount); err == nil {
		lazyMergedCheck(ctx, client, projectID, repoID)
	}
	groupMergedCheck(ctx)

	q := client.Task.Query().Where(entTask.ProjectID(projectID))

	// Explicit --commitment is a hard filter (and a loud error if bad)
	if f.commitment != "" {
		c, err := parseCommitment(f.commitment)
		if err != nil {
			return err
		}
		q = q.Where(entTask.CommitmentEQ(c))
	}

	if f.all {
		// Show everything; only an explicit --status still narrows
		if f.status != "" {
			q = q.Where(entTask.StatusEQ(taskStatus(f.status)))
		}
	} else {
		// Status: explicit --status wins, else hide done + cancelled
		if f.status != "" {
			q = q.Where(entTask.StatusEQ(taskStatus(f.status)))
		} else {
			q = q.Where(entTask.StatusNotIn(entTask.StatusDone, entTask.StatusCancelled))
		}
		// Commitment: default accepted-only; widen with --include-* flags.
		// Skipped if an explicit --commitment was already applied
		if f.commitment == "" {
			allowed := []entTask.Commitment{entTask.CommitmentAccepted}
			if f.includeProposed {
				allowed = append(allowed, entTask.CommitmentProposed)
			}
			if f.includeSomeday {
				allowed = append(allowed, entTask.CommitmentSomeday)
			}
			q = q.Where(entTask.CommitmentIn(allowed...))
		}
		// Deferral: hide future-deferred unless asked to include them
		if !f.includeDeferred {
			q = q.Where(entTask.Or(
				entTask.DeferredUntilIsNil(),
				entTask.DeferredUntilLTE(time.Now()),
			))
		}
	}
	if f.mission != "" {
		q = q.Where(entTask.MissionID(f.mission))
	}
	assigneeID := ""
	if f.assignee != "" {
		actorID, err := resolveActorRef(ctx, client, f.assignee)
		if err != nil {
			return err
		}
		q = q.Where(entTask.AssignedToActorID(actorID))
		assigneeID = actorID
	}
	rows, err := q.Order(ent.Asc(entTask.FieldID)).All(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "list tasks").WithCause(err)
	}
	rows, states, err := applyTaskListTeam(ctx, client, &f.team, rows)
	if err != nil {
		return err
	}
	// Store and personal tasks of this repo's sources pass the same filters
	// as the rows above (stores_tasks.go; the predicate mirrors the query).
	ext := externalTasksHere(ctx, taskListPredicate{
		status: f.status, commitment: entTask.Commitment(f.commitment), all: f.all,
		includeProposed: f.includeProposed, includeSomeday: f.includeSomeday, includeDeferred: f.includeDeferred,
		mission: f.mission, assigneeID: assigneeID, now: time.Now(),
	})

	if f.jsonOutput {
		out := make([]taskBrief, 0, len(rows))
		for _, t := range rows {
			out = append(out, briefFromTask(t))
		}
		extJSON := externalTaskJSON(ext)
		all := make([]any, 0, len(out)+len(extJSON))
		for _, b := range out {
			all = append(all, b)
		}
		for _, e := range extJSON {
			all = append(all, e)
		}
		printJSON(constants.KindTaskList, all, len(all))
		return nil
	}

	// Store and personal tasks of this repo's sources, with their label
	// (F20). "(no tasks)" only when no source has any: in a client repo
	// whose work lives in the store it would contradict the rows below it.
	if len(rows) == 0 && len(ext) == 0 {
		printTaskRowsWithState(nil, nil)
		return nil
	}
	if len(rows) > 0 {
		printTaskRowsWithState(rows, states)
	}
	printExternalTasks(os.Stdout, ext)
	return nil
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

// runTaskView is the shared body for the triage / someday / deferred
// read-only views — a project-scoped query with a caller-supplied predicate
func runTaskView(ctx context.Context, f *commonFlags, narrow func(*ent.TaskQuery) *ent.TaskQuery) error {
	rctx, client, err := resolveContext(f)
	if err != nil {
		return err
	}
	defer client.Close()
	projectID, err := resolveProjectID(ctx, client, rctx.ProjectID)
	if err != nil {
		return err
	}
	q := narrow(client.Task.Query().Where(entTask.ProjectID(projectID)))
	rows, err := q.Order(ent.Asc(entTask.FieldID)).All(ctx)
	if err != nil {
		return errcodes.New(errcodes.Internal, "list tasks").WithCause(err)
	}
	printTaskRows(rows)
	return nil
}

func newTaskTriageCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "Show proposed tasks (agent-suggested, not yet committed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskView(cmd.Context(), &f, func(q *ent.TaskQuery) *ent.TaskQuery {
				return q.Where(
					entTask.CommitmentEQ(entTask.CommitmentProposed),
					entTask.StatusNotIn(entTask.StatusDone, entTask.StatusCancelled),
				)
			})
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newTaskSomedayCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "someday",
		Short: "Show someday/maybe tasks (parking lot, no commitment)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskView(cmd.Context(), &f, func(q *ent.TaskQuery) *ent.TaskQuery {
				return q.Where(entTask.CommitmentEQ(entTask.CommitmentSomeday))
			})
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
}

func newTaskDeferredCommand() *cobra.Command {
	var f commonFlags
	cmd := &cobra.Command{
		Use:   "deferred",
		Short: "Show deferred tasks (snoozed; deferred_until in the future)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskView(cmd.Context(), &f, func(q *ent.TaskQuery) *ent.TaskQuery {
				return q.Where(
					entTask.DeferredUntilNotNil(),
					entTask.DeferredUntilGT(time.Now()),
				)
			})
		},
	}
	bindCommonFlags(cmd, &f)
	return cmd
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

func newTaskShowCommand() *cobra.Command {
	var f commonFlags
	var jsonOut, showSignals bool
	var from string
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show task details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from != "" && from != toRepo {
				return showTaskFrom(cmd.Context(), args[0], from, jsonOut)
			}
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
				if from == "" {
					if handled, serr := showStoreTask(cmd.Context(), args[0], jsonOut); handled {
						return serr
					}
				}
				return err
			}
			t = newerTask(cmd.Context(), t)
			if jsonOut {
				printJSON(constants.KindTaskShow, fullFromTask(t), 0)
				return nil
			}
			fmt.Printf("%s\n", style.Code(t.ID))
			fmt.Printf("  title:    %s\n", t.Title)
			fmt.Printf("  status:   %s\n", t.Status)
			fmt.Printf("  priority: %s\n", t.Priority)
			fmt.Printf("  commit:   %s\n", t.Commitment)
			if t.DeferredUntil != nil {
				fmt.Printf("  deferred: %s\n", t.DeferredUntil.Format("2006-01-02"))
			}
			if t.DueAt != nil {
				fmt.Printf("  due:      %s\n", t.DueAt.Format("2006-01-02"))
			}
			if t.MissionID != nil {
				fmt.Printf("  mission:  %s\n", *t.MissionID)
			}
			if t.DoneBy != nil {
				fmt.Printf("  done by:  %s\n", actorName(cmd.Context(), client, *t.DoneBy))
			}
			if t.Body != nil && *t.Body != "" {
				fmt.Println()
				fmt.Println(*t.Body)
			}
			printTaskNotes(cmd.Context(), client, t.ID)
			if showSignals {
				return printTaskSignals(cmd.Context(), t.ID)
			}
			return nil
		},
	}
	bindCommonFlags(cmd, &f)
	cmd.Flags().BoolVar(&jsonOut, constants.FlagJSON, false, "JSON output")
	cmd.Flags().BoolVar(&showSignals, flagSignals, false, "show the claim as seen now (holder, token prefix, expires, renewed_seq)")
	bindFromFlag(cmd, &from)
	return cmd
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
