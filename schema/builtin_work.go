package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

// Work tables (phase 4 group 3): tasks, tasklists, missions and plans. The
// columns are v0.1.17's (dbent/schema/{task,tasklists,mission,plans}.go),
// so a v0.1.17 row file reads unchanged; id, created_at and updated_at are
// envelope fields.
const (
	TableTasks     = "tasks"
	TableTasklists = "task_lists"
	TableMissions  = "missions"
	TablePlans     = "plans"
	// TableTaskViews is the saved task views (TASK 16.2): a snake_case name
	// and filter_json {where, sort, group_by}, read by list/board --view.
	TableTaskViews = "task_views"
)

// Task view fields (TASK 16.2).
const (
	FieldTaskViewName   = "name"
	FieldTaskViewFilter = "filter_json"
)

// Cache-only join tables filled from the work tables' tag_ids (O11):
// v0.1.17's entity_tags could tag any row, and its tag usage count read
// tasks and missions by name.
const (
	JoinTaskTags     = "task_tags"
	JoinTasklistTags = "tasklist_tags"
	JoinMissionTags  = "mission_tags"
	JoinPlanTags     = "plan_tags"
)

// Task statuses (dbent/schema/enums.go TaskStatus), in v0.1.17 order.
const (
	TaskStatusTodo       = "todo"
	TaskStatusInProgress = "in_progress"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"
	TaskStatusBlocked    = "blocked"
)

// TaskStatuses lists the task statuses in v0.1.17 order.
func TaskStatuses() []string {
	return []string{TaskStatusTodo, TaskStatusInProgress, TaskStatusDone, TaskStatusCancelled, TaskStatusBlocked}
}

// Task priorities (TaskPriority).
const (
	TaskPriorityLow    = "low"
	TaskPriorityMedium = "medium"
	TaskPriorityHigh   = "high"
	TaskPriorityUrgent = "urgent"
)

// TaskPriorities lists the priorities, lowest first (v0.1.17 order).
func TaskPriorities() []string {
	return []string{TaskPriorityLow, TaskPriorityMedium, TaskPriorityHigh, TaskPriorityUrgent}
}

// Task commitments (TaskCommitment, TASK_COMMITMENT_SPEC.md): is this
// agreed work? Orthogonal to status.
const (
	TaskCommitmentAccepted = "accepted"
	TaskCommitmentProposed = "proposed"
	TaskCommitmentSomeday  = "someday"
)

// TaskCommitments lists the commitments in v0.1.17 order.
func TaskCommitments() []string {
	return []string{TaskCommitmentAccepted, TaskCommitmentProposed, TaskCommitmentSomeday}
}

// Task close reasons (TASK_SYSTEM_DESIGN 8.3): why a closed task closed.
// done sets completed, cancel cancelled by default. Old rows have none
// and read as completed when done, cancelled when cancelled.
const (
	TaskCloseCompleted = "completed"
	TaskCloseDuplicate = "duplicate"
	TaskCloseWontDo    = "wont_do"
	TaskCloseObsolete  = "obsolete"
	TaskCloseStale     = "stale"
	TaskCloseCancelled = "cancelled"
	// TaskCloseSkipped is `lore task recur skip` (TASK 10.3): this
	// instance was not done, the series goes on.
	TaskCloseSkipped = "skipped"
)

// TaskCloseReasons lists the close reasons (8.3 order).
func TaskCloseReasons() []string {
	return []string{TaskCloseCompleted, TaskCloseDuplicate, TaskCloseWontDo, TaskCloseObsolete, TaskCloseStale, TaskCloseCancelled, TaskCloseSkipped}
}

// Task tree fields (TASK_SYSTEM_DESIGN 6.2, DQ5): a row with either is part
// of the task_tree feature (T14).
const (
	FieldTaskParent      = "parent_id"
	FieldTaskRank        = "rank"
	FieldTaskCloseReason = "close_reason"
	// FieldTaskRecur is the recurrence of a task (TASK 10.3, option C): one
	// JSON object {rule, series, index, until, stopped}, copied forward to
	// each next instance. Absent on a task that does not recur.
	FieldTaskRecur = "recur"
)

// Checklist, estimate and people fields (TASK_SYSTEM_DESIGN 7, 9, 14).
const (
	// FieldTaskChecklist is the list of checklist items (model/checklist),
	// merged by item id (7.2).
	FieldTaskChecklist = "checklist"
	// FieldTaskEstimate is the estimate as a decimal text literal, so 3,
	// 3.0 and 3.00 round-trip as typed (TSQ4, 9.2); the unit is the
	// tasks.estimate_unit policy.
	FieldTaskEstimate = "estimate"
	// FieldTaskAssignees is everyone responsible, the lead included; the
	// lead stays in assigned_to_actor_id for old readers (TSQ5, 14.2).
	FieldTaskAssignees = "assignees"
	// FieldTaskWatchers is who gets told about changes (14).
	FieldTaskWatchers = "watchers"
	// FieldTaskHistory is the capped activity history of tracked fields
	// (TASK 17, TSQ10): append-only entries merged by entry id (the_…).
	FieldTaskHistory = "history"
)

