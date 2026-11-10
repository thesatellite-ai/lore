package mission

import (
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/work"

	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the mission operations (op/fields.go tags).
// Outputs keep v0.1.17's --json envelope: list rows are missionWithTasks
// (saas/cmd/cli/json_helper.go at 4e6ed1c), show is the ent Mission plus
// its tasks with count = the number of tasks.

// Envelope kinds (v0.1.17 saas/pkg/constants/envelopes.go).
const (
	KindList = "mission.list"
	KindShow = "mission.show"
	// KindAdd is mission add's envelope (v0.1.17 printJSON(KindMissionAdd)).
	KindAdd    = "mission.add"
	KindSearch = "mission.search"
)

// AddIn is the input of mission.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title    string `json:"title" arg:"0" rest:"true" desc:"the mission title (several words are joined)"`
	Body     string `json:"body,omitempty" flag:"body" desc:"longer description" ui:"widget=markdown"`
	Target   string `json:"target,omitempty" flag:"target" desc:"target completion date (YYYY-MM-DD or a date phrase: eom, next fri, in 2w, …)"`
	StartsAt string `json:"starts_at,omitempty" flag:"starts-at" desc:"when the mission's work starts (YYYY-MM-DD or a date phrase)"`
	// Approvers are actor ids (act_…) or v0.1.17's @handles (resolved
	// through the host's roster, work.Team).
	Approvers []string `json:"approvers,omitempty" flag:"approvers" desc:"only these people (@handle or act_…, comma separated) may approve the mission's gates"`
	Key       string   `json:"key,omitempty" flag:"key" desc:"idempotency key: a re-run with the same key adds nothing"`
	// Plan links the mission to its plan document (TSQ9).
	Plan    string `json:"plan,omitempty" flag:"plan" desc:"plan_id (pln_*) this mission carries out"`
	To      string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// ListIn is the input of mission.list.
type ListIn struct {
	Status  string `json:"status,omitempty" flag:"status" desc:"filter: active | paused | done | cancelled (empty = default open set, see --all)"`
	All     bool   `json:"all,omitempty" flag:"all" desc:"include done + cancelled missions (default: hide them)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only missions of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" alias:"to" desc:"only missions of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	work.MetaFlags
}

// ListOut is the output of mission.list.
type ListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
	Data          []Summary `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
}

// Summary is v0.1.17's missionWithTasks.
type Summary struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Body        string          `json:"body,omitempty"`
	Status      string          `json:"status"`
	TargetDate  string          `json:"target_date,omitempty"`
	StartsAt    string          `json:"starts_at,omitempty"`
	CompletedAt string          `json:"completed_at,omitempty"`
	CreatedAt   string          `json:"created_at"`
	Tasks       []opstask.Brief `json:"tasks,omitempty"`
	Store       store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked: the mission's metadata on list --json too
	// (METADATA 3.17, MQ13); absent without metadata.
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the older keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Record is the ent Mission JSON plus its tasks (v0.1.17 missionShowJSON).
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	Body             string          `json:"body,omitempty"`
	Status           string          `json:"status,omitempty"`
	TargetDate       time.Time       `json:"target_date,omitempty,omitzero"`
	StartsAt         time.Time       `json:"starts_at,omitempty,omitzero"`
	CompletedAt      time.Time       `json:"completed_at,omitempty,omitzero"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	Approvers        []string        `json:"approvers,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	PlanID           string          `json:"plan_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Edges            Edges           `json:"edges"`
	Tasks            []opstask.Brief `json:"tasks"`
	// Estimate is the rollup of the tasks' estimates and tracked time
	// (TASK 9.2, 9.3).
	Estimate opstask.MissionEstimate `json:"estimate_rollup,omitzero"`
	Store    store.SourceRef         `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked are the row's metadata (METADATA 3.17 "metadata
	// on every read", MQ13, G-META-10 via metashow.Row): absent for a row
	// without metadata, so v0.1.17's record is unchanged.
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the v0.1.17 keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Edges is ent's edges object: v0.1.17 printed it empty.
type Edges struct{}

// ShowOut is the output of mission.show (and of mission.add --json).
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Count         int    `json:"count,omitempty"`
	Data          Record `json:"data"`
	// Human is the human output (set by add; show builds it in Text).
	Human string `json:"-"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
	// timed: a TIME 10 flag was given, so each task line shows its time.
	timed bool
}

// IDIn is the input of the operations that take one mission id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
}

// DeleteIn is the input of mission.delete.
type DeleteIn struct {
	ID string `json:"id,omitempty" arg:"0" desc:"the mission id (msn_…); leave out with --where"`
	// Where / DryRun are the bulk delete (DELETION 3.2, FA-LTASK-2): every
	// matching row in one bat_ batch, always asked (ops/internal/wheredel).
	Where  []string `json:"where,omitempty" flag:"where" desc:"bulk delete: every mission matching this filter (field=value, field:a,b, created_at<2026-06-01; repeatable, all must match)"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"with --where: list what would be deleted, write nothing"`
	Reason string   `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
}

// EditIn is the input of mission.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID          string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
	Title       string `json:"title,omitempty" flag:"title" desc:"new title"`
	Body        string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Status      string `json:"status,omitempty" flag:"status" desc:"active | paused | done | cancelled"`
	Target      string `json:"target,omitempty" flag:"target" desc:"target completion date (YYYY-MM-DD or a date phrase)"`
	ClearTarget bool   `json:"clear_target,omitempty" flag:"clear-target" desc:"remove target date"`
	StartsAt    string `json:"starts_at,omitempty" flag:"starts-at" desc:"when the mission's work starts (YYYY-MM-DD or a date phrase)"`
	ClearStarts bool   `json:"clear_starts,omitempty" flag:"clear-starts-at" desc:"remove the start date"`
	Plan        string `json:"plan,omitempty" flag:"plan" desc:"link to plan_id (pln_*)"`
	ClearPlan   bool   `json:"clear_plan,omitempty" flag:"clear-plan" desc:"unlink the plan"`
}

// SearchIn is the input of mission.search.
type SearchIn struct {
	Query   string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Limit   int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only missions of this project (prj_ id or name)"`
	// Columns restricts the match to some full-text fields (v0.1.17
	// --column).
	Columns []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable; missions have: title, body, status)"`
	Store   string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only missions of this store (default every attached store)"`
}
