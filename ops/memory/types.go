package memory

import (
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/refpoint"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/store"
)

// Input and output structs of the memory operations. Struct tags are the
// op contract (op/fields.go): `json` is the REST and --json name (no
// omitempty = required), `arg`/`flag` the CLI spelling, `desc` the help
// text. Outputs keep v0.1.17's --json envelope byte for byte
// (`{schema_version, kind, count, data}`, saas/cmd/cli/json_helper.go at
// 4e6ed1c; search: `{schema_version, kind, query, count, data}`,
// search_shared.go), so scripts written against v0.1.17 keep working.

// SchemaVersion is v0.1.17's envelope schema_version (json_helper.go).
const SchemaVersion = 1

// Envelope kinds (v0.1.17 saas/pkg/constants/envelopes.go and
// Entity.SearchKind: "<fts entity>.search").
const (
	KindList   = "memory.list"
	KindShow   = "memory.show"
	KindSearch = "memory.search"
)

// AddIn is the input of memory.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	// Body is v0.1.17's --body. Text is the same value given positionally
	// (`lore memory add "…"`); exactly one of them must be set.
	Body string `json:"body,omitempty" flag:"body" desc:"the memory text (or give it as the first argument)" ui:"widget=markdown"`
	Text string `json:"text,omitempty" arg:"0" desc:"the memory text, instead of --body"`
	// Kind is one of schema.MemoryKinds; empty means retrieved.
	Kind string `json:"kind,omitempty" flag:"kind" desc:"core | retrieved | episodic | procedural | archival (default retrieved)"`
	// Source is source_kind; empty means manual.
	Source    string `json:"source,omitempty" flag:"source" desc:"source_kind: manual | learn-from | agent-proposal | plugin | imported (default manual)"`
	SourceRef string `json:"source_ref,omitempty" flag:"source-ref" desc:"free-form provenance pointer"`
	To        string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
	// Repo scopes the memory to one repo of the project (mount name or
	// rep_ id); empty is the project-wide ("master") scope.
	Repo string `json:"repo,omitempty" flag:"repo" desc:"scope to a repo of the project (mount name or rep_ id; default project-wide)"`
	// Scope is --scope (CONTEXT_BUDGET 6.2.3, CQ12): a folder scope ranks
	// the memory in lore context only.
	Scope string `json:"scope,omitempty" flag:"scope" desc:"where it attaches: global | project | repo | dir:<path> (folder scopes rank it in lore context)"`
	// Project picks the project when the repo store holds several
	// (v0.1.17 --project: a prj_ id or a project name).
	Project string `json:"project,omitempty" flag:"project" desc:"the project (prj_ id or name; default the repo's one project)"`
	// Supersedes is v0.1.17 --supersedes: the new memory replaces this one,
	// whose superseded_by_id is then set to the new id.
	Supersedes string `json:"supersedes,omitempty" flag:"supersedes" desc:"memory id (mem_…) this entry replaces"`
	// NoMeta (--no-meta) keeps the replaced memory's metadata off the new one (METADATA 3.19).
	NoMeta bool `json:"no_meta,omitempty" flag:"no-meta" desc:"with --supersedes: do not copy the replaced memory's metadata"`
	// CreatedBy and ValidatedBy are v0.1.17's actor columns
	// created_by_actor_id / validated_by_actor_id (an act_ id of an
	// existing actor row; the engine refuses a missing one).
	CreatedBy   string `json:"created_by,omitempty" flag:"created-by" desc:"actor id (act_…); default: the row's created_by is the current identity"`
	ValidatedBy string `json:"validated_by,omitempty" flag:"validated-by" desc:"actor id (act_…) that validated this memory"`
	// Tags are tag names of the project (repeatable --tag; each must exist:
	// `lore tag add --name=<tag>`), stored as tag_ids (O11).
	Tags []string `json:"tags,omitempty" flag:"tag" desc:"tag name (repeatable; the tag must exist)"`
}

