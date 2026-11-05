// Package content holds what the pattern, playbook and prompt operations
// (ops/pattern, ops/playbook, ops/prompt) share: the Env they run on and
// the row helpers every one of them needs (id parsing, field reads, write
// outputs, v0.1.17's human lines). The three nouns are v0.1.17's
// "generated" knowledge commands (saas/cmd/cli/generated_commands.go at
// 4e6ed1c), so their verbs and outputs are the same shape; each noun keeps
// its own typed inputs and outputs in its own package.
package content

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/metalist"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	"github.com/thesatellite-ai/lore/ops/internal/trashline"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is everything a content operation uses: the engine's row API plus
// the cache search and the repo's project, as for memories (the
// composition root's *wire.App implements both).
//
// Invariant: every write goes through Create, Update or Delete, the
// engine's one write path (scan, validate, policy, audit).
type Env interface {
	Create(ctx context.Context, target store.Source, table row.Table, fields map[string]value.Value) (engine.Written, error)
	Update(ctx context.Context, ref engine.Ref, patch map[string]value.Value) (engine.Written, error)
	Delete(ctx context.Context, ref engine.Ref) (engine.Written, error)
	Get(ctx context.Context, ref engine.Ref) (store.Source, row.Row, error)
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	// Stores lists the attached stores in precedence order.
	Stores() []store.Source
	// Search runs a full-text match over table in every attached store's
	// cache, best first, at most limit hits in total.
	Search(ctx context.Context, table row.Table, match string, limit int) ([]opsmemory.Hit, error)
	// Project is the repo's one project (new rows belong to it).
	Project(ctx context.Context) (ids.ID[ids.Project], error)
	// RepoID resolves a --repo value (mount name or rep_ id).
	RepoID(ctx context.Context, mountOrID string) (ids.ID[ids.Repo], error)
}

// SchemaVersion is v0.1.17's --json envelope schema_version.
const SchemaVersion = 1

// DefaultSource is the source_kind a new row gets (v0.1.17 SourceManual).
const DefaultSource = "manual"

// DefaultTrust is v0.1.17's trust_score default (LifecycleMixin).
const DefaultTrust = 0.5

// Search limits: DefaultSearchLimit is v0.1.17's `<kind> search --limit`
// default; MaxSearchLimit bounds one search so a REST caller cannot ask the
// cache for an unbounded result.
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 1000
)

// Mark is v0.1.17's success mark (style.Success("✓"), uncoloured).
const Mark = "✓"

// Shared field names of the content tables (schema/builtin_content.go).
const (
	FieldProject    = "project_id"
	FieldRepo       = "repo_id"
	FieldBody       = "body"
	FieldArchivedAt = "archived_at"
	FieldCreatedBy  = "created_by_actor_id"
	FieldSourceKind = "source_kind"
	FieldSourceRef  = "source_ref"
	FieldTrust      = "trust_score"
	FieldConfidence = "confidence"
	FieldValidated  = "last_validated_at"
	FieldName       = "name"
)

// WriteErrors are the codes any content write can return.
func WriteErrors() []errs.Code {
	return []errs.Code{
		errs.InvalidInput, errs.InvalidID, errs.EmptyBody, errs.NotFound, errs.AmbiguousID,
		errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
		errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
	}
}

// DeleteErrors are WriteErrors plus E_HAS_DEPENDENTS and E_REASON_REQUIRED.
func DeleteErrors() []errs.Code {
	return append(WriteErrors(), errs.HasDependents, errs.ReasonRequired)
}

// ReadErrors are the codes a content read can return.
func ReadErrors() []errs.Code {
	return []errs.Code{errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}
}

// SearchErrors are the codes a content search can return.
func SearchErrors() []errs.Code {
	return []errs.Code{errs.InvalidInput, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal}
}

// WriteOut is the output of every content write: the row id, the store
// that holds it, any warnings, and v0.1.17's human line.
type WriteOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Warnings []Warning `json:"warnings,omitempty"`
	// Human is the v0.1.17 line the op set (CLI only, never JSON).
	Human string `json:"-"`
}

