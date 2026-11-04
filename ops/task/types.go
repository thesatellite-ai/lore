package task

import (
	"github.com/thesatellite-ai/lore/engine"
	"time"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/timeentry"
	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the task operations. Struct tags are the op
// contract (op/fields.go): `json` is the REST and --json name, `arg`/`flag`
// the CLI spelling, `desc` the help text. Outputs keep v0.1.17's --json
// envelope (`{schema_version, kind, count, data}`, saas/cmd/cli
// json_helper.go at 4e6ed1c: taskBrief for lists, taskFull for show and
// add), so scripts written against v0.1.17 keep working.

// Envelope kinds (v0.1.17 saas/pkg/constants/envelopes.go).
const (
	KindList   = "task.list"
	KindShow   = "task.show"
	KindSearch = "task.search"
)

// AddIn is the input of task.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags
	// Title is v0.1.17's positional <title> (quote a title of several
	// words: an op argument is one value).
	Title      string `json:"title,omitempty" arg:"0" rest:"true" desc:"the task title (several words are joined; omitted with --template)"`
	Body       string `json:"body,omitempty" flag:"body" desc:"longer description" ui:"widget=markdown"`
	Priority   string `json:"priority,omitempty" flag:"priority" desc:"low | medium | high | urgent (default medium)"`
	Commitment string `json:"commitment,omitempty" flag:"commitment" desc:"accepted | proposed | someday (default accepted)"`
	DeferUntil string `json:"defer_until,omitempty" flag:"defer-until" desc:"snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM); hides from active list until then"`
	Due        string `json:"due,omitempty" flag:"due" desc:"due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)"`
	Mission    string `json:"mission,omitempty" flag:"mission" desc:"mission_id (msn_*) to attach to"`
	// Tasklist empty means the project's "default" list (created on
	// demand, v0.1.17 coordinator answer Q1).
	Tasklist   string `json:"tasklist,omitempty" flag:"tasklist" desc:"tasklist_id (tlt_*); default: the project's default list (created on demand)"`
	Plan       string `json:"plan,omitempty" flag:"plan" desc:"plan_id (pln_*) to attach to"`
	AssignedTo string `json:"assigned_to,omitempty" flag:"assigned-to" desc:"actor_id (act_*) the task is assigned to"`
	CreatedBy  string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id (act_*); default: the row's created_by is the current identity"`
	// After are --after dependencies: <id>[:done|:merged], repeatable or
	// comma separated.
	After   []string      `json:"after,omitempty" flag:"after" desc:"dependencies: <id>[:done|:merged][,…] (default: done in the same repo, merged across repos)"`
	Timebox time.Duration `json:"timebox,omitempty" flag:"timebox" desc:"time box, e.g. 60m (active lease time summed across holders)"`
	Role    string        `json:"role,omitempty" flag:"role" desc:"plan role matched by task next --role (e.g. coordinator)"`
	Key     string        `json:"key,omitempty" flag:"key" desc:"idempotency key: a re-run with the same key (in the same mission) adds nothing"`
	// Assign is v0.1.17's --assign @handle (a person sets the assignee, a
	// team the assigned team; team.go). Ref pointers are refs.go.

	Assign []string `json:"assign,omitempty" flag:"assign" desc:"assign to @handle or an actor; repeatable: the assignees, the first is the lead (a team sets the assigned team)"`
	// Lead names the one owner of the outcome (TSQ5); joins the assignees.
	Lead string `json:"lead,omitempty" flag:"lead" desc:"the lead (@handle or actor); added to the assignees if missing"`
	// Estimate is in the tasks.estimate_unit unit (TSQ4, 9.1).
	Estimate string   `json:"estimate,omitempty" flag:"estimate" desc:"estimate: a number >= 0 (2 decimals) in the project's unit (tasks.estimate_unit); hours also take 90m, days 2d"`
	Ref      []string `json:"ref,omitempty" flag:"ref" desc:"a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
	Relation string   `json:"relation,omitempty" flag:"relation" desc:"the relation each --ref records (snake_case; default mentions)"`
	To       string   `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Repo     string   `json:"repo,omitempty" flag:"repo" desc:"scope to a repo of the project (mount name or rep_ id)"`
	Project  string   `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	// Parent makes the task a sub-task (TASK 6.3); it inherits the
	// parent's mission, tasklist, plan, repo, commitment, priority and team
	// unless those flags are given (6.5).
	Parent       string `json:"parent,omitempty" flag:"parent" desc:"the parent task (tsk_…): add as a sub-task, inheriting mission, tasklist, plan, repo, commitment, priority"`
	ReopenParent bool   `json:"reopen_parent,omitempty" flag:"reopen-parent" desc:"the parent is done or cancelled: reopen it to in_progress in the same write"`
	// Recur makes the task recurring (TASK 10.2); finishing it creates
	// the next instance.
	Recur string `json:"recur,omitempty" flag:"recur" desc:"recurrence: every day|weekday|week|month|quarter|year, every N days|weeks|months, every mon[,thu], every month on N|last, every! N days|weeks (from completion); optional until <date> | for N"`
	// Template applies a task template (TASK 11.3) with --arg values.
	Template string   `json:"template,omitempty" flag:"template" desc:"apply this task template (its name or ttp_ id); the title comes from the template"`
	Args     []string `json:"args,omitempty" flag:"arg" desc:"a template argument name=value (repeatable)"`
	DryRun   bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --template: show the tasks that would be added, write nothing"`
}