// WriteOut is the output of every write operation: the row's id, the store
// that holds it and any warnings the write raised.
type WriteOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Warnings []Warning `json:"warnings,omitempty"`
	// CarriedMeta is how many metadata keys a --supersedes add copied (3.19).
	CarriedMeta int `json:"carried_meta,omitempty"`
	// Human is the v0.1.17 human line(s) the op set for this write (see
	// Text); never part of --json or REST.
	Human string `json:"-"`
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ListIn is the input of memory.list.
type ListIn struct {
	// Repo scope (v0.1.17 repo_scope.go; op structs cannot embed, so the
	// four fields repeat on SearchIn): no flag = project-wide memories
	// only; --repo adds that repo's; --no-inherit keeps only that repo's.
	Repo       string `json:"repo,omitempty" flag:"repo" desc:"include this repo's memories (mount name or rep_ id)"`
	NoInherit  bool   `json:"no_inherit,omitempty" flag:"no-inherit" desc:"with --repo: only that repo's memories, not the project-wide ones"`
	MasterOnly bool   `json:"master_only,omitempty" flag:"master-only" desc:"only project-wide memories"`
	AllRepos   bool   `json:"all_repos,omitempty" flag:"all-repos" desc:"memories of every repo"`
	Project    string `json:"project,omitempty" flag:"project" desc:"only memories of this project (prj_ id or name; default the repo's one project)"`
	Kind       string `json:"kind,omitempty" flag:"kind" desc:"only memories of this kind"`
	// Tags keeps memories carrying every named tag.
	Tags  []string `json:"tags,omitempty" flag:"tag" desc:"only memories with this tag (repeatable: all must match)"`
	Store string   `json:"store,omitempty" flag:"store" desc:"only memories of this store (default every attached store)"`
	// MetaFlags: --meta / --place / --sort meta.<key> (METADATA 3.7).
	metalist.Flags
}

// ListOut is the output of memory.list: v0.1.17's envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
	// Notes are the metadata filter's lines (METADATA 3.7, 3.13).
	Notes []string `json:"notes,omitempty"`
}

