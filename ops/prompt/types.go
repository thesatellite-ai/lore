package prompt

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/ops/content"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the prompt operations (op/fields.go tags).
// --json outputs keep v0.1.17's envelope.

// Envelope kinds (v0.1.17 saas/pkg/constants KindPrompt*).
const (
	KindList   = "prompt.list"
	KindShow   = "prompt.show"
	KindSearch = "prompt.search"
)

// badge is v0.1.17's two-letter list badge of a prompt.
const badge = "PR"

// AddIn is the input of prompt.add. v0.1.17's --title sets the name.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	// ArgsSchema is new on the CLI (the v0.1.17 column had no flag).
	ArgsSchema  string `json:"args_schema,omitempty" flag:"args-schema" desc:"JSON schema of the {{args.x}} placeholders"`
	Title       string `json:"title" flag:"title" desc:"title (required)"`
	Description string `json:"description,omitempty" flag:"description" desc:"one-line summary (default: the title)"`
	Body        string `json:"body,omitempty" flag:"body" desc:"body (or give it as the first argument)" ui:"widget=markdown"`
	Text        string `json:"text,omitempty" arg:"0" desc:"the body, instead of --body"`
	Project     string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	CreatedBy   string `json:"created_by,omitempty" flag:"created-by" desc:"actor_id; defaults to current identity"`
	To          string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// ListIn is the input of prompt.list.
type ListIn struct {
	// MetaFlags: --meta / --place / --sort meta.<key> / --archived (METADATA 3.7).
	content.MetaFlags

	Project string `json:"project,omitempty" flag:"project" desc:"only prompts of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only prompts of this store (default every attached store)"`
}

// ListOut is v0.1.17's list envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter\'s lines (unknown key, skipped, not checked).
	Notes []string `json:"notes,omitempty"`
}

// Text prints v0.1.17's "PR  <id>" lines, or "(no prompts)".
func (o ListOut) Text() string { return o.rowsText() + content.NotesText(o.Notes) }

// rowsText is the list rows (v0.1.17's lines).
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no prompts)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(badge + "  " + r.ID + "\n")
	}
	return b.String()
}

// Record is one prompt in v0.1.17's --json form: ent Prompt's keys in
// its order (dbent/gen/ent/prompt.go). TagIDs and Store (`_source`) are
// 0.1.18 additions, absent on a v0.1.17-shaped row.
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Description      string          `json:"description,omitempty"`
	ArgsSchema       string          `json:"args_schema,omitempty"`
	Body             string          `json:"body,omitempty"`
	ArchivedAt       time.Time       `json:"archived_at,omitempty,omitzero"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// IDIn is the input of the operations that take one prompt id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the prompt id (prm_…)"`
}

// DeleteIn is the input of prompt.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the prompt id (prm_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// ShowOut is v0.1.17's show envelope.
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
}

// Text prints v0.1.17's "PR  <id>" line.
func (o ShowOut) baseText() string { return badge + "  " + o.Data.ID + "\n" }

// EditIn is the input of prompt.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID         string `json:"id" arg:"0" desc:"the prompt id (prm_…)"`
	Name       string `json:"name,omitempty" flag:"name" desc:"new name"`
	Body       string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	ArgsSchema string `json:"args_schema,omitempty" flag:"args-schema" desc:"new JSON schema of the args"`
	// Description is new on edit (v0.1.17 could only set it on add).
	Description string `json:"description,omitempty" flag:"description" desc:"new one-line summary"`
}

// SearchIn is the input of prompt.search (v0.1.17 entitySearchFlags; the
// repo scope flags are accepted, a prompt is project-wide).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"accepted for v0.1.17 scripts (prompts are project-wide)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"accepted for v0.1.17 scripts (prompts are project-wide)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"accepted for v0.1.17 scripts (prompts are project-wide)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts (prompts are project-wide)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only prompts of this project (prj_ id or name)"`
}

// SearchOut is v0.1.17's search envelope.
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

// HitRow is v0.1.17's prompt hit row (keys sorted).
type HitRow struct {
	Body string `json:"body"`
	Name string `json:"name"`
}