// Inherited is what a new sub-task copied from its parent (6.5: the output
// lists it, so nothing is silent).
type Inherited struct {
	MissionID  string `json:"mission_id,omitempty"`
	TasklistID string `json:"tasklist_id,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	RepoID     string `json:"repo_id,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Commitment string `json:"commitment,omitempty"`
	Team       string `json:"assigned_team,omitempty"`
}

// AddOut is the output of task.add: the new (or, with --key, the
// existing) task in v0.1.17's task.show envelope, plus the write details.
type AddOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Data          Full      `json:"data"`
	Store         string    `json:"store,omitempty"`
	Warnings      []Warning `json:"warnings,omitempty"`
	// Parent and Inherited are set for a sub-task (6.3).
	Parent    string    `json:"parent_id,omitempty"`
	Inherited Inherited `json:"inherited,omitzero"`
	// Template lists the tasks a --template --dry-run would add.
	Template []TemplateAdded `json:"template,omitempty"`
	// Human is the v0.1.17 human line.
	Human string `json:"-"`
	// existed: --key found the task already there (template apply).
	existed bool
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Brief is v0.1.17's taskBrief (task list --json, mission/tasklist/plan
// eager loads).
type Brief struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	Commitment    string `json:"commitment"`
	DeferredUntil string `json:"deferred_until,omitempty"`
	DueAt         string `json:"due_at,omitempty"`
	MissionID     string `json:"mission_id,omitempty"`
	// ParentID is the parent task (6.2); Rollup the derived progress of an
	// umbrella (6.6, absent for a task without sub-tasks).
	ParentID string          `json:"parent_id,omitempty"`
	Rollup   tasktree.Rollup `json:"rollup,omitzero"`
	// Estimate and its subtree rollup (9.2, estimateInfo).
	Estimate        string `json:"estimate,omitempty"`
	EstimateUnit    string `json:"estimate_unit,omitempty"`
	EstimateSubtree string `json:"estimate_subtree,omitempty"`
	Unestimated     int    `json:"unestimated,omitempty"`
	// Lead is the lead (assigned_to_actor_id, 14.2).
	Lead string `json:"lead,omitempty"`
	// SLA is ok | at_risk | breached when the priority has an SLA (18.4).
	SLA string `json:"sla,omitempty"`
	// Store is set for a task of a store other than the repo's own.
	Store store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Line is the v0.1.17 human list line.
	Line string `json:"-"`
	// base is Line without the store label (withState inserts there).
	base string
}

