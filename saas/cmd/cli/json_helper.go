// json_helper.go — common JSON output envelope for all commands
//
// Per PLAN.md Round 25 #3 (stable contract): every --json output uses the
// envelope `{schema_version, kind, count, data}` so consumers can rely on
// the shape across versions
//
// Helpers also handle eager-loading relationships for entities with edges
// (Mission→Tasks, TaskList→Tasks, Plan→Tasks, etc.) so JSON listings include
// related rows by default
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// jsonEnvelope is the canonical wire format for --json output
type jsonEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Count         int    `json:"count,omitempty"`
	Data          any    `json:"data"`
}

// printJSON writes a stable JSON envelope to stdout
//
//	kind: short identifier of what this is ("memory.list", "mission.show", ...)
//	data: the payload (single row or list of rows)
//	count: optional count for listings
func printJSON(kind string, data any, count int) {
	// Substitute empty list for nil so the envelope always serializes
	// `data: []` instead of `data: null` (R25 #3 stable contract)
	if data == nil {
		data = []any{}
	}
	env := jsonEnvelope{
		SchemaVersion: 1,
		Kind:          kind,
		Count:         count,
		Data:          data,
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "json marshal:", err)
		return
	}
	fmt.Println(string(out))
}

// missionWithTasks is the canonical JSON shape for mission show/list with
// eager-loaded tasks. Fields use snake_case for stable consumer parsing
type missionWithTasks struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Body        string      `json:"body,omitempty"`
	Status      string      `json:"status"`
	TargetDate  string      `json:"target_date,omitempty"`
	CompletedAt string      `json:"completed_at,omitempty"`
	CreatedAt   string      `json:"created_at"`
	Tasks       []taskBrief `json:"tasks,omitempty"`
}

// taskBrief is the lightweight task projection used in eager-load contexts
type taskBrief struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	Commitment    string `json:"commitment"`
	DeferredUntil string `json:"deferred_until,omitempty"`
	DueAt         string `json:"due_at,omitempty"`
	MissionID     string `json:"mission_id,omitempty"`
}

// taskFull is the JSON shape for task show
type taskFull struct {
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
}

// projectWithRepos projects a Project with its repos
type projectWithRepos struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	OriginURL    string      `json:"origin_url,omitempty"`
	ArchivedAt   string      `json:"archived_at,omitempty"`
	CreatedAt    string      `json:"created_at"`
	LastActiveAt string      `json:"last_active_at"`
	Repos        []repoBrief `json:"repos,omitempty"`
}

type repoBrief struct {
	ID          string `json:"id"`
	MountName   string `json:"mount_name"`
	DisplayName string `json:"display_name,omitempty"`
	OriginURL   string `json:"origin_url,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
}

// briefTask projects an ent Task into the lightweight JSON form used in
// eager-load contexts (mission show, plan show, tasklist show)
//
// Input is `any` to avoid the cmd package importing dbent/gen/ent for this
// helper. Caller passes through the *ent.Task and we use type assertion
// at the boundary
//
// Implementation lives next to the consumers — see mission.go briefTask()

// derefStr unwraps *string → string (empty if nil)
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefTime unwraps *time.Time → ISO-8601 string (empty if nil)
func derefTime(p *time.Time) string {
	if p == nil {
		return ""
	}
	return p.Format("2006-01-02T15:04:05Z07:00")
}
