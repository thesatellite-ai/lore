// Package schema is the one definition of every table's shape: built-in
// tables declared in Go with Table(...), and resource kinds loaded at run
// time from kind rows into the SAME TableDef type (KindDefFromRows). The
// SQLite cache builds its tables, indexes and join tables from these
// definitions, the validator checks fields against them, and the surfaces
// read their UI hints, so nothing is generated and nothing is declared twice.
//
// Layer L1: imports model only. A Registry is a value (NewRegistry), never a
// package global, so two stores with different kinds can each have their own.
package schema

import (
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
)

// TableDef is one table.
//
// The envelope fields (id, _hlc, audit fields, _o, _deleted, meta) are
// implicit on every table and never appear in Fields.
type TableDef struct {
	Name   row.Table
	Prefix ids.Prefix
	// Description is one line for docs and the admin UI.
	Description string
	Fields      []FieldDef
	Relations   []Relation
	Indexes     []Index
	// Synced tables are stored rows that travel with the store (every
	// table a backend holds). A non-synced table is a cache-only projection.
	Synced bool
	// FTS names the text fields the cache indexes for full-text search.
	FTS []string
	// FTSWeights overrides the bm25 weight of an FTS field (FTSWeightOf
	// gives the default); nil when no field is overridden.
	FTSWeights map[string]float64
	// Kind is set only for a definition built from kind rows: its rows live
	// in the `resources` table with `kind` = this id, and Name is the typed
	// cache table "res_<kind name>" built from them (spec 4.4).
	Kind ids.ID[ids.Kind]
	// GlobalKey: the natural id ignores the store (engine naturalParts), so
	// a row with the same key has one id in every store (actors).
	GlobalKey bool
}

// Field returns the field named name.
func (t TableDef) Field(name string) (FieldDef, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldDef{}, false
}

// MergeStrategy is the closed set of per-field merge strategies logic/merge
// applies when both sides of a three-way merge changed the field.
type MergeStrategy string

const (
	// MergeDefault is the row rule: equal sides agree, otherwise the newer
	// side by HLC (free text gets conflict markers).
	MergeDefault MergeStrategy = ""
	// MergeListByID unions a JSON list of objects keyed by "id": an entry on
	// either side is kept, the same id on both sides takes the newer
	// "updated_at" (TIME_TRACKING_DESIGN 7.2). Used by tasks.time_entries.
	MergeListByID MergeStrategy = "list_by_id"
	// MergeSetUnion is the three-way set merge of a list by normalised
	// value: adds from both sides kept, a removal kept unless the other
	// side added it anew (RQ5). Used by tasks.assignees, tasks.watchers,
	// tasks.blocked_by.
	MergeSetUnion MergeStrategy = "set_union"
	// MergeURLSet is MergeSetUnion keyed by the normalised url of each
	// entry (PROVENANCE_DESIGN 7, 10.2). Used by repos.urls.
	MergeURLSet MergeStrategy = "url_set"
	// MergeEntriesByID merges an object keyed by a permanent id entry by
	// entry ({name, v, strict, sensitive}): lists as sets, strict clashes go
	// to a person (RESOURCES 3.13, R4, RQ5). Used by resources.fields.
	MergeEntriesByID MergeStrategy = "entries_by_id"
)

// FieldDef is one column of a table.
type FieldDef struct {
	Name string
	Kind value.Kind
	// Elem is the item kind of a list field; empty otherwise.
	Elem value.Kind
	// RefTable is the table a ref field points at; empty means "any table"
	// (the id's prefix names it, as in secret_allows.row_id).
	RefTable row.Table
	// RefKind is set on a kind's ref field (kind_fields.ref_kind): the ref
	// must point at a resource of that kind. RefTable is then "resources".
	RefKind ids.ID[ids.Kind]
	// Values is the closed set of allowed text values (memories.kind), empty
	// when the field is free. Drives validation and the select widget.
	Values    []string
	Required  bool
	Sensitive bool // hidden from agents and sql output
	Internal  bool // never written into a client repo
	// LoreRecord marks a structured record only lore itself writes
	// (tasks.claim_log). Its session ids are lore's own bookkeeping, so the
	// session-id rule (scan.RuleSessionID) does not apply to it; every
	// other secret rule still does. Free text people write never carries
	// it (BUG-T7).
	LoreRecord bool
	// Merge is the field's merge strategy (MergeDefault when empty).
	Merge       MergeStrategy
	Description string
	UI          UI
}