// Task gates (TaskGate, LORE_TEAM_SPEC 7.4): a human approval step.
const (
	TaskGateRequired  = "required"
	TaskGateRequested = "requested"
	TaskGateApproved  = "approved"
)

// TaskGates lists the gate states in v0.1.17 order.
func TaskGates() []string { return []string{TaskGateRequired, TaskGateRequested, TaskGateApproved} }

// Mission statuses (MissionStatus).
const (
	MissionStatusActive    = "active"
	MissionStatusPaused    = "paused"
	MissionStatusDone      = "done"
	MissionStatusCancelled = "cancelled"
)

// MissionStatuses lists the mission statuses in v0.1.17 order.
func MissionStatuses() []string {
	return []string{MissionStatusActive, MissionStatusPaused, MissionStatusDone, MissionStatusCancelled}
}

// Plan statuses (TSQ9: v0.1.17's free `status_str` becomes `status` with
// this closed set, through the format migration).
const (
	PlanStatusDraft      = "draft"
	PlanStatusApproved   = "approved"
	PlanStatusInProgress = "in_progress"
	PlanStatusDone       = "done"
	PlanStatusDropped    = "dropped"
)

// PlanStatuses lists the plan statuses (TSQ9 order).
func PlanStatuses() []string {
	return []string{PlanStatusDraft, PlanStatusApproved, PlanStatusInProgress, PlanStatusDone, PlanStatusDropped}
}

// FieldPlanStatusStr is v0.1.17's plans.status_str. It stays declared
// (internal, optional) only so a `_v: 1` plan row still validates; a write
// through the core clears it and sets status (TSQ9). Never set by new code.
const FieldPlanStatusStr = "status_str"

