// Package comment defines the `comment` noun's operations (layer L5): add,
// list, search and delete, with v0.1.17's flag spellings (`lore comment add
// --on-table --on-id --body`).
//
// A comment points at any row through the polymorphic (entity_table,
// entity_id) pair. When the target's table is on the core, the target must
// be a live row of that table (fail closed); a target in a table not yet on
// the core is accepted as v0.1.17 accepted every target.
package comment

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/pointer"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Operation names (`lore comment …`, REST /v1/comments).
const (
	Noun     = "comment"
	OpAdd    = "comment.add"
	OpList   = "comment.list"
	OpSearch = "comment.search"
	OpDelete = "comment.delete"
)

// Envelope values of v0.1.17's --json.
const (
	SchemaVersion = 1
	KindList      = "comment.list"
	KindSearch    = "comment.search"
)

// Search limits (v0.1.17 --limit default 20).
const (
	DefaultSearchLimit = 20
	maxSearchLimit     = 1000
	// searchBodyMax is how much of a body a search line shows (v0.1.17
	// trunc(body, 80)); listBodyMax the list excerpt (60, cut at 57+...).
	searchBodyMax = 80
	listBodyMax   = 60
	listBodyCut   = 57
	// ftsColumnBody is the one full-text column of comments (--column).
	ftsColumnBody = "body"
)

// Comment field names.
const (
	fieldBody      = "body"
	fieldCreatedBy = "created_by_actor_id"
)

// table is the comments table.
const table row.Table = schema.TableComments

// Env is what the comment operations use; *wire.App implements it.
//
// Invariant: every write goes through Create or Delete (the engine's one
// write path: validation, secret scan, policy, audit).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
	Search(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error)
}

// AddIn is the input of comment.add.
type AddIn struct {
	// MetaFlags: --meta, --meta-json, --meta-stdin (engine binds them, R13).
	engine.MetaFlags

	OnTable string `json:"on_table" flag:"on-table" desc:"target entity table (memories|rules|decisions|...)"`
	OnID    string `json:"on_id" flag:"on-id" desc:"target entity id"`
	Body    string `json:"body" flag:"body" desc:"the comment text" ui:"widget=markdown"`
	To      string `json:"to,omitempty" flag:"to" desc:"store to write to (a name from .lore/stores.yaml; default repo)"`
}

// AddOut is the output of comment.add.
type AddOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	OnTable  string    `json:"on_table"`
	OnID     string    `json:"on_id"`
	Warnings []Warning `json:"warnings,omitempty"`
	// label is the store's `_label` ("" for the repo): v0.1.17 printed
	// route.Label at the end of the add line.
	label string
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Text is v0.1.17's line: "✓ comment <id> on <table>/<id> <label>"; the
// label is empty for a repo comment and v0.1.17 kept the space before it.
func (o AddOut) Text() string {
	out := "✓ comment " + o.ID + " on " + o.OnTable + "/" + o.OnID + " " + o.label + "\n"
	for _, w := range o.Warnings { // OV10: metadata notes like meta set
		out += metalist.NoteLine(w.Code, w.Message)
	}
	return out
}

// ListIn is the input of comment.list.
type ListIn struct {
	OnTable string `json:"on_table,omitempty" flag:"on-table" desc:"filter by entity table"`
	OnID    string `json:"on_id,omitempty" flag:"on-id" desc:"filter by entity id"`
	Store   string `json:"store,omitempty" flag:"store" desc:"only comments of this store (default every attached store)"`
}

// ListOut is the output of comment.list: v0.1.17's envelope.
type ListOut struct {
	SchemaVersion int      `json:"schema_version"`
	Kind          string   `json:"kind"`
	Count         int      `json:"count,omitempty"`
	Data          []Record `json:"data"`
}

// Text is v0.1.17's list: "<id> on <table>/<id>  <excerpt>".
func (o ListOut) Text() string {
	if len(o.Data) == 0 {
		return "(no comments)\n"
	}
	var b strings.Builder
	for _, c := range o.Data {
		ex := textcut.Cut(c.Body, listBodyMax, listBodyCut)
		fmt.Fprintf(&b, "%s on %s/%s  %s\n", c.ID, c.EntityTable, c.EntityID, ex)
	}
	return b.String()
}

// Record is one comment in v0.1.17's --json form (the ent Comment struct).
type Record struct {
	ID               string          `json:"id"`
	CreatedAt        string          `json:"created_at,omitempty"`
	UpdatedAt        string          `json:"updated_at,omitempty"`
	EntityTable      string          `json:"entity_table"`
	EntityID         string          `json:"entity_id"`
	EntityProjectID  string          `json:"entity_project_id,omitempty"`
	Body             string          `json:"body"`
	CreatedByActorID string          `json:"created_by_actor_id,omitempty"`
	Store            store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
}