// Full is v0.1.17's taskFull (task show --json).
type Full struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Body          string `json:"body,omitempty"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	Commitment    string `json:"commitment"`
	DeferredUntil string `json:"deferred_until,omitempty"`
	DueAt         string `json:"due_at,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	MissionID     string `json:"mission_id,omitempty"`
	TaskListID    string `json:"tasklist_id,omitempty"`
	PlanID        string `json:"plan_id,omitempty"`
	ProjectID     string `json:"project_id"`
	RepoID        string `json:"repo_id,omitempty"`
	// Tree fields (6.2, 8.3) and the derived rollup (6.6).
	ParentID    string          `json:"parent_id,omitempty"`
	Rank        string          `json:"rank,omitempty"`
	CloseReason string          `json:"close_reason,omitempty"`
	Rollup      tasktree.Rollup `json:"rollup,omitzero"`
	// Estimate, subtree rollup and tracked time (9.3, estimateInfo).
	Estimate        string  `json:"estimate,omitempty"`
	EstimateUnit    string  `json:"estimate_unit,omitempty"`
	EstimateSubtree string  `json:"estimate_subtree,omitempty"`
	EstimateDone    string  `json:"estimate_done,omitempty"`
	EstimateOpen    string  `json:"estimate_open,omitempty"`
	Unestimated     int     `json:"unestimated,omitempty"`
	TrackedSeconds  int64   `json:"tracked_seconds,omitempty"`
	Ratio           float64 `json:"ratio,omitempty"`
	// People (14.3): "lead" equals assigned_to_actor_id.
	Lead      string   `json:"lead,omitempty"`
	Assignees []string `json:"assignees,omitempty"`
	Watchers  []string `json:"watchers,omitempty"`
	// Checklist is the live checklist (7.3).
	Checklist []ChecklistItemOut `json:"checklist,omitempty"`
	// PeopleLine / EstimateLine are the human show lines.
	PeopleLine   string `json:"-"`
	EstimateLine string `json:"-"`
	// Tree is the subtree, depth first, for show --tree.
	Tree []TreeLine `json:"tree,omitempty"`
	// Why / Who / Relations are the show --why / --who / --relations
	// blocks; nil without the flag.
	Why       Why           `json:"why,omitzero"`
	Who       Who           `json:"who,omitzero"`
	Relations ShowRelations `json:"relations,omitzero"`
	// TreeWarnings are E_PARENT_CYCLE warnings of the merged tree (6.13).
	TreeWarnings []Warning `json:"tree_warnings,omitempty"`
	CreatedAt    string    `json:"created_at"`
	// TagIDs is new in 0.1.18 (O11; absent when untagged).
	TagIDs []string        `json:"tag_ids,omitempty"`
	Store  store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Refs are the task's --ref pointers (new in the show output; v0.1.17
	// wrote them but never printed them).
	Refs []RefOut `json:"refs,omitempty"`
	// TimeEntries is the task's closed time spans (model/timeentry), shown
	// by --json; absent when none.
	TimeEntries []timeentry.Entry `json:"time_entries,omitempty"`
	// TimeLine is the human "time:" line (TimeLine); "" when none.
	TimeLine string `json:"-"`
	// DoneBy / Detail feed the human show only.
	DoneBy string `json:"-"`
	// Notes are the task's note lines, oldest first (notesOn).
	Notes []string `json:"-"`
	// Detail is the host's coordination view of a store task
	// (ShowDetailer); zero when the host added nothing.
	Detail ShowDetail `json:"-"`
}

// TreeLine is one task of a show --tree subtree.
type TreeLine struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Rank   string `json:"rank,omitempty"`
	// Depth is 1 for a direct child.
	Depth int `json:"depth"`
}

