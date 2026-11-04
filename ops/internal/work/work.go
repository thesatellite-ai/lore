// Package work is the shared row work behind the task, tasklist, mission
// and plan operations (ops/task, ops/tasklist, ops/mission, ops/plan): the
// four nouns are v0.1.17 work tables that share the project field, id
// parsing, date flags and the v0.1.17 write lines. Each noun package keeps
// its own typed In/Out structs and v0.1.17 output; this package holds only
// mechanism, so a fix lands once.
//
// Every write goes through Env.Create / Update / Delete (the engine's one
// write path); nothing here writes around it.
package work

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
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/lookup"
	"github.com/thesatellite-ai/lore/ops/internal/opclock"
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the engine + composition-root API the work operations use; it is
// the memory operations' Env (wire.App implements it), so wiring a work
// noun needs no new adapter.
type Env = opsmemory.Env

// Hit is one cache search result.
type Hit = opsmemory.Hit

// Shared field names of the work tables (schema/builtin_work.go).
const (
	FieldProject   = "project_id"
	FieldRepo      = "repo_id"
	FieldTitle     = "title"
	FieldBody      = "body"
	FieldStatus    = "status"
	FieldCreatedBy = "created_by_actor_id"
	FieldMission   = "mission_id"
	FieldTasklist  = "tasklist_id"
	FieldPlan      = "plan_id"
	FieldCompleted = "completed_at"
	FieldKey       = "idempotency_key"
	fieldName      = "name"
)

// DateLayout is v0.1.17's --due / --defer-until / --target flag layout.
const DateLayout = "2006-01-02"

// TimeLayout is v0.1.17's hand-formatted --json time layout (taskFull,
// missionWithTasks: seconds, no fraction).
const TimeLayout = "2006-01-02T15:04:05Z07:00"

// Success is v0.1.17's success mark (style.Success("✓") without colour).
const Success = "✓"

// WriteErrors are the codes any work write can return (engine write path:
// validation, reference check, secret scan, policy, backend).
var WriteErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.EmptyBody, errs.NotFound, errs.AmbiguousID,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// DeleteErrors are WriteErrors plus E_HAS_DEPENDENTS and E_REASON_REQUIRED.
var DeleteErrors = append(slices.Clone(WriteErrors), errs.HasDependents, errs.ReasonRequired)

// ReadErrors are the codes a work read can return.
var ReadErrors = []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// SearchErrors are the codes a work search can return.
var SearchErrors = []errs.Code{errs.InvalidInput, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal}

// maxQuotedID bounds how much of a bad id an error repeats.
const maxQuotedID = 48

// Quote renders a user-given value for an error, truncated.
func Quote(s string) string {
	return `"` + textcut.Cut(s, maxQuotedID, maxQuotedID) + `"`
}

// ParseID parses an id argument of prefix p (E_NOT_FOUND naming noun
// otherwise: ops/internal/lookup).
func ParseID(noun string, p ids.Prefix, s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err == nil && id.Prefix() != p {
		err = errs.New(errs.NotFound, "wrong prefix")
	}
	if err != nil {
		return ids.AnyID{}, lookup.BadID(noun+": "+Quote(s)+" is not a "+noun+" id ("+string(p)+"_…)", err)
	}
	return id, nil
}

// RefTo is a ref value to an id given by the user (prefix p), or nil
// when s is empty.
func RefTo(noun, flag string, p ids.Prefix, s string) (value.Value, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err == nil && id.Prefix() != p {
		err = errs.New(errs.NotFound, "wrong prefix")
	}
	if err != nil {
		return nil, lookup.BadID(noun+": --"+flag+" "+Quote(s)+" is not a "+string(p)+"_ id", err)
	}
	return value.RefOf(id)
}

// Texts turns the non-empty strings of m into text values (an empty
// string means "not given"), in a stable order so an error names the same
// field every run.
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

// OneOf checks v against the closed set (E_INVALID_INPUT naming flag).
func OneOf(noun, flag, v string, set []string) error {
	if slices.Contains(set, v) {
		return nil
	}
	return errs.New(errs.InvalidInput, noun+": bad --"+flag+" "+v+" (want: "+strings.Join(set, " | ")+")")
}

