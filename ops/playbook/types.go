package playbook

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the playbook operations (op/fields.go tags).
// --json outputs keep v0.1.17's envelope.

// Envelope kinds (v0.1.17 saas/pkg/constants KindPlaybook*).
const (
	KindList   = "playbook.list"
	KindShow   = "playbook.show"
	KindSearch = "playbook.search"
)

// badge is v0.1.17's two-letter list badge of a playbook.
const badge = "PL"

// AddIn is the input of playbook.add. v0.1.17's --title sets the name.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title       string `json:"title" flag:"title" alias:"name" desc:"title (required)"`
	Description string `json:"description,omitempty" flag:"description" desc:"one-line summary (default: the title)"`
	Body        string `json:"body,omitempty" flag:"body" desc:"body" ui:"widget=markdown"`
	Text        string `json:"text,omitempty" arg:"0" argof:"body" desc:"the body, instead of --body"`
	Project     string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	To          string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
}

// ListIn is the input of playbook.list.
type ListIn struct {
	// MetaFlags: --meta / --place / --sort meta.<key> / --archived (METADATA 3.7).
	content.MetaFlags

	Project string `json:"project,omitempty" flag:"project" desc:"only playbooks of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" alias:"to" desc:"only playbooks of this store (default every attached store)"`
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

// Text prints "PL  <id>  <title, name or body>" lines (content.ListLine), or "(no playbooks)".
func (o ListOut) Text() string { return o.rowsText() + content.NotesText(o.Notes) }

// rowsText is the list rows (v0.1.17's lines).
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no playbooks)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(content.ListLine(badge, r.ID, r.Name, r.Label))
	}
	return b.String()
}

// Record is one playbook in v0.1.17's --json form: ent Playbook's keys in
// its order (dbent/gen/ent/playbook.go). TagIDs and Store (`_source`) are
// 0.1.18 additions, absent on a v0.1.17-shaped row.
type Record struct {
	ID               string          `json:"id,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
	TrustScore       float64         `json:"trust_score,omitempty"`
	Confidence       float64         `json:"confidence,omitempty"`
	LastValidatedAt  time.Time       `json:"last_validated_at,omitempty,omitzero"`
	ArchivedAt       time.Time       `json:"archived_at,omitempty,omitzero"`
	SourceKind       string          `json:"source_kind,omitempty"`
	SourceRef        string          `json:"source_ref,omitempty"`
	ProjectID        string          `json:"project_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Description      string          `json:"description,omitempty"`
	Body             string          `json:"body,omitempty"`
	Composes         string          `json:"composes,omitempty"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	TagIDs           []string        `json:"tag_ids,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Meta / MetaMasked are the row's metadata by name, sensitive values
	// masked (MQ13, METADATA 3.17/3.20; absent when the row has none, so
	// v0.1.17's bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
	// CreatedBy / UpdatedBy are the row envelope's writers (INTEG-AUDIT-1,
	// auditrec), last so the v0.1.17 keys keep their order.
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

// IDIn is the input of the operations that take one playbook id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the playbook id (pbk_…)"`
}

// DeleteIn is the input of playbook.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the playbook id (pbk_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
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
	// Revealed, Forced and Warning: --reveal (SECRETS 2.7, SQ10).
	Revealed int    `json:"revealed,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// baseText is v0.1.17's "PL  <id>" line, then the title or name and the body (content.ShowText, INTEG-KNOWLEDGE-3).
func (o ShowOut) baseText() string {
	return content.ShowText(badge, o.Data.ID, "name", o.Data.Name, o.Data.Body)
}

// EditIn is the input of playbook.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID string `json:"id" arg:"0" desc:"the playbook id (pbk_…)"`
	// Name is the REST/--json spelling of the new name; the CLI
	// spells it --title (alias --name) like add (S-3: one flag per field).
	Name string `json:"name,omitempty" desc:"new name (same as title)"`
	// Title is the new name under the spelling add takes (INTEG-KNOWLEDGE-4).
	Title string `json:"title,omitempty" flag:"title" alias:"name" desc:"new name"`
	Body  string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	// Description is new on edit (v0.1.17 could only set it on add).
	Description string `json:"description,omitempty" flag:"description" desc:"new one-line summary"`
}

// SearchIn is the input of playbook.search (v0.1.17 entitySearchFlags; the
// repo scope flags are accepted, a playbook is project-wide).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"accepted for v0.1.17 scripts (playbooks are project-wide)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"accepted for v0.1.17 scripts (playbooks are project-wide)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"accepted for v0.1.17 scripts (playbooks are project-wide)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts (playbooks are project-wide)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only playbooks of this project (prj_ id or name)"`
	Store           string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only playbooks of this store (default every attached store)"`
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
	// Meta / MetaMasked are the row's metadata by name, sensitive values
	// masked (MQ13, METADATA 3.17/3.20; absent when the row has none, so
	// v0.1.17's bytes stay for such rows).
	Meta       map[string]string `json:"meta,omitempty"`
	MetaMasked []string          `json:"meta_masked,omitempty"`
}

// HitRow is v0.1.17's playbook hit row (keys sorted).
type HitRow struct {
	Body string `json:"body"`
	Name string `json:"name"`
}