// ListIn is the input of task.list.
type ListIn struct {
	Status          string `json:"status,omitempty" flag:"status" desc:"filter: todo | in_progress | done | cancelled | blocked (empty = default active set, see --all)"`
	Commitment      string `json:"commitment,omitempty" flag:"commitment" desc:"filter: accepted | proposed | someday"`
	Mission         string `json:"mission,omitempty" flag:"mission" desc:"filter by mission_id"`
	Assignee        string `json:"assignee,omitempty" flag:"assignee" desc:"filter by assignee: actor_id (act_*)"`
	All             bool   `json:"all,omitempty" flag:"all" desc:"show everything: done/cancelled + proposed/someday + deferred"`
	IncludeProposed bool   `json:"include_proposed,omitempty" flag:"include-proposed" desc:"also include commitment=proposed"`
	IncludeSomeday  bool   `json:"include_someday,omitempty" flag:"include-someday" desc:"also include commitment=someday"`
	IncludeDeferred bool   `json:"include_deferred,omitempty" flag:"include-deferred" desc:"also include deferred (deferred_until in the future)"`
	// Repo and AllRepos are accepted for v0.1.17 scripts: v0.1.17's task
	// list filtered by project only.
	Repo     string `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts (task list shows every repo's tasks)"`
	AllRepos bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"every repo of the project (the default)"`
	Project  string `json:"project,omitempty" flag:"project" desc:"only tasks of this project (prj_ id or name)"`
	Store    string `json:"store,omitempty" flag:"store" desc:"only tasks of this store (default every attached store)"`
	// Mine and ClaimedBy read claims on the signals ref through the
	// host's work.Team (team.go).
	Mine      bool   `json:"mine,omitempty" flag:"mine" desc:"assigned to me or my team, and unclaimed or claimed by me"`
	ClaimedBy string `json:"claimed_by,omitempty" flag:"claimed-by" desc:"only tasks claimed right now by @handle (or actor id)"`
	// Where is the one filter grammar (XQ7, logic/filter): its terms AND
	// with every flag above, and --all/--include-* still pick the base set.
	Where []string `json:"where,omitempty" flag:"where" desc:"filter on a task field: status:todo,in_progress priority>=high due_at<2026-12-01 (= != ~= > >= < <= ? !?; repeatable, all must match)"`
	// Sort is the one sort grammar (XQ10, logic/sortspec).
	Sort []string `json:"sort,omitempty" flag:"sort" desc:"sort by a task field: <key>, <key>:asc or <key>:desc (repeatable; ties keep the default order)"`
	// Tree filters (TASK 6.3).
	Parent string `json:"parent,omitempty" flag:"parent" desc:"only the direct sub-tasks of this task (tsk_…)"`
	Under  string `json:"under,omitempty" flag:"under" desc:"only the whole subtree under this task (tsk_…)"`
	Top    bool   `json:"top,omitempty" flag:"top" desc:"only top-level tasks"`
	Leaves bool   `json:"leaves,omitempty" flag:"leaves" desc:"only tasks with no open sub-tasks (what can be worked on)"`
	// Lead filters by the one lead (14.3); --assignee matches any assignee.
	Lead string `json:"lead,omitempty" flag:"lead" desc:"only tasks led by this actor (@me, act_ id, stable key or name)"`
	// Series lists one recurrence series (TASK 10.3).
	Series string `json:"series,omitempty" flag:"series" desc:"only the instances of this recurrence series (rcs_…); add --all for closed ones"`
	// Meta and Place are work.MetaFlags without its sort (METADATA 3.7):
	// --sort above is shared, and its meta.<key> entry feeds the filter.
	Meta  []string `json:"meta_where,omitempty" flag:"meta" desc:"filter by metadata: key=value, key!=value, key~=text, key>n, key?, key!? (repeatable, all must match)"`
	Place string   `json:"place,omitempty" flag:"place" desc:"narrow --meta names to one place: repo, project, org or a scope label (project:acme-hub)"`
}

// ViewIn is the input of the triage, someday and deferred views.
type ViewIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only tasks of this project (prj_ id or name)"`
	Repo    string `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts"`
}

