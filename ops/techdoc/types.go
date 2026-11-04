package techdoc

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the techdoc operations. Struct tags are the
// op contract (op/fields.go). --json outputs keep v0.1.17's envelope.

// Envelope kinds (v0.1.17 saas/pkg/constants KindTechDoc*).
const (
	KindList   = "techdoc.list"
	KindShow   = "techdoc.show"
	KindSearch = "techdoc.search"
)

// AddIn is the input of techdoc.add.
type AddIn struct {
	Name        string `json:"name" flag:"name" desc:"doc name (required)"`
	BaseURL     string `json:"base_url,omitempty" flag:"base-url" desc:"base URL"`
	Description string `json:"description,omitempty" flag:"description" desc:"description"`
	Project     string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	To          string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// ListIn is the input of techdoc.list.
type ListIn struct {
	Project string `json:"project,omitempty" flag:"project" desc:"only techdocs of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only techdocs of this store (default every attached store)"`
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text prints v0.1.17's "<id> <name>[ <base url>]" lines, or "(no techdocs)".
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no techdocs)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(r.ID + " " + r.Name)
		if r.BaseURL != "" {
			b.WriteString(" " + r.BaseURL)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Record is one techdoc in v0.1.17's --json form: ent TechDoc's keys in
// its order, with its always-empty `edges` object (the pages edge is never
// loaded). TagIDs (O11) and Store (`_source`) are 0.1.18 additions.
type Record struct {
	ID          string          `json:"id,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	BaseURL     string          `json:"base_url,omitempty"`
	Description string          `json:"description,omitempty"`
	Edges       Edges           `json:"edges"`
	TagIDs      []string        `json:"tag_ids,omitempty"`
	Store       store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// Edges is v0.1.17's unloaded ent edges object (always `{}`).
type Edges struct{}

// IDIn is the input of the operations that take one techdoc id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the techdoc id (tdc_…)"`
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
}

// Text prints v0.1.17's id, name, base_url and desc lines.
func (o ShowOut) Text() string {
	d := o.Data
	s := d.ID + "\n  name:     " + d.Name + "\n"
	if d.BaseURL != "" {
		s += "  base_url: " + d.BaseURL + "\n"
	}
	if d.Description != "" {
		s += "  desc:     " + d.Description + "\n"
	}
	return s
}

// SearchIn is the input of techdoc.search (v0.1.17 entitySearchFlags).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only techdocs of this project (prj_ id or name)"`
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

// HitRow is v0.1.17's techdoc hit row (keys sorted).
type HitRow struct {
	BaseURL     string `json:"base_url"`
	Description string `json:"description"`
	Name        string `json:"name"`
}