// workDefs returns the built-in task, tasklist, mission and plan tables.
func workDefs() []TableDef {
	return []TableDef{
		Table(TableTasks, ids.PrefixTask, Synced(), FTS("title", "body"), Description("a unit of work"),
			Ref("project_id", TableProjects, Required()),
			Ref("repo_id", TableRepos),
			Text("title", Required()),
			Text("body", WithWidget(WidgetMarkdown)),
			Text("status", Required(), OneOf(TaskStatuses()...)),
			Text("priority", Required(), OneOf(TaskPriorities()...)),
			Text("commitment", Required(), OneOf(TaskCommitments()...)),
			DateTime("deferred_until"),
			DateTime("due_at"),
			DateTime("started_at"),
			DateTime("completed_at"),
			Ref("mission_id", TableMissions),
			Ref("tasklist_id", TableTasklists),
			Ref("plan_id", TablePlans),
			Ref("created_by_actor_id", TableActors),
			Ref("assigned_to_actor_id", TableActors),
			// Team fields (LORE_TEAM_SPEC 7, 10 C14); all optional so rows
			// written before the team feature stay valid.
			Text("assigned_team", Describe("team id (tem_…) when --assign named a team")),
			// RQ5 / OV12 leftover: concurrent edits of these sets union instead of newest-wins.
			JSON("blocked_by", MergeBy(MergeSetUnion), Describe("--after edges: [{task_id, project_id?, until}]")),
			Number("claim_epoch", Internal()),
			Number("attempts", Internal()),
			JSON("claim_log", Internal()),
			// time_entries: closed spans of work (model/timeentry), union-merged
			// by entry id; internal so client repos get no time data (TQ3).
			JSON("time_entries", Internal(), MergeBy(MergeListByID),
				Describe("closed time spans [{id, kind, source, actor_id, start, end, seconds, …}] (TIME_TRACKING_DESIGN 6.2)")),
			// time_open: open status spans, one per person working on the task
			// (TIME_TRACKING_DESIGN 4.1, D2); same union-by-id merge, internal (TQ3).
			JSON("time_open", Internal(), MergeBy(MergeListByID),
				Describe("open status spans [{id, source, actor_id, start}] (TIME_TRACKING_DESIGN 4.1)")),
			Number("timebox_seconds"),
			Text("gate", OneOf(TaskGates()...)),
			Text("approved_by"),
			DateTime("approved_at"),
			Text("approved_hash", Internal()),
			Text("done_by"),
			Text("done_commit"),
			Number("done_pr"),
			DateTime("merged_at"),
			Text("work_branch"),
			Text("idempotency_key"),
			Text("role"),
			// Sub-tasks (DQ5, TASK 6.2): the parent is stored on the child
			// only (T1: children are a query), rank orders siblings.
			Ref(FieldTaskParent, TableTasks, Describe("the parent task (tsk_…); empty = top level")),
			Text(FieldTaskRank, Describe("order among siblings (fractional index; ties by id)")),
			Text(FieldTaskCloseReason, OneOf(TaskCloseReasons()...), Describe("why a closed task closed (TASK 8.3)")),
			JSON(FieldTaskChecklist, MergeBy(MergeListByID),
				Describe("checklist items [{id, text, done, done_at, done_by, rank, …}] (TASK 7.2)")),
			JSON(FieldTaskRecur, Describe("recurrence {rule, series (rcs_…), index, until, stopped} (TASK 10.3)")),
			Text(FieldTaskEstimate, Describe("estimate, a decimal >= 0 in the tasks.estimate_unit unit (TASK 9)")),
			List(FieldTaskAssignees, value.KindText, MergeBy(MergeSetUnion), Describe("actor ids responsible, the lead (assigned_to_actor_id) included (TASK 14.2)")),
			List(FieldTaskWatchers, value.KindText, MergeBy(MergeSetUnion), Describe("actor ids told about changes (TASK 14)")),
			// TSQ10: last tasks.history_max entries + one summary fold; union by id (17.3).
			JSON(FieldTaskHistory, MergeBy(MergeListByID),
				Describe("activity history [{id (the_…), at, by, field, from, to, note}] of tracked fields (TASK 17.2)")),
			IndexOn("project_id", "status"),
			IndexOn("project_id", FieldTaskParent),
			IndexOn("project_id", "commitment"),
			IndexOn("mission_id"),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("repo", "repo_id", TableRepos, SetNull),
			BelongsToRel("mission", "mission_id", TableMissions, SetNull),
			// DQ6: deleting a tasklist takes its tasks with it (one batch,
			// behind the op's confirm).
			BelongsToRel("tasklist", "tasklist_id", TableTasklists, Cascade),
			BelongsToRel("plan", "plan_id", TablePlans, SetNull),
			// DQ5/TSQ3: children are dependents of their parent; a delete
			// takes the subtree (the op asks first when there is a child).
			SelfRel("parent", FieldTaskParent, Cascade),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
			BelongsToRel("assigned_to", "assigned_to_actor_id", TableActors, TombstoneOnly),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinTaskTags, Cascade),
		),
		// TASK 16.2: v0.1.17's machine-local task_views becomes a synced row so a
		// team shares its views; filter_json keeps the text expression (the
		// parser can improve without rewriting rows).
		Table(TableTaskViews, ids.PrefixTaskView, Synced(), Description("a saved task list filter"),
			Ref("project_id", TableProjects, Required()),
			Text(FieldTaskViewName, Required(), Describe("snake_case view name, unique per project")),
			Text(FieldTaskViewFilter, Describe(`{"where": "...", "sort": "...", "group_by": "..."}`)),
			IndexOn("project_id", FieldTaskViewName),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
		),
		Table(TableTasklists, ids.PrefixTaskList, Synced(), FTS("title", "body", "status_str"), Description("a list that groups tasks"),
			Ref("project_id", TableProjects, Required()),
			Text("title", Required()),
			Text("body", WithWidget(WidgetMarkdown)),
			Text("status_str", Required(), Describe("free-form status (v0.1.17)")),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			HasManyRel("tasks", TableTasks, "tasklist_id"),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinTasklistTags, Cascade),
		),
		Table(TableMissions, ids.PrefixMission, Synced(), FTS("title", "body", "status"), Description("a goal that groups tasks"),
			Ref("project_id", TableProjects, Required()),
			Text("title", Required()),
			Text("body", WithWidget(WidgetMarkdown)),
			Text("status", Required(), OneOf(MissionStatuses()...)),
			DateTime("target_date"),
			// starts_at is new in 0.1.18 (TASK 15, option B: a mission is the
			// "cycle" when it has dates); optional, so no migration.
			DateTime("starts_at", Describe("when the mission's work starts (TASK 15)")),
			DateTime("completed_at"),
			Ref("created_by_actor_id", TableActors),
			List("approvers", value.KindText),
			Text("idempotency_key"),
			// plan_id is new in 0.1.18 (TSQ9: missions link to their plan
			// document); optional, so no migration.
			Ref("plan_id", TablePlans),
			IndexOn("project_id", "status"),
			IndexOn("project_id", "idempotency_key"),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
			BelongsToRel("plan", "plan_id", TablePlans, SetNull),
			HasManyRel("tasks", TableTasks, "mission_id"),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinMissionTags, Cascade),
		),
		Table(TablePlans, ids.PrefixPlan, Synced(), FTS("title", "body", "status"), Description("a plan document"),
			Ref("project_id", TableProjects, Required()),
			Text("title", Required()),
			Text("body", Required(), WithWidget(WidgetMarkdown)),
			Text("status", OneOf(PlanStatuses()...)),
			Text(FieldPlanStatusStr, Internal(), Describe("v0.1.17 free-form status; replaced by status (TSQ9)")),
			Ref("created_by_actor_id", TableActors),
			BelongsToRel("project", "project_id", TableProjects, Restrict),
			BelongsToRel("created_by", "created_by_actor_id", TableActors, TombstoneOnly),
			HasManyRel("tasks", TableTasks, "plan_id"),
			HasManyRel("missions", TableMissions, "plan_id"),
			tagIDs(),
			ManyToManyRel(RelTags, FieldTagIDs, TableTags, JoinPlanTags, Cascade),
		),
	}
}