// Text implements op.Texter.
func (o WriteOut) Text() string {
	// OV10: the metadata notes of an add/edit --meta, as meta set prints them
	var b strings.Builder
	b.WriteString(o.Human)
	for _, w := range o.Warnings {
		b.WriteString(metalist.NoteLine(w.Code, w.Message))
	}
	return b.String()
}

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Written is the WriteOut of a landed write, with human line line.
func Written(src store.Source, w engine.Written, line string) WriteOut {
	out := WriteOut{ID: w.Row.ID.String(), Store: string(src), Human: line}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out
}

// ParseID parses an id argument of a row with prefix p (E_INVALID_ID
// naming noun otherwise).
func ParseID(noun string, p ids.Prefix, s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err == nil && id.Prefix() != p {
		err = errs.New(errs.NotFound, "wrong prefix")
	}
	if err != nil {
		return ids.AnyID{}, lookup.BadID(noun+": "+QuoteID(s)+" is not a "+noun+" id ("+string(p)+"_…)", err)
	}
	return id, nil
}

// maxQuotedID bounds how much of a bad id an error repeats.
const maxQuotedID = 48

// QuoteID renders a user-given value for an error, truncated on a
// character boundary (textcut; ASCII bytes as v0.1.17's s[:48]+"...").
func QuoteID(s string) string {
	return `"` + textcut.Cut(s, maxQuotedID, maxQuotedID) + `"`
}

// Field returns the text form of a row field ("" when absent or null).
func Field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
}

// Number is a number field, 0 when absent or not a number.
func Number(r row.Row, name string) float64 {
	n, ok := r.Fields[name].(value.Number)
	if !ok {
		return 0
	}
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return f
}

// Time is a datetime field (zero when absent or not a datetime).
func Time(r row.Row, name string) time.Time {
	switch v := r.Fields[name].(type) {
	case value.DateTime:
		return v.Time()
	case value.Text:
		if dt, err := value.DateTimeOf(v.String()); err == nil {
			return dt.Time()
		}
	}
	return time.Time{}
}

// CreatedBy is the row's created_by_actor_id, or the envelope's creator
// (a 0.1.18 row that never set the column names the same actor there).
func CreatedBy(r row.Row) string {
	if s := Field(r, FieldCreatedBy); s != "" {
		return s
	}
	if !r.CreatedBy.IsZero() {
		return r.CreatedBy.String()
	}
	return ""
}

// Texts turns the non-empty strings of m into text values (empty = not
// given), in a stable order so an error names the same field every run.
func Texts(noun string, m map[string]string) (map[string]value.Value, error) {
	out := map[string]value.Value{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if m[k] == "" {
			continue
		}
		t, err := value.TextOf(m[k])
		if err != nil {
			return nil, errs.Wrap(errs.CodeOf(err), noun+": field "+k, err)
		}
		out[k] = t
	}
	return out, nil
}

// Body returns the body from --body or the positional argument: exactly
// one, not blank (E_EMPTY_BODY), as v0.1.17's resolveBodyInput.
func Body(noun, flag, arg string) (string, error) {
	if flag != "" && arg != "" {
		return "", errs.New(errs.InvalidInput, noun+" add: give the body once, as an argument or with --body, not both")
	}
	body := flag + arg
	if strings.TrimSpace(body) == "" {
		return "", errs.New(errs.EmptyBody, noun+" add: the body is empty").
			WithHint("example: lore " + noun + ` add --title=X --body="the body"`)
	}
	return body, nil
}

// Required refuses an empty required flag the way cobra's
// MarkFlagRequired did in v0.1.17.
func Required(flag, v string) error {
	if strings.TrimSpace(v) == "" {
		return errs.New(errs.InvalidInput, `required flag(s) "`+flag+`" not set`)
	}
	return nil
}

// ActorRef parses a --created-by value (an act_ id; the engine's ref
// check refuses a missing actor row).
func ActorRef(noun, raw string) (value.Value, error) {
	a, err := ids.Parse[ids.Actor](strings.TrimSpace(raw))
	if err != nil {
		return nil, lookup.BadID(noun+": "+QuoteID(raw)+" is not an actor id (act_…)", err)
	}
	return value.RefOf(a.Any())
}

// Trust is the default trust_score value.
func Trust() (value.Value, error) { return value.Float(DefaultTrust) }

// Ops binds the shared run steps to one Env and table.
type Ops struct {
	Env   Env
	Table row.Table
	// Noun is the CLI noun ("pattern"), used in messages.
	Noun   string
	Prefix ids.Prefix
}

