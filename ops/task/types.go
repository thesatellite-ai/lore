package task

import (
	"time"

	"github.com/thesatellite-ai/lore/model/timeentry"
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
	// Title is v0.1.17's positional <title> (quote a title of several
	// words: an op argument is one value).
	Title      string `json:"title" arg:"0" rest:"true" desc:"the task title (several words are joined)"`
	Body       string `json:"body,omitempty" flag:"body" desc:"longer description" ui:"widget=markdown"`
	Priority   string `json:"priority,omitempty" flag:"priority" desc:"low | medium | high | urgent (default medium)"`
	Commitment string `json:"commitment,omitempty" flag:"commitment" desc:"accepted | proposed | someday (default accepted)"`
	DeferUntil string `json:"defer_until,omitempty" flag:"defer-until" desc:"snooze until date (YYYY-MM-DD); hides from active list until then"`
	Due        string `json:"due,omitempty" flag:"due" desc:"due date (YYYY-MM-DD)"`
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

	Assign  string   `json:"assign,omitempty" flag:"assign" desc:"assign to @handle (a person sets the assignee, a team the assigned team)"`
	Ref     []string `json:"ref,omitempty" flag:"ref" desc:"a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
	To      string   `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Repo    string   `json:"repo,omitempty" flag:"repo" desc:"scope to a repo of the project (mount name or rep_ id)"`
	Project string   `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// AddOut is the output of task.add: the new (or, with --key, the
// existing) task in v0.1.17's task.show envelope, plus the write details.
type AddOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Data          Full      `json:"data"`
	Store         string    `json:"store,omitempty"`
	Warnings      []Warning `json:"warnings,omitempty"`
	// Human is the v0.1.17 human line.
	Human string `json:"-"`
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
	// Store is set for a task of a store other than the repo's own.
	Store string `json:"_source,omitempty"`
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
	CreatedAt     string `json:"created_at"`
	// TagIDs is new in 0.1.18 (O11; absent when untagged).
	TagIDs []string `json:"tag_ids,omitempty"`
	Store  string   `json:"_source,omitempty"`
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
}

// IDIn is the input of the operations that take one task id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
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
	ID            string        `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Title         string        `json:"title,omitempty" flag:"title" desc:"new title"`
	Body          string        `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Priority      string        `json:"priority,omitempty" flag:"priority" desc:"low | medium | high | urgent"`
	Status        string        `json:"status,omitempty" flag:"status" desc:"todo | in_progress | done | cancelled | blocked"`
	Commitment    string        `json:"commitment,omitempty" flag:"commitment" desc:"accepted | proposed | someday"`
	DeferUntil    string        `json:"defer_until,omitempty" flag:"defer-until" desc:"snooze until date YYYY-MM-DD"`
	ClearDefer    bool          `json:"clear_defer,omitempty" flag:"clear-defer" desc:"un-defer (clear deferred_until)"`
	Due           string        `json:"due,omitempty" flag:"due" desc:"due date YYYY-MM-DD"`
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
	Assign        string        `json:"assign,omitempty" flag:"assign" desc:"assign to @handle (a person sets the assignee, a team the assigned team)"`
	Ref           []string      `json:"ref,omitempty" flag:"ref" desc:"a pointer to another row (<id> or <project>:<id>) or a commit (commit:<sha>); repeatable"`
}

// DoneIn is the input of task.done.
type DoneIn struct {
	ID       string `json:"id" arg:"0" desc:"the task id (tsk_…)"`
	Commit   string `json:"commit,omitempty" flag:"commit" desc:"the commit that finished the task"`
	PR       int    `json:"pr,omitempty" flag:"pr" desc:"the pull request that finished the task"`
	Lesson   string `json:"lesson,omitempty" flag:"lesson" desc:"save a lesson learned (a memory pointing at the task)"`
	NoLesson bool   `json:"no_lesson,omitempty" flag:"no-lesson" desc:"skip the lesson reminder"`
}

// DoneOut is the output of task.done: the write, and the lesson memory
// when --lesson saved one.
type DoneOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Lesson   string    `json:"lesson_id,omitempty"`
	Warnings []Warning `json:"warnings,omitempty"`
	Human    string    `json:"-"`
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
