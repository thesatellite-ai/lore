package behaviour

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the behaviour operations. Struct tags are
// the op contract (op/fields.go). --json outputs keep v0.1.17's envelope
// (`{schema_version, kind, count, data}`; search adds `query`), so scripts
// written against v0.1.17 keep working.

// Envelope kinds (v0.1.17 saas/pkg/constants; search uses the FTS name).
const (
	KindList   = "behaviour.list"
	KindShow   = "behaviour.show"
	KindSearch = "behaviour.search"
)

// badge is v0.1.17's two-letter list badge of a behaviour.
const badge = "BE"

// AddIn is the input of behaviour.add.
type AddIn struct {
	// Name is set with --title, as v0.1.17 add did.
	Name      string `json:"name" flag:"title" desc:"title (required)"`
	Body      string `json:"body,omitempty" flag:"body" desc:"body (or give it as the first argument)" ui:"widget=markdown"`
	Text      string `json:"text,omitempty" arg:"0" desc:"the body, instead of --body"`
	Project   string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	CreatedBy string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id; defaults to current identity"`
	To        string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// ListIn is the input of behaviour.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only behaviours of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only behaviours of this store (default every attached store)"`
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text prints v0.1.17's "BE  <id>" lines, or "(no behaviours)".
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no behaviours)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(badge + "  " + r.ID + "\n")
	}
	return b.String()
}

// Record is one behaviour in v0.1.17's --json form: the ent row's keys
// in its order (dbent/gen/ent; nillable columns are omitempty here).
// TagIDs (O11) and Store (`_source`, a row of a store other than repo) are
// 0.1.18 additions, absent on a v0.1.17-shaped row.
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Body             string          `json:"body,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// IDIn is the input of the operations that take one behaviour id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the behaviour id (bhv_…)"`
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
}

// Text prints v0.1.17's "BE  <id>" line.
func (o ShowOut) Text() string { return badge + "  " + o.Data.ID + "\n" }

// EditIn is the input of behaviour.edit (only the flags given change).
type EditIn struct {
	ID   string `json:"id" arg:"0" desc:"the behaviour id (bhv_…)"`
	Name string `json:"name,omitempty" flag:"name" desc:"new name"`
	Body string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
}

// SearchIn is the input of behaviour.search (v0.1.17 entitySearchFlags).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only behaviours of this project (prj_ id or name)"`
}

// SearchOut is v0.1.17's search envelope (count always printed).
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// Text prints v0.1.17's "<id> <name>" lines, or "(no matches)".
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		b.WriteString(h.ID + " " + h.Row.Name + "\n")
	}
	return b.String()
}

// SearchHit is one v0.1.17 search hit.
type SearchHit struct {
	ID      string          `json:"id"`
	Score   float64         `json:"score"`
	Snippet string          `json:"snippet,omitempty"`
	Row     HitRow          `json:"row"`
	Store   store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// HitRow is v0.1.17's behaviour hit row (keys sorted, as json.Marshal
// of its map printed them).
type HitRow struct {
	Body string `json:"body"`
	Name string `json:"name"`
}