// Index is one cache index over a table's fields.
type Index struct {
	Fields []string
	Unique bool
}

// RelationKind is the closed set of relation shapes (CODE ORG 2A).
type RelationKind string

// Relation kinds.
const (
	// BelongsTo: this table's Field holds the id of a Target row.
	BelongsTo RelationKind = "belongs_to"
	// HasMany: Target's Field holds the id of a row of this table (the
	// inverse of a BelongsTo, declared for joins, nested routes and docs).
	HasMany RelationKind = "has_many"
	// ManyToMany: Field is a list-of-ref field on this table holding the
	// Target ids (memories.tag_ids → tags, decision O11). The links travel
	// inside the owning row, so they share its envelope, merge rule and
	// sync path; JoinTable is a cache-only table of (from id, to id) pairs
	// the cache fills from Field on every apply, for joins and reverse
	// lookups. It is not a registered table and has no id prefix.
	ManyToMany RelationKind = "many_to_many"
	// Self: a BelongsTo whose Target is this table (supersession chains).
	Self RelationKind = "self"
	// Polymorphic: TypeField names the target table and Field holds its id.
	Polymorphic RelationKind = "polymorphic"
)

// RelationKinds lists every kind.
func RelationKinds() []RelationKind {
	return []RelationKind{BelongsTo, HasMany, ManyToMany, Self, Polymorphic}
}

// OnDelete is what happens to rows that point at a row being deleted.
type OnDelete string

// OnDelete behaviours.
const (
	// Restrict refuses the delete while pointing rows are alive
	// (E_HAS_DEPENDENTS).
	Restrict OnDelete = "restrict"
	// Cascade deletes (tombstones) the pointing rows with it.
	Cascade OnDelete = "cascade"
	// SetNull clears the pointing field; the field must be optional.
	SetNull OnDelete = "set_null"
	// TombstoneOnly leaves pointing rows alone; they keep pointing at the
	// tombstone, which still answers "what was this".
	TombstoneOnly OnDelete = "tombstone_only"
)

// OnDeletes lists every behaviour.
func OnDeletes() []OnDelete { return []OnDelete{Restrict, Cascade, SetNull, TombstoneOnly} }

// Relation is one link between tables.
//
// Relations are metadata, never database constraints (DOCS O2): the SQLite
// cache enforces no foreign keys on any table, because it is a rebuildable
// copy and rows may point into other stores or dangle by design (RES-OQ2).
// They drive the cache's indexes (every ref column is indexed), the
// engine's write-time ref check, joins, nested routes, docs and lint.
//
// Field meaning per kind: BelongsTo/Self/Polymorphic — the local ref
// field holding the id; HasMany — the ref field on Target holding this
// table's id; ManyToMany — the local list-of-ref field holding Target ids.
type Relation struct {
	Name      string
	Kind      RelationKind
	Field     string
	Target    row.Table
	TypeField string // Polymorphic only: the local text field naming the target table
	// JoinTable is the cache-only join table a ManyToMany fills from Field
	// (empty for every other kind). NewRegistry refuses a name that is also
	// a registered table or used by another relation.
	JoinTable row.Table
	// OnDelete is behaviour the engine applies when the target is deleted;
	// it is never a database constraint. For a ManyToMany it acts on the
	// link, never on the owning row: Cascade and SetNull remove the deleted
	// id from the list, Restrict refuses the delete while a live row lists
	// it, TombstoneOnly leaves the id in the list.
	OnDelete OnDelete
	// AllowDangling lets a write keep a ref whose target is not in any
	// visible store (rows may reference other stores, RES-OQ2). False, the
	// default, makes the engine refuse a write whose target is missing (fail
	// closed); lore lint still reports dangling refs either way.
	AllowDangling bool
}

// FTS bm25 weights by field role: v0.1.17's search registry convention
// (saas/pkg/aicoder/fts5 Registry: title or name 4, body or description 3,
// any other column 1), kept so `lore search` ranks hits as v0.1.17 did.
const (
	ftsWeightTitle = 4
	ftsWeightBody  = 3
	ftsWeightOther = 1
)

// FTSWeightOf is the bm25 weight of FTS field name: FTSWeights[name] when
// set, else the role convention above.
func (t TableDef) FTSWeightOf(name string) float64 {
	if w, ok := t.FTSWeights[name]; ok {
		return w
	}
	switch name {
	case "title", "name":
		return ftsWeightTitle
	case "body", "description":
		return ftsWeightBody
	}
	return ftsWeightOther
}