// Live returns the live row id names, from whichever store holds it.
func (o Ops) Live(ctx context.Context, raw string) (ids.AnyID, store.Source, row.Row, error) {
	id, err := ParseID(o.Noun, o.Prefix, raw)
	if err != nil {
		return ids.AnyID{}, "", row.Row{}, err
	}
	src, r, err := o.Env.Get(ctx, engine.Ref{Table: o.Table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		notFound := errs.New(errs.NotFound, o.Noun+" "+QuoteID(raw)+" not found")
		if err == nil {
			return ids.AnyID{}, "", row.Row{}, lookup.Gone(ctx, o.Env, engine.Ref{Table: o.Table, ID: id}, notFound) // DELETION 3.2
		}
		return ids.AnyID{}, "", row.Row{}, notFound
	}
	if err != nil {
		return ids.AnyID{}, "", row.Row{}, err
	}
	return id, src, r, nil
}

// Patch updates one live row through the engine; line is its v0.1.17
// human line with %s for the id.
func (o Ops) Patch(ctx context.Context, raw string, patch map[string]value.Value, line func(id string) string) (WriteOut, error) {
	id, src, _, err := o.Live(ctx, raw)
	if err != nil {
		return WriteOut{}, err
	}
	w, err := o.Env.Update(ctx, engine.Ref{Store: src, Table: o.Table, ID: id}, patch)
	if err != nil {
		return WriteOut{}, err
	}
	return Written(src, w, line(id.String())), nil
}

// Create writes a new row into target (default store when empty) and
// names the store that holds it.
func (o Ops) Create(ctx context.Context, target string, fields map[string]value.Value) (WriteOut, error) {
	w, err := o.Env.Create(ctx, store.Source(target), o.Table, fields)
	if err != nil {
		return WriteOut{}, err
	}
	src := store.Source(target)
	if src == "" {
		if s, _, gerr := o.Env.Get(ctx, engine.Ref{Table: o.Table, ID: w.Row.ID}); gerr == nil {
			src = s
		}
	}
	return Written(src, w, Mark+" "+w.Row.ID.String()+"\n"), nil
}

// Archive sets archived_at to now ("✓ <id> archived").
func (o Ops) Archive(ctx context.Context, raw string) (WriteOut, error) {
	return o.Patch(ctx, raw, map[string]value.Value{FieldArchivedAt: value.DateTimeFrom(opclock.Now(o.Env))},
		func(id string) string { return Mark + " " + id + " archived\n" })
}

// Unarchive clears archived_at ("✓ <id> unarchived").
func (o Ops) Unarchive(ctx context.Context, raw string) (WriteOut, error) {
	return o.Patch(ctx, raw, map[string]value.Value{FieldArchivedAt: value.Null{}},
		func(id string) string { return Mark + " " + id + " unarchived\n" })
}

// DeletedText is the human line of a soft delete (DELETION 3.1, DQ8).
func DeletedText(id string) string {
	return trashline.Moved(id, id)
}

// Delete moves the row to the trash ("✓ <id> moved to the trash; restore
// with lore trash restore <id>": DQ8 every delete is soft, so v0.1.17's
// "deleted (hard)" no longer tells the truth). reason is --reason (DELETION 2.1, stored as
// deleted_reason; the engine refuses an empty one under
// delete.require_reason with E_REASON_REQUIRED).
func (o Ops) Delete(ctx context.Context, raw, reason string) (WriteOut, error) {
	ctx = engine.WithDeleteReason(ctx, reason)
	id, src, _, err := o.Live(ctx, raw)
	if err != nil {
		return WriteOut{}, err
	}
	w, err := o.Env.Delete(ctx, engine.Ref{Store: src, Table: o.Table, ID: id})
	if err != nil {
		return WriteOut{}, err
	}
	return Written(src, w, DeletedText(id.String())), nil
}

// Listed is one live row of a list, with the store holding it.
type Listed struct {
	Source store.Source
	Row    row.Row
}

// List returns the live rows of every attached store (or only store),
// those keep accepts, in store order.
func (o Ops) List(ctx context.Context, only string, keep func(row.Row) bool) ([]Listed, error) {
	sources := o.Env.Stores()
	if only != "" {
		sources = []store.Source{store.Source(only)}
	}
	var out []Listed
	for _, src := range sources {
		rows, err := o.Env.List(ctx, src, o.Table)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if r.Deleted || !keep(r) {
				continue
			}
			out = append(out, Listed{Source: src, Row: r})
		}
	}
	return out, nil
}