// SearchIn is the input of comment.search. The repo-scope flags are
// v0.1.17's shared search flags; comments carry no repo scope, so they are
// accepted and change nothing.
type SearchIn struct {
	Query           string   `json:"query" arg:"0" desc:"full-text query: words, \"exact phrase\", prefix*, AND / OR / NOT"`
	Limit           int      `json:"limit,omitempty" flag:"limit" desc:"max hits to return (default 20)"`
	Columns         []string `json:"columns,omitempty" flag:"column" desc:"restrict MATCH to these FTS5 columns (repeatable; comments have: body)"`
	IncludeArchived bool     `json:"include_archived,omitempty" flag:"include-archived" desc:"include archived rows (comments are never archived)"`
	Repo            string   `json:"repo,omitempty" flag:"repo" desc:"accepted for v0.1.17 scripts; comments have no repo scope"`
	NoInherit       bool     `json:"no_inherit,omitempty" flag:"no-inherit" desc:"accepted for v0.1.17 scripts; comments have no repo scope"`
	MasterOnly      bool     `json:"master_only,omitempty" flag:"master-only" desc:"accepted for v0.1.17 scripts; comments have no repo scope"`
	AllRepos        bool     `json:"all_repos,omitempty" flag:"all-repos" desc:"accepted for v0.1.17 scripts; comments have no repo scope"`
}

// SearchOut is v0.1.17's search envelope (count always printed).
type SearchOut struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// Text is v0.1.17's search output: "<id> [<table>/<id>] <body…>".
func (o SearchOut) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		body := textcut.Cut(h.Row.Body, searchBodyMax, searchBodyMax)
		fmt.Fprintf(&b, "%s [%s/%s] %s\n", h.ID, h.Row.EntityTable, h.Row.EntityID, body)
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

// HitRow is the row map v0.1.17's comment search printed (keys sorted).
type HitRow struct {
	Body        string `json:"body"`
	EntityID    string `json:"entity_id"`
	EntityTable string `json:"entity_table"`
}

// DeleteIn is the input of comment.delete.
type DeleteIn struct {
	ID     string `json:"id" arg:"0" desc:"the comment id (cmt_…)"`
	Reason string `json:"reason,omitempty" flag:"reason" desc:"why it is deleted (stored as deleted_reason; required when policy delete.require_reason is true)"`
	// PurgeFlags: --purge / --confirm (engine binds them, P3-DEL-1).
	op.PurgeFlags
}

// DeleteOut is the output of comment.delete.
type DeleteOut struct {
	ID    string `json:"id"`
	Store string `json:"store"`
}

// Text is the delete line (v0.1.17 said "deleted (hard)"; DQ8 every
// delete is soft, so it names the trash and the restore command).
func (o DeleteOut) Text() string { return trashline.Moved(o.ID, o.ID) }

// writeErrors are the codes a comment write can return.
var writeErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.EmptyBody, errs.NotFound, errs.AmbiguousID,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// readErrors are the codes a comment read can return.
var readErrors = []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// Ops returns every comment operation bound to env.
func Ops(env Env) []op.Entry {
	o := ops{env: env}
	return []op.Entry{
		op.Define(op.Spec{
			Name: OpAdd, Summary: "Add a comment on a target entity",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm: op.ConfirmRule{MayAsk: true},
			Examples: []op.Example{{Title: "comment on a memory",
				CLI: `lore comment add --on-table=memories --on-id=mem_<id> --body="agreed; revisit Q3"`, Want: "✓ comment cmt_"}},
		}, o.add),
		op.Define(op.Spec{
			Name: OpList, Summary: "List comments (optionally filtered by entity)",
			Permission: op.PermRead, Access: op.AccessRead, Errors: readErrors,
			Examples: []op.Example{{Title: "comments of one row", CLI: "lore comment list --on-id=mem_<id> --json", Want: `"data"`}},
		}, o.list),
		op.Define(op.Spec{
			Name: OpSearch, Summary: "FTS5 search across comment body",
			Permission: op.PermRead, Access: op.AccessRead,
			Errors: []errs.Code{errs.InvalidInput, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal},
		}, o.search),
		op.Define(op.Spec{
			Name: OpDelete, Summary: "Delete a comment (it goes to the trash; lore trash restore brings it back)",
			Permission: op.PermWrite, Access: op.AccessWrite, Errors: writeErrors,
			Confirm:  op.ConfirmRule{Always: true},
			Examples: []op.Example{{Title: "delete without a terminal", CLI: "lore comment delete cmt_<id> --yes", Want: "moved to the trash"}},
		}, o.delete),
	}
}

