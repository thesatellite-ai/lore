// Package knowledge is the shared mechanism behind the rule, decision and
// hotfix operations (ops/rule, ops/decision, ops/hotfix): the three nouns
// are v0.1.17 knowledge tables with the same lifecycle columns, the same
// repo scope, the same supersede chain and the same tag field, and differ
// only in a few typed fields. Each noun package keeps its own typed In/Out
// structs and v0.1.17 output; this package holds the row work they share,
// so a fix lands once.
//
// Every write goes through Env.Create / Update / Delete (the engine's one
// write path); nothing here writes around it.
package knowledge

import (
	"cmp"
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
	"github.com/thesatellite-ai/lore/ops/internal/textcut"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Env is the operations' environment. It is memory's Env (the engine row
// API plus cache search, the repo's project and repo lookup), which
// wire.App implements once for every noun.
type Env = opsmemory.Env

// Hit is one cache search result.
type Hit = opsmemory.Hit

// Shared field names of the knowledge tables (schema/builtin_knowledge.go).
const (
	FieldProject    = "project_id"
	FieldRepo       = "repo_id"
	FieldBody       = "body"
	FieldTitle      = "title"
	FieldSourceKind = "source_kind"
	FieldSourceRef  = "source_ref"
	FieldArchivedAt = "archived_at"
	FieldSuperseded = "superseded_by_id"
	FieldTrust      = "trust_score"
	FieldConfidence = "confidence"
	FieldValidated  = "last_validated_at"
	FieldCreatedBy  = "created_by_actor_id"
	fieldName       = "name"
	fieldMount      = "mount_name"
)

// Defaults shared by every knowledge add (v0.1.17 flag defaults).
const (
	DefaultSource = "manual"
	// DefaultTrust is LifecycleMixin's trust_score default.
	DefaultTrust = 0.5
	// DefaultSearchLimit is v0.1.17's --limit default of `<noun> search`.
	DefaultSearchLimit = 20
	// MaxSearchLimit bounds one search (a REST caller cannot ask the cache
	// for an unbounded result).
	MaxSearchLimit = 1000
	// ListCut / SearchCut are how many characters of a body or title a
	// list / search line shows (v0.1.17 cut at 60 and 80, with "...").
	ListCut   = 60
	SearchCut = 80
	ellipsis  = "..."
	// maxQuoted bounds how much of a bad user value an error repeats.
	maxQuoted = 48
)

// WriteErrors are the codes any knowledge write can return.
var WriteErrors = []errs.Code{
	errs.InvalidInput, errs.InvalidID, errs.EmptyBody, errs.NotFound, errs.AmbiguousID,
	errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied, errs.ConfigInvalid,
	errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.OldLore, errs.Internal,
}

// ReadErrors are the codes a knowledge read can return.
var ReadErrors = []errs.Code{errs.InvalidInput, errs.InvalidID, errs.NotFound, errs.AmbiguousID, errs.StoreUnreachable, errs.OldLore, errs.Internal}

// SearchErrors are the codes a knowledge search can return.
var SearchErrors = []errs.Code{errs.InvalidInput, errs.Unsupported, errs.DBCorrupt, errs.StoreUnreachable, errs.Internal}

// DeleteErrors are WriteErrors plus E_HAS_DEPENDENTS.
func DeleteErrors() []errs.Code { return append(slices.Clone(WriteErrors), errs.HasDependents) }

// Noun describes one knowledge table.
type Noun struct {
	// Name is the CLI noun and message word ("rule").
	Name string
	// Table is the schema table.
	Table row.Table
	// Prefix is the id prefix ("rul").
	Prefix ids.Prefix
}

// ID parses a user-given id of this noun (E_INVALID_ID otherwise).
func (n Noun) ID(s string) (ids.AnyID, error) {
	id, err := ids.ParseAny(strings.TrimSpace(s))
	if err == nil && id.Prefix() != n.Prefix {
		err = errs.New(errs.InvalidID, "wrong prefix")
	}
	if err != nil {
		return ids.AnyID{}, errs.Wrap(errs.InvalidID, n.Name+": "+Quote(s)+" is not a "+n.Name+" id ("+string(n.Prefix)+"_…)", err)
	}
	return id, nil
}

// Item is one row and the store that holds it.
type Item struct {
	Source store.Source
	Row    row.Row
}

// Scope is a read's repo scope (v0.1.17 repo_scope.go) and project.
type Scope struct {
	Repo       string
	NoInherit  bool
	MasterOnly bool
	AllRepos   bool
	// Project limits the read to one project (prj_ id or name; "" = every
	// project, as memory reads).
	Project string
}

// Create is what an add writes beyond the noun's typed fields.
type Create struct {
	// To is the target store ("" = the default target).
	To string
	// Project and Repo are v0.1.17's --project / --repo context flags.
	Project, Repo string
	// Supersedes is an id of the same noun the new row replaces.
	Supersedes string
	// CreatedBy is an act_ id; empty means the envelope's created_by.
	CreatedBy string
	// Tags are tag names of the project (each must exist).
	Tags []string
	// Texts are the noun's text fields; an empty value is not written.
	Texts map[string]string
	// SupersededPatch is extra fields set on the replaced row (a
	// decision's status becomes superseded).
	SupersededPatch map[string]value.Value
}

// Add creates one row of noun n and returns the store that holds it.
// With Supersedes the old row is checked first (live, same project, no
// successor yet) and pointed at the new one after; on failure the new row
// is deleted again (v0.1.17 supersede.go).
func Add(ctx context.Context, env Env, n Noun, c Create) (store.Source, engine.Written, error) {
	project, err := ResolveProject(ctx, env, n, c.Project)
	if err != nil {
		return "", engine.Written{}, err
	}
	texts := maps.Clone(c.Texts)
	if texts[FieldSourceKind] == "" {
		texts[FieldSourceKind] = DefaultSource
	}
	fields, err := Texts(n, texts)
	if err != nil {
		return "", engine.Written{}, err
	}
	if fields[FieldProject], err = value.RefOf(project.Any()); err != nil {
		return "", engine.Written{}, err
	}
	if c.Repo != "" {
		repo, rerr := env.RepoID(ctx, c.Repo)
		if rerr != nil {
			return "", engine.Written{}, rerr
		}
		if fields[FieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return "", engine.Written{}, err
		}
	}
	if c.CreatedBy != "" {
		a, aerr := ids.Parse[ids.Actor](strings.TrimSpace(c.CreatedBy))
		if aerr != nil {
			return "", engine.Written{}, errs.Wrap(errs.InvalidID, n.Name+" add: "+Quote(c.CreatedBy)+" is not an actor id (act_…)", aerr)
		}
		if fields[FieldCreatedBy], err = value.RefOf(a.Any()); err != nil {
			return "", engine.Written{}, err
		}
	}
	if len(c.Tags) > 0 {
		tags, terr := TagRefs(ctx, env, n, project.String(), c.Tags)
		if terr != nil {
			return "", engine.Written{}, terr
		}
		if fields[schema.FieldTagIDs], err = TagList(tags); err != nil {
			return "", engine.Written{}, err
		}
	}
	trust, err := value.Float(DefaultTrust)
	if err != nil {
		return "", engine.Written{}, errs.Wrap(errs.Internal, n.Name+" add: trust score", err)
	}
	fields[FieldTrust] = trust
	var old ids.AnyID
	var oldSrc store.Source
	if c.Supersedes != "" {
		if old, oldSrc, err = checkSupersedes(ctx, env, n, project, c.Supersedes); err != nil {
			return "", engine.Written{}, err
		}
	}
	w, err := env.Create(ctx, store.Source(c.To), n.Table, fields)
	if err != nil {
		return "", engine.Written{}, err
	}
	src := StoreOf(ctx, env, n, w.Row.ID, store.Source(c.To))
	if !old.IsZero() {
		if err := finishSupersedes(ctx, env, n, oldSrc, old, src, w.Row.ID, c.SupersededPatch); err != nil {
			return "", engine.Written{}, err
		}
	}
	return src, w, nil
}

// checkSupersedes validates --supersedes before anything is written.
func checkSupersedes(ctx context.Context, env Env, n Noun, project ids.ID[ids.Project], raw string) (ids.AnyID, store.Source, error) {
	// v0.1.17 parity: any value that does not name a live row of this noun
	// in this project (well-formed unknown, malformed, another noun's
	// prefix) is E_NOT_FOUND here; plain id arguments keep E_INVALID_ID.
	notFound := errs.New(errs.NotFound, "--supersedes "+textcut.Cut(strings.TrimSpace(raw), maxQuoted, maxQuoted)+": no "+n.Name+" with that id in this project").
		WithHint("run `lore " + n.Name + " list` to see the ids")
	id, err := n.ID(raw)
	if err != nil {
		return ids.AnyID{}, "", notFound
	}
	src, r, err := env.Get(ctx, engine.Ref{Table: n.Table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && (r.Deleted || Field(r, FieldProject) != project.String())) {
		return ids.AnyID{}, "", notFound
	}
	if err != nil {
		return ids.AnyID{}, "", err
	}
	if next := Field(r, FieldSuperseded); next != "" {
		return ids.AnyID{}, "", errs.New(errs.InvalidInput, "--supersedes "+id.String()+": that "+n.Name+" was already replaced by "+next).
			WithHint("supersede " + next + " instead, so the chain stays one line")
	}
	return id, src, nil
}

// finishSupersedes points old at the new row; on failure the new row is
// deleted again and the marking error returned.
func finishSupersedes(ctx context.Context, env Env, n Noun, oldSrc store.Source, old ids.AnyID, newSrc store.Source, newID ids.AnyID, extra map[string]value.Value) error {
	ref, err := value.RefOf(newID)
	if err != nil {
		return err
	}
	patch := maps.Clone(extra)
	if patch == nil {
		patch = map[string]value.Value{}
	}
	patch[FieldSuperseded] = ref
	_, err = env.Update(ctx, engine.Ref{Store: oldSrc, Table: n.Table, ID: old}, patch)
	if err == nil {
		return nil
	}
	if _, derr := env.Delete(ctx, engine.Ref{Store: newSrc, Table: n.Table, ID: newID}); derr != nil {
		return errs.Wrap(errs.CodeOf(err), n.Name+" add: could not mark "+old.String()+" superseded by "+newID.String(), err).
			WithHint("the new " + n.Name + " " + newID.String() + " was written but could not be removed again; delete it with `lore " + n.Name + " delete " + newID.String() + " --yes`")
	}
	return errs.Wrap(errs.CodeOf(err), n.Name+" add: could not mark "+old.String()+" superseded by "+newID.String()+"; nothing was kept", err)
}

// ResolveProject resolves --project (a prj_ id or a project name among
// the repo store's projects); "" is the repo's one project.
func ResolveProject(ctx context.Context, env Env, n Noun, flag string) (ids.ID[ids.Project], error) {
	if flag == "" {
		return env.Project(ctx)
	}
	rows, err := env.List(ctx, store.SourceRepo, schema.TableProjects)
	if err != nil {
		return ids.ID[ids.Project]{}, err
	}
	var hits []row.Row
	for _, r := range rows {
		if r.ID.String() == flag || Field(r, fieldName) == flag {
			hits = append(hits, r)
		}
	}
	switch len(hits) {
	case 0:
		return ids.ID[ids.Project]{}, errs.New(errs.NotFound, n.Name+": no project "+Quote(flag)).
			WithHint("run `lore project list` to see the projects")
	case 1:
		return ids.As[ids.Project](hits[0].ID)
	}
	return ids.ID[ids.Project]{}, errs.New(errs.ProjectNameCollision, n.Name+": several projects are named "+Quote(flag)).
		WithHint("name it by its prj_ id")
}

// filter is a resolved Scope.
type filter struct {
	scope   scope
	repo    string
	project string
}

// resolve turns a Scope into a filter (repo and project looked up once).
func (s Scope) resolve(ctx context.Context, env Env, n Noun) (filter, error) {
	f := filter{}
	if s.Repo != "" {
		r, err := env.RepoID(ctx, s.Repo)
		if err != nil {
			return filter{}, err
		}
		f.repo = r.String()
	}
	f.scope = scopeOf(f.repo, s.NoInherit, s.MasterOnly, s.AllRepos)
	if s.Project != "" {
		p, err := ResolveProject(ctx, env, n, s.Project)
		if err != nil {
			return filter{}, err
		}
		f.project = p.String()
	}
	return f, nil
}

// keeps reports whether r is in the filter.
func (f filter) keeps(r row.Row) bool {
	return f.scope.keeps(Field(r, FieldRepo), f.repo) && (f.project == "" || Field(r, FieldProject) == f.project)
}

// List returns the live rows of noun n in scope from every attached store
// (or the one named), in id order within each store (v0.1.17 ordered by
// id). Archived rows are kept only when withArchived is set; keep, when
// set, filters further (a --tag).
func List(ctx context.Context, env Env, n Noun, s Scope, withArchived bool, only store.Source, keep func(row.Row) bool) ([]Item, error) {
	f, err := s.resolve(ctx, env, n)
	if err != nil {
		return nil, err
	}
	sources := env.Stores()
	if only != "" {
		sources = []store.Source{only}
	}
	var out []Item
	for _, src := range sources {
		rows, err := env.List(ctx, src, n.Table)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(rows, func(a, b row.Row) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
		for _, r := range rows {
			if r.Deleted || (!withArchived && Archived(r)) || !f.keeps(r) || (keep != nil && !keep(r)) {
				continue
			}
			out = append(out, Item{Source: src, Row: r})
		}
	}
	return out, nil
}

// Show returns one live row of noun n by a user-given id.
func Show(ctx context.Context, env Env, n Noun, raw string) (Item, error) {
	id, err := n.ID(raw)
	if err != nil {
		return Item{}, err
	}
	src, r, err := env.Get(ctx, engine.Ref{Table: n.Table, ID: id})
	if errs.Has(err, errs.NotFound) || (err == nil && r.Deleted) {
		return Item{}, errs.New(errs.NotFound, n.Name+" "+Quote(raw)+" not found")
	}
	if err != nil {
		return Item{}, err
	}
	return Item{Source: src, Row: r}, nil
}

// Edit is the shared part of `<noun> edit`.
type Edit struct {
	// Texts are text fields to set; an empty value leaves the field.
	Texts map[string]string
	// RebindRepo / RebindMaster move the row to a repo, or project-wide.
	RebindRepo   string
	RebindMaster bool
	// Tags / Untags add and remove tags by name.
	Tags, Untags []string
	// Hint names the flags for the "nothing to change" error.
	Hint string
}

// Apply patches the row whose id is raw with e (only given fields change).
func Apply(ctx context.Context, env Env, n Noun, raw string, e Edit) (store.Source, engine.Written, error) {
	it, err := Show(ctx, env, n, raw)
	if err != nil {
		return "", engine.Written{}, err
	}
	patch, err := Texts(n, e.Texts)
	if err != nil {
		return "", engine.Written{}, err
	}
	switch {
	case e.RebindRepo != "" && e.RebindMaster:
		return "", engine.Written{}, errs.New(errs.InvalidInput, n.Name+" edit: give --rebind-repo or --rebind-master, not both")
	case e.RebindRepo != "":
		repo, rerr := env.RepoID(ctx, e.RebindRepo)
		if rerr != nil {
			return "", engine.Written{}, rerr
		}
		if patch[FieldRepo], err = value.RefOf(repo.Any()); err != nil {
			return "", engine.Written{}, err
		}
	case e.RebindMaster:
		patch[FieldRepo] = value.Null{}
	}
	if len(e.Tags) > 0 || len(e.Untags) > 0 {
		v, terr := retagPatch(ctx, env, n, it.Row, e.Tags, e.Untags)
		if terr != nil {
			return "", engine.Written{}, terr
		}
		patch[schema.FieldTagIDs] = v
	}
	if len(patch) == 0 {
		return "", engine.Written{}, errs.New(errs.InvalidInput, n.Name+" edit: nothing to change").WithHint(e.Hint)
	}
	w, err := env.Update(ctx, engine.Ref{Store: it.Source, Table: n.Table, ID: it.Row.ID}, patch)
	return it.Source, w, err
}

// Stamp sets one field of the row whose id is raw (archive, unarchive).
func Stamp(ctx context.Context, env Env, n Noun, raw, field string, v value.Value) (engine.Written, error) {
	it, err := Show(ctx, env, n, raw)
	if err != nil {
		return engine.Written{}, err
	}
	return env.Update(ctx, engine.Ref{Store: it.Source, Table: n.Table, ID: it.Row.ID}, map[string]value.Value{field: v})
}

// Now is the archive stamp.
func Now() value.Value { return value.DateTimeFrom(time.Now()) }

// Delete writes the row's tombstone.
func Delete(ctx context.Context, env Env, n Noun, raw string) (engine.Written, error) {
	it, err := Show(ctx, env, n, raw)
	if err != nil {
		return engine.Written{}, err
	}
	return env.Delete(ctx, engine.Ref{Store: it.Source, Table: n.Table, ID: it.Row.ID})
}

// Found is one search hit with its row.
type Found struct {
	Item
	Hit Hit
}

// Search matches query against every attached store's cache, keeps live
// hits in scope (archived only with withArchived), best first, cut at
// limit after the filter (0 = DefaultSearchLimit).
func Search(ctx context.Context, env Env, n Noun, query string, columns []string, limit int, s Scope, withArchived bool) ([]Found, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errs.New(errs.InvalidInput, n.Name+" search: empty query")
	}
	switch {
	case limit == 0:
		limit = DefaultSearchLimit
	case limit < 0 || limit > MaxSearchLimit:
		return nil, errs.New(errs.InvalidInput, n.Name+" search: --limit must be between 1 and 1000")
	}
	f, err := s.resolve(ctx, env, n)
	if err != nil {
		return nil, err
	}
	hits, err := opsmemory.SearchFields(ctx, env, n.Name, n.Table, query, columns, MaxSearchLimit)
	if err != nil {
		return nil, err
	}
	var out []Found
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		src, r, err := env.Get(ctx, engine.Ref{Store: h.Source, Table: n.Table, ID: h.ID})
		if errs.Has(err, errs.NotFound) {
			continue // deleted since the cache was refreshed
		}
		if err != nil {
			return nil, err
		}
		if r.Deleted || (!withArchived && Archived(r)) || !f.keeps(r) {
			continue
		}
		out = append(out, Found{Item: Item{Source: src, Row: r}, Hit: h})
	}
	return out, nil
}

// StoreOf names the store that holds id after a write: the requested one,
// else the one Get finds.
func StoreOf(ctx context.Context, env Env, n Noun, id ids.AnyID, requested store.Source) store.Source {
	if requested != "" {
		return requested
	}
	src, _, err := env.Get(ctx, engine.Ref{Table: n.Table, ID: id})
	if err != nil {
		return ""
	}
	return src
}