// SearchArgs are the shared search flags after decoding.
type SearchArgs struct {
	Query           string
	Limit           int
	Columns         []string
	IncludeArchived bool
}

// Found is one live search hit.
type Found struct {
	Source  store.Source
	Row     row.Row
	Score   float64
	Snippet string
}

// Search matches the query against every attached store's cache and keeps
// the live hits keep accepts (archived ones only with IncludeArchived),
// best first, cut at the limit after the filter.
func (o Ops) Search(ctx context.Context, a SearchArgs, keep func(row.Row) bool) ([]Found, error) {
	if strings.TrimSpace(a.Query) == "" {
		return nil, errs.New(errs.InvalidInput, o.Noun+" search: empty query")
	}
	limit := a.Limit
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 0 || limit > MaxSearchLimit:
		return nil, errs.New(errs.InvalidInput, o.Noun+" search: --limit must be between 1 and 1000")
	}
	hits, err := opsmemory.SearchFields(ctx, o.Env, o.Noun, o.Table, a.Query, a.Columns, MaxSearchLimit)
	if err != nil {
		return nil, err
	}
	out := []Found{}
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		src, r, err := o.Env.Get(ctx, engine.Ref{Store: h.Source, Table: o.Table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return nil, err
		}
		if r.Deleted || (Field(r, FieldArchivedAt) != "" && !a.IncludeArchived) || !keep(r) {
			continue
		}
		out = append(out, Found{Source: src, Row: r, Score: h.Score, Snippet: h.Snippet})
	}
	return out, nil
}

// ProjectFilter resolves a --project value to the project id a read keeps.
// Without --project it is the repo's own project: v0.1.17 listed and
// searched these nouns in the current project only (Where ProjectID), so
// rows another repo wrote into a shared store stay out unless named.
func (o Ops) ProjectFilter(ctx context.Context, flag string) (string, error) {
	p, err := o.ProjectOf(ctx, flag)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// ProjectOf resolves --project (a name among the repo store's projects, or
// any prj_ id); "" is the repo's one project.
func (o Ops) ProjectOf(ctx context.Context, flag string) (ids.ID[ids.Project], error) {
	if flag == "" {
		return o.Env.Project(ctx)
	}
	rows, err := o.Env.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	var hits []row.Row
	for _, r := range rows {
		if !r.Deleted && (r.ID.String() == flag || Field(r, FieldName) == flag) {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		// A prj_ id of another repo's project (its rows in a shared store)
		// is taken as given: `list --project prj_…` reads that project.
		if id, perr := ids.Parse[ids.Project](flag); perr == nil {
			return id, nil
		}
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, o.Noun+": no project "+QuoteID(flag)).
			WithHint("run `lore project list` to see the projects")
	case 1:
		return ids.As[ids.Project](hits[0].ID)
	}
	return ids.ID[ids.Project]{}, errs.New(errs.ProjectNameCollision, o.Noun+": several projects are named "+QuoteID(flag)).
		WithHint("name it by its prj_ id")
}

// SourceOf is the `_source` object of a row of src (zero, so omitted, for the repo;
// FLAG1, opsmemory.SourceOf).
func SourceOf(l Labeler, src store.Source) store.SourceRef { return opsmemory.SourceOf(l, src) }

// Labeler is opsmemory.Labeler: the optional Env upgrade naming a store
// as a person reads it (wire.App.StoreLabel).
type Labeler = opsmemory.Labeler

// Label is the `_label` of a row of src: "" for the repo, else
// "[<label>]" with the label from l (nil = the store name; opsmemory.Label).
func Label(l Labeler, src store.Source) string { return opsmemory.Label(l, src) }

// Labeler is o.Env's Labeler upgrade, or nil when it has none.
func (o Ops) Labeler() Labeler { return opsmemory.LabelerOf(o.Env) }

// TagIDs is a row's tag ids (O11 tag_ids; nil when untagged).
func TagIDs(r row.Row) []string {
	// ItemStrings: the same ids whether the list was built in memory or
	// decoded from the row file.
	return value.ItemStrings(r.Fields[schema.FieldTagIDs])
}