// ops binds the run functions to one Env.
type ops struct{ env Env }

// add checks the target and writes the comment.
func (o ops) add(ctx context.Context, in AddIn) (AddOut, error) {
	if strings.TrimSpace(in.Body) == "" {
		return AddOut{}, errs.New(errs.EmptyBody, "comment add: the body is empty").
			WithHint(`lore comment add --on-table=<table> --on-id=<id> --body="<text>"`)
	}
	tbl, id, err := o.target(ctx, store.Source(in.To), in.OnTable, in.OnID)
	if err != nil {
		return AddOut{}, err
	}
	fields := map[string]value.Value{}
	if fields[schema.FieldEntityTable], err = value.TextOf(string(tbl)); err != nil {
		return AddOut{}, err
	}
	if fields[schema.FieldEntityID], err = value.RefOf(id); err != nil {
		return AddOut{}, err
	}
	if fields[fieldBody], err = value.TextOf(in.Body); err != nil {
		return AddOut{}, errs.Wrap(errs.CodeOf(err), "comment add: body", err)
	}
	w, err := o.env.Create(ctx, store.Source(in.To), table, fields)
	if err != nil {
		return AddOut{}, err
	}
	src := o.storeOf(ctx, w.Row.ID, store.Source(in.To))
	out := AddOut{ID: w.Row.ID.String(), Store: src, OnTable: string(tbl), OnID: id.String()}
	out.label = opsmemory.Label(opsmemory.LabelerOf(o.env), store.Source(src))
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out, nil
}

// target validates --on-table/--on-id. A table on the core must hold the
// id as a live row (E_NOT_FOUND otherwise) and the id must carry the
// table's prefix, and F15 must allow a comment written to store to point at
// it (ops/internal/pointer); a table not on the core only needs a valid id.
func (o ops) target(ctx context.Context, to store.Source, rawTable, rawID string) (row.Table, ids.AnyID, error) {
	tbl, err := row.ParseTable(strings.TrimSpace(rawTable))
	if err != nil {
		return "", ids.AnyID{}, errs.Wrap(errs.InvalidInput, "comment: --on-table "+quote(rawTable)+" is not a table name", err)
	}
	id, err := ids.ParseAny(strings.TrimSpace(rawID))
	if err != nil {
		return "", ids.AnyID{}, lookup.BadID("comment: --on-id "+quote(rawID)+" is not an id", err).
			WithHint("use `lore <kind> list` to find the real id")
	}
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		return "", ids.AnyID{}, errs.Wrap(errs.Internal, "comment: built-in schema", err)
	}
	def, onCore := reg.Table(tbl)
	if !onCore {
		return tbl, id, nil
	}
	if id.Prefix() != def.Prefix {
		return "", ids.AnyID{}, lookup.BadID("comment: "+id.String()+" is not an id of "+string(tbl)+" ("+string(def.Prefix)+"_…)", nil)
	}
	_, r, err := o.env.Get(ctx, engine.Ref{Table: tbl, ID: id})
	if err == nil && r.Deleted { // DELETION 3.2: E_IN_TRASH for a trashed target
		err = lookup.Gone(ctx, o.env, engine.Ref{Table: tbl, ID: id}, errs.New(errs.NotFound, "comment: "+id.String()+" is deleted"))
	}
	if err != nil {
		return "", ids.AnyID{}, err
	}
	if err := pointer.Check(ctx, o.env, to, engine.Ref{Table: tbl, ID: id}); err != nil {
		return "", ids.AnyID{}, err
	}
	return tbl, id, nil
}

// list returns the live comments of every attached store (or one), oldest
// id first as v0.1.17 ordered them.
func (o ops) list(ctx context.Context, in ListIn) (ListOut, error) {
	sources := o.env.Stores()
	if in.Store != "" {
		sources = []store.Source{store.Source(in.Store)}
	}
	out := ListOut{SchemaVersion: SchemaVersion, Kind: KindList, Data: []Record{}}
	for _, src := range sources {
		rows, err := o.env.List(ctx, src, table)
		if err != nil {
			return ListOut{}, err
		}
		for _, r := range rows {
			if r.Deleted || (in.OnTable != "" && field(r, schema.FieldEntityTable) != in.OnTable) ||
				(in.OnID != "" && field(r, schema.FieldEntityID) != in.OnID) {
				continue
			}
			out.Data = append(out.Data, recordOf(opsmemory.LabelerOf(o.env), src, r))
		}
	}
	sortByID(out.Data)
	out.Count = len(out.Data)
	return out, nil
}