// Date parses a YYYY-MM-DD flag as a UTC datetime value (v0.1.17 stored
// midnight UTC).
func Date(flag, s string) (value.Value, error) {
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "--"+flag+" format: YYYY-MM-DD", err)
	}
	return value.DateTimeFrom(t), nil
}

// Now is the current time as a datetime value, on env's clock
// (opclock: the engine's wall clock when env offers it).
func Now[E any](env E) value.Value { return value.DateTimeFrom(opclock.Now(env)) }

// Field returns the text form of a row field ("" when absent or null).
func Field(r row.Row, name string) string {
	v, ok := r.Fields[name]
	if !ok || v == nil || v.Kind() == value.KindNull {
		return ""
	}
	return v.String()
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

// DateText is a datetime field as YYYY-MM-DD ("" when unset).
func DateText(r row.Row, name string) string {
	t := Time(r, name)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(DateLayout)
}

// TimeText is a datetime field in TimeLayout ("" when unset).
func TimeText(r row.Row, name string) string {
	t := Time(r, name)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

// Int is a number field as an int64 (0 when absent or not whole).
func Int(r row.Row, name string) int64 {
	n, ok := r.Fields[name].(value.Number)
	if !ok {
		return 0
	}
	i, err := n.Int64()
	if err != nil {
		return 0
	}
	return i
}

// List is the text forms of a list field's items.
func List(r row.Row, name string) []string {
	// ItemStrings: the same items whether the list was built in memory or
	// decoded from the row file.
	return value.ItemStrings(r.Fields[name])
}

// TagIDs is a row's tag ids.
func TagIDs(r row.Row) []string { return List(r, schema.FieldTagIDs) }

// WriteOut is the output of a write: the row's id, the store that holds it,
// any warnings the write raised, and the v0.1.17 human line(s).
type WriteOut struct {
	ID       string    `json:"id"`
	Store    string    `json:"store"`
	Warnings []Warning `json:"warnings,omitempty"`
	// Human is the v0.1.17 human output of this write (op.Texter).
	Human string `json:"-"`
}

// Text implements op.Texter.
func (o WriteOut) Text() string { return o.Human }

// Warning is one engine warning copied into an output (W_SCAN_DEGRADED).
type Warning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Out is the output of a write by w to src, printing human.
func Out(src store.Source, w engine.Written, human string) WriteOut {
	out := WriteOut{ID: w.Row.ID.String(), Store: string(src), Human: human}
	for _, x := range w.Warnings {
		out.Warnings = append(out.Warnings, Warning{Code: string(x.Code), Field: x.Field, Message: x.Message})
	}
	return out
}

// Get returns the live row id of table (E_NOT_FOUND "<noun> <id> not
// found" when absent or deleted, v0.1.17's text).
func Get(ctx context.Context, env Env, noun string, table row.Table, id ids.AnyID) (store.Source, row.Row, error) {
	src, r, err := env.Get(ctx, engine.Ref{Table: table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		notFound := errs.New(errs.NotFound, noun+" "+Quote(id.String())+" not found")
		if err == nil {
			return "", row.Row{}, lookup.Gone(ctx, env, engine.Ref{Table: table, ID: id}, notFound) // DELETION 3.2: E_IN_TRASH
		}
		return "", row.Row{}, notFound
	}
	return src, r, err
}

// Patch updates one row through the engine, in the store that holds it.
func Patch(ctx context.Context, env Env, noun string, table row.Table, id ids.AnyID, patch map[string]value.Value, human string) (WriteOut, error) {
	src, _, err := Get(ctx, env, noun, table, id)
	if err != nil {
		return WriteOut{}, err
	}
	w, err := env.Update(ctx, engine.Ref{Store: src, Table: table, ID: id}, patch)
	if err != nil {
		return WriteOut{}, err
	}
	return Out(src, w, human), nil
}

// Delete tombstones one row (the engine applies its relations' OnDelete).
func Delete(ctx context.Context, env Env, noun string, table row.Table, id ids.AnyID, human string) (WriteOut, error) {
	src, _, err := Get(ctx, env, noun, table, id)
	if err != nil {
		return WriteOut{}, err
	}
	w, err := env.Delete(ctx, engine.Ref{Store: src, Table: table, ID: id})
	if err != nil {
		return WriteOut{}, err
	}
	return Out(src, w, human), nil
}

// Create writes one row to target ("" = the default store) and names the
// store that now holds it.
func Create(ctx context.Context, env Env, target string, table row.Table, fields map[string]value.Value) (store.Source, engine.Written, error) {
	w, err := env.Create(ctx, store.Source(target), table, fields)
	if err != nil {
		return "", engine.Written{}, err
	}
	if target != "" {
		return store.Source(target), w, nil
	}
	src, _, gerr := env.Get(ctx, engine.Ref{Table: table, ID: w.Row.ID})
	if gerr != nil {
		return "", w, nil
	}
	return src, w, nil
}

// Project resolves --project (a prj_ id or a project name among the repo
// store's projects); "" is the repo's one project.
func Project(ctx context.Context, env Env, noun, flag string) (ids.ID[ids.Project], error) {
	if flag == "" {
		return env.Project(ctx)
	}
	rows, err := env.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	var hits []row.Row
	for _, r := range rows {
		if !r.Deleted && (r.ID.String() == flag || Field(r, fieldName) == flag) {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, noun+": no project "+Quote(flag)).
			WithHint("run `lore project list` to see the projects")
	case 1:
		return ids.As[ids.Project](hits[0].ID)
	}
	return ids.ID[ids.Project]{}, errs.New(errs.ProjectNameCollision, noun+": several projects are named "+Quote(flag)).
		WithHint("name it by its prj_ id")
}

// ProjectFilter is the project a read keeps ("" = every project: other
// stores' rows belong to their own folders' projects, as v0.1.17 appended
// them).
func ProjectFilter(ctx context.Context, env Env, noun, flag string) (string, error) {
	if flag == "" {
		return "", nil
	}
	p, err := Project(ctx, env, noun, flag)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// Sourced is one live row with the store that holds it.
type Sourced struct {
	Source store.Source
	Row    row.Row
}

// ListAll returns the live rows of table in every attached store (or only
// in only), in store precedence order then id order (v0.1.17 ordered by
// id).
func ListAll(ctx context.Context, env Env, table row.Table, only string) ([]Sourced, error) {
	sources := env.Stores()
	if only != "" {
		sources = []store.Source{store.Source(only)}
	}
	var out []Sourced
	for _, src := range sources {
		rows, err := env.List(ctx, src, table)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(rows, func(a, b row.Row) int { return strings.Compare(a.ID.String(), b.ID.String()) })
		for _, r := range rows {
			if !r.Deleted {
				out = append(out, Sourced{Source: src, Row: r})
			}
		}
	}
	return out, nil
}

// SourceName is the store name of a write output's `store` key ("" for the
// repo store). Not `_source`: write outputs keep the plain name.
func SourceName(src store.Source) string {
	if src == store.SourceRepo {
		return ""
	}
	return string(src)
}

// SourceOf is the `_source` object of a row of src (zero, so omitted, for the repo;
// FLAG1, opsmemory.SourceOf).
func SourceOf(l Labeler, src store.Source) store.SourceRef { return opsmemory.SourceOf(l, src) }

// Labeler is opsmemory.Labeler: the optional Env upgrade naming a store
// as a person reads it (wire.App.StoreLabel).
type Labeler = opsmemory.Labeler

// LabelerOf is env's Labeler upgrade, or nil (opsmemory.LabelerOf).
func LabelerOf(env Env) Labeler { return opsmemory.LabelerOf(env) }

// Label is the `_label` of a row of src: "" for the repo, else
// "[<label>]" (opsmemory.Label; l nil = the store name). Human lines
// print it in place of the bare store name, as v0.1.17 did.
func Label(l Labeler, src store.Source) string { return opsmemory.Label(l, src) }

// Search runs a full-text query over table and returns the live hits with
// their rows, best first, before any caller filter.
func Search(ctx context.Context, env Env, table row.Table, query string, fetch int) ([]SearchRow, error) {
	return SearchColumns(ctx, env, "search", table, query, nil, fetch)
}

// SearchColumns is Search restricted to some of table's full-text fields
// (`search --column`, v0.1.17 named the FTS5 columns by field name); no
// columns searches them all. An unknown column is E_INVALID_INPUT naming
// the table's columns, and an Env without the column seam E_UNSUPPORTED
// (opsmemory.SearchFields). noun prefixes the messages.
func SearchColumns(ctx context.Context, env Env, noun string, table row.Table, query string, columns []string, fetch int) ([]SearchRow, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errs.New(errs.InvalidInput, "search: empty query")
	}
	hits, err := opsmemory.SearchFields(ctx, env, noun, table, query, columns, fetch)
	if err != nil {
		return nil, err
	}
	out := make([]SearchRow, 0, len(hits))
	for _, h := range hits {
		src, r, err := env.Get(ctx, engine.Ref{Store: h.Source, Table: table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return nil, err
		}
		if r.Deleted {
			continue
		}
		out = append(out, SearchRow{Hit: h, Source: src, Row: r})
	}
	return out, nil
}

// SearchRow is one live search hit.
type SearchRow struct {
	Hit    Hit
	Source store.Source
	Row    row.Row
}

// Search limits (v0.1.17 `<noun> search --limit`, default 20).
const (
	DefaultSearchLimit = 20
	MaxSearchLimit     = 1000
)

// Limit checks a --limit value (0 = DefaultSearchLimit).
func Limit(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultSearchLimit, nil
	case n < 0 || n > MaxSearchLimit:
		return 0, errs.New(errs.InvalidInput, "search: --limit must be between 1 and 1000")
	}
	return n, nil
}

// SearchEnvelope is v0.1.17's search --json envelope (search_shared.go
// entitySearchEnvelope; count is always printed).
type SearchEnvelope struct {
	SchemaVersion int         `json:"schema_version"`
	Kind          string      `json:"kind"`
	Query         string      `json:"query"`
	Count         int         `json:"count"`
	Data          []SearchHit `json:"data"`
}

// SearchHit is one v0.1.17 search hit: id, BM25 score, snippet and the
// row's searchable columns (keys sorted, as json.Marshal of a map did).
type SearchHit struct {
	ID      string            `json:"id"`
	Score   float64           `json:"score"`
	Snippet string            `json:"snippet,omitempty"`
	Row     map[string]string `json:"row"`
	// Relations is v0.1.17's eager-loaded references (task search only).
	Relations Relations       `json:"relations,omitzero"`
	Store     store.SourceRef `json:"_source,omitempty,omitzero"`
	// Label is v0.1.17's `_label`: "[<store label>]" (opsmemory.Label).
	Label string `json:"_label,omitempty"`
	// Human is the hit's v0.1.17 human line.
	Human string `json:"-"`
}

// Relations is the task search hit's eager-loaded references; each is
// JSON null when the task does not point at one, as v0.1.17 printed it
// (op.Null: op output fields hold no pointers). The fields are in key
// order: v0.1.17 marshalled a map, so its bytes list mission, plan,
// tasklist, and --json byte compat keeps that order.
type Relations struct {
	Mission  op.Null[Brief] `json:"mission"`
	Plan     op.Null[Brief] `json:"plan"`
	Tasklist op.Null[Brief] `json:"tasklist"`
}

// Brief is a referenced row's id and title (and status for a mission).
type Brief struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
}

// Text implements op.Texter: "(no matches)" or each hit's human line.
func (o SearchEnvelope) Text() string {
	if len(o.Data) == 0 {
		return "(no matches)\n"
	}
	var b strings.Builder
	for _, h := range o.Data {
		b.WriteString(h.Human + "\n")
	}
	return b.String()
}

// SchemaVersion is v0.1.17's envelope schema_version.
const SchemaVersion = 1

// HitOf builds the v0.1.17 hit of s with the named columns; withStatus
// selects the "<id> [<status>] <title>" human line (tasks, missions) over
// "<id> <title>" (tasklists, plans); lab labels a store hit (`_label`).
func HitOf(lab Labeler, s SearchRow, withStatus bool, cols ...string) SearchHit {
	m := make(map[string]string, len(cols))
	for _, c := range cols {
		m[c] = Field(s.Row, c)
	}
	h := SearchHit{ID: s.Row.ID.String(), Score: s.Hit.Score, Snippet: s.Hit.Snippet, Row: m, Store: SourceOf(lab, s.Source), Label: Label(lab, s.Source)}
	h.Human = h.ID + " " + Field(s.Row, FieldTitle)
	if withStatus {
		h.Human = h.ID + " [" + Field(s.Row, FieldStatus) + "] " + Field(s.Row, FieldTitle)
	}
	return h
}

// BriefOf is the Brief of the row id points at in table (zero when id is
// empty or the row is gone).
func BriefOf(ctx context.Context, env Env, table row.Table, id string, withStatus bool) Brief {
	if id == "" {
		return Brief{}
	}
	aid, err := ids.ParseAny(id)
	if err != nil {
		return Brief{}
	}
	_, r, err := env.Get(ctx, engine.Ref{Table: table, ID: aid})
	if err != nil || r.Deleted {
		return Brief{}
	}
	b := Brief{ID: id, Title: Field(r, FieldTitle)}
	if withStatus {
		b.Status = Field(r, FieldStatus)
	}
	return b
}

// GroupRepoer is the optional Env upgrade that resolves a --repo name in
// a store other than the repo's own: the repos row of that store's
// project, found or created (v0.1.17 found-or-created it in the target
// store's project). wire.App implements it.
type GroupRepoer interface {
	GroupRepoID(ctx context.Context, to store.Source, name string) (ids.AnyID, error)
}

// RepoFor resolves a --repo value (mount name or rep_ id) for a write
// whose target is to: the repo store (to "" or repo) uses env.RepoID; any
// other store uses GroupRepoer when env has it, else falls back to
// env.RepoID (an Env without stores beyond the repo's own).
func RepoFor(ctx context.Context, env Env, to store.Source, name string) (ids.AnyID, error) {
	if to != "" && to != store.SourceRepo {
		if g, ok := env.(GroupRepoer); ok {
			return g.GroupRepoID(ctx, to, name)
		}
	}
	r, err := env.RepoID(ctx, name)
	if err != nil {
		return ids.AnyID{}, err
	}
	return r.Any(), nil
}

// InScope applies v0.1.17's repo scope (saas/cmd/cli/repo_scope.go) to a
// row with repo_id got: --all-repos keeps every row, --master-only only
// project-wide ones, --repo X --no-inherit only X's, --repo X X's plus
// project-wide ones, and no flag only project-wide ones.
func InScope(got, want string, noInherit, masterOnly, allRepos bool) bool {
	switch {
	case allRepos:
		return true
	case masterOnly:
		return got == ""
	case noInherit && want != "":
		return got == want
	case want != "":
		return got == "" || got == want
	}
	return got == ""
}

// SearchTable is the whole search of a noun without extra filters: the
// query over table, live hits of project ("" = any), cut at limit (0 =
// DefaultSearchLimit), each hit carrying cols; withStatus picks the human
// line (see HitOf). columns is `search --column` (nil = every full-text
// field; see SearchColumns).
func SearchTable(ctx context.Context, env Env, table row.Table, kind, query string, limit int, projectFlag string, columns []string, withStatus bool, cols ...string) (SearchEnvelope, error) {
	limit, err := Limit(limit)
	if err != nil {
		return SearchEnvelope{}, err
	}
	project, err := ProjectFilter(ctx, env, string(table), projectFlag)
	if err != nil {
		return SearchEnvelope{}, err
	}
	hits, err := SearchColumns(ctx, env, string(table), table, query, columns, MaxSearchLimit)
	if err != nil {
		return SearchEnvelope{}, err
	}
	out := SearchEnvelope{SchemaVersion: SchemaVersion, Kind: kind, Query: query}
	for _, h := range hits {
		if len(out.Data) == limit {
			break
		}
		if project != "" && Field(h.Row, FieldProject) != project {
			continue
		}
		out.Data = append(out.Data, HitOf(LabelerOf(env), h, withStatus, cols...))
	}
	out.Count = len(out.Data)
	return out, nil
}
