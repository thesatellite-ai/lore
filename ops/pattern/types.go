package pattern

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the pattern operations. Struct tags are the
// op contract (op/fields.go). --json outputs keep v0.1.17's envelope
// (`{schema_version, kind, count, data}`; search adds `query`), so scripts
// written against v0.1.17 keep working.

// Envelope kinds (v0.1.17 saas/pkg/constants KindPattern*).
const (
	KindList   = "pattern.list"
	KindShow   = "pattern.show"
	KindSearch = "pattern.search"
)

// AddIn is the input of pattern.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	Title string `json:"title" flag:"title" desc:"title (required)"`
	Body  string `json:"body,omitempty" flag:"body" desc:"body" ui:"widget=markdown"`
	Text  string `json:"text,omitempty" arg:"0" argof:"body" desc:"the body, instead of --body"`
	// Language is new on the CLI (the v0.1.17 column had no flag).
	Language string `json:"language,omitempty" flag:"language" desc:"programming language of the exemplar (go, ts, …)"`
	Repo     string `json:"repo,omitempty" flag:"repo" desc:"scope to a repo of the project (mount name or rep_ id; default project-wide)"`
	// Scope is --scope (CONTEXT_BUDGET 6.2.3, CQ12): a folder scope ranks
	// the row in lore context only.
	Scope      string `json:"scope,omitempty" flag:"scope" desc:"where it attaches: global | project | repo | dir:<path> (folder scopes rank it in lore context)"`
	Project    string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	Supersedes string `json:"supersedes,omitempty" flag:"supersedes" desc:"pattern_id this entry replaces"`
	To         string `json:"to,omitempty" flag:"to" alias:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
}

// ListIn is the input of pattern.list (v0.1.17 repo scope flags).
type ListIn struct {
	// MetaFlags: --meta / --place / --sort meta.<key> / --archived (METADATA 3.7).
	content.MetaFlags

	Repo       string `json:"repo,omitempty" flag:"repo" desc:"include this repo's patterns (mount name or rep_ id)"`
	NoInherit  bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	MasterOnly bool   `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	AllRepos   bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"list across every repo (ignore --repo)"`
	All        bool   `json:"all,omitempty" flag:"all" desc:"every row: every repo's and the archived ones (the default list says how many it hides)"`
	Project    string `json:"project,omitempty" flag:"project" desc:"only patterns of this project (prj_ id or name)"`
	Store      string `json:"store,omitempty" flag:"store" alias:"to" desc:"only patterns of this store (default every attached store)"`
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

// Text prints "PA  <id>  <title, name or body>" lines (content.ListLine), or "(no patterns)".
func (o ListOut) Text() string { return o.rowsText() + content.NotesText(o.Notes) }

// rowsText is the list rows (v0.1.17's lines).
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no patterns)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(content.ListLine(badge, r.ID, r.Title, r.Label))
	}
	return b.String()
}

// badge is v0.1.17's two-letter list badge of a pattern.
const badge = "PA"

// Record is one pattern in v0.1.17's --json form: ent Pattern's keys in
// its order (dbent/gen/ent/pattern.go; nillable columns are omitempty
// values here). TagIDs (O11) and Store (`_source`, a row of a store other
// than repo) are 0.1.18 additions, absent on a v0.1.17-shaped row.
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
	RepoID           string          `json:"repo_id,omitempty"`
	Title            string          `json:"title,omitempty"`
	Body             string          `json:"body,omitempty"`
	Language         string          `json:"language,omitempty"`
	SupersededByID   string          `json:"superseded_by_id,omitempty"`
	ScopeID          string          `json:"scope_id,omitempty"`
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

// IDIn is the input of the operations that take one pattern id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the pattern id (pat_…)"`
}

// DeleteIn is the input of pattern.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the pattern id (pat_…)"`
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

// baseText is v0.1.17's "PA  <id>" line, then the title or name and the body (content.ShowText, INTEG-KNOWLEDGE-3).
func (o ShowOut) baseText() string {
	return content.ShowText(badge, o.Data.ID, "title", o.Data.Title, o.Data.Body)
}

// EditIn is the input of pattern.edit (only the flags given change).
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID           string `json:"id" arg:"0" desc:"the pattern id (pat_…)"`
	Title        string `json:"title,omitempty" flag:"title" desc:"new title"`
	Body         string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Language     string `json:"language,omitempty" flag:"language" desc:"new language"`
	RebindRepo   string `json:"rebind_repo,omitempty" flag:"rebind-repo" desc:"move this row to repo <mount> (rebinds repo_id; distinct from context --repo)"`
	RebindMaster bool   `json:"rebind_master,omitempty" flag:"rebind-master" desc:"move this row to master scope (clears repo_id)"`
	// Scope moves the row to another scope (6.2.3, CQ12).
	Scope string `json:"scope,omitempty" flag:"scope" desc:"move to another scope: project | repo | dir:<path>"`
}

// SearchIn is the input of pattern.search (v0.1.17 entitySearchFlags).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only patterns of this project (prj_ id or name)"`
	Store           string   `json:"store,omitempty" flag:"store" alias:"to" desc:"only patterns of this store (default every attached store)"`
}

// SearchOut is v0.1.17's search envelope (count always printed).
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// Text prints v0.1.17's "<id> <title>" lines, or "(no matches)".
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		b.WriteString(h.ID + " " + h.Row.Title + "\n")
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

// HitRow is v0.1.17's pattern hit row (keys sorted, as json.Marshal of
// its map printed them).
type HitRow struct {
	Body  string `json:"body"`
	Title string `json:"title"`
}
