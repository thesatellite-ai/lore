package handoff

import (
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/content"

	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the handoff operations. Struct tags are the
// op contract (op/fields.go). --json outputs keep v0.1.17's envelope.

// Envelope kinds (v0.1.17 saas/pkg/constants KindHandoff*).
const (
	KindList   = "handoff.list"
	KindShow   = "handoff.show"
	KindSearch = "handoff.search"
)

// badge is v0.1.17's two-letter list badge of a handoff.
const badge = "HA"

// AddIn is the input of handoff.add (v0.1.17 `handoff add --for <@handle>
// [--task <id>] <text>`). --to keeps v0.1.17's meaning (deprecated alias
// of --for), so the store target of this one command is --store.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	For string `json:"for,omitempty" flag:"for" desc:"who the handoff is for (@handle, stable key, display name or act_ id)"`
	// To is v0.1.17's --to spelling of --for. Its JSON name is not "to":
	// the engine reads an input field named "to" as the write's target
	// store (engine.InputTarget), so `--to @alice` would look for a store
	// named @alice; the handoff's store is --store.
	To    string `json:"to_actor,omitempty" flag:"to" desc:"deprecated alias of --for"`
	Task  string `json:"task,omitempty" flag:"task" desc:"the task it is about (its mission is recorded too)"`
	Body  string `json:"body,omitempty" flag:"body" desc:"the handoff text" ui:"widget=markdown"`
	Text  string `json:"text,omitempty" arg:"0" rest:"true" argof:"body" desc:"the handoff text, instead of --body"`
	Store string `json:"store,omitempty" flag:"store" desc:"store to write to (a name from .lore/stores.yaml, me, <store>/<folder>; default repo)"`
	// Project is the project the handoff belongs to.
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
}

// ListIn is the input of handoff.list.
type ListIn struct {
	// MetaFlags: --meta / --place / --sort meta.<key> / --archived (METADATA 3.7).
	content.MetaFlags

	Project string `json:"project,omitempty" flag:"project" desc:"only handoffs of this project (prj_ id or name)"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only handoffs of this store (default every attached store)"`
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

// Text prints "HA  <id>  <title, name or body>" lines (content.ListLine), or "(no handoffs)".
func (o ListOut) Text() string { return o.rowsText() + content.NotesText(o.Notes) }

// rowsText is the list rows (v0.1.17's lines).
func (o ListOut) rowsText() string {
	if len(o.Data) == 0 {
		return "(no handoffs)\n"
	}
	var b strings.Builder
	for _, r := range o.Data {
		b.WriteString(content.ListLine(badge, r.ID, r.Body, r.Label))
	}
	return b.String()
}

// Record is one handoff in v0.1.17's --json form: ent Handoff's keys in
// its order. TagIDs (O11) and Store (`_source`) are 0.1.18 additions.
type Record struct {
	ID          string          `json:"id,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at,omitempty"`
	ProjectID   string          `json:"project_id,omitempty"`
	FromActorID string          `json:"from_actor_id,omitempty"`
	ToActorID   string          `json:"to_actor_id,omitempty"`
	Body        string          `json:"body,omitempty"`
	StatusStr   string          `json:"status_str,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	MissionID   string          `json:"mission_id,omitempty"`
	TagIDs      []string        `json:"tag_ids,omitempty"`
	Store       store.SourceRef `json:"_source,omitempty,omitzero"`
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

// IDIn is the input of the operations that take one handoff id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the handoff id (hnd_…)"`
}

// DeleteIn is the input of handoff.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the handoff id (hnd_…)"`
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

// Text prints v0.1.17's "HA  <id>" line.
func (o ShowOut) baseText() string { return badge + "  " + o.Data.ID + "\n" }

// SearchIn is the input of handoff.search (v0.1.17 entitySearchFlags).
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"search across every repo (ignore --repo)"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"only master-scoped rows (repo_id IS NULL)"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"strict scope: --repo only, no master inheritance"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"scope to a specific repo (mount_name or rep_id)"`
	Project         string   `json:"project,omitempty" flag:"project" desc:"only handoffs of this project (prj_ id or name)"`
	Store           string   `json:"store,omitempty" flag:"store" desc:"only handoffs of this store (default every attached store)"`
}

// SearchOut is v0.1.17's search envelope (count always printed).
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// hitBodyWidth is v0.1.17's search line cut of the body (trunc(body, 80)).
const hitBodyWidth = 80

// Text prints v0.1.17's "<id> [<status>] <body cut at 80>" lines, or
// "(no matches)".
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		b.WriteString(h.ID + " [" + h.Row.StatusStr + "] " + cut(h.Row.Body, hitBodyWidth) + "\n")
	}
	return b.String()
}

// cut shortens s to n characters with "..." as v0.1.17's trunc did, on
// rune boundaries so a multi-byte character is never split.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
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

// HitRow is v0.1.17's handoff hit row (keys sorted).
type HitRow struct {
	Body      string `json:"body"`
	StatusStr string `json:"status_str"`
}