// Record is one memory in v0.1.17's --json form: the ent Memory struct's
// JSON (dbent/gen/ent/memory.go), same keys, same order. ent's nillable
// columns are pointers there; op fields cannot be pointers, so they are
// values with omitempty (and omitzero for times), which print the same
// bytes for every value a memory row can hold (an absent field is never
// an empty string here). Machine-local columns v0.1.17 never synced
// (last_accessed_at, embedding_model_id, embedding_dim) are always absent
// here; embedding itself always prints null (see Embedding). Store, Label are
// the v0.1.17 external-row keys `_source`/`_label` (stores_reads.go), set
// only for a row of a store other than the repo's own.
type Record struct {
	ID                 string    `json:"id,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
	TrustScore         float64   `json:"trust_score,omitempty"`
	Confidence         float64   `json:"confidence,omitempty"`
	LastValidatedAt    time.Time `json:"last_validated_at,omitempty,omitzero"`
	ArchivedAt         time.Time `json:"archived_at,omitempty,omitzero"`
	SourceKind         string    `json:"source_kind,omitempty"`
	SourceRef          string    `json:"source_ref,omitempty"`
	ProjectID          string    `json:"project_id,omitempty"`
	RepoID             string    `json:"repo_id,omitempty"`
	Kind               string    `json:"kind,omitempty"`
	Body               string    `json:"body,omitempty"`
	ValidAt            time.Time `json:"valid_at,omitempty,omitzero"`
	ValidUntil         time.Time `json:"valid_until,omitempty,omitzero"`
	TxAt               time.Time `json:"tx_at,omitempty"`
	SupersededByID     string    `json:"superseded_by_id,omitempty"`
	CreatedByActorID   string    `json:"created_by_actor_id,omitempty"`
	ValidatedByActorID string    `json:"validated_by_actor_id,omitempty"`
	// Embedding is always JSON null: v0.1.17's ent struct scanned the
	// NULL column into a non-nil *[]byte, so omitempty never dropped it
	// and every row printed `"embedding": null` here. The vector itself
	// is machine-local and never synced, so the core never holds one.
	Embedding op.Null[string] `json:"embedding"`
	// TagIDs is new in 0.1.18 (O11; absent when untagged, so an untagged
	// row prints v0.1.17's bytes).
	TagIDs []string        `json:"tag_ids,omitempty"`
	Edges  Edges           `json:"edges"`
	Store  store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// Edges is ent's edges object: v0.1.17 printed it empty (no eager load).
type Edges struct{}

// IDIn is the input of the operations that take one memory id.
type IDIn struct {
	ID string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
}

// ShowOut is the output of memory.show: v0.1.17's envelope (count 0 is
// omitted, as printJSON(kind, row, 0) did).
type ShowOut struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Data          Record `json:"data"`
	// Expanded / Refs are --expand's additions (SECRETS 12: "--json adds
	// expanded and refs"); empty without --expand.
	Expanded map[string]string `json:"expanded,omitempty"`
	Refs     []refpoint.Used   `json:"refs,omitempty"`
}

// EditIn is the input of memory.edit. An empty string flag leaves its
// field as it is.
type EditIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	ID        string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
	Body      string `json:"body,omitempty" flag:"body" desc:"new body" ui:"widget=markdown"`
	Kind      string `json:"kind,omitempty" flag:"kind" desc:"core | retrieved | episodic | procedural | archival"`
	Source    string `json:"source,omitempty" flag:"source" desc:"new source_kind"`
	SourceRef string `json:"source_ref,omitempty" flag:"source-ref" desc:"new source-ref"`
	// RebindRepo / RebindMaster are v0.1.17's edit-time scope flags
	// (repo_scope.go): move the row to one repo, or to the project-wide
	// scope.
	RebindRepo   string `json:"rebind_repo,omitempty" flag:"rebind-repo" desc:"move this memory to repo <mount> (sets repo_id)"`
	RebindMaster bool   `json:"rebind_master,omitempty" flag:"rebind-master" desc:"move this memory to the project-wide scope (clears repo_id)"`
	// Scope moves the memory to another scope (6.2.3, CQ12).
	Scope string `json:"scope,omitempty" flag:"scope" desc:"move to another scope: project | repo | dir:<path>"`
	// Tags adds and Untags removes tags by name (repeatable).
	Tags   []string `json:"tags,omitempty" flag:"tag" desc:"add this tag (repeatable; the tag must exist)"`
	Untags []string `json:"untags,omitempty" flag:"untag" desc:"remove this tag (repeatable)"`
}

// DeleteIn is the input of memory.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
}

// MoveIn is the input of memory.move (PROV5: the row keeps its id).
type MoveIn struct {
	ID string `json:"id" arg:"0" desc:"the memory id (mem_…)"`
	To string `json:"to" flag:"to" desc:"the attached store to move it to (a name from .lore/stores.yaml, or repo)"`
}

// SearchIn is the input of memory.search.
type SearchIn struct {
	// Repo scope (v0.1.17 repo_scope.go; op structs cannot embed, so the
	// four fields repeat on ListIn): no flag = project-wide memories
	// only; --repo adds that repo's; --no-inherit keeps only that repo's.
	Repo       string   `json:"repo,omitempty" flag:"repo" desc:"include this repo's memories (mount name or rep_ id)"`
	NoInherit  bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"with --repo: only that repo's memories, not the project-wide ones"`
	MasterOnly bool     `json:"master_only,omitempty" flag:"master-only" desc:"only project-wide memories"`
	AllRepos   bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"memories of every repo"`
	Project    string   `json:"project,omitempty" flag:"project" desc:"only memories of this project (prj_ id or name; default the repo's one project)"`
	Query      string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Columns    []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these full-text fields (repeatable)"`
	// Limit caps the hits AFTER the scope filter; 0 means
	// DefaultSearchLimit.
	Limit           int  `json:"limit,omitempty" flag:"limit" desc:"at most this many results (default 10)"`
	IncludeArchived bool `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived memories"`
}

// SearchOut is the output of memory.search: v0.1.17's search envelope
// (search_shared.go entitySearchEnvelope; count is always printed).
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// SearchHit is one v0.1.17 search hit (entitySearchHit): id, BM25 score,
// snippet and the row's searchable columns.
type SearchHit struct {
	ID      string  `json:"id"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
	Row     HitRow  `json:"row"`
	// Store is set for a hit of a store other than the repo's own.
	Store store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// HitRow is the row map v0.1.17's memory search printed
// (search_entities.go at 4e6ed1c): body, kind, source_kind, trust_score —
// json.Marshal of that map sorted the keys alphabetically, as here.
type HitRow struct {
	Body       string  `json:"body"`
	Kind       string  `json:"kind"`
	SourceKind string  `json:"source_kind"`
	TrustScore float64 `json:"trust_score"`
}
