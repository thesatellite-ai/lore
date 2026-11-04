package mission

import (
	"time"

	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// Input and output structs of the mission operations (op/fields.go tags).
// Outputs keep v0.1.17's --json envelope: list rows are missionWithTasks
// (saas/cmd/cli/json_helper.go at 4e6ed1c), show is the ent Mission plus
// its tasks with count = the number of tasks.

// Envelope kinds (v0.1.17 saas/pkg/constants/envelopes.go).
const (
	KindList   = "mission.list"
	KindShow   = "mission.show"
	KindSearch = "mission.search"
)

// AddIn is the input of mission.add.
type AddIn struct {
	Title     string `json:"title" arg:"0" rest:"true" desc:"the mission title (several words are joined)"`
	Body      string `json:"body,omitempty" flag:"body" desc:"longer description" ui:"widget=markdown"`
	Target    string `json:"target,omitempty" flag:"target" desc:"target completion date (YYYY-MM-DD)"`
	CreatedBy string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id (act_*); default: the row's created_by is the current identity"`
	// Approvers are actor ids (act_…); v0.1.17's @handle form needs the
	// team roster, which has not moved to the core yet.
	Approvers []string `json:"approvers,omitempty" flag:"approvers" desc:"only these people (act_… ids) may approve the mission's gates"`
	Key       string   `json:"key,omitempty" flag:"key" desc:"idempotency key: a re-run with the same key adds nothing"`
	// Plan links the mission to its plan document (TSQ9).
	Plan    string `json:"plan,omitempty" flag:"plan" desc:"plan_id (pln_*) this mission carries out"`
	To      string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// ListIn is the input of mission.list.
type ListIn struct {
	Status  string `json:"status,omitempty" flag:"status" desc:"filter: active | paused | done | cancelled (empty = default open set, see --all)"`
	All     bool   `json:"all,omitempty" flag:"all" desc:"include done + cancelled missions (default: hide them)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only missions of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only missions of this store (default every attached store)"`
}

// ListOut is the output of mission.list.
type ListOut struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	Count         int       `json:"count,omitempty"`
	Data          []Summary `json:"data"`
}

// Summary is v0.1.17's missionWithTasks.
type Summary struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Body        string          `json:"body,omitempty"`
	Status      string          `json:"status"`
	TargetDate  string          `json:"target_date,omitempty"`
	CompletedAt string          `json:"completed_at,omitempty"`
	CreatedAt   string          `json:"created_at"`
	Tasks       []opstask.Brief `json:"tasks,omitempty"`
	Store       string          `json:"_source,omitempty"`
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
	CompletedAt      time.Time       `json:"completed_at,omitempty,omitzero"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	Approvers        []string        `json:"approvers,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	PlanID           string          `json:"plan_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Edges            Edges           `json:"edges"`
	Tasks            []opstask.Brief `json:"tasks"`
	Store            string          `json:"_source,omitempty"`
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
}

// IDIn is the input of the operations that take one mission id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
}

// EditIn is the input of mission.edit (only the flags given change).
type EditIn struct {
	ID          string `json:"id" arg:"0" desc:"the mission id (msn_…)"`
	Title       string `json:"title,omitempty" flag:"title" desc:"new title"`
	Body        string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Status      string `json:"status,omitempty" flag:"status" desc:"active | paused | done | cancelled"`
	Target      string `json:"target,omitempty" flag:"target" desc:"target completion date (YYYY-MM-DD)"`
	ClearTarget bool   `json:"clear_target,omitempty" flag:"clear-target" desc:"remove target date"`
	Plan        string `json:"plan,omitempty" flag:"plan" desc:"link to plan_id (pln_*)"`
	ClearPlan   bool   `json:"clear_plan,omitempty" flag:"clear-plan" desc:"unlink the plan"`
}

// SearchIn is the input of mission.search.
type SearchIn struct {
	Query   string `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Limit   int    `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Project string `json:"project,omitempty" flag:"project" desc:"only missions of this project (prj_ id or name)"`
}