// ListOut is the output of task.list and the views: v0.1.17's envelope.
type ListOut struct {
	SchemaVersion int     `json:"schema_version"`
	Kind          string  `json:"kind"`
	Count         int     `json:"count,omitempty"`
	Data          []Brief `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
}

// IDIn is the input of the operations that take one task id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
}

// ShowIn is the input of task.show. From is v0.1.17's `task show --from`:
// read the row from one source instead of whichever attached store holds
// it (an id found in several, or a store task read from a client repo).
type ShowIn struct {
	ID   string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	From string `json:"from,omitempty" flag:"from" desc:"the source of the id: repo | <org> | <org>/<folder>"`
	Tree bool   `json:"tree,omitempty" flag:"tree" desc:"also print the sub-task tree"`
	// TASK 13.3, 8.2, AGENT_IDENTITY 6.2.
	Why       bool `json:"why,omitempty" flag:"why" desc:"explain the urgency score term by term"`
	Who       bool `json:"who,omitempty" flag:"who" desc:"who created and last changed it (and, on the writing machine, with which session)"`
	Relations bool `json:"relations,omitempty" flag:"relations" desc:"show every relation, both directions"`
}

// ShowOut is the output of task.show.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Full   `json:"data"`
}

// EditIn is the input of task.edit. An empty flag leaves its field as it
// is; the --clear-* flags remove one.
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags
	ID string `json:"id,omitempty" arg:"0" desc:"the task id (tsk_…); leave out with --where"`
	// Where / DryRun are the bulk edit (TASK 18.1): every matching task gets
	// the same flags in one bat_ batch, previewed above batch.preview_rows.
	Where         []string      `json:"where,omitempty" flag:"where" desc:"bulk edit: every task matching this filter (the task list --where grammar; repeatable, all must match)"`
	DryRun        bool          `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: list the tasks that would change, write nothing"`
	Title         string        `json:"title,omitempty" flag:"title" desc:"new title"`
	Body          string        `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Priority      string        `json:"priority,omitempty" flag:"priority" desc:"low | medium | high | urgent"`
	Status        string        `json:"status,omitempty" flag:"status" desc:"todo | in_progress | done | cancelled | blocked"`
	Commitment    string        `json:"commitment,omitempty" flag:"commitment" desc:"accepted | proposed | someday"`
	DeferUntil    string        `json:"defer_until,omitempty" flag:"defer-until" desc:"snooze until date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)"`
	ClearDefer    bool          `json:"clear_defer,omitempty" flag:"clear-defer" desc:"un-defer (clear deferred_until)"`
	Due           string        `json:"due,omitempty" flag:"due" desc:"due date (YYYY-MM-DD or a phrase: tomorrow, fri, next fri, in 3d, eom, nov 20, + HH:MM)"`
	ClearDue      bool          `json:"clear_due,omitempty" flag:"clear-due" desc:"remove due date"`
	Tasklist      string        `json:"tasklist,omitempty" flag:"tasklist" desc:"move to tasklist_id (tlt_*)"`
	Mission       string        `json:"mission,omitempty" flag:"mission" desc:"attach to mission_id (msn_*)"`
	ClearMission  bool          `json:"clear_mission,omitempty" flag:"clear-mission" desc:"detach from mission"`
	Plan          string        `json:"plan,omitempty" flag:"plan" desc:"attach to plan_id (pln_*)"`
	ClearPlan     bool          `json:"clear_plan,omitempty" flag:"clear-plan" desc:"detach from plan"`
	AssignedTo    string        `json:"assigned_to,omitempty" flag:"assigned-to" desc:"actor_id (act_*) to assign to"`
	ClearAssignee bool          `json:"clear_assignee,omitempty" flag:"clear-assignee" desc:"unassign"`
	After         []string      `json:"after,omitempty" flag:"after" desc:"dependencies: <id>[:done|:merged][,…] (replaces the list)"`
	Timebox       time.Duration `json:"timebox,omitempty" flag:"timebox" desc:"time box, e.g. 60m"`
	Role          string        `json:"role,omitempty" flag:"role" desc:"plan role matched by task next --role (e.g. coordinator)"`
	Assign        []string      `json:"assign,omitempty" flag:"assign" desc:"set the assignees: @handle or actor, repeatable; the first is the lead (a team sets the assigned team)"`
	// People (TASK 14.3, TSQ5).
	Lead           string   `json:"lead,omitempty" flag:"lead" desc:"make this person the lead (added to the assignees if missing)"`
	AddAssignee    []string `json:"add_assignee,omitempty" flag:"add-assignee" desc:"add an assignee (repeatable); the first one on a task with no lead leads"`
	RemoveAssignee []string `json:"remove_assignee,omitempty" flag:"remove-assignee" desc:"remove an assignee (repeatable); removing the lead needs --lead"`
	AddWatcher     []string `json:"add_watcher,omitempty" flag:"add-watcher" desc:"add a watcher (repeatable)"`
	RemoveWatcher  []string `json:"remove_watcher,omitempty" flag:"remove-watcher" desc:"remove a watcher (repeatable)"`
	// Estimate (TSQ4).
	Estimate      string   `json:"estimate,omitempty" flag:"estimate" desc:"estimate in the project's unit (tasks.estimate_unit)"`
	ClearEstimate bool     `json:"clear_estimate,omitempty" flag:"clear-estimate" desc:"remove the estimate"`
	Ref           []string `json:"ref,omitempty" flag:"ref" desc:"a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
	Relation      string   `json:"relation,omitempty" flag:"relation" desc:"the relation each --ref records (snake_case; default mentions)"`
	// Tree edits (TASK 6.3, 6.8): re-parent (with the subtree), detach,
	// reorder among siblings. --after-sibling because --after is a
	// dependency.
	Parent       string `json:"parent,omitempty" flag:"parent" desc:"move under this task (tsk_…), with its subtree"`
	NoParent     bool   `json:"no_parent,omitempty" flag:"no-parent" desc:"detach: make it a top-level task"`
	Before       string `json:"before,omitempty" flag:"before" desc:"place just before this sibling (tsk_…)"`
	AfterSibling string `json:"after_sibling,omitempty" flag:"after-sibling" desc:"place just after this sibling (tsk_…)"`
	First        bool   `json:"first,omitempty" flag:"first" desc:"place first among its siblings"`
	Last         bool   `json:"last,omitempty" flag:"last" desc:"place last among its siblings"`
	ReopenParent bool   `json:"reopen_parent,omitempty" flag:"reopen-parent" desc:"the parent is closed: reopen it to in_progress in the same write"`
	// Recur sets or changes the rule (TASK 10.3: this and later instances).
	Recur string `json:"recur,omitempty" flag:"recur" desc:"set or change the recurrence rule (applies to this and later instances)"`
}