// search matches the query against every attached store's cache.
func (o ops) search(ctx context.Context, in SearchIn) (SearchOut, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return SearchOut{}, errs.New(errs.InvalidInput, "comment search: empty query")
	}
	limit := in.Limit
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 0 || limit > maxSearchLimit:
		return SearchOut{}, errs.New(errs.InvalidInput, "comment search: --limit must be between 1 and 1000")
	}
	for _, c := range in.Columns {
		if c != ftsColumnBody {
			return SearchOut{}, errs.New(errs.InvalidInput, "comment search: unknown --column "+quote(c)+" (comments have: body)")
		}
	}
	hits, err := o.env.Search(ctx, table, in.Query, maxSearchLimit)
	if err != nil {
		return SearchOut{}, err
	}
	out := SearchOut{SchemaVersion: SchemaVersion, Kind: KindSearch, Query: in.Query}
	for _, h := range hits {
		if len(out.Data) == limit {
			break
		}
		src, r, err := o.env.Get(ctx, engine.Ref{Store: h.Source, Table: table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return SearchOut{}, err
		}
		if r.Deleted {
			continue
		}
		hit := SearchHit{ID: r.ID.String(), Score: h.Score, Snippet: h.Snippet, Row: HitRow{
			Body: field(r, fieldBody), EntityID: field(r, schema.FieldEntityID), EntityTable: field(r, schema.FieldEntityTable),
		}}
		if src != store.SourceRepo {
			hit.Store = opsmemory.SourceOf(opsmemory.LabelerOf(o.env), src)
			hit.Label = opsmemory.Label(opsmemory.LabelerOf(o.env), src)
		}
		out.Data = append(out.Data, hit)
	}
	out.Count = len(out.Data)
	return out, nil
}

// delete writes the comment's tombstone.
func (o ops) delete(ctx context.Context, in DeleteIn) (DeleteOut, error) {
	ctx = engine.WithDeleteReason(ctx, in.Reason) // DELETION 2.1 --reason; E_REASON_REQUIRED under delete.require_reason
	id, err := ids.ParseAny(strings.TrimSpace(in.ID))
	if err != nil || id.Prefix() != ids.PrefixComment {
		return DeleteOut{}, lookup.BadID("comment: "+quote(in.ID)+" is not a comment id (cmt_…)", nil)
	}
	src := o.storeOf(ctx, id, "")
	w, err := o.env.Delete(ctx, engine.Ref{Table: table, ID: id})
	if err != nil {
		return DeleteOut{}, err
	}
	return DeleteOut{ID: w.Row.ID.String(), Store: string(src)}, nil
}

// storeOf names the store holding id after a write.
func (o ops) storeOf(ctx context.Context, id ids.AnyID, requested store.Source) string {
	if requested != "" {
		return string(requested)
	}
	src, _, err := o.env.Get(ctx, engine.Ref{Table: table, ID: id})
	if err != nil {
		return ""
	}
	return string(src)
}

// recordOf is the v0.1.17 --json form of a comment row. A 0.1.18 row
// without created_by_actor_id shows the envelope's created_by.
func recordOf(lab opsmemory.Labeler, src store.Source, r row.Row) Record {
	rec := Record{
		ID: r.ID.String(), CreatedAt: timeText(r.CreatedAt), UpdatedAt: timeText(r.UpdatedAt),
		EntityTable: field(r, schema.FieldEntityTable), EntityID: field(r, schema.FieldEntityID),
		EntityProjectID: field(r, "entity_project_id"), Body: field(r, fieldBody),
		CreatedByActorID: field(r, fieldCreatedBy),
	}
	if rec.CreatedByActorID == "" && !r.CreatedBy.IsZero() {
		rec.CreatedByActorID = r.CreatedBy.String()
	}
	if src != store.SourceRepo {
		rec.Store = opsmemory.SourceOf(lab, src)
		rec.Label = opsmemory.Label(lab, src)
	}
	return rec
}

// sortByID orders records by id (time-ordered ids: oldest first).
func sortByID(rs []Record) {
	slices.SortStableFunc(rs, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
}

// field returns the text form of a row field ("" when absent or null).
func field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// timeText renders an envelope time as v0.1.17's JSON did.
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// maxQuoted bounds how much of a user value an error repeats.
const maxQuoted = 48

// quote renders a user value for an error, truncated.
func quote(s string) string {
	return `"` + textcut.Cut(s, maxQuoted, maxQuoted) + `"`
}