// DoneIn is the input of task.done.
type DoneIn struct {
	ID       string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Commit   string `json:"commit,omitempty" flag:"commit" desc:"the commit that finished the task"`
	PR       int    `json:"pr,omitempty" flag:"pr" desc:"the pull request that finished the task"`
	Lesson   string `json:"lesson,omitempty" flag:"lesson" desc:"save a lesson learned (a memory pointing at the task)"`
	NoLesson bool   `json:"no_lesson,omitempty" flag:"no-lesson" desc:"skip the lesson reminder"`
	Children string `json:"children,omitempty" flag:"children" desc:"open sub-tasks: done | cancel them in the same write (required when there are any)"`
	// TSQ14: unticked checklist items refuse the close (E_OPEN_ITEMS).
	TickAll bool `json:"tick_all,omitempty" flag:"tick-all" desc:"unticked checklist items: tick them in the same write"`
	Force   bool `json:"force,omitempty" flag:"force" desc:"close even with unticked checklist items"`
}

// CancelIn is the input of task.cancel.
type CancelIn struct {
	ID       string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Reason   string `json:"reason,omitempty" flag:"reason" desc:"close reason: cancelled (default) | duplicate | wont_do | obsolete | stale"`
	Children string `json:"children,omitempty" flag:"children" desc:"open sub-tasks: cancel | done them in the same write (required when there are any)"`
}

// SplitIn is the input of task.split (TASK 6.3).
type SplitIn struct {
	ID     string   `json:"id" arg:"0" desc:"the task to split (tsk_…)"`
	Titles []string `json:"titles,omitempty" flag:"title" desc:"a sub-task title; repeatable, one sub-task each, in order"`
	From   string   `json:"from,omitempty" flag:"from" desc:"read titles from a file (- = stdin): one per line, or YAML: a list of titles or of {title, priority, commitment, body}"`
	Key    string   `json:"key,omitempty" flag:"key" desc:"idempotency key: child n gets <key>/<n>, so a re-run adds nothing"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"show what would be added, write nothing"`
}

// SplitAdded is one sub-task of a split.
type SplitAdded struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	Rank  string `json:"rank"`
}

// SplitOut is the output of task.split.
type SplitOut struct {
	ParentID  string       `json:"parent_id"`
	Added     []SplitAdded `json:"added"`
	Inherited Inherited    `json:"inherited,omitzero"`
	DryRun    bool         `json:"dry_run,omitempty"`
	Batch     string       `json:"batch,omitempty"`
	Human     string       `json:"-"`
}

// Text implements op.Texter.
func (o SplitOut) Text() string { return o.Human }

// DoneOut is the output of task.done: the write, and the lesson memory
// when --lesson saved one.
type DoneOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Lesson   string    `json:"lesson_id,omitempty"`
	Warnings []Warning `json:"warnings,omitempty"`
	// Parent and Next are the parent auto-close answer (6.7, TSQ2).
	Parent ParentNote `json:"parent,omitzero"`
	Next   Next       `json:"next,omitzero"`
	// OpenItemsAtClose is how many checklist items were unticked when
	// --force closed the task (TSQ14).
	OpenItemsAtClose int `json:"open_items_at_close,omitempty"`
	// Recur is the next instance of a recurring task (TASK 10.3).
	Recur RecurNext `json:"recur,omitzero"`
	Human string    `json:"-"`
}

// SearchIn is the input of task.search.
type SearchIn struct {
	Query string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	// All includes proposed/someday/deferred/done/cancelled hits.
	All             bool   `json:"all,omitempty" flag:"all" desc:"include proposed/someday/deferred/done/cancelled hits (default: active only)"`
	Limit           int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Repo            string `json:"repo,omitempty" flag:"repo" desc:"include this repo's tasks (mount name or rep_ id)"`
	NoInherit       bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly      bool   `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos        bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	IncludeArchived bool   `json:"include_archived,omitempty" flag:"include-archived" desc:"accepted for v0.1.17 scripts (tasks have no archive)"`
	Project         string `json:"project,omitempty" flag:"project" desc:"only tasks of this project (prj_ id or name)"`
	// Columns restricts the match to some full-text fields (v0.1.17
	// --column; tasks index title and body).
	Columns []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable; tasks have: title, body)"`
}
